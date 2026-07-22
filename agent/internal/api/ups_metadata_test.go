package api

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUPSMetadataRoundTripIsDeterministic(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ups-metadata.json")
	metadata := map[string]upsMetadata{
		"ups-b": {DisplayName: "Network UPS", Tags: []string{"Critical", "network", "NETWORK"}},
		"ups-a": {DisplayName: "NAS UPS"},
	}
	changed, err := saveUPSMetadata(path, metadata)
	if err != nil {
		t.Fatalf("save UPS metadata: %v", err)
	}
	if !changed {
		t.Fatal("first save changed = false, want true")
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved metadata: %v", err)
	}
	changed, err = saveUPSMetadata(path, metadata)
	if err != nil {
		t.Fatalf("save unchanged UPS metadata: %v", err)
	}
	if changed {
		t.Fatal("unchanged save changed = true, want false")
	}

	loaded, err := loadUPSMetadata(path)
	if err != nil {
		t.Fatalf("load UPS metadata: %v", err)
	}
	want := map[string]upsMetadata{
		"ups-b": {DisplayName: "Network UPS", Tags: []string{"Critical", "network"}},
		"ups-a": {DisplayName: "NAS UPS"},
	}
	if !reflect.DeepEqual(loaded, want) {
		t.Fatalf("loaded metadata = %#v, want %#v", loaded, want)
	}
	if !strings.Contains(string(first), "\"ups-a\"") || !strings.Contains(string(first), "\"ups-b\"") {
		t.Fatalf("saved metadata was not deterministic JSON: %s", first)
	}
}

func TestLoadUPSMetadataHandlesMissingAndInvalidFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ups-metadata.json")
	missing, err := loadUPSMetadata(path)
	if err != nil {
		t.Fatalf("load missing metadata: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing metadata = %#v, want empty", missing)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write invalid metadata: %v", err)
	}
	if _, err := loadUPSMetadata(path); err == nil {
		t.Fatal("load invalid metadata error = nil, want error")
	}
}

func TestNormalizeUPSMetadataRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, metadata := range []upsMetadata{
		{DisplayName: strings.Repeat("a", maxUPSMetadataText+1)},
		{DisplayName: "NAS\nSwitch"},
		{Tags: make([]string, maxUPSMetadataTags+1)},
	} {
		if _, err := normalizeUPSMetadata(metadata); err == nil {
			t.Fatalf("normalizeUPSMetadata(%#v) error = nil, want error", metadata)
		}
	}
}
