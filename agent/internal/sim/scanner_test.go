package sim

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScannerScanParsesDevFixtures(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fixture := "device.mfr: APC\ndevice.model: Back-UPS BE1050G3\ndevice.serial: SIM123\nups.status: OL\n"
	if err := os.WriteFile(filepath.Join(dir, "node-a.dev"), []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	scanner := NewScanner(nil, dir, true)
	devices, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	device := devices[0]
	if device.Driver != "dummy-ups" {
		t.Fatalf("driver = %q, want dummy-ups", device.Driver)
	}
	if device.Serial != "SIM123" {
		t.Fatalf("serial = %q, want SIM123", device.Serial)
	}
	if device.Vendor != "APC" {
		t.Fatalf("vendor = %q, want APC", device.Vendor)
	}
	if device.Product != "Back-UPS BE1050G3" {
		t.Fatalf("product = %q, want model", device.Product)
	}
}

func TestScannerUsesFileNameFallbackSerial(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.dev"), []byte("ups.status: OL\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	scanner := NewScanner(nil, dir, true)
	devices, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	if devices[0].Serial != "demo" {
		t.Fatalf("serial = %q, want demo", devices[0].Serial)
	}
}
