package updates

import (
	"archive/tar"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeGitHubRelease builds a self-contained fake GitHub release server
// serving a release descriptor and its manifest/signature/archive assets,
// signed with the given private key.
func fakeGitHubRelease(t *testing.T, priv ed25519.PrivateKey, version string, artifact Artifact, archiveBytes []byte) *httptest.Server {
	t.Helper()

	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Version:       version,
		KeyID:         "test-key",
		Artifacts:     []Artifact{artifact},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	signature := ed25519.Sign(priv, manifestBytes)

	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		release := map[string]any{
			"tag_name":   version,
			"draft":      false,
			"prerelease": false,
			"html_url":   "https://github.com/owner/repo/releases/" + version,
			"assets": []map[string]any{
				{"name": manifestAssetName, "size": len(manifestBytes), "browser_download_url": server.URL + "/assets/manifest.json"},
				{"name": manifestSignatureAssetName, "size": len(signature), "browser_download_url": server.URL + "/assets/manifest.json.sig"},
				{"name": artifact.Filename, "size": len(archiveBytes), "browser_download_url": server.URL + "/assets/" + artifact.Filename},
			},
		}
		_ = json.NewEncoder(w).Encode(release)
	})
	mux.HandleFunc("/assets/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(manifestBytes)
	})
	mux.HandleFunc("/assets/manifest.json.sig", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(signature)
	})
	mux.HandleFunc("/assets/"+artifact.Filename, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiveBytes)
	})

	server = httptest.NewServer(mux)
	return server
}

func newTestChecker(t *testing.T, store *Store, github *GitHubClient, pub ed25519.PublicKey, installedVersion string) *Checker {
	t.Helper()
	github.ValidateDownloadURL = func(string) error { return nil }
	return &Checker{
		Store:            store,
		GitHub:           github,
		PublicKey:        pub,
		InstalledVersion: installedVersion,
		GOOS:             "linux",
		GOARCH:           "arm64",
	}
}

