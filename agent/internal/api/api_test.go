package api

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Foehammer82/strom/agent/internal/nutconf"
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
		DisableAuth: true,
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

	service := New(nil, Options{RootPath: t.TempDir(), DisableAuth: true})
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

func TestAPIAboutReturnsNodeAndInstalledSoftwareDetails(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	writeAboutFixture(t, tempDir, "etc/os-release", "PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nVERSION_ID=\"13\"\n")
	writeAboutFixture(t, tempDir, "proc/sys/kernel/osrelease", "6.12.0-v8+\n")
	writeAboutFixture(t, tempDir, "var/lib/dpkg/status", "Package: systemd\nStatus: install ok installed\nVersion: 257.7-1\nArchitecture: arm64\n\nPackage: nut-server\nStatus: install ok installed\nVersion: 2.8.3-1\nArchitecture: arm64\n\nPackage: ignored\nStatus: deinstall ok config-files\nVersion: 1.0\n\n")

	service := New(nil, Options{
		Version:     "1.2.3",
		Serial:      "serial-1",
		StartedAt:   time.Now().Add(-2 * time.Minute),
		RootPath:    tempDir,
		DisableAuth: true,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/about", nil)
	recorder := httptest.NewRecorder()

	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response aboutResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Version != "1.2.3" || response.Serial != "serial-1" {
		t.Fatalf("identity = %#v, want version and serial", response)
	}
	if response.OperatingSystem.Name != "Debian GNU/Linux 13 (trixie)" || response.Kernel != "6.12.0-v8+" {
		t.Fatalf("host information = %#v, want fixture values", response)
	}
	if len(response.DebianPackages) != 2 || response.DebianPackages[0].Name != "nut-server" || response.DebianPackages[1].Name != "systemd" {
		t.Fatalf("DebianPackages = %#v, want installed packages sorted by name", response.DebianPackages)
	}
	if len(response.Featured) < 2 {
		t.Fatalf("Featured = %#v, want runtime acknowledgments", response.Featured)
	}
}

func TestAPIAboutRequiresLocalSession(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/about", nil)
	unauthenticatedRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(unauthenticatedRecorder, unauthenticated)
	if unauthenticatedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthenticatedRecorder.Code, http.StatusUnauthorized)
	}

	cookies := loginAsDefaultAdmin(t, service)
	authenticated := httptest.NewRequest(http.MethodGet, "/api/about", nil)
	authenticated.AddCookie(cookieByName(cookies, sessionCookieName))
	authenticatedRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(authenticatedRecorder, authenticated)
	if authenticatedRecorder.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want %d body=%s", authenticatedRecorder.Code, http.StatusOK, authenticatedRecorder.Body.String())
	}
}

func writeAboutFixture(t *testing.T, rootPath, relativePath, content string) {
	t.Helper()
	path := filepath.Join(rootPath, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", relativePath, err)
	}
}

func TestAPIDiagnosticsReturnsUSBAndNUTChecks(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "etc", "nut")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "ups.conf"), []byte("[ups-a]\ndriver = usbhid-ups\n"), 0o600); err != nil {
		t.Fatalf("write ups.conf: %v", err)
	}

	service := New(nil, Options{
		RootPath:    tempDir,
		DisableAuth: true,
		Runner: fakeRunner{outputs: map[string]commandResult{
			"lsusb":                          {output: []byte("Bus 001 Device 002: ID 051d:0002 APC UPS\n")},
			"nut-scanner -U -q":              {output: []byte("[nutdev1]\ndriver = usbhid-ups\nport = auto\n")},
			"systemctl is-active nut-server": {output: []byte("active\n")},
		}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response diagnosticsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(response.USBDevices.Output, "APC UPS") {
		t.Fatalf("USB devices = %q, want APC UPS", response.USBDevices.Output)
	}
	if !strings.Contains(response.Scanner.Output, "usbhid-ups") {
		t.Fatalf("scanner = %q, want usbhid-ups", response.Scanner.Output)
	}
	if response.NUTServer.Output != "active" || response.NUTServer.Error != "" {
		t.Fatalf("NUT server = %#v, want active with no error", response.NUTServer)
	}
	if !strings.Contains(response.NUTConfig.Output, "driver = usbhid-ups") {
		t.Fatalf("ups.conf = %q, want driver", response.NUTConfig.Output)
	}
}

func TestDiagnosticsPageIncludesResponsiveNavigation(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json"), Runner: fakeRunner{}})
	cookies := loginAsDefaultAdmin(t, service)
	cookie := cookieByName(cookies, sessionCookieName)

	req := httptest.NewRequest(http.MethodGet, "/diagnostics", nil)
	req.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"diagnostics-menu-toggle",
		"diagnostics-profile-menu",
		"strom-theme-preference",
		"data-about-open",
		"about-dialog",
		"acknowledgements-dialog",
		"/assets/about.js",
		"aria-label=\"Docs (opens in a new tab)\"",
		"menu-link-icon-wrap",
		"M14 5h5v5M19 5l-9 9M19 14v5H5V5h5",
		"event.stopPropagation(); toggleMenu();",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("diagnostics page missing %q: %s", want, body)
		}
	}
}

