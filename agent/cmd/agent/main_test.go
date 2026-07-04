package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Foehammer82/wattkeeper/agent/internal/api"
	"github.com/Foehammer82/wattkeeper/agent/internal/hotplug"
	"github.com/Foehammer82/wattkeeper/agent/internal/nutconf"
	"github.com/Foehammer82/wattkeeper/agent/internal/services"
)

func TestRuntimeLoopWritesConfigsAndSkipsReloadWhenUnchanged(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configDir := t.TempDir()
	stateDir := t.TempDir()
	agentConfigPath := filepath.Join(stateDir, "agent.yaml")
	if err := os.WriteFile(agentConfigPath, []byte("nut:\n  username: agent\n  password: secret\n"), 0o600); err != nil {
		t.Fatalf("write agent config: %v", err)
	}

	events := make(chan hotplug.Event, 2)
	events <- hotplug.Event{Synthetic: true, Time: time.Now()}
	events <- hotplug.Event{Synthetic: false, Time: time.Now()}

	runner := &scriptedRunner{}
	inventorySink := &fakeInventorySink{}
	countSink := &fakeUPSCountSink{}
	loggerOutput := &bytes.Buffer{}
	runtime := &agentRuntime{
		watcher:         fakeWatcher{events: events},
		scanner:         &fakeScanner{cancel: cancel, results: [][]nutconf.DetectedUPS{{sampleUPS()}, {sampleUPS()}}},
		reloader:        &services.Manager{Logger: newTestLogger(loggerOutput), Runner: runner},
		inventory:       inventorySink,
		upsCount:        countSink,
		logger:          newTestLogger(loggerOutput),
		configDir:       configDir,
		agentConfigPath: agentConfigPath,
		namesPath:       filepath.Join(stateDir, "names.json"),
	}

	if err := runtime.run(ctx); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	assertFileContains(t, filepath.Join(configDir, "ups.conf"), "[ups-3b1519x12345]")
	assertFileContains(t, filepath.Join(configDir, "ups.conf"), "driver = usbhid-ups")
	assertFileContains(t, filepath.Join(configDir, "nut.conf"), "MODE=netserver")
	assertFileContains(t, filepath.Join(configDir, "upsd.conf"), "LISTEN 0.0.0.0 3493")
	assertFileContains(t, filepath.Join(configDir, "upsd.users"), "[agent]")
	assertFileContains(t, filepath.Join(configDir, "upsd.users"), "password = secret")
	assertFileContains(t, runtime.namesPath, "\"serial:3b1519x12345\": \"ups-3b1519x12345\"")

	if got := runner.Commands(); len(got) != 3 {
		t.Fatalf("systemctl command count = %d, want 3; commands=%v", len(got), got)
	}
	if got := runner.Commands(); got[0] != "systemctl show --property LoadState --value nut-driver-enumerator.service" {
		t.Fatalf("unexpected first command: %v", got)
	}
	if got := runner.Commands(); got[1] != "systemctl restart nut-driver@ups-3b1519x12345.service" {
		t.Fatalf("unexpected driver restart command: %v", got)
	}
	if got := runner.Commands(); got[2] != "systemctl reload-or-restart nut-server.service" {
		t.Fatalf("unexpected server reload command: %v", got)
	}
	if strings.Count(loggerOutput.String(), "no inventory changes") != 1 {
		t.Fatalf("expected unchanged second scan log, got %q", loggerOutput.String())
	}
	if strings.Count(strings.Join(runner.Commands(), "\n"), "reload-or-restart") != 1 {
		t.Fatalf("reload should happen once, commands=%v", runner.Commands())
	}
	if strings.Count(strings.Join(runner.Commands(), "\n"), "restart nut-driver@") != 1 {
		t.Fatalf("driver restart should happen once, commands=%v", runner.Commands())
	}
	if strings.Contains(loggerOutput.String(), "service reload failed") {
		t.Fatalf("unexpected reload failure log: %q", loggerOutput.String())
	}
	if strings.Contains(loggerOutput.String(), "config apply failed") {
		t.Fatalf("unexpected apply failure log: %q", loggerOutput.String())
	}
	if strings.Count(loggerOutput.String(), "run loop started") != 1 {
		t.Fatalf("unexpected runtime logs: %q", loggerOutput.String())
	}
	if strings.Count(loggerOutput.String(), "received shutdown signal") != 1 {
		t.Fatalf("expected shutdown log, got %q", loggerOutput.String())
	}
	if got := inventorySink.count(); got != 2 {
		t.Fatalf("inventory updates = %d, want 2", got)
	}
	if got := countSink.counts(); len(got) != 2 || got[0] != 1 || got[1] != 1 {
		t.Fatalf("UPS count updates = %v, want [1 1]", got)
	}
}

