// Package updates implements standalone node update discovery, verification,
// and durable installation for the Strom agent.
//
// The trust model is intentionally independent from the existing
// controller-signed OTA path (see agent/internal/api's "/api/agent/update"
// handler): a public Ed25519 "release key" authenticates official Strom
// GitHub releases, while the controller CA continues to authorize
// controller-pushed updates. Both paths converge on the same durable slot
// Installer in store.go so every delivery mechanism activates, health-checks,
// and rolls back updates identically.
package updates

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"strings"
)

// ManifestSchemaVersion is the only manifest schema version this build
// understands. Bumping it is a breaking change for older agents, so it
// should only change alongside a coordinated rollout plan.
const ManifestSchemaVersion = 1

// Artifact describes a single downloadable release archive for one
// architecture, as recorded in a signed release manifest.
type Artifact struct {
	// OS is the Go GOOS value the archive was built for, e.g. "linux".
	OS string `json:"os"`
	// Arch is the Go GOARCH value the archive was built for, e.g. "arm64"
	// or "arm".
	Arch string `json:"arch"`
	// GOARM is the ARM variant, only set when Arch is "arm" (e.g. "6").
	GOARM string `json:"goarm,omitempty"`
	// Filename is the exact GitHub release asset name for this archive.
	// It is matched by exact string equality against the release's asset
	// list; it is never used to construct a URL directly.
	Filename string `json:"filename"`
	// Size is the exact expected archive size in bytes.
	Size int64 `json:"size"`
	// SHA256 is the lowercase hex SHA-256 digest of the archive file.
	SHA256 string `json:"sha256"`
	// BinarySHA256 is the lowercase hex SHA-256 digest of the single
	// strom-agent executable extracted from the archive, verified
	// independently of the archive-level digest as defense in depth.
	BinarySHA256 string `json:"binary_sha256"`
}

// Manifest is the signed, versioned description of one stable Strom agent
// release, published as a release asset alongside the existing tarballs.
type Manifest struct {
	SchemaVersion int        `json:"schema_version"`
	Version       string     `json:"version"`
	KeyID         string     `json:"key_id"`
	Artifacts     []Artifact `json:"artifacts"`
}

// ArtifactFor returns the artifact matching the given GOOS/GOARCH/GOARM, or
// false if this manifest has no artifact for that target.
func (m Manifest) ArtifactFor(goos, goarch, goarm string) (Artifact, bool) {
	for _, artifact := range m.Artifacts {
		if artifact.OS != goos || artifact.Arch != goarch {
			continue
		}
		if artifact.Arch == "arm" && artifact.GOARM != goarm {
			continue
		}
		return artifact, true
	}
	return Artifact{}, false
}

// VerifyManifestSignature verifies a detached Ed25519 signature over the
// exact manifest bytes as published, using the embedded release public key.
// The manifest MUST be verified before any of its fields (including Version)
// are trusted for comparison or download decisions.
func VerifyManifestSignature(publicKey ed25519.PublicKey, manifestBytes, signature []byte) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid release public key size %d", len(publicKey))
	}
	if len(manifestBytes) == 0 {
		return fmt.Errorf("manifest payload is empty")
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("invalid manifest signature size %d", len(signature))
	}
	if !ed25519.Verify(publicKey, manifestBytes, signature) {
		return fmt.Errorf("manifest signature verification failed")
	}
	return nil
}

// ParseManifest decodes and validates the structural contract of a manifest.
// Callers must call VerifyManifestSignature on the same bytes first.
func ParseManifest(manifestBytes []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return Manifest{}, fmt.Errorf("unsupported manifest schema_version %d, want %d", manifest.SchemaVersion, ManifestSchemaVersion)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return Manifest{}, fmt.Errorf("manifest is missing version")
	}
	if len(manifest.Artifacts) == 0 {
		return Manifest{}, fmt.Errorf("manifest has no artifacts")
	}
	for _, artifact := range manifest.Artifacts {
		if strings.TrimSpace(artifact.Filename) == "" {
			return Manifest{}, fmt.Errorf("manifest artifact is missing filename")
		}
		if artifact.Size <= 0 {
			return Manifest{}, fmt.Errorf("manifest artifact %s has invalid size", artifact.Filename)
		}
		if len(artifact.SHA256) != 64 {
			return Manifest{}, fmt.Errorf("manifest artifact %s has invalid sha256", artifact.Filename)
		}
		if len(artifact.BinarySHA256) != 64 {
			return Manifest{}, fmt.Errorf("manifest artifact %s has invalid binary_sha256", artifact.Filename)
		}
	}
	return manifest, nil
}
