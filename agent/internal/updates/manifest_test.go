package updates

import (
	"crypto/ed25519"
	"testing"
)

func TestParseStableVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		{name: "valid", version: "v1.2.3", wantErr: false},
		{name: "valid zero", version: "v0.1.0", wantErr: false},
		{name: "missing v prefix", version: "1.2.3", wantErr: true},
		{name: "prerelease rc", version: "v1.2.3-rc1", wantErr: true},
		{name: "prerelease beta", version: "v1.2.3-beta1", wantErr: true},
		{name: "dev placeholder", version: "dev", wantErr: true},
		{name: "empty", version: "", wantErr: true},
		{name: "extra segment", version: "v1.2.3.4", wantErr: true},
		{name: "non numeric", version: "vX.Y.Z", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseStableVersion(tt.version)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseStableVersion(%q) error = %v, wantErr %v", tt.version, err, tt.wantErr)
			}
		})
	}
}

func TestIsNewerStableVersion(t *testing.T) {
	tests := []struct {
		name      string
		candidate string
		installed string
		want      bool
		wantErr   bool
	}{
		{name: "patch newer", candidate: "v1.2.4", installed: "v1.2.3", want: true},
		{name: "minor newer", candidate: "v1.3.0", installed: "v1.2.9", want: true},
		{name: "major newer", candidate: "v2.0.0", installed: "v1.9.9", want: true},
		{name: "equal", candidate: "v1.2.3", installed: "v1.2.3", want: false},
		{name: "older", candidate: "v1.2.2", installed: "v1.2.3", want: false},
		{name: "candidate prerelease errors", candidate: "v1.2.3-rc1", installed: "v1.2.2", wantErr: true},
		{name: "installed dev errors", candidate: "v1.2.3", installed: "dev", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsNewerStableVersion(tt.candidate, tt.installed)
			if (err != nil) != tt.wantErr {
				t.Fatalf("IsNewerStableVersion error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("IsNewerStableVersion(%q, %q) = %v, want %v", tt.candidate, tt.installed, got, tt.want)
			}
		})
	}
}

func TestVerifyManifestSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	payload := []byte(`{"schema_version":1,"version":"v1.0.0"}`)
	sig := ed25519.Sign(priv, payload)

	t.Run("valid signature", func(t *testing.T) {
		if err := VerifyManifestSignature(pub, payload, sig); err != nil {
			t.Fatalf("expected valid signature, got error: %v", err)
		}
	})

	t.Run("tampered payload", func(t *testing.T) {
		tampered := append([]byte(nil), payload...)
		tampered[0] = 'X'
		if err := VerifyManifestSignature(pub, tampered, sig); err == nil {
			t.Fatal("expected signature verification to fail for tampered payload")
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		otherPub, _, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		if err := VerifyManifestSignature(otherPub, payload, sig); err == nil {
			t.Fatal("expected signature verification to fail for wrong key")
		}
	})

	t.Run("empty payload", func(t *testing.T) {
		if err := VerifyManifestSignature(pub, nil, sig); err == nil {
			t.Fatal("expected error for empty payload")
		}
	})

	t.Run("bad signature size", func(t *testing.T) {
		if err := VerifyManifestSignature(pub, payload, []byte("short")); err == nil {
			t.Fatal("expected error for malformed signature size")
		}
	})
}

func TestParseManifest(t *testing.T) {
	valid := `{
		"schema_version": 1,
		"version": "v1.2.3",
		"key_id": "key-1",
		"artifacts": [
			{"os": "linux", "arch": "arm64", "filename": "a.tar.gz", "size": 10, "sha256": "` + repeatHex("a", 64) + `", "binary_sha256": "` + repeatHex("b", 64) + `"}
		]
	}`
	if _, err := ParseManifest([]byte(valid)); err != nil {
		t.Fatalf("expected valid manifest to parse, got %v", err)
	}

	tests := []struct {
		name string
		json string
	}{
		{name: "wrong schema version", json: `{"schema_version":2,"version":"v1.0.0","artifacts":[{"filename":"a","size":1,"sha256":"` + repeatHex("a", 64) + `","binary_sha256":"` + repeatHex("b", 64) + `"}]}`},
		{name: "missing version", json: `{"schema_version":1,"artifacts":[{"filename":"a","size":1,"sha256":"` + repeatHex("a", 64) + `","binary_sha256":"` + repeatHex("b", 64) + `"}]}`},
		{name: "no artifacts", json: `{"schema_version":1,"version":"v1.0.0","artifacts":[]}`},
		{name: "missing filename", json: `{"schema_version":1,"version":"v1.0.0","artifacts":[{"size":1,"sha256":"` + repeatHex("a", 64) + `","binary_sha256":"` + repeatHex("b", 64) + `"}]}`},
		{name: "zero size", json: `{"schema_version":1,"version":"v1.0.0","artifacts":[{"filename":"a","size":0,"sha256":"` + repeatHex("a", 64) + `","binary_sha256":"` + repeatHex("b", 64) + `"}]}`},
		{name: "short sha256", json: `{"schema_version":1,"version":"v1.0.0","artifacts":[{"filename":"a","size":1,"sha256":"abc","binary_sha256":"` + repeatHex("b", 64) + `"}]}`},
		{name: "invalid json", json: `not json`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tt.json)); err == nil {
				t.Fatalf("expected error parsing manifest: %s", tt.json)
			}
		})
	}
}

func TestManifestArtifactFor(t *testing.T) {
	manifest := Manifest{
		Artifacts: []Artifact{
			{OS: "linux", Arch: "arm64", Filename: "arm64.tar.gz"},
			{OS: "linux", Arch: "arm", GOARM: "6", Filename: "armv6.tar.gz"},
		},
	}

	if _, ok := manifest.ArtifactFor("linux", "arm64", ""); !ok {
		t.Fatal("expected arm64 artifact to be found")
	}
	if _, ok := manifest.ArtifactFor("linux", "arm", "6"); !ok {
		t.Fatal("expected armv6 artifact to be found")
	}
	if _, ok := manifest.ArtifactFor("linux", "arm", "7"); ok {
		t.Fatal("expected armv7 to not match armv6-only artifact")
	}
	if _, ok := manifest.ArtifactFor("darwin", "arm64", ""); ok {
		t.Fatal("expected darwin to not match linux-only artifacts")
	}
}

func repeatHex(s string, n int) string {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, s...)
	}
	return string(out[:n])
}
