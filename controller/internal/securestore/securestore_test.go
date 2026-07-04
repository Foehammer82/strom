package securestore

import "testing"

func TestEnsureSealOpenRoundTrip(t *testing.T) {
	t.Parallel()

	store, err := Ensure(t.TempDir())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	sealed, err := store.SealString("secret-value")
	if err != nil {
		t.Fatalf("SealString() error = %v", err)
	}
	opened, err := store.OpenString(sealed)
	if err != nil {
		t.Fatalf("OpenString() error = %v", err)
	}
	if opened != "secret-value" {
		t.Fatalf("OpenString() = %q, want %q", opened, "secret-value")
	}
}
