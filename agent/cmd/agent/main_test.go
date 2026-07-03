package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	loggerOutput := &bytes.Buffer{}
	runtime := &agentRuntime{
		watcher:         fakeWatcher{events: events},
		scanner:         &fakeScanner{cancel: cancel, results: [][]nutconf.DetectedUPS{{sampleUPS()}, {sampleUPS()}}},
		reloader:        &services.Manager{Logger: newTestLogger(loggerOutput), Runner: runner},
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
