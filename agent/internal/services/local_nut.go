package services

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

const (
	defaultUpsdPath  = "upsd"
	defaultUpsdrvctl = "upsdrvctl"
)

type LocalNUTOptions struct {
	ConfigDir string
	UPSDPath  string
	UPSDrvctl string
	Runner    CommandRunner
}

type LocalNUTManager struct {
	Logger    *log.Logger
	configDir string
	upsdPath  string
	drvPath   string
	runner    CommandRunner
	started   bool
}

type localExecRunner struct{}

func (localExecRunner) CombinedOutput(ctx context.Context, path string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

func NewLocalNUTManager(logger *log.Logger, options LocalNUTOptions) *LocalNUTManager {
	configDir := strings.TrimSpace(options.ConfigDir)
	upsdPath := strings.TrimSpace(options.UPSDPath)
	if upsdPath == "" {
		upsdPath = defaultUpsdPath
	}
	drvPath := strings.TrimSpace(options.UPSDrvctl)
	if drvPath == "" {
		drvPath = defaultUpsdrvctl
	}
	runner := options.Runner
	if runner == nil {
		runner = localExecRunner{}
	}
	return &LocalNUTManager{
		Logger:    logger,
		configDir: configDir,
		upsdPath:  upsdPath,
		drvPath:   drvPath,
		runner:    runner,
	}
}

func (m *LocalNUTManager) Reload(ctx context.Context, changed bool, _ []string) error {
	if !changed {
		return nil
	}

	if m.started {
		_ = m.run(ctx, m.drvPath, "stop")
	}
	if err := m.run(ctx, m.drvPath, "start"); err != nil {
		return err
	}

	if m.started {
		if err := m.run(ctx, m.upsdPath, "-c", "reload"); err == nil {
			return nil
		}
	}
	if err := m.run(ctx, m.upsdPath); err != nil {
		return err
	}
	m.started = true
	return nil
}

func (m *LocalNUTManager) run(ctx context.Context, path string, args ...string) error {
	output, err := m.runner.CombinedOutput(ctx, path, args...)
	if err == nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(output))
	if m.Logger != nil {
		m.Logger.Printf("command failed cmd=%s args=%q err=%v output=%q", path, strings.Join(args, " "), err, trimmed)
	}
	return fmt.Errorf("run %s %s: %w", path, strings.Join(args, " "), err)
}