func TestStatusReturnsBasicPublicPayload(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{
		Version:     "2.0.0",
		Serial:      "serial-1",
		StartedAt:   time.Now().Add(-30 * time.Second),
		Runner:      fakeRunner{outputs: map[string]commandResult{"upsc ups-a": {output: []byte("ups.status: OL\n")}, "upsc ups-b": {output: []byte("Error: Driver not connected\n"), err: errors.New("exit status 1")}}},
		RootPath:    tempDir,
		DisableAuth: true,
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-b", Driver: "blazer_usb"}, {Name: "ups-a", Driver: "usbhid-ups"}})

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var response statusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "degraded" {
		t.Fatalf("Status = %q, want %q", response.Status, "degraded")
	}
	if response.UPSCount != 2 {
		t.Fatalf("UPSCount = %d, want %d", response.UPSCount, 2)
	}

	var raw map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	for _, forbidden := range []string{"version", "serial", "uptime_seconds", "cpu_temperature_celsius", "disk_free_bytes", "upses"} {
		if _, ok := raw[forbidden]; ok {
			t.Fatalf("public status should not expose %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestStatusDetailsReturnsRichPayload(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tempPath := filepath.Join(tempDir, "temp")
	if err := os.WriteFile(tempPath, []byte("42125\n"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	service := New(nil, Options{
		Version:     "2.0.0",
		Serial:      "serial-1",
		StartedAt:   time.Now().Add(-30 * time.Second),
		Runner:      fakeRunner{outputs: map[string]commandResult{"upsc ups-a": {output: []byte("ups.status: OL\n")}}},
		CPUTempPath: tempPath,
		RootPath:    tempDir,
		DisableAuth: true,
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	req := httptest.NewRequest(http.MethodGet, "/status/details", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Version != "2.0.0" {
		t.Fatalf("Version = %q, want %q", response.Version, "2.0.0")
	}
	if response.Serial != "serial-1" {
		t.Fatalf("Serial = %q, want %q", response.Serial, "serial-1")
	}
	if len(response.UPSes) != 1 || response.UPSes[0].Name != "ups-a" || response.UPSes[0].Driver != "usbhid-ups" {
		t.Fatalf("UPSes = %#v, want one rich ups entry", response.UPSes)
	}
}

func TestIndexRendersHTMLDashboard(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{
		Version:     "2.0.0",
		Serial:      "serial-1",
		StartedAt:   time.Now().Add(-30 * time.Second),
		Runner:      fakeRunner{outputs: map[string]commandResult{"upsc ups-a": {output: []byte("ups.status: OL\n")}}},
		RootPath:    tempDir,
		DisableAuth: true,
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"Strom Node",
		"Refresh",
		"/assets/app.js",
		"/assets/about.js",
		"/assets/styles.css",
		"data-about-open",
		"about-dialog",
		"acknowledgements-dialog",
		"ups-a",
		"usbhid-ups",
		"OL",
		"aria-label=\"Docs (opens in a new tab)\"",
		"menu-link-icon-wrap",
		"M14 5h5v5M19 5l-9 9M19 14v5H5V5h5",
		"ups-metadata-modal",
		"ups-metadata-form",
		"ups-metadata-display-name",
		"ups-metadata-tags",
		"Save details",
		"raw-json-copy",
		"Copy JSON",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestAuthenticatedIndexRendersCompleteDashboard(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json"), Runner: fakeRunner{outputs: map[string]commandResult{"upsc ups-a": {output: []byte("ups.status: OL\n")}}}})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})
	cookies := loginAsDefaultAdmin(t, service)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookieByName(cookies, sessionCookieName))
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{"action=\"/auth/logout\"", "name=\"csrf_token\"", "ups-metadata-modal", "/assets/app.js", "/assets/about.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestAPIUPSDetailReturnsMetricsVariablesAndCommands(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{
		Version:     "2.0.0",
		Serial:      "serial-1",
		StartedAt:   time.Now().Add(-30 * time.Second),
		RootPath:    tempDir,
		DisableAuth: true,
		Runner: fakeRunner{outputs: map[string]commandResult{
			"upsc -j ups-a": {
				output: []byte(`{"ups.status":"OL","battery.charge":"97","battery.runtime":"1870","input.voltage":"120.5","output.voltage":"120.1","ups.load":"22"}`),
			},
			"upscmd -l ups-a": {
				output: []byte("beeper.toggle - Toggle beeper\nshutdown.return - Shutdown and restore on utility return\n"),
			},
		}},
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	req := httptest.NewRequest(http.MethodGet, "/api/ups/ups-a", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response upsDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Name != "ups-a" || response.Driver != "usbhid-ups" || response.Status != "OL" {
		t.Fatalf("detail = %#v, want ups-a/usbhid-ups/OL", response)
	}
	if response.Metrics.BatteryChargePercent == nil || *response.Metrics.BatteryChargePercent != 97 {
		t.Fatalf("battery charge = %v, want 97", response.Metrics.BatteryChargePercent)
	}
	if got := response.Variables["input.voltage"]; got != "120.5" {
		t.Fatalf("input.voltage = %q, want %q", got, "120.5")
	}
	if len(response.Commands) != 2 || response.Commands[1].Name != "shutdown.return" || !response.Commands[1].Destructive {
		t.Fatalf("commands = %#v, want destructive shutdown.return", response.Commands)
	}
}

func TestAPIUPSCommandExecutesSupportedCommand(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{
		StartedAt:   time.Now().Add(-30 * time.Second),
		RootPath:    tempDir,
		DisableAuth: true,
		NUTUser:     "agent",
		NUTPassword: "secret",
		Runner: fakeRunner{outputs: map[string]commandResult{
			"upscmd -l ups-a": {
				output: []byte("test.battery.start.quick - Start a quick self test\n"),
			},
			"upscmd -u agent -p secret -w ups-a test.battery.start.quick": {
				output: []byte("OK\n"),
			},
		}},
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	req := httptest.NewRequest(http.MethodPost, "/api/ups/ups-a/command", strings.NewReader(`{"cmd":"test.battery.start.quick"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response upsCommandResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UPS != "ups-a" || response.Command != "test.battery.start.quick" || response.Output != "OK" {
		t.Fatalf("command response = %#v, want ups-a/test.battery.start.quick/OK", response)
	}
}

func TestAPIUPSDetailReturnsWritableVariables(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{
		StartedAt:   time.Now().Add(-30 * time.Second),
		RootPath:    tempDir,
		DisableAuth: true,
		Runner: fakeRunner{outputs: map[string]commandResult{
			"upsc -j ups-a": {
				output: []byte(`{"ups.status":"OL","input.transfer.high":"136","ups.beeper.status":"enabled"}`),
			},
			"upscmd -l ups-a": {output: []byte("")},
			"upsrw -l ups-a": {
				output: []byte("input.transfer.high: High transfer voltage\nType: RANGE\nRange: 127..144\nValue: 136\n\nups.beeper.status: Audible alarm\nType: ENUM\nOption: enabled\nOption: disabled\nValue: enabled\n"),
			},
		}},
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	req := httptest.NewRequest(http.MethodGet, "/api/ups/ups-a", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response upsDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Writable) != 2 {
		t.Fatalf("writable = %#v, want 2 entries", response.Writable)
	}
	if response.Writable[0].Name != "input.transfer.high" || response.Writable[0].Editor != "number" {
		t.Fatalf("first writable = %#v, want input.transfer.high number editor", response.Writable[0])
	}
	if response.Writable[1].Name != "ups.beeper.status" || response.Writable[1].Editor != "select" || len(response.Writable[1].Options) != 2 {
		t.Fatalf("second writable = %#v, want select options", response.Writable[1])
	}
}

func TestAPIUPSSetVarExecutesSupportedVariable(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{
		StartedAt:   time.Now().Add(-30 * time.Second),
		RootPath:    tempDir,
		DisableAuth: true,
		NUTUser:     "agent",
		NUTPassword: "secret",
		Runner: fakeRunner{outputs: map[string]commandResult{
			"upsc -j ups-a": {
				output: []byte(`{"ups.status":"OL","input.transfer.high":"136"}`),
			},
			"upsrw -l ups-a": {
				output: []byte("input.transfer.high: High transfer voltage\nType: RANGE\nRange: 127..144\nValue: 136\n"),
			},
			"upsrw -s input.transfer.high=140 -u agent -p secret -w ups-a": {
				output: []byte("OK\n"),
			},
		}},
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	req := httptest.NewRequest(http.MethodPost, "/api/ups/ups-a/setvar", strings.NewReader(`{"var":"input.transfer.high","value":"140"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response upsSetVarResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Variable != "input.transfer.high" || response.Value != "140" || response.Output != "OK" {
		t.Fatalf("setvar response = %#v, want input.transfer.high/140/OK", response)
	}
}

func TestAPIUPSMetadataUpdatePersistsAndReturnsDetail(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	metadataPath := filepath.Join(tempDir, "ups-metadata.json")
	service := New(nil, Options{RootPath: tempDir, UPSMetadataPath: metadataPath, DisableAuth: true, Runner: fakeRunner{outputs: map[string]commandResult{
		"upsc -j ups-a":   {output: []byte(`{"ups.status":"OL"}`)},
		"upscmd -l ups-a": {output: []byte("")},
		"upsrw -l ups-a":  {output: []byte("")},
	}}})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	update := httptest.NewRequest(http.MethodPatch, "/api/ups/ups-a/metadata", strings.NewReader(`{"display_name":"Network UPS","tags":["network","Critical","NETWORK"]}`))
	update.Header.Set("Content-Type", "application/json")
	updateRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(updateRecorder, update)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("metadata update status = %d, want %d body=%s", updateRecorder.Code, http.StatusOK, updateRecorder.Body.String())
	}

	detail := httptest.NewRequest(http.MethodGet, "/api/ups/ups-a", nil)
	detailRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d body=%s", detailRecorder.Code, http.StatusOK, detailRecorder.Body.String())
	}
	var response upsDetailResponse
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if response.Metadata.DisplayName != "Network UPS" || !reflect.DeepEqual(response.Metadata.Tags, []string{"Critical", "network"}) {
		t.Fatalf("metadata = %#v", response.Metadata)
	}
	loaded, err := loadUPSMetadata(metadataPath)
	if err != nil || loaded["ups-a"].DisplayName != "Network UPS" {
		t.Fatalf("persisted metadata = %#v, err=%v", loaded, err)
	}
}

func TestAdoptAppliesProvisioningAndReturnsMetadata(t *testing.T) {
	t.Parallel()

	service := New(nil, Options{
		Serial:      "serial-1234",
		Version:     "v0.3.0",
		RootPath:    t.TempDir(),
		DisableAuth: true,
		Adopter:     fakeAdopter{response: adoptResponse{Serial: "serial-1234", Version: "v0.3.0", ControllerURL: "https://controller.local", TokenSHA256: tokenSHA256Hex("token")}},
	})

	req := httptest.NewRequest(http.MethodPost, "/adopt", strings.NewReader(`{"ca_pem":"pem","nut_user":"controller","nut_password":"secret","api_token":"token","controller_url":"https://controller.local"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response adoptResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Serial != "serial-1234" || response.ControllerURL != "https://controller.local" || response.TokenSHA256 != tokenSHA256Hex("token") {
		t.Fatalf("adopt response = %#v, want serial/controller/token hash", response)
	}
}

func TestAdoptedBearerTokenCanRunUPSCommandWithoutSession(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	adoptionPath := filepath.Join(tempDir, "adoption.json")
	if err := os.WriteFile(adoptionPath, []byte(`{"token_sha256":"`+tokenSHA256Hex("controller-token")+`"}`), 0o600); err != nil {
		t.Fatalf("write adoption config: %v", err)
	}
	service := New(nil, Options{
		StartedAt:    time.Now().Add(-30 * time.Second),
		RootPath:     tempDir,
		AdoptionPath: adoptionPath,
		AuthPath:     filepath.Join(tempDir, "webui-auth.json"),
		NUTUser:      "controller",
		NUTPassword:  "secret",
		Runner: fakeRunner{outputs: map[string]commandResult{
			"upscmd -l ups-a": {output: []byte("beeper.toggle - Toggle beeper\n")},
			"upscmd -u controller -p secret -w ups-a beeper.toggle": {output: []byte("OK\n")},
		}},
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	req := httptest.NewRequest(http.MethodPost, "/api/ups/ups-a/command", strings.NewReader(`{"cmd":"beeper.toggle"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer controller-token")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestAgentUpdateRequiresValidControllerSignature(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	adoptionPath := filepath.Join(tempDir, "adoption.json")
	updatesRoot := filepath.Join(tempDir, "updates")

	caPEM, signer := testGenerateControllerCA(t)
	controllerToken := "controller-token"
	adoptionPayload := []byte(`{"token_sha256":"` + tokenSHA256Hex(controllerToken) + `","ca_pem":` + strconv.Quote(caPEM) + `}`)
	if err := os.WriteFile(adoptionPath, adoptionPayload, 0o600); err != nil {
		t.Fatalf("write adoption config: %v", err)
	}

	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json"), AdoptionPath: adoptionPath, UpdatesRoot: updatesRoot})

	binaryPayload := []byte("new-agent-bytes")
	digest := sha256.Sum256(binaryPayload)
	validSignature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		t.Fatalf("sign digest: %v", err)
	}

	badRequest := httptest.NewRequest(http.MethodPost, "/api/agent/update", strings.NewReader(`{"version":"v0.4.0","binary_base64":"`+base64.StdEncoding.EncodeToString(binaryPayload)+`","sha256":"`+fmt.Sprintf("%x", digest[:])+`","signature_base64":"`+base64.StdEncoding.EncodeToString([]byte("bad-signature"))+`"}`))
	badRequest.Header.Set("Content-Type", "application/json")
	badRequest.Header.Set("Authorization", "Bearer "+controllerToken)
	badRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(badRecorder, badRequest)
	if badRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d, want %d body=%s", badRecorder.Code, http.StatusUnauthorized, badRecorder.Body.String())
	}

	goodRequest := httptest.NewRequest(http.MethodPost, "/api/agent/update", strings.NewReader(`{"version":"v0.4.0","binary_base64":"`+base64.StdEncoding.EncodeToString(binaryPayload)+`","sha256":"`+fmt.Sprintf("%x", digest[:])+`","signature_base64":"`+base64.StdEncoding.EncodeToString(validSignature)+`"}`))
	goodRequest.Header.Set("Content-Type", "application/json")
	goodRequest.Header.Set("Authorization", "Bearer "+controllerToken)
	goodRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(goodRecorder, goodRequest)
	if goodRecorder.Code != http.StatusOK {
		t.Fatalf("good signature status = %d, want %d body=%s", goodRecorder.Code, http.StatusOK, goodRecorder.Body.String())
	}

	version, ok := service.updatesStore.InstalledVersion()
	if !ok || version != "v0.4.0" {
		t.Fatalf("InstalledVersion() = %q, %v; want v0.4.0, true", version, ok)
	}
	content, err := os.ReadFile(filepath.Join(updatesRoot, "releases", "v0.4.0", "strom-agent"))
	if err != nil {
		t.Fatalf("read staged agent binary: %v", err)
	}
	if string(content) != string(binaryPayload) {
		t.Fatalf("staged agent binary = %q, want %q", string(content), string(binaryPayload))
	}
}

func TestIndexReturnsNotFoundForUnknownPaths(t *testing.T) {
	t.Parallel()

	service := New(nil, Options{RootPath: t.TempDir(), DisableAuth: true})
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	recorder := httptest.NewRecorder()

	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestLogoutRequiresCSRFProtectedPOST(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})
	cookies := loginAsDefaultAdmin(t, service)
	sessionCookie := cookieByName(cookies, sessionCookieName)
	csrfCookie := cookieByName(cookies, csrfCookieName)

	getRequest := httptest.NewRequest(http.MethodGet, "/auth/logout", nil)
	getRequest.Header.Set("Accept", "text/html")
	getRequest.AddCookie(sessionCookie)
	getRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET logout status = %d, want %d", getRecorder.Code, http.StatusMethodNotAllowed)
	}

	badRequest := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader("csrf_token=invalid"))
	badRequest.Header.Set("Accept", "text/html")
	badRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRequest.AddCookie(sessionCookie)
	badRequest.AddCookie(csrfCookie)
	badRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(badRecorder, badRequest)
	if badRecorder.Code != http.StatusForbidden {
		t.Fatalf("invalid CSRF logout status = %d, want %d", badRecorder.Code, http.StatusForbidden)
	}

	postRequest := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader("csrf_token="+url.QueryEscape(csrfCookie.Value)))
	postRequest.Header.Set("Accept", "text/html")
	postRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postRequest.AddCookie(sessionCookie)
	postRequest.AddCookie(csrfCookie)
	postRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(postRecorder, postRequest)
	if postRecorder.Code != http.StatusSeeOther {
		t.Fatalf("POST logout status = %d, want %d body=%s", postRecorder.Code, http.StatusSeeOther, postRecorder.Body.String())
	}
	if location := postRecorder.Header().Get("Location"); location != "/auth/login" {
		t.Fatalf("logout redirect = %q, want /auth/login", location)
	}

	indexRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRequest.Header.Set("Accept", "text/html")
	indexRequest.AddCookie(sessionCookie)
	indexRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(indexRecorder, indexRequest)
	if indexRecorder.Code == http.StatusOK {
		t.Fatalf("logged-out session remained usable: status=%d body=%s", indexRecorder.Code, indexRecorder.Body.String())
	}
}

func TestIndexRedirectsToBootstrapOnFreshNode(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	recorder := httptest.NewRecorder()

	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusSeeOther)
	}
	if location := recorder.Header().Get("Location"); location != "/auth/bootstrap" {
		t.Fatalf("location = %q, want %q", location, "/auth/bootstrap")
	}

	bootstrapPage := httptest.NewRequest(http.MethodGet, "/auth/bootstrap", nil)
	bootstrapPage.Header.Set("Accept", "text/html")
	bootstrapPageRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(bootstrapPageRecorder, bootstrapPage)
	if bootstrapPageRecorder.Code != http.StatusOK {
		t.Fatalf("bootstrap page status = %d, want %d", bootstrapPageRecorder.Code, http.StatusOK)
	}
	body := bootstrapPageRecorder.Body.String()
	for _, want := range []string{"Set Admin Password", "admin"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestBootstrapCreatesAdminAccountAndSession(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	cookies := loginAsDefaultAdmin(t, service)
	sessionCookie := cookieByName(cookies, sessionCookieName)
	if sessionCookie == nil {
		t.Fatal("expected bootstrap to issue a session cookie")
	}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.AddCookie(sessionCookie)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", recorder.Code, http.StatusOK)
	}

	// Bootstrapping again must fail now that the account exists.
	replay := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{"new_password":"`+testAdminPassword+`","confirm_password":"`+testAdminPassword+`"}`))
	replay.Header.Set("Content-Type", "application/json")
	replayRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(replayRecorder, replay)
	if replayRecorder.Code != http.StatusConflict {
		t.Fatalf("re-bootstrap status = %d, want %d body=%s", replayRecorder.Code, http.StatusConflict, replayRecorder.Body.String())
	}
}

func TestBootstrapRejectsMismatchedPasswords(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	mismatch := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{"new_password":"`+testAdminPassword+`","confirm_password":"something-else"}`))
	mismatch.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, mismatch)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("mismatched bootstrap status = %d, want %d body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestBootstrapHTMLFormRequiresCSRFToken(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	form := "new_password=" + testAdminPassword + "&confirm_password=" + testAdminPassword
	missingToken := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(form))
	missingToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingToken.Header.Set("Accept", "text/html")
	missingTokenRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(missingTokenRecorder, missingToken)
	if missingTokenRecorder.Code != http.StatusForbidden {
		t.Fatalf("bootstrap form without csrf status = %d, want %d body=%s", missingTokenRecorder.Code, http.StatusForbidden, missingTokenRecorder.Body.String())
	}

	bootstrapPage := httptest.NewRequest(http.MethodGet, "/auth/bootstrap", nil)
	bootstrapPageRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(bootstrapPageRecorder, bootstrapPage)
	csrfCookie := cookieByName(bootstrapPageRecorder.Result().Cookies(), csrfCookieName)
	if csrfCookie == nil {
		t.Fatal("expected bootstrap page csrf cookie")
	}

	withToken := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(form+"&csrf_token="+csrfCookie.Value))
	withToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	withToken.Header.Set("Accept", "text/html")
	withToken.AddCookie(csrfCookie)
	withTokenRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(withTokenRecorder, withToken)
	if withTokenRecorder.Code != http.StatusSeeOther {
		t.Fatalf("bootstrap form with csrf status = %d, want %d body=%s", withTokenRecorder.Code, http.StatusSeeOther, withTokenRecorder.Body.String())
	}
}

func TestChangePasswordRequiresCSRFToken(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	cookies := loginAsDefaultAdmin(t, service)
	sessionCookie := cookieByName(cookies, sessionCookieName)
	csrfCookie := cookieByName(cookies, csrfCookieName)
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("expected session and csrf cookies, got session=%v csrf=%v", sessionCookie != nil, csrfCookie != nil)
	}

	form := "current_password=" + testAdminPassword + "&new_password=another-strong-pass&confirm_password=another-strong-pass"

	missingToken := httptest.NewRequest(http.MethodPost, "/settings/password", strings.NewReader(form))
	missingToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingToken.Header.Set("Accept", "text/html")
	missingToken.AddCookie(sessionCookie)
	missingTokenRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(missingTokenRecorder, missingToken)
	if missingTokenRecorder.Code != http.StatusForbidden {
		t.Fatalf("change password without csrf status = %d, want %d body=%s", missingTokenRecorder.Code, http.StatusForbidden, missingTokenRecorder.Body.String())
	}

	withToken := httptest.NewRequest(http.MethodPost, "/settings/password", strings.NewReader(form+"&csrf_token="+csrfCookie.Value))
	withToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	withToken.Header.Set("Accept", "text/html")
	withToken.AddCookie(sessionCookie)
	withTokenRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(withTokenRecorder, withToken)
	if withTokenRecorder.Code != http.StatusSeeOther {
		t.Fatalf("change password with csrf status = %d, want %d body=%s", withTokenRecorder.Code, http.StatusSeeOther, withTokenRecorder.Body.String())
	}
}

func TestSessionProtectsDetailedRoutes(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tempPath := filepath.Join(tempDir, "temp")
	if err := os.WriteFile(tempPath, []byte("42125\n"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	service := New(nil, Options{
		Version:     "2.0.0",
		Serial:      "serial-1",
		StartedAt:   time.Now().Add(-30 * time.Second),
		Runner:      fakeRunner{outputs: map[string]commandResult{"upsc ups-a": {output: []byte("ups.status: OL\n")}}},
		CPUTempPath: tempPath,
		RootPath:    tempDir,
		AuthPath:    filepath.Join(tempDir, "webui-auth.json"),
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	beforeLogin := httptest.NewRequest(http.MethodGet, "/status/details", nil)
	beforeLoginRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(beforeLoginRecorder, beforeLogin)
	if beforeLoginRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("status before bootstrap = %d, want %d", beforeLoginRecorder.Code, http.StatusUnauthorized)
	}

	cookies := loginAsDefaultAdmin(t, service)
	sessionCookie := cookieByName(cookies, sessionCookieName)
	if sessionCookie == nil {
		t.Fatal("login should issue a session cookie")
	}

	withoutAuth := httptest.NewRequest(http.MethodGet, "/status/details", nil)
	withoutAuthRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(withoutAuthRecorder, withoutAuth)
	if withoutAuthRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("status without auth = %d, want %d", withoutAuthRecorder.Code, http.StatusUnauthorized)
	}
	if got := withoutAuthRecorder.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate = %q, want empty for session auth", got)
	}

	withAuth := httptest.NewRequest(http.MethodGet, "/status/details", nil)
	withAuth.AddCookie(sessionCookie)
	withAuthRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(withAuthRecorder, withAuth)
	if withAuthRecorder.Code != http.StatusOK {
		t.Fatalf("status with auth = %d, want %d body=%s", withAuthRecorder.Code, http.StatusOK, withAuthRecorder.Body.String())
	}

	var response healthResponse
	if err := json.Unmarshal(withAuthRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Serial != "serial-1" || len(response.UPSes) != 1 {
		t.Fatalf("unexpected detailed response: %#v", response)
	}
}

func TestLoginCreatesSessionCookieForDetailedRoutes(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tempPath := filepath.Join(tempDir, "temp")
	if err := os.WriteFile(tempPath, []byte("42125\n"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	service := New(nil, Options{
		Version:     "2.0.0",
		Serial:      "serial-1",
		StartedAt:   time.Now().Add(-30 * time.Second),
		Runner:      fakeRunner{outputs: map[string]commandResult{"upsc ups-a": {output: []byte("ups.status: OL\n")}}},
		CPUTempPath: tempPath,
		RootPath:    tempDir,
		AuthPath:    filepath.Join(tempDir, "webui-auth.json"),
	})
	service.UpdateInventory([]nutconf.DetectedUPS{{Name: "ups-a", Driver: "usbhid-ups"}})

	loginCookies := loginAsDefaultAdmin(t, service)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.AddCookie(cookieByName(loginCookies, sessionCookieName))
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestLoginRotatesExistingSessionToken(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{
		Version:   "2.0.0",
		Serial:    "serial-1",
		StartedAt: time.Now().Add(-30 * time.Second),
		RootPath:  tempDir,
		AuthPath:  filepath.Join(tempDir, "webui-auth.json"),
	})

	originalCookies := loginAsDefaultAdmin(t, service)
	originalSession := cookieByName(originalCookies, sessionCookieName)
	if originalSession == nil {
		t.Fatal("expected initial login to issue a session cookie")
	}

	login := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"`+defaultAdminUsername+`","password":"`+testAdminPassword+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.AddCookie(originalSession)
	loginRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(loginRecorder, login)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d body=%s", loginRecorder.Code, http.StatusOK, loginRecorder.Body.String())
	}
	rotatedSession := cookieByName(loginRecorder.Result().Cookies(), sessionCookieName)
	if rotatedSession == nil {
		t.Fatal("expected login to issue a rotated session cookie")
	}
	if rotatedSession.Value == originalSession.Value {
		t.Fatalf("rotated session token should differ from original token")
	}

	oldSessionRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	oldSessionRequest.AddCookie(originalSession)
	oldSessionRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(oldSessionRecorder, oldSessionRequest)
	if oldSessionRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("old session status = %d, want %d", oldSessionRecorder.Code, http.StatusUnauthorized)
	}

	newSessionRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	newSessionRequest.AddCookie(rotatedSession)
	newSessionRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(newSessionRecorder, newSessionRequest)
	if newSessionRecorder.Code != http.StatusOK {
		t.Fatalf("new session status = %d, want %d body=%s", newSessionRecorder.Code, http.StatusOK, newSessionRecorder.Body.String())
	}
}

func TestAuthCookiesUseSecureAttributeForTLSRequests(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	bootstrap := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{"new_password":"`+testAdminPassword+`","confirm_password":"`+testAdminPassword+`"}`))
	bootstrap.Header.Set("Content-Type", "application/json")
	bootstrap.TLS = &tls.ConnectionState{}
	bootstrapRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(bootstrapRecorder, bootstrap)
	if bootstrapRecorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want %d body=%s", bootstrapRecorder.Code, http.StatusOK, bootstrapRecorder.Body.String())
	}

	loginPage := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	loginPage.TLS = &tls.ConnectionState{}
	loginPageRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(loginPageRecorder, loginPage)
	if loginPageRecorder.Code != http.StatusOK {
		t.Fatalf("login page status = %d, want %d", loginPageRecorder.Code, http.StatusOK)
	}
	loginPageCSRFCookie := cookieByName(loginPageRecorder.Result().Cookies(), csrfCookieName)
	if loginPageCSRFCookie == nil {
		t.Fatal("expected login page csrf cookie")
	}
	if !loginPageCSRFCookie.Secure {
		t.Fatal("expected login page csrf cookie to be Secure over TLS")
	}

	login := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"`+defaultAdminUsername+`","password":"`+testAdminPassword+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.TLS = &tls.ConnectionState{}
	loginRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(loginRecorder, login)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d body=%s", loginRecorder.Code, http.StatusOK, loginRecorder.Body.String())
	}
	sessionCookie := cookieByName(loginRecorder.Result().Cookies(), sessionCookieName)
	csrfCookie := cookieByName(loginRecorder.Result().Cookies(), csrfCookieName)
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("expected session and csrf cookies, got session=%v csrf=%v", sessionCookie != nil, csrfCookie != nil)
	}
	if !sessionCookie.Secure || !csrfCookie.Secure {
		t.Fatalf("expected Secure cookies for TLS login, got session=%t csrf=%t", sessionCookie.Secure, csrfCookie.Secure)
	}
}

func TestAuthCookiesOmitSecureAttributeOverPlainHTTP(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	bootstrap := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{"new_password":"`+testAdminPassword+`","confirm_password":"`+testAdminPassword+`"}`))
	bootstrap.Header.Set("Content-Type", "application/json")
	bootstrapRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(bootstrapRecorder, bootstrap)
	if bootstrapRecorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want %d body=%s", bootstrapRecorder.Code, http.StatusOK, bootstrapRecorder.Body.String())
	}

	loginPage := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	loginPageRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(loginPageRecorder, loginPage)
	if loginPageRecorder.Code != http.StatusOK {
		t.Fatalf("login page status = %d, want %d", loginPageRecorder.Code, http.StatusOK)
	}
	loginPageCSRFCookie := cookieByName(loginPageRecorder.Result().Cookies(), csrfCookieName)
	if loginPageCSRFCookie == nil {
		t.Fatal("expected login page csrf cookie")
	}
	if loginPageCSRFCookie.Secure {
		t.Fatal("expected login page csrf cookie to omit Secure over plain HTTP")
	}

	login := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"`+defaultAdminUsername+`","password":"`+testAdminPassword+`"}`))
	login.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(loginRecorder, login)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d body=%s", loginRecorder.Code, http.StatusOK, loginRecorder.Body.String())
	}
	sessionCookie := cookieByName(loginRecorder.Result().Cookies(), sessionCookieName)
	csrfCookie := cookieByName(loginRecorder.Result().Cookies(), csrfCookieName)
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("expected session and csrf cookies, got session=%v csrf=%v", sessionCookie != nil, csrfCookie != nil)
	}
	if sessionCookie.Secure || csrfCookie.Secure {
		t.Fatalf("expected non-Secure cookies for plain HTTP login, got session=%t csrf=%t", sessionCookie.Secure, csrfCookie.Secure)
	}
}

func TestSettingsCanToggleLocalUIAndResetAuth(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	cookies := loginAsDefaultAdmin(t, service)
	cookie := cookieByName(cookies, sessionCookieName)

	settings := httptest.NewRequest(http.MethodGet, "/settings", nil)
	settings.AddCookie(cookie)
	settingsRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(settingsRecorder, settings)
	if settingsRecorder.Code != http.StatusOK {
		t.Fatalf("settings status = %d, want %d", settingsRecorder.Code, http.StatusOK)
	}
	if !strings.Contains(settingsRecorder.Body.String(), "Node Settings") {
		t.Fatalf("settings page missing heading: %s", settingsRecorder.Body.String())
	}
	if !strings.Contains(settingsRecorder.Body.String(), "strom-theme-preference") {
		t.Fatalf("settings page missing persisted theme bootstrap: %s", settingsRecorder.Body.String())
	}
	for _, removed := range []string{"<h2>Local UI</h2>", "<h2>Session</h2>", "Signed in as", "action=\"/settings/ui\"", "href=\"/auth/logout\""} {
		if strings.Contains(settingsRecorder.Body.String(), removed) {
			t.Fatalf("settings page still contains removed section markup %q: %s", removed, settingsRecorder.Body.String())
		}
	}
	if !strings.Contains(settingsRecorder.Body.String(), "SSH access") {
		t.Fatalf("settings page missing SSH access controls: %s", settingsRecorder.Body.String())
	}
	for _, want := range []string{"Health endpoints", "href=\"/status\"", "href=\"/status/details\"", "href=\"/healthz\"", "href=\"/api/health\"", "<h3>API documentation</h3>", "action=\"/settings/api-docs\"", "api-docs-enabled-toggle", "ssh-enabled-toggle", "ssh-enable-dialog", "Current dashboard password"} {
		if !strings.Contains(settingsRecorder.Body.String(), want) {
			t.Fatalf("settings page missing API endpoint or documentation markup %q: %s", want, settingsRecorder.Body.String())
		}
	}
	if strings.Contains(settingsRecorder.Body.String(), "<h2>API documentation</h2>") {
		t.Fatalf("settings page should combine API documentation into API access: %s", settingsRecorder.Body.String())
	}
	if strings.Contains(settingsRecorder.Body.String(), "Open API documentation") {
		t.Fatalf("settings page should not link to disabled API documentation: %s", settingsRecorder.Body.String())
	}
	if !strings.Contains(settingsRecorder.Body.String(), "Reset local web access?") {
		t.Fatalf("settings page missing reset confirmation: %s", settingsRecorder.Body.String())
	}
	if !strings.Contains(settingsRecorder.Body.String(), "settings-menu-toggle") {
		t.Fatalf("settings page missing responsive menu toggle: %s", settingsRecorder.Body.String())
	}
	for _, want := range []string{
		"data-about-open",
		"about-dialog",
		"acknowledgements-dialog",
		"/assets/about.js",
		"aria-label=\"Docs (opens in a new tab)\"",
		"menu-link-icon-wrap",
		"M14 5h5v5M19 5l-9 9M19 14v5H5V5h5",
	} {
		if !strings.Contains(settingsRecorder.Body.String(), want) {
			t.Fatalf("settings page missing Docs external-link markup %q: %s", want, settingsRecorder.Body.String())
		}
	}

	disable := httptest.NewRequest(http.MethodPost, "/settings/ui", strings.NewReader(`{"enabled":false}`))
	disable.Header.Set("Content-Type", "application/json")
	disable.AddCookie(cookie)
	disableRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(disableRecorder, disable)
	if disableRecorder.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want %d body=%s", disableRecorder.Code, http.StatusOK, disableRecorder.Body.String())
	}

	root := httptest.NewRequest(http.MethodGet, "/", nil)
	root.AddCookie(cookie)
	rootRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(rootRecorder, root)
	if rootRecorder.Code != http.StatusSeeOther {
		t.Fatalf("root status = %d, want %d", rootRecorder.Code, http.StatusSeeOther)
	}
	if location := rootRecorder.Header().Get("Location"); !strings.HasPrefix(location, "/settings") {
		t.Fatalf("redirect location = %q, want /settings...", location)
	}

	reset := httptest.NewRequest(http.MethodPost, "/auth/reset", nil)
	reset.AddCookie(cookie)
	resetRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(resetRecorder, reset)
	if resetRecorder.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want %d body=%s", resetRecorder.Code, http.StatusOK, resetRecorder.Body.String())
	}

	afterReset := httptest.NewRequest(http.MethodGet, "/", nil)
	afterReset.Header.Set("Accept", "text/html")
	afterResetRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(afterResetRecorder, afterReset)
	if afterResetRecorder.Code != http.StatusSeeOther {
		t.Fatalf("after reset status = %d, want %d", afterResetRecorder.Code, http.StatusSeeOther)
	}
	if location := afterResetRecorder.Header().Get("Location"); location != "/auth/bootstrap" {
		t.Fatalf("after reset location = %q, want %q", location, "/auth/bootstrap")
	}

	reLoginCookies := loginAsDefaultAdmin(t, service)
	if cookieByName(reLoginCookies, sessionCookieName) == nil {
		t.Fatal("expected admin bootstrap to work again after reset")
	}
}

