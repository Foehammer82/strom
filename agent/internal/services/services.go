package services

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strings"
)

const defaultSystemctlPath = "systemctl"

type CommandRunner interface {
	CombinedOutput(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) CombinedOutput(ctx context.Context, path string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

type Manager struct {
	Logger        *log.Logger
	Runner        CommandRunner
	SystemctlPath string
}

func NewManager(logger *log.Logger) *Manager {
	return &Manager{
		Logger:        logger,
		Runner:        execRunner{},
		SystemctlPath: defaultSystemctlPath,
	}
}

func (m *Manager) Reload(ctx context.Context, changed bool, upsNames []string) error {
	if !changed {
		return nil
	}

	if len(upsNames) > 0 {
		hasEnumerator, err := m.hasUnit(ctx, "nut-driver-enumerator.service")
		if err != nil {
			return err
		}
		if hasEnumerator {
			if err := m.runSystemctl(ctx, "restart", "nut-driver-enumerator.service"); err != nil {
				return err
			}
		} else {
			for _, name := range uniqueSortedNames(upsNames) {
				if err := m.runSystemctl(ctx, "restart", fmt.Sprintf("nut-driver@%s.service", name)); err != nil {
					return err
				}
			}
		}
	}

	if err := m.runSystemctl(ctx, "reload-or-restart", "nut-server.service"); err != nil {
		return err
	}

	return nil
}

func (m *Manager) hasUnit(ctx context.Context, unit string) (bool, error) {
	output, err := m.runner().CombinedOutput(ctx, m.systemctlPath(), "show", "--property", "LoadState", "--value", unit)
	if err != nil {
		return false, fmt.Errorf("detect %s: %w", unit, err)
	}
	state := strings.TrimSpace(string(output))
	return state != "" && state != "not-found", nil
}

func (m *Manager) runSystemctl(ctx context.Context, action, unit string) error {
	output, err := m.runner().CombinedOutput(ctx, m.systemctlPath(), action, unit)
	if err == nil {
		return nil
	}

	trimmed := strings.TrimSpace(string(output))
	if m.Logger != nil {
		m.Logger.Printf("systemctl %s %s failed: %v output=%q hint=journalctl -u %s -n 50 --no-pager", action, unit, err, trimmed, unit)
	}
	return fmt.Errorf("systemctl %s %s: %w", action, unit, err)
}

func (m *Manager) runner() CommandRunner {
	if m.Runner != nil {
		return m.Runner
	}
	return execRunner{}
}

func (m *Manager) systemctlPath() string {
	if m.SystemctlPath != "" {
		return m.SystemctlPath
	}
	return defaultSystemctlPath
}

func uniqueSortedNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, name := range names {
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, name)
	}
	sort.Strings(unique)
	return unique
}
