package ca

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsurePersistsAndReusesCA(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first, err := Ensure(dir)
	if err != nil {
		t.Fatalf("Ensure() first error = %v", err)
	}
	if !strings.Contains(first.CAPEM(), "BEGIN CERTIFICATE") {
		t.Fatalf("CAPEM() = %q, want certificate PEM", first.CAPEM())
	}
	if _, err := Ensure(dir); err != nil {
		t.Fatalf("Ensure() second error = %v", err)
	}
	if _, err := Ensure(filepath.Join(dir, "nested")); err != nil {
		t.Fatalf("Ensure() nested error = %v", err)
	}
}
