package services

import (
	"bytes"
	"context"
	"errors"
	"log"
	"reflect"
	"strings"
	"testing"
)

func TestReloadSkipsWhenUnchanged(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	manager := &Manager{Runner: runner}

	if err := manager.Reload(context.Background(), false, []string{"ups-a"}); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unexpected commands: %v", runner.commands)
	}
}

func TestReloadUsesEnumeratorWhenAvailable(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: map[string]fakeResponse{
		"systemctl show --property LoadState --value nut-driver-enumerator.service": {output: "loaded\n"},
	}}
	manager := &Manager{Runner: runner}

	if err := manager.Reload(context.Background(), true, []string{"ups-b", "ups-a"}); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	want := []string{
		"systemctl show --property LoadState --value nut-driver-enumerator.service",
		"systemctl restart nut-driver-enumerator.service",
		"systemctl reload-or-restart nut-server.service",
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %v, want %v", runner.commands, want)
	}
}

func TestReloadFallsBackToPerDeviceUnitsAndLogsHints(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"systemctl show --property LoadState --value nut-driver-enumerator.service": {output: "not-found\n"},
		"systemctl restart nut-driver@ups-b.service":                                {err: errors.New("boom")},
	}}
	manager := &Manager{Runner: runner, Logger: log.New(&logs, "", 0)}

	err := manager.Reload(context.Background(), true, []string{"ups-b", "ups-a", "ups-a"})
	if err == nil {
		t.Fatal("expected error")
	}

	want := []string{
		"systemctl show --property LoadState --value nut-driver-enumerator.service",
		"systemctl restart nut-driver@ups-a.service",
		"systemctl restart nut-driver@ups-b.service",
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %v, want %v", runner.commands, want)
	}
	if !strings.Contains(logs.String(), "journalctl -u nut-driver@ups-b.service -n 50 --no-pager") {
		t.Fatalf("expected journalctl hint, got %q", logs.String())
	}
}

type fakeRunner struct {
	commands  []string
	responses map[string]fakeResponse
}

type fakeResponse struct {
	output string
	err    error
}

func (f *fakeRunner) CombinedOutput(_ context.Context, path string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{path}, args...), " ")
	f.commands = append(f.commands, command)
	if response, ok := f.responses[command]; ok {
		return []byte(response.output), response.err
	}
	return []byte("ok\n"), nil
}