func TestCheckerCheckReportsAvailableUpdate(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	binaryContent := []byte("fake-binary")
	archive := buildTarGz(t, []tarEntry{
		{name: "strom-agent-v1.1.0-linux-arm64/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
	})
	artifact := Artifact{
		OS: "linux", Arch: "arm64",
		Filename:     "strom-agent-v1.1.0-linux-arm64.tar.gz",
		Size:         int64(len(archive)),
		SHA256:       sha256Hex(archive),
		BinarySHA256: sha256Hex(binaryContent),
	}
	server := fakeGitHubRelease(t, priv, "v1.1.0", artifact, archive)
	defer server.Close()

	store := NewStore(t.TempDir())
	github := &GitHubClient{BaseURL: server.URL, Repository: "owner/repo"}
	checker := newTestChecker(t, store, github, pub, "v1.0.0")

	result, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.UpToDate {
		t.Fatal("expected update to be available")
	}
	if result.AvailableVersion != "v1.1.0" {
		t.Fatalf("AvailableVersion = %q, want v1.1.0", result.AvailableVersion)
	}

	status, err := store.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if status.AvailableVersion != "v1.1.0" || status.LastCheckError != "" {
		t.Fatalf("persisted status = %+v", status)
	}
}

func TestCheckerCheckUpToDate(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	binaryContent := []byte("fake-binary")
	archive := buildTarGz(t, []tarEntry{
		{name: "strom-agent-v1.0.0-linux-arm64/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
	})
	artifact := Artifact{
		OS: "linux", Arch: "arm64",
		Filename:     "strom-agent-v1.0.0-linux-arm64.tar.gz",
		Size:         int64(len(archive)),
		SHA256:       sha256Hex(archive),
		BinarySHA256: sha256Hex(binaryContent),
	}
	server := fakeGitHubRelease(t, priv, "v1.0.0", artifact, archive)
	defer server.Close()

	store := NewStore(t.TempDir())
	github := &GitHubClient{BaseURL: server.URL, Repository: "owner/repo"}
	checker := newTestChecker(t, store, github, pub, "v1.0.0")

	result, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.UpToDate {
		t.Fatal("expected already up to date")
	}
}

func TestCheckerCheckDevBuildAlwaysReportsLatestAvailable(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	binaryContent := []byte("fake-binary")
	archive := buildTarGz(t, []tarEntry{
		{name: "strom-agent-v0.1.8-linux-arm64/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
	})
	artifact := Artifact{
		OS: "linux", Arch: "arm64",
		Filename:     "strom-agent-v0.1.8-linux-arm64.tar.gz",
		Size:         int64(len(archive)),
		SHA256:       sha256Hex(archive),
		BinarySHA256: sha256Hex(binaryContent),
	}
	// The published release is "older" than the dev build's base version,
	// but a non-stable installed version (e.g. a local "-dirty" build) can
	// never be compared against a release tag, so the latest verified
	// release should still be reported as available rather than erroring
	// or claiming to be up to date.
	server := fakeGitHubRelease(t, priv, "v0.1.8", artifact, archive)
	defer server.Close()

	store := NewStore(t.TempDir())
	github := &GitHubClient{BaseURL: server.URL, Repository: "owner/repo"}
	checker := newTestChecker(t, store, github, pub, "v0.1.9-dirty")

	result, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.UpToDate {
		t.Fatal("expected an available update to be reported for a non-stable installed version")
	}
	if result.AvailableVersion != "v0.1.8" {
		t.Fatalf("AvailableVersion = %q, want v0.1.8", result.AvailableVersion)
	}
}

func TestCheckerCheckRejectsBadSignature(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	_, wrongPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	binaryContent := []byte("fake-binary")
	archive := buildTarGz(t, []tarEntry{
		{name: "strom-agent-v1.1.0-linux-arm64/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
	})
	artifact := Artifact{
		OS: "linux", Arch: "arm64",
		Filename:     "strom-agent-v1.1.0-linux-arm64.tar.gz",
		Size:         int64(len(archive)),
		SHA256:       sha256Hex(archive),
		BinarySHA256: sha256Hex(binaryContent),
	}
	// Sign with a different key than the one the checker trusts.
	server := fakeGitHubRelease(t, wrongPriv, "v1.1.0", artifact, archive)
	defer server.Close()

	store := NewStore(t.TempDir())
	github := &GitHubClient{BaseURL: server.URL, Repository: "owner/repo"}
	checker := newTestChecker(t, store, github, pub, "v1.0.0")

	if _, err := checker.Check(context.Background()); err == nil {
		t.Fatal("expected signature verification failure")
	}

	status, err := store.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if status.LastCheckError == "" {
		t.Fatal("expected persisted last check error")
	}
}

func TestCheckerInstallEndToEnd(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	binaryContent := []byte("fake-binary-v1.1.0")
	archive := buildTarGz(t, []tarEntry{
		{name: "strom-agent-v1.1.0-linux-arm64/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
	})
	artifact := Artifact{
		OS: "linux", Arch: "arm64",
		Filename:     "strom-agent-v1.1.0-linux-arm64.tar.gz",
		Size:         int64(len(archive)),
		SHA256:       sha256Hex(archive),
		BinarySHA256: sha256Hex(binaryContent),
	}
	server := fakeGitHubRelease(t, priv, "v1.1.0", artifact, archive)
	defer server.Close()

	store := NewStore(t.TempDir())
	github := &GitHubClient{BaseURL: server.URL, Repository: "owner/repo"}
	checker := newTestChecker(t, store, github, pub, "v1.0.0")

	result, err := checker.Install(context.Background(), "v1.1.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Version != "v1.1.0" || !result.RestartRequired {
		t.Fatalf("InstallResult = %+v", result)
	}

	version, ok := store.InstalledVersion()
	if !ok || version != "v1.1.0" {
		t.Fatalf("InstalledVersion() = %q, %v; want v1.1.0, true", version, ok)
	}

	status, err := store.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if status.LastInstallError != "" || status.AvailableVersion != "" {
		t.Fatalf("status after install = %+v", status)
	}
}

func TestCheckerInstallRejectsStaleRequestedVersion(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	binaryContent := []byte("fake-binary")
	archive := buildTarGz(t, []tarEntry{
		{name: "strom-agent-v1.1.0-linux-arm64/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
	})
	artifact := Artifact{
		OS: "linux", Arch: "arm64",
		Filename:     "strom-agent-v1.1.0-linux-arm64.tar.gz",
		Size:         int64(len(archive)),
		SHA256:       sha256Hex(archive),
		BinarySHA256: sha256Hex(binaryContent),
	}
	server := fakeGitHubRelease(t, priv, "v1.1.0", artifact, archive)
	defer server.Close()

	store := NewStore(t.TempDir())
	github := &GitHubClient{BaseURL: server.URL, Repository: "owner/repo"}
	checker := newTestChecker(t, store, github, pub, "v1.0.0")

	if _, err := checker.Install(context.Background(), "v1.2.0"); err == nil {
		t.Fatal("expected error requesting a version that is not the currently available one")
	}
}

func TestCheckerInstallRejectsWhenUpToDate(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	binaryContent := []byte("fake-binary")
	archive := buildTarGz(t, []tarEntry{
		{name: "strom-agent-v1.0.0-linux-arm64/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
	})
	artifact := Artifact{
		OS: "linux", Arch: "arm64",
		Filename:     "strom-agent-v1.0.0-linux-arm64.tar.gz",
		Size:         int64(len(archive)),
		SHA256:       sha256Hex(archive),
		BinarySHA256: sha256Hex(binaryContent),
	}
	server := fakeGitHubRelease(t, priv, "v1.0.0", artifact, archive)
	defer server.Close()

	store := NewStore(t.TempDir())
	github := &GitHubClient{BaseURL: server.URL, Repository: "owner/repo"}
	checker := newTestChecker(t, store, github, pub, "v1.0.0")

	if _, err := checker.Install(context.Background(), "v1.0.0"); err == nil {
		t.Fatal("expected error installing when already up to date")
	}
}

func TestDefaultReleasePublicKeyIsValid(t *testing.T) {
	key := DefaultReleasePublicKey()
	if len(key) != ed25519.PublicKeySize {
		t.Fatalf("DefaultReleasePublicKey() length = %d, want %d", len(key), ed25519.PublicKeySize)
	}
}

func TestParseReleasePublicKeyHex(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	hexKey := fmt.Sprintf("%x", []byte(pub))
	parsed, err := ParseReleasePublicKeyHex(hexKey)
	if err != nil {
		t.Fatalf("ParseReleasePublicKeyHex: %v", err)
	}
	if !parsed.Equal(pub) {
		t.Fatal("parsed key does not match original")
	}

	if _, err := ParseReleasePublicKeyHex("not-hex"); err == nil {
		t.Fatal("expected error for invalid hex")
	}
	if _, err := ParseReleasePublicKeyHex("abcd"); err == nil {
		t.Fatal("expected error for wrong-length key")
	}
}
