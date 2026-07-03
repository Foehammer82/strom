package nutconf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseScannerOutputFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		fixture      string
		want         []DetectedUPS
		wantLogParts []string
	}{
		{
			name:    "zero devices",
			fixture: "empty.conf",
			want:    []DetectedUPS{},
		},
		{
			name:    "single device",
			fixture: "single-apc.conf",
			want: []DetectedUPS{{
				Driver:    "usbhid-ups",
				Port:      "auto",
				VendorID:  "051d",
				ProductID: "0002",
				Product:   "Back-UPS ES 1050G3",
				Serial:    "3B1519X12345",
				Vendor:    "American Power Conversion",
				Bus:       "001",
			}},
		},
		{
			name:    "multiple devices with fallback identity",
			fixture: "multi-apc-missing-serial.conf",
			want: []DetectedUPS{
				{
					Driver:    "usbhid-ups",
					Port:      "auto",
					VendorID:  "051d",
					ProductID: "0002",
					Product:   "Back-UPS ES 1050G3",
					Serial:    "3B1519X12345",
					Vendor:    "American Power Conversion",
					Bus:       "001",
				},
				{
					Driver:    "usbhid-ups",
					Port:      "auto",
					VendorID:  "051d",
					ProductID: "0002",
					Product:   "Back-UPS 900M",
					Serial:    "",
					Vendor:    "American Power Conversion",
					Bus:       "002",
				},
			},
			wantLogParts: []string{"missing serial", "bus=002", "vendorid=051d", "productid=0002"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fixturePath := filepath.Join("testdata", tt.fixture)
			file, err := os.Open(fixturePath)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer file.Close()

			var logs bytes.Buffer
			logger := log.New(&logs, "", 0)

			got, err := parseScannerOutput(file, logger)
			if err != nil {
				t.Fatalf("parseScannerOutput() error = %v", err)
			}

			if diff := compareDetectedUPS(got, tt.want); diff != "" {
				t.Fatalf("parseScannerOutput() mismatch:\n%s", diff)
			}

			for _, part := range tt.wantLogParts {
				if !strings.Contains(logs.String(), part) {
					t.Fatalf("expected log to contain %q, got %q", part, logs.String())
				}
			}
		})
	}
}

func TestParseScannerOutputRejectsMissingFallbackIdentity(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("[ups]\ndriver = \"usbhid-ups\"\nport = \"auto\"\nvendorid = \"051d\"\n")

	_, err := parseScannerOutput(input, log.New(io.Discard, "", 0))
	if err == nil || !strings.Contains(err.Error(), "missing serial and fallback identity fields") {
		t.Fatalf("expected fallback identity error, got %v", err)
	}
}

func TestScannerScanHandlesCommandFailures(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(log.New(io.Discard, "", 0))
	scanner.Path = "test-nut-scanner"
	scanner.Runner = fakeRunner{
		output: []byte("scanner blew up"),
		err:    errors.New("exit status 1"),
	}

	_, err := scanner.Scan(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "run test-nut-scanner -U -q") || !strings.Contains(err.Error(), "scanner blew up") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeRunner struct {
	output []byte
	err    error
}

func (f fakeRunner) CombinedOutput(_ context.Context, path string, args ...string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("missing path")
	}
	if len(args) != 2 || args[0] != "-U" || args[1] != "-q" {
		return nil, fmt.Errorf("unexpected args: %v", args)
	}
	return f.output, f.err
}

func compareDetectedUPS(got, want []DetectedUPS) string {
	if len(got) != len(want) {
		return fmt.Sprintf("length mismatch: got %d want %d\ngot=%#v\nwant=%#v", len(got), len(want), got, want)
	}

	for index := range want {
		if got[index] != want[index] {
			return fmt.Sprintf("entry %d mismatch: got %#v want %#v", index, got[index], want[index])
		}
	}

	return ""
}