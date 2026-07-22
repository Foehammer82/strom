package updates

import (
	"os"
	"testing"
)

func TestManualCrossLanguageManifestVerification(t *testing.T) {
	manifestBytes, err := os.ReadFile("../../../dist/release/strom-agent-manifest.json")
	if err != nil {
		t.Skipf("manifest not present: %v", err)
	}
	sig, err := os.ReadFile("../../../dist/release/strom-agent-manifest.json.sig")
	if err != nil {
		t.Fatalf("read sig: %v", err)
	}
	pub, err := ParseReleasePublicKeyHex("08489b5c6e4b5e7def8740a5747541c4edc5d7e1f242353e466013e39d529dc6")
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	if err := VerifyManifestSignature(pub, manifestBytes, sig); err != nil {
		t.Fatalf("verify: %v", err)
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	t.Logf("OK: verified manifest version=%s artifacts=%d", manifest.Version, len(manifest.Artifacts))
}
