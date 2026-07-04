package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	for _, want := range []string{"Wattkeeper Node", "Refresh now", "/assets/app.js", "/assets/styles.css", "ups-a", "usbhid-ups", "OL"} {
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

func TestIndexRendersBootstrapWhenAuthUninitialized(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	recorder := httptest.NewRecorder()

	service.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Initialize Node Access", "Create admin", "/status"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestBootstrapProtectsDetailedRoutes(t *testing.T) {
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

	beforeBootstrap := httptest.NewRequest(http.MethodGet, "/status/details", nil)
	beforeBootstrapRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(beforeBootstrapRecorder, beforeBootstrap)
	if beforeBootstrapRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status before bootstrap = %d, want %d", beforeBootstrapRecorder.Code, http.StatusServiceUnavailable)
	}

	bootstrap := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{"username":"admin","password":"secret-pass","confirm_password":"secret-pass"}`))
	bootstrap.Header.Set("Content-Type", "application/json")
	bootstrapRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(bootstrapRecorder, bootstrap)
	if bootstrapRecorder.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d, want %d body=%s", bootstrapRecorder.Code, http.StatusCreated, bootstrapRecorder.Body.String())
	}
	bootstrapCookie := bootstrapRecorder.Result().Cookies()
	if len(bootstrapCookie) == 0 {
		t.Fatal("bootstrap should issue a session cookie")
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
	withAuth.AddCookie(bootstrapCookie[0])
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

	bootstrap := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{"username":"admin","password":"secret-pass","confirm_password":"secret-pass"}`))
	bootstrap.Header.Set("Content-Type", "application/json")
	bootstrapRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(bootstrapRecorder, bootstrap)
	if bootstrapRecorder.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d, want %d", bootstrapRecorder.Code, http.StatusCreated)
	}

	logout := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logout.AddCookie(bootstrapRecorder.Result().Cookies()[0])
	logoutRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(logoutRecorder, logout)
	if logoutRecorder.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d", logoutRecorder.Code, http.StatusOK)
	}

	login := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"admin","password":"secret-pass"}`))
	login.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(loginRecorder, login)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d body=%s", loginRecorder.Code, http.StatusOK, loginRecorder.Body.String())
	}
	loginCookies := loginRecorder.Result().Cookies()
	if len(loginCookies) == 0 {
		t.Fatal("login should issue a session cookie")
	}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.AddCookie(loginCookies[0])
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestSettingsCanToggleLocalUIAndResetAuth(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := New(nil, Options{RootPath: tempDir, AuthPath: filepath.Join(tempDir, "webui-auth.json")})

	bootstrap := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{"username":"admin","password":"secret-pass","confirm_password":"secret-pass"}`))
	bootstrap.Header.Set("Content-Type", "application/json")
	bootstrapRecorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(bootstrapRecorder, bootstrap)
	if bootstrapRecorder.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d, want %d", bootstrapRecorder.Code, http.StatusCreated)
	}
	cookie := bootstrapRecorder.Result().Cookies()[0]

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
	if afterResetRecorder.Code != http.StatusOK {
		t.Fatalf("after reset status = %d, want %d", afterResetRecorder.Code, http.StatusOK)
	}
	if !strings.Contains(afterResetRecorder.Body.String(), "Initialize Node Access") {
		t.Fatalf("after reset body missing bootstrap page: %s", afterResetRecorder.Body.String())
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

type fakeRunner struct {
	outputs map[string]commandResult
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