func TestRuntimeAdopterWritesStateAndReloadsServices(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	adoptionPath := filepath.Join(t.TempDir(), "adoption.json")
	runner := &scriptedRunner{}
	inventory := &fakeInventorySink{}
	adopted := &fakeAdoptedSink{}
	adopter := &runtimeAdopter{
		configDir:    configDir,
		adoptionPath: adoptionPath,
		reloader:     &services.Manager{Runner: runner},
		inventory:    inventory,
		advertiser:   adopted,
		version:      "v0.3.0",
		serial:       "serial-1234",
		tlsPort:      8443,
		tlsCertPath:  filepath.Join(t.TempDir(), "node-api.crt"),
		tlsKeyPath:   filepath.Join(t.TempDir(), "node-api.key"),
	}

	response, err := adopter.ApplyAdoption(context.Background(), api.AdoptRequest{
		CAPEM:         "pem-data",
		NUTUser:       "controller",
		NUTPassword:   "secret",
		APIToken:      "token-123",
		ControllerURL: "https://controller.local",
	})
	if err != nil {
		t.Fatalf("ApplyAdoption() error = %v", err)
	}
	if response.Serial != "serial-1234" || response.Version != "v0.3.0" {
		t.Fatalf("response = %#v, want serial/version", response)
	}
	if response.TLSPort != 8443 || response.TLSFingerprint == "" {
		t.Fatalf("response TLS metadata = %#v, want port and fingerprint", response)
	}

	assertFileContains(t, filepath.Join(configDir, "upsd.users"), "[controller]")
	assertFileContains(t, filepath.Join(configDir, "upsd.users"), "password = secret")

	content, err := os.ReadFile(adoptionPath)
	if err != nil {
		t.Fatalf("read adoption state: %v", err)
	}
	var state adoptionState
	if err := json.Unmarshal(content, &state); err != nil {
		t.Fatalf("decode adoption state: %v", err)
	}
	if state.ControllerURL != "https://controller.local" || state.TokenSHA256 != api.TokenSHA256Hex("token-123") {
		t.Fatalf("state = %#v, want controller URL and token hash", state)
	}
	if state.TLSPort != 8443 || state.TLSFingerprint == "" {
		t.Fatalf("state TLS metadata = %#v, want port and fingerprint", state)
	}
	if _, err := os.Stat(adopter.tlsCertPath); err != nil {
		t.Fatalf("TLS cert missing: %v", err)
	}
	if _, err := os.Stat(adopter.tlsKeyPath); err != nil {
		t.Fatalf("TLS key missing: %v", err)
	}
	if len(runner.Commands()) == 0 || runner.Commands()[0] != "systemctl reload-or-restart nut-server.service" {
		t.Fatalf("commands = %v, want nut-server reload", runner.Commands())
	}
	if got := inventory.credentials(); len(got) != 1 || got[0] != "controller:secret" {
		t.Fatalf("inventory credentials = %v, want controller:secret", got)
	}
	if got := adopted.values(); len(got) != 1 || !got[0] {
		t.Fatalf("adopted updates = %v, want [true]", got)
	}
}

type fakeWatcher struct {
	events <-chan hotplug.Event
}

func (f fakeWatcher) Events(context.Context) (<-chan hotplug.Event, error) {
	return f.events, nil
}

type fakeScanner struct {
	results [][]nutconf.DetectedUPS
	index   int
	cancel  context.CancelFunc
}

func (f *fakeScanner) Scan(context.Context) ([]nutconf.DetectedUPS, error) {
	if f.index >= len(f.results) {
		return nil, errors.New("unexpected scan")
	}
	result := f.results[f.index]
	f.index++
	if f.index == len(f.results) {
		f.cancel()
	}
	return result, nil
}

type scriptedRunner struct {
	commands []string
}

func (s *scriptedRunner) CombinedOutput(_ context.Context, path string, args ...string) ([]byte, error) {
	command := strings.TrimSpace(strings.Join(append([]string{path}, args...), " "))
	s.commands = append(s.commands, command)
	if command == "systemctl show --property LoadState --value nut-driver-enumerator.service" {
		return []byte("not-found\n"), nil
	}
	if strings.HasPrefix(command, "systemctl ") {
		return []byte("ok\n"), nil
	}
	return nil, errors.New("unexpected command")
}

func (s *scriptedRunner) Commands() []string {
	commands := make([]string, len(s.commands))
	copy(commands, s.commands)
	return commands
}

func sampleUPS() nutconf.DetectedUPS {
	return nutconf.DetectedUPS{
		Driver:    "usbhid-ups",
		Port:      "auto",
		VendorID:  "051d",
		ProductID: "0002",
		Product:   "Back-UPS ES 1050G3",
		Serial:    "3B1519X12345",
		Vendor:    "American Power Conversion",
		Bus:       "001",
	}
}

func assertFileContains(t *testing.T, path, substring string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(content), substring) {
		t.Fatalf("file %s missing %q in %q", path, substring, string(content))
	}
}

type fakeInventorySink struct {
	updates [][]nutconf.DetectedUPS
	creds   []string
}

func (f *fakeInventorySink) UpdateInventory(devices []nutconf.DetectedUPS) {
	cloned := make([]nutconf.DetectedUPS, len(devices))
	copy(cloned, devices)
	f.updates = append(f.updates, cloned)
}

func (f *fakeInventorySink) count() int {
	return len(f.updates)
}

func (f *fakeInventorySink) UpdateNUTCredentials(username, password string) {
	f.creds = append(f.creds, username+":"+password)
}

func (f *fakeInventorySink) credentials() []string {
	values := make([]string, len(f.creds))
	copy(values, f.creds)
	return values
}

type fakeUPSCountSink struct {
	values []int
}

func (f *fakeUPSCountSink) UpdateUPSCount(count int) {
	f.values = append(f.values, count)
}

func (f *fakeUPSCountSink) counts() []int {
	values := make([]int, len(f.values))
	copy(values, f.values)
	return values
}

type fakeAdoptedSink struct {
	states []bool
}

func (f *fakeAdoptedSink) UpdateAdopted(adopted bool) {
	f.states = append(f.states, adopted)
}

func (f *fakeAdoptedSink) values() []bool {
	values := make([]bool, len(f.states))
	copy(values, f.states)
	return values
}