func TestAPIDocumentationIsOptInAndSessionProtected(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})
	cookies := loginAsDefaultAdmin(t, service)
	sessionCookie := cookieByName(cookies, sessionCookieName)
	if sessionCookie == nil {
		t.Fatal("expected admin session cookie")
	}

	for _, path := range []string{"/api/docs/", "/openapi.json"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		service.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("disabled API documentation %s status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}

	enable := httptest.NewRequest(http.MethodPost, "/settings/api-docs", strings.NewReader(`{"enabled":true}`))
	enable.Header.Set("Content-Type", "application/json")
	enable.AddCookie(sessionCookie)
	enableRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(enableRecorder, enable)
	if enableRecorder.Code != http.StatusOK {
		t.Fatalf("enable API documentation status = %d, want %d body=%s", enableRecorder.Code, http.StatusOK, enableRecorder.Body.String())
	}

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/docs/", nil)
	unauthenticatedRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(unauthenticatedRecorder, unauthenticated)
	if unauthenticatedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API documentation status = %d, want %d", unauthenticatedRecorder.Code, http.StatusUnauthorized)
	}

	for _, path := range []string{"/api/docs/", "/openapi.json"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(sessionCookie)
		recorder := httptest.NewRecorder()
		service.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("enabled API documentation %s status = %d, want %d body=%s", path, recorder.Code, http.StatusOK, recorder.Body.String())
		}
	}
}

func TestSettingsSynchronizesSSHAccessWithAdminCredentials(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	sshAccess := &fakeSSHAccessManager{}
	service := New(nil, Options{
		RootPath:  tempDir,
		AuthPath:  filepath.Join(tempDir, "webui-auth.json"),
		SSHAccess: sshAccess,
	})
	cookies := loginAsDefaultAdmin(t, service)
	sessionCookie := cookieByName(cookies, sessionCookieName)
	if sessionCookie == nil {
		t.Fatal("expected session cookie")
	}

	enable := httptest.NewRequest(http.MethodPost, "/settings/ssh", strings.NewReader(`{"enabled":true,"password":"`+testAdminPassword+`"}`))
	enable.Header.Set("Content-Type", "application/json")
	enable.AddCookie(sessionCookie)
	enableRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(enableRecorder, enable)
	if enableRecorder.Code != http.StatusOK {
		t.Fatalf("enable SSH status = %d, want %d body=%s", enableRecorder.Code, http.StatusOK, enableRecorder.Body.String())
	}
	if sshAccess.enabledPassword != testAdminPassword {
		t.Fatalf("SSH enable password = %q, want %q", sshAccess.enabledPassword, testAdminPassword)
	}
	if sshAccess.enabledPasswordHash == "" {
		t.Fatal("expected SSH enable to return a persistent password hash")
	}
	sshEnabled, err := service.auth.SSHEnabled()
	if err != nil || !sshEnabled {
		t.Fatalf("SSH enabled = %t, %v; want true, nil", sshEnabled, err)
	}
	settings := httptest.NewRequest(http.MethodGet, "/settings", nil)
	settings.Host = "192.168.20.63:80"
	settings.AddCookie(sessionCookie)
	settingsRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(settingsRecorder, settings)
	if settingsRecorder.Code != http.StatusOK {
		t.Fatalf("settings status = %d, want %d body=%s", settingsRecorder.Code, http.StatusOK, settingsRecorder.Body.String())
	}
	if !strings.Contains(settingsRecorder.Body.String(), `<code id="ssh-command">ssh admin@192.168.20.63</code>`) {
		t.Fatalf("settings page missing copyable SSH command: %s", settingsRecorder.Body.String())
	}

	const updatedPassword = "another-strong-pass"
	change := httptest.NewRequest(http.MethodPost, "/settings/password", strings.NewReader(`{"current_password":"`+testAdminPassword+`","new_password":"`+updatedPassword+`","confirm_password":"`+updatedPassword+`"}`))
	change.Header.Set("Content-Type", "application/json")
	change.AddCookie(sessionCookie)
	changeRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(changeRecorder, change)
	if changeRecorder.Code != http.StatusOK {
		t.Fatalf("change password status = %d, want %d body=%s", changeRecorder.Code, http.StatusOK, changeRecorder.Body.String())
	}
	if sshAccess.syncedPassword != updatedPassword {
		t.Fatalf("SSH synced password = %q, want %q", sshAccess.syncedPassword, updatedPassword)
	}

	disable := httptest.NewRequest(http.MethodPost, "/settings/ssh", strings.NewReader(`{"enabled":false}`))
	disable.Header.Set("Content-Type", "application/json")
	disable.AddCookie(sessionCookie)
	disableRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(disableRecorder, disable)
	if disableRecorder.Code != http.StatusOK {
		t.Fatalf("disable SSH status = %d, want %d body=%s", disableRecorder.Code, http.StatusOK, disableRecorder.Body.String())
	}
	sshEnabled, err = service.auth.SSHEnabled()
	if err != nil || sshEnabled {
		t.Fatalf("SSH enabled after disable = %t, %v; want false, nil", sshEnabled, err)
	}
	if sshAccess.disableCalls != 1 {
		t.Fatalf("SSH disable calls after toggle = %d, want 1", sshAccess.disableCalls)
	}

	reset := httptest.NewRequest(http.MethodPost, "/auth/reset", nil)
	reset.AddCookie(sessionCookie)
	resetRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(resetRecorder, reset)
	if resetRecorder.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want %d body=%s", resetRecorder.Code, http.StatusOK, resetRecorder.Body.String())
	}
	if sshAccess.disableCalls != 1 {
		t.Fatalf("SSH disable calls = %d, want 1", sshAccess.disableCalls)
	}
}

