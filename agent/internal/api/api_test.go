package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Foehammer82/wattkeeper/agent/internal/nutconf"
)

func TestHealthzReturnsAgentMetricsAndUPSStatus(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tempPath := filepath.Join(tempDir, "temp")
	if err := os.WriteFile(tempPath, []byte("42125\n"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	service := New(nil, Options{
		Version:     "1.2.3",
		Serial:      "abc1234",
		StartedAt:   time.Now().Add(-2 * time.Minute),
		Runner:      fakeRunner{outputs: map[string]commandResult{"upsc ups-a": {output: []byte("ups.status: OL\n")}, "upsc ups-b": {output: []byte("Error: Driver not connected\n"), err: errors.New("exit status 1")}}},
		CPUTempPath: tempPath,
		RootPath:    tempDir,
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-b", Driver: "blazer_usb"}, {Name: "ups-a", Driver: "usbhid-ups"}})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response.Version != "1.2.3" {
		t.Fatalf("Version = %q, want %q", response.Version, "1.2.3")
	}
	if response.Serial != "abc1234" {
		t.Fatalf("Serial = %q, want %q", response.Serial, "abc1234")
	}
	if response.UptimeSeconds < 119 {
		t.Fatalf("UptimeSeconds = %d, want >= 119", response.UptimeSeconds)
	}
	if response.CPUTemperatureCelsius == nil || *response.CPUTemperatureCelsius != 42.125 {
		t.Fatalf("CPUTemperatureCelsius = %v, want %v", response.CPUTemperatureCelsius, 42.125)
	}
	if response.DiskFreeBytes == 0 {
		t.Fatal("DiskFreeBytes = 0, want non-zero")
	}
	if len(response.UPSes) != 2 {
		t.Fatalf("UPS count = %d, want 2", len(response.UPSes))
	}
	if response.UPSes[0].Name != "ups-a" || response.UPSes[0].Status != "OL" {
		t.Fatalf("first UPS = %#v, want name/status ups-a/OL", response.UPSes[0])
	}
	if response.UPSes[1].Name != "ups-b" || response.UPSes[1].Status != startingStatus {
		t.Fatalf("second UPS = %#v, want name/status ups-b/%s", response.UPSes[1], startingStatus)
	}
}

func TestHealthzRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	service := New(nil, Options{RootPath: t.TempDir()})
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	recorder := httptest.NewRecorder()

	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func TestParseUPSStatusAcceptsColonAndEquals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "colon", output: "ups.status: OB DISCHRG\n", want: "OB DISCHRG"},
		{name: "equals", output: "ups.status = OL\n", want: "OL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseUPSStatus([]byte(tc.output))
			if err != nil {
				t.Fatalf("parseUPSStatus() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseUPSStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

type fakeRunner struct {
	outputs map[string]commandResult
}

type commandResult struct {
	output []byte
	err    error
}

func (f fakeRunner) CombinedOutput(_ context.Context, path string, args ...string) ([]byte, error) {
	key := path
	for _, arg := range args {
		key += " " + arg
	}
	result, ok := f.outputs[key]
	if !ok {
		return nil, errors.New("unexpected command: " + key)
	}
	return result.output, result.err
}
