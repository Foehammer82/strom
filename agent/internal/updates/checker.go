package updates

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"time"
)

const (
	// manifestAssetName and manifestSignatureAssetName are the exact,
	// fixed release asset names published by
	// tools/__main__.py:release_agent_artifacts for every stable release.
	manifestAssetName          = "strom-agent-manifest.json"
	manifestSignatureAssetName = manifestAssetName + ".sig"
)

// defaultReleasePublicKeyHex is the Ed25519 public key used to verify
// official Strom release manifests.
//
// This is a development placeholder. Before enabling real update signing in
// production, generate a real keypair with `uv run strom release
// generate-signing-key`, replace this constant with the printed public key,
// store the private key ONLY as the STROM_RELEASE_SIGNING_KEY_PEM GitHub
// Actions secret, and never commit the private key.
const defaultReleasePublicKeyHex = "8c9c5a5f0227b453737c56bea808c6cf7328bea97d1b4e4be113fc6658fdc604"

// DefaultReleasePublicKey returns the parsed default release public key.
func DefaultReleasePublicKey() ed25519.PublicKey {
	key, err := ParseReleasePublicKeyHex(defaultReleasePublicKeyHex)
	if err != nil {
		// This can only happen if the embedded constant above was edited
		// incorrectly; fail loudly at the earliest possible point.
		panic(fmt.Sprintf("invalid embedded default release public key: %v", err))
	}
	return key
}

// ParseReleasePublicKeyHex decodes a hex-encoded Ed25519 public key.
func ParseReleasePublicKeyHex(hexKey string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil {
		return nil, fmt.Errorf("decode release public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("release public key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// CheckResult reports the outcome of checking GitHub for a new stable
// release.
type CheckResult struct {
	UpToDate         bool
	InstalledVersion string
	AvailableVersion string
	ReleaseURL       string
}

// InstallResult reports the outcome of downloading, verifying, and staging
// a new release.
type InstallResult struct {
	Version         string
	RestartRequired bool
}

// Checker orchestrates checking for and installing standalone node
// updates: fetching the latest stable GitHub release, verifying its signed
// manifest, and handing the verified binary to a Store for durable
// activation.
type Checker struct {
	Store  *Store
	GitHub *GitHubClient

	// PublicKey verifies release manifest signatures. Defaults to
	// DefaultReleasePublicKey() when nil.
	PublicKey ed25519.PublicKey

	// InstalledVersion is the version of the currently running process
	// (the build-time version string). Required.
	InstalledVersion string

	// GOOS, GOARCH, GOARM select which manifest artifact to consider.
	// Default to the running binary's own target when empty.
	GOOS   string
	GOARCH string
	GOARM  string
}

func (c *Checker) publicKey() ed25519.PublicKey {
	if len(c.PublicKey) == ed25519.PublicKeySize {
		return c.PublicKey
	}
	return DefaultReleasePublicKey()
}

func (c *Checker) goos() string {
	if c.GOOS != "" {
		return c.GOOS
	}
	return runtime.GOOS
}

func (c *Checker) goarch() string {
	if c.GOARCH != "" {
		return c.GOARCH
	}
	return runtime.GOARCH
}

// Check fetches and verifies the latest stable release manifest and reports
// whether a newer stable version than InstalledVersion is available. The
// result (and any error) is persisted to the Store's status file so the
// node UI/API can report it without re-checking GitHub on every request.
func (c *Checker) Check(ctx context.Context) (CheckResult, error) {
	result, _, _, err := c.checkAndVerify(ctx)
	return result, err
}

// checkAndVerify fetches the latest release, verifies its manifest
// signature, and resolves the artifact for this node's platform. It always
// persists the outcome (success or failure) to the store's status file.
func (c *Checker) checkAndVerify(ctx context.Context) (CheckResult, Release, Manifest, error) {
	result, release, manifest, err := c.doCheck(ctx)

	status, statusErr := c.Store.ReadStatus()
	if statusErr == nil {
		status.LastCheckedAt = time.Now().UTC()
		if err != nil {
			status.LastCheckError = err.Error()
			status.AvailableVersion = ""
			status.AvailableReleaseURL = ""
		} else {
			status.LastCheckError = ""
			if result.UpToDate {
				status.AvailableVersion = ""
				status.AvailableReleaseURL = ""
			} else {
				status.AvailableVersion = result.AvailableVersion
				status.AvailableReleaseURL = result.ReleaseURL
			}
		}
		_ = c.Store.WriteStatus(status)
	}

	return result, release, manifest, err
}

func (c *Checker) doCheck(ctx context.Context) (CheckResult, Release, Manifest, error) {
	if c.Store == nil || c.GitHub == nil {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("checker is not configured")
	}
	if strings.TrimSpace(c.InstalledVersion) == "" {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("installed version is required")
	}
	// Development/local builds (e.g. a "git describe --dirty" version like
	// "v0.1.9-dirty") are never a strict stable release tag, so they can
	// never be safely compared against a published release. Rather than
	// refusing to check at all, treat any installed version that isn't a
	// comparable stable release as always wanting the latest available
	// release: skip the version-gate below and always report the latest
	// verified release as available.
	_, err := ParseStableVersion(c.InstalledVersion)
	installedIsStable := err == nil

	release, err := c.GitHub.LatestRelease(ctx)
	if err != nil {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("fetch latest release: %w", err)
	}

	if installedIsStable {
		// Compare the release tag against the installed version before
		// ever fetching the manifest/signature. This avoids surfacing a
		// confusing "release has no manifest asset" error when the latest
		// published release simply isn't newer than what's already
		// installed (e.g. an older release published before manifest
		// signing was added): in that case there's nothing to install, so
		// a missing/unverifiable manifest on that release is irrelevant.
		// Both versions are already confirmed stable release tags
		// above/by LatestRelease, so this cannot fail.
		newer, err := IsNewerStableVersion(release.TagName, c.InstalledVersion)
		if err != nil {
			return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("compare versions: %w", err)
		}
		if !newer {
			return CheckResult{
				UpToDate:         true,
				InstalledVersion: c.InstalledVersion,
				AvailableVersion: release.TagName,
				ReleaseURL:       release.HTMLURL,
			}, release, Manifest{}, nil
		}
	}

	manifestAsset, ok := release.AssetByName(manifestAssetName)
	if !ok {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("release %s has no %s asset", release.TagName, manifestAssetName)
	}
	sigAsset, ok := release.AssetByName(manifestSignatureAssetName)
	if !ok {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("release %s has no %s asset", release.TagName, manifestSignatureAssetName)
	}

	manifestBytes, err := c.GitHub.DownloadAsset(ctx, manifestAsset.BrowserDownloadURL)
	if err != nil {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("download release manifest: %w", err)
	}
	sigBytes, err := c.GitHub.DownloadAsset(ctx, sigAsset.BrowserDownloadURL)
	if err != nil {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("download release manifest signature: %w", err)
	}

	if err := VerifyManifestSignature(c.publicKey(), manifestBytes, sigBytes); err != nil {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("verify release manifest: %w", err)
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("parse release manifest: %w", err)
	}
	if manifest.Version != release.TagName {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("manifest version %q does not match release tag %q", manifest.Version, release.TagName)
	}

	if _, ok := manifest.ArtifactFor(c.goos(), c.goarch(), c.GOARM); !ok {
		return CheckResult{}, Release{}, Manifest{}, fmt.Errorf("release %s has no artifact for %s/%s", manifest.Version, c.goos(), c.goarch())
	}

	// Reaching here means either the release was confirmed newer than a
	// stable installed version above, or the installed version isn't
	// stable at all (in which case the latest verified release is always
	// reported as available) — so this is always a genuine update.
	result := CheckResult{
		UpToDate:         false,
		InstalledVersion: c.InstalledVersion,
		AvailableVersion: manifest.Version,
		ReleaseURL:       release.HTMLURL,
	}
	return result, release, manifest, nil
}

// Install re-verifies the latest release, requires that its version match
// requestedVersion (to avoid acting on stale caller state), downloads and
// verifies the release archive for this node's platform, and hands the
// verified binary to the Store for durable activation.
func (c *Checker) Install(ctx context.Context, requestedVersion string) (InstallResult, error) {
	result, release, manifest, err := c.checkAndVerify(ctx)
	if err != nil {
		c.recordInstallError(err)
		return InstallResult{}, err
	}
	if result.UpToDate {
		err := fmt.Errorf("installed version %s is already up to date", c.InstalledVersion)
		c.recordInstallError(err)
		return InstallResult{}, err
	}
	if manifest.Version != requestedVersion {
		err := fmt.Errorf("requested version %s no longer matches the latest available release %s", requestedVersion, manifest.Version)
		c.recordInstallError(err)
		return InstallResult{}, err
	}

	artifact, ok := manifest.ArtifactFor(c.goos(), c.goarch(), c.GOARM)
	if !ok {
		err := fmt.Errorf("release %s has no artifact for %s/%s", manifest.Version, c.goos(), c.goarch())
		c.recordInstallError(err)
		return InstallResult{}, err
	}
	archiveAsset, ok := release.AssetByName(artifact.Filename)
	if !ok {
		err := fmt.Errorf("release %s is missing expected asset %s", manifest.Version, artifact.Filename)
		c.recordInstallError(err)
		return InstallResult{}, err
	}

	archiveBytes, err := c.GitHub.DownloadAsset(ctx, archiveAsset.BrowserDownloadURL)
	if err != nil {
		err = fmt.Errorf("download release archive: %w", err)
		c.recordInstallError(err)
		return InstallResult{}, err
	}

	binary, err := ExtractAgentBinary(archiveBytes, artifact.Size, artifact.SHA256, artifact.BinarySHA256)
	if err != nil {
		err = fmt.Errorf("verify release archive: %w", err)
		c.recordInstallError(err)
		return InstallResult{}, err
	}

	if err := c.Store.StageAndActivate(manifest.Version, binary); err != nil {
		err = fmt.Errorf("activate release: %w", err)
		c.recordInstallError(err)
		return InstallResult{}, err
	}

	status, statusErr := c.Store.ReadStatus()
	if statusErr == nil {
		status.LastInstallAt = time.Now().UTC()
		status.LastInstallError = ""
		status.AvailableVersion = ""
		status.AvailableReleaseURL = ""
		_ = c.Store.WriteStatus(status)
	}

	return InstallResult{Version: manifest.Version, RestartRequired: true}, nil
}

func (c *Checker) recordInstallError(installErr error) {
	status, err := c.Store.ReadStatus()
	if err != nil {
		return
	}
	status.LastInstallAt = time.Now().UTC()
	status.LastInstallError = installErr.Error()
	_ = c.Store.WriteStatus(status)
}