func TestSystemSSHAccessManagerWritesPasswordAuthConfig(t *testing.T) {
	t.Parallel()

	rootPath := t.TempDir()
	manager := newSystemSSHAccessManager(rootPath)
	if err := manager.writeConfig(); err != nil {
		t.Fatalf("writeConfig() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(rootPath, sshAccessConfigPath))
	if err != nil {
		t.Fatalf("read SSH config: %v", err)
	}
	if !strings.Contains(string(content), "Match User admin\n    PasswordAuthentication yes") {
		t.Fatalf("SSH config = %q, want password access restricted to admin", content)
	}
}

func TestSettingsHTMLFormRequiresCSRFTokens(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	cookies := loginAsDefaultAdmin(t, service)
	sessionCookie := cookieByName(cookies, sessionCookieName)
	csrfCookie := cookieByName(cookies, csrfCookieName)
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("expected session and csrf cookies, got session=%v csrf=%v", sessionCookie != nil, csrfCookie != nil)
	}

	missingToken := httptest.NewRequest(http.MethodPost, "/settings/ui", strings.NewReader("enabled=false"))
	missingToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingToken.Header.Set("Accept", "text/html")
	missingToken.AddCookie(sessionCookie)
	missingTokenRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(missingTokenRecorder, missingToken)
	if missingTokenRecorder.Code != http.StatusForbidden {
		t.Fatalf("settings form without csrf status = %d, want %d body=%s", missingTokenRecorder.Code, http.StatusForbidden, missingTokenRecorder.Body.String())
	}

	withToken := httptest.NewRequest(http.MethodPost, "/settings/ui", strings.NewReader("enabled=false&csrf_token="+csrfCookie.Value))
	withToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	withToken.Header.Set("Accept", "text/html")
	withToken.AddCookie(sessionCookie)
	withTokenRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(withTokenRecorder, withToken)
	if withTokenRecorder.Code != http.StatusSeeOther {
		t.Fatalf("settings form with csrf status = %d, want %d body=%s", withTokenRecorder.Code, http.StatusSeeOther, withTokenRecorder.Body.String())
	}

	missingDocsToken := httptest.NewRequest(http.MethodPost, "/settings/api-docs", strings.NewReader("enabled=true"))
	missingDocsToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingDocsToken.Header.Set("Accept", "text/html")
	missingDocsToken.AddCookie(sessionCookie)
	missingDocsTokenRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(missingDocsTokenRecorder, missingDocsToken)
	if missingDocsTokenRecorder.Code != http.StatusForbidden {
		t.Fatalf("API documentation form without csrf status = %d, want %d body=%s", missingDocsTokenRecorder.Code, http.StatusForbidden, missingDocsTokenRecorder.Body.String())
	}

	withDocsToken := httptest.NewRequest(http.MethodPost, "/settings/api-docs", strings.NewReader("enabled=true&csrf_token="+csrfCookie.Value))
	withDocsToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	withDocsToken.Header.Set("Accept", "text/html")
	withDocsToken.AddCookie(sessionCookie)
	withDocsTokenRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(withDocsTokenRecorder, withDocsToken)
	if withDocsTokenRecorder.Code != http.StatusSeeOther {
		t.Fatalf("API documentation form with csrf status = %d, want %d body=%s", withDocsTokenRecorder.Code, http.StatusSeeOther, withDocsTokenRecorder.Body.String())
	}
	if location := withDocsTokenRecorder.Header().Get("Location"); location != "/settings?message=API+documentation+enabled" {
		t.Fatalf("API documentation form redirect = %q, want readable enabled message", location)
	}
}

func TestControllerPolicyCanManageLocalUIAndBlockSessionToggle(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	adoptionPath := filepath.Join(tempDir, "adoption.json")
	controllerToken := "controller-token"
	payload := []byte(`{"token_sha256":"` + tokenSHA256Hex(controllerToken) + `"}`)
	if err := os.WriteFile(adoptionPath, payload, 0o600); err != nil {
		t.Fatalf("write adoption file: %v", err)
	}
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json"), AdoptionPath: adoptionPath})

	cookies := loginAsDefaultAdmin(t, service)
	cookie := cookieByName(cookies, sessionCookieName)

	policy := httptest.NewRequest(http.MethodPost, "/api/settings/ui/policy", strings.NewReader(`{"managed":true,"enabled":false}`))
	policy.Header.Set("Authorization", "Bearer "+controllerToken)
	policy.Header.Set("Content-Type", "application/json")
	policyRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(policyRecorder, policy)
	if policyRecorder.Code != http.StatusOK {
		t.Fatalf("policy status = %d, want %d body=%s", policyRecorder.Code, http.StatusOK, policyRecorder.Body.String())
	}

	localToggle := httptest.NewRequest(http.MethodPost, "/settings/ui", strings.NewReader(`{"enabled":true}`))
	localToggle.Header.Set("Content-Type", "application/json")
	localToggle.AddCookie(cookie)
	localToggleRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(localToggleRecorder, localToggle)
	if localToggleRecorder.Code != http.StatusConflict {
		t.Fatalf("local toggle status = %d, want %d body=%s", localToggleRecorder.Code, http.StatusConflict, localToggleRecorder.Body.String())
	}

	root := httptest.NewRequest(http.MethodGet, "/", nil)
	root.AddCookie(cookie)
	rootRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(rootRecorder, root)
	if rootRecorder.Code != http.StatusSeeOther {
		t.Fatalf("root status = %d, want %d", rootRecorder.Code, http.StatusSeeOther)
	}
	if location := rootRecorder.Header().Get("Location"); !strings.HasPrefix(location, "/settings") {
		t.Fatalf("redirect location = %q, want /settings...", location)
	}
}

func TestControllerPolicyEndpointRequiresControllerBearer(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	adoptionPath := filepath.Join(tempDir, "adoption.json")
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json"), AdoptionPath: adoptionPath})

	request := httptest.NewRequest(http.MethodPost, "/api/settings/ui/policy", strings.NewReader(`{"managed":true,"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}
}

func TestDisableAuthBypassesProtectedRoutes(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{
		Version:     "2.0.0",
		Serial:      "serial-1",
		StartedAt:   time.Now().Add(-30 * time.Second),
		Runner:      fakeRunner{outputs: map[string]commandResult{}},
		RootPath:    tempDir,
		DisableAuth: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/status/details", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
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

func TestParseUPSCommandsSkipsUpscmdHeaderLine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		output string
		want   []upsCommand
	}{
		{
			// Realistic `upscmd -l <ups>` output: a prose header line naming the
			// UPS, a blank separator line, then the actual command list. The
			// header must never be parsed as a command (see parseUPSCommands).
			name:   "header line is skipped",
			output: "Instant commands supported on UPS [ups-simbe1050g3a]:\n\nload.off - Turn off the load immediately\n",
			want: []upsCommand{
				{Name: "load.off", Description: "Turn off the load immediately", Destructive: true},
			},
		},
		{
			name:   "header line with no trailing commands yields no commands",
			output: "Instant commands supported on UPS [ups-a]:\n",
			want:   []upsCommand{},
		},
		{
			name:   "command with no description and no header",
			output: "beeper.toggle\n",
			want:   []upsCommand{{Name: "beeper.toggle", Destructive: false}},
		},
		{
			name:   "colon-style description",
			output: "input.transfer.high: High transfer voltage\n",
			want:   []upsCommand{{Name: "input.transfer.high", Description: "High transfer voltage", Destructive: false}},
		},
		{
			name:   "uppercase FSD token is preserved",
			output: "FSD - Forced shutdown\n",
			want:   []upsCommand{{Name: "FSD", Description: "Forced shutdown", Destructive: true}},
		},
		{
			name:   "empty output yields no commands",
			output: "",
			want:   []upsCommand{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := parseUPSCommands([]byte(tc.output))
			if len(got) != len(tc.want) {
				t.Fatalf("parseUPSCommands() = %#v, want %#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("parseUPSCommands()[%d] = %#v, want %#v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

type fakeRunner struct {
	outputs map[string]commandResult
}

type fakeSSHAccessManager struct {
	enabledPassword     string
	enabledPasswordHash string
	syncedPassword      string
	disableCalls        int
	err                 error
}

func (f *fakeSSHAccessManager) Enable(_ context.Context, password string) (string, error) {
	f.enabledPassword = password
	f.enabledPasswordHash = "$6$test$hash"
	return f.enabledPasswordHash, f.err
}

func (f *fakeSSHAccessManager) Disable(_ context.Context) error {
	f.disableCalls++
	return f.err
}

func (f *fakeSSHAccessManager) SyncPassword(_ context.Context, password string) (string, error) {
	f.syncedPassword = password
	return "$6$test$updated-hash", f.err
}

func (f *fakeSSHAccessManager) Sync(_ context.Context, _ string) error {
	return f.err
}

type commandResult struct {
	output []byte
	err    error
}

type fakeAdopter struct {
	response adoptResponse
	err      error
}

func (f fakeAdopter) ApplyAdoption(_ context.Context, _ adoptRequest) (adoptResponse, error) {
	return f.response, f.err
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// testAdminPassword is the password used to bootstrap the local admin
// account in tests. There is no built-in default password; the first
// browser client to reach a fresh node must set one via /auth/bootstrap.
const testAdminPassword = "strom-test-admin-pass"

// loginAsDefaultAdmin bootstraps the local admin account with
// testAdminPassword and returns the resulting session cookies.
func loginAsDefaultAdmin(t *testing.T, service *Service) []*http.Cookie {
	t.Helper()
	bootstrap := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{"new_password":"`+testAdminPassword+`","confirm_password":"`+testAdminPassword+`"}`))
	bootstrap.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, bootstrap)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected bootstrap to issue cookies")
	}
	return cookies
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

func testGenerateControllerCA(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial generation error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{CommonName: "Strom Controller CA Test"},
		NotBefore:             time.Now().Add(-1 * time.Hour).UTC(),
		NotAfter:              time.Now().AddDate(5, 0, 0).UTC(),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return string(certificatePEM), privateKey
}
