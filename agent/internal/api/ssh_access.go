package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const sshAccessConfigPath = "/etc/ssh/sshd_config.d/20-strom-admin.conf"

type sshAccessManager interface {
	Enable(context.Context, string) (string, error)
	Disable(context.Context) error
	SyncPassword(context.Context, string) (string, error)
	Sync(context.Context, string) error
}

type systemSSHAccessManager struct {
	rootPath string
}

func newSystemSSHAccessManager(rootPath string) *systemSSHAccessManager {
	if rootPath == "" {
		rootPath = defaultRootPath
	}
	return &systemSSHAccessManager{rootPath: rootPath}
}

func (m *systemSSHAccessManager) Enable(ctx context.Context, password string) (string, error) {
	if err := m.ensureAdminAccount(ctx); err != nil {
		return "", err
	}
	passwordHash, err := m.SyncPassword(ctx, password)
	if err != nil {
		return "", err
	}
	if err := m.writeConfig(); err != nil {
		return "", err
	}
	if err := m.validateAndRestart(ctx); err != nil {
		return "", err
	}
	return passwordHash, nil
}

func (m *systemSSHAccessManager) Disable(ctx context.Context) error {
	if err := os.Remove(m.path(sshAccessConfigPath)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove SSH access config: %w", err)
	}
	if err := runCommand(ctx, "usermod", "--lock", defaultAdminUsername); err != nil {
		return fmt.Errorf("lock SSH admin account: %w", err)
	}
	return m.validateAndRestart(ctx)
}

func (m *systemSSHAccessManager) SyncPassword(ctx context.Context, password string) (string, error) {
	command := exec.CommandContext(ctx, "chpasswd")
	command.Stdin = bytes.NewBufferString(defaultAdminUsername + ":" + password + "\n")
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("set SSH admin password: %w: %s", err, bytes.TrimSpace(output))
	}
	return m.passwordHash(ctx)
}

func (m *systemSSHAccessManager) Sync(ctx context.Context, passwordHash string) error {
	if passwordHash == "" {
		return errors.New("SSH password hash is missing")
	}
	if err := m.ensureAdminAccount(ctx); err != nil {
		return err
	}
	if err := runCommand(ctx, "usermod", "--password", passwordHash, "--unlock", defaultAdminUsername); err != nil {
		return fmt.Errorf("restore SSH admin password: %w", err)
	}
	return m.writeConfig()
}

func (m *systemSSHAccessManager) ensureAdminAccount(ctx context.Context) error {
	if err := runCommand(ctx, "id", "-u", defaultAdminUsername); err != nil {
		if err := runCommand(ctx, "useradd", "--create-home", "--shell", "/bin/bash", "--groups", "sudo", defaultAdminUsername); err != nil {
			return fmt.Errorf("create SSH admin account: %w", err)
		}
		return nil
	}
	if err := runCommand(ctx, "usermod", "--append", "--groups", "sudo", defaultAdminUsername); err != nil {
		return fmt.Errorf("grant SSH admin sudo access: %w", err)
	}
	return nil
}

func (m *systemSSHAccessManager) passwordHash(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, "getent", "shadow", defaultAdminUsername)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("read SSH admin password hash: %w", err)
	}
	parts := bytes.SplitN(bytes.TrimSpace(output), []byte(":"), 3)
	if len(parts) < 2 || len(parts[1]) == 0 || bytes.Equal(parts[1], []byte("!")) || bytes.Equal(parts[1], []byte("*")) {
		return "", errors.New("read SSH admin password hash: account password is locked")
	}
	return string(parts[1]), nil
}

func (m *systemSSHAccessManager) writeConfig() error {
	path := m.path(sshAccessConfigPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create SSH config directory: %w", err)
	}
	const config = "# Managed by Strom. Remove this file to disable SSH password access.\nMatch User admin\n    PasswordAuthentication yes\nMatch all\n"
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		return fmt.Errorf("write SSH access config: %w", err)
	}
	return nil
}

func (m *systemSSHAccessManager) validateAndRestart(ctx context.Context) error {
	if err := runCommand(ctx, "sshd", "-t"); err != nil {
		return fmt.Errorf("validate SSH configuration: %w", err)
	}
	if err := runCommand(ctx, "systemctl", "restart", "ssh"); err != nil {
		return fmt.Errorf("restart SSH service: %w", err)
	}
	return nil
}

func (m *systemSSHAccessManager) path(value string) string {
	return filepath.Join(m.rootPath, value)
}

func runCommand(ctx context.Context, path string, args ...string) error {
	command := exec.CommandContext(ctx, path, args...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %v: %w: %s", path, args, err, bytes.TrimSpace(output))
	}
	return nil
}

// SyncSSHAccess restores opt-in SSH access before ssh.service starts. The
// image uses an overlay root filesystem, so system account and sshd changes
// must be recreated from the persistent auth record after every reboot.
func SyncSSHAccess(ctx context.Context, authPath string) error {
	if authPath == "" {
		authPath = defaultAuthPath
	}
	content, err := os.ReadFile(authPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read SSH access state: %w", err)
	}
	var stored storedAuth
	if err := json.Unmarshal(content, &stored); err != nil {
		return fmt.Errorf("decode SSH access state: %w", err)
	}
	if stored.SSHEnabled == nil || !*stored.SSHEnabled {
		return nil
	}
	return newSystemSSHAccessManager(defaultRootPath).Sync(ctx, stored.SSHPasswordHash)
}
