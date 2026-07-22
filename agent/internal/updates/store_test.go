package updates

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreStageAndActivateFirstInstall(t *testing.T) {
	store := NewStore(t.TempDir())

	if _, ok := store.InstalledVersion(); ok {
		t.Fatal("expected no installed version on a fresh store")
	}

	if err := store.StageAndActivate("v1.0.0", []byte("binary-v1")); err != nil {
		t.Fatalf("StageAndActivate: %v", err)
	}

	version, ok := store.InstalledVersion()
	if !ok || version != "v1.0.0" {
		t.Fatalf("InstalledVersion() = %q, %v; want v1.0.0, true", version, ok)
	}

	pendingVersion, ok := store.PendingVersion()
	if !ok || pendingVersion != "v1.0.0" {
		t.Fatalf("PendingVersion() = %q, %v; want v1.0.0, true", pendingVersion, ok)
	}

	// No previous slot should exist yet.
	if _, err := os.Readlink(store.previousLink()); err == nil {
		t.Fatal("expected no previous slot after first-ever activation")
	}
}

func TestStoreStageAndActivateRejectsSameOrOlderVersion(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.StageAndActivate("v1.2.0", []byte("binary")); err != nil {
		t.Fatalf("StageAndActivate: %v", err)
	}

	if err := store.StageAndActivate("v1.2.0", []byte("binary")); err == nil {
		t.Fatal("expected error re-activating the same version")
	}
	if err := store.StageAndActivate("v1.1.0", []byte("binary")); err == nil {
		t.Fatal("expected error downgrading to an older version")
	}
}

func TestStoreReconcileStartupCommitsMatchingVersion(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.StageAndActivate("v1.0.0", []byte("binary")); err != nil {
		t.Fatalf("StageAndActivate: %v", err)
	}

	action, err := store.ReconcileStartup("v1.0.0")
	if err != nil {
		t.Fatalf("ReconcileStartup: %v", err)
	}
	if action != ReconcileAwaitingHealth {
		t.Fatalf("action = %v, want ReconcileAwaitingHealth", action)
	}

	if err := store.ConfirmHealthy("v1.0.0"); err != nil {
		t.Fatalf("ConfirmHealthy: %v", err)
	}
	if _, ok := store.PendingVersion(); ok {
		t.Fatal("expected pending activation to be cleared after ConfirmHealthy")
	}

	// A restart of the same, now-confirmed version should be a no-op.
	action, err = store.ReconcileStartup("v1.0.0")
	if err != nil {
		t.Fatalf("ReconcileStartup after confirm: %v", err)
	}
	if action != ReconcileNone {
		t.Fatalf("action after confirm = %v, want ReconcileNone", action)
	}
}

func TestStoreReconcileStartupRollsBackAfterMaxAttempts(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.StageAndActivate("v1.0.0", []byte("binary-v1")); err != nil {
		t.Fatalf("StageAndActivate v1: %v", err)
	}
	if err := store.ConfirmHealthy("v1.0.0"); err != nil {
		t.Fatalf("ConfirmHealthy v1: %v", err)
	}

	if err := store.StageAndActivate("v2.0.0", []byte("binary-v2")); err != nil {
		t.Fatalf("StageAndActivate v2: %v", err)
	}

	// Simulate the new version crashing on startup repeatedly without ever
	// confirming healthy.
	var lastAction ReconcileAction
	for i := 0; i < defaultMaxActivationAttempts+1; i++ {
		action, err := store.ReconcileStartup("v2.0.0")
		if err != nil {
			t.Fatalf("ReconcileStartup attempt %d: %v", i, err)
		}
		lastAction = action
		if action == ReconcileRolledBack {
			break
		}
	}
	if lastAction != ReconcileRolledBack {
		t.Fatalf("expected rollback after exceeding max attempts, last action = %v", lastAction)
	}

	version, ok := store.InstalledVersion()
	if !ok || version != "v1.0.0" {
		t.Fatalf("InstalledVersion() after rollback = %q, %v; want v1.0.0, true", version, ok)
	}
	if _, ok := store.PendingVersion(); ok {
		t.Fatal("expected pending activation to be cleared after rollback")
	}
}

func TestStoreReconcileStartupRollsBackAfterMaxAge(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.StageAndActivate("v1.0.0", []byte("binary-v1")); err != nil {
		t.Fatalf("StageAndActivate v1: %v", err)
	}
	if err := store.ConfirmHealthy("v1.0.0"); err != nil {
		t.Fatalf("ConfirmHealthy v1: %v", err)
	}
	if err := store.StageAndActivate("v2.0.0", []byte("binary-v2")); err != nil {
		t.Fatalf("StageAndActivate v2: %v", err)
	}

	// Force the pending activation to look old.
	pending, ok, err := store.readPending()
	if err != nil || !ok {
		t.Fatalf("readPending: ok=%v err=%v", ok, err)
	}
	pending.FirstAttemptAt = time.Now().Add(-2 * defaultMaxActivationAge)
	if err := store.writePending(pending); err != nil {
		t.Fatalf("writePending: %v", err)
	}

	action, err := store.ReconcileStartup("v2.0.0")
	if err != nil {
		t.Fatalf("ReconcileStartup: %v", err)
	}
	if action != ReconcileRolledBack {
		t.Fatalf("action = %v, want ReconcileRolledBack", action)
	}
	version, ok := store.InstalledVersion()
	if !ok || version != "v1.0.0" {
		t.Fatalf("InstalledVersion() after rollback = %q, %v; want v1.0.0, true", version, ok)
	}
}

func TestStoreReconcileStartupFirstActivationRollbackRemovesCurrent(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.StageAndActivate("v1.0.0", []byte("binary-v1")); err != nil {
		t.Fatalf("StageAndActivate: %v", err)
	}

	var lastAction ReconcileAction
	for i := 0; i < defaultMaxActivationAttempts+1; i++ {
		action, err := store.ReconcileStartup("v1.0.0")
		if err != nil {
			t.Fatalf("ReconcileStartup attempt %d: %v", i, err)
		}
		lastAction = action
		if action == ReconcileRolledBack {
			break
		}
	}
	if lastAction != ReconcileRolledBack {
		t.Fatalf("expected rollback, last action = %v", lastAction)
	}
	if _, ok := store.InstalledVersion(); ok {
		t.Fatal("expected current slot to be removed after rolling back a first-ever activation")
	}
}

func TestStoreReconcileStartupIgnoresUnrelatedVersion(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.StageAndActivate("v1.0.0", []byte("binary-v1")); err != nil {
		t.Fatalf("StageAndActivate: %v", err)
	}

	// A process running some other version entirely (e.g. the recovery
	// binary after a manual intervention) should not be affected, and
	// stale pending state should be cleared.
	action, err := store.ReconcileStartup("v9.9.9")
	if err != nil {
		t.Fatalf("ReconcileStartup: %v", err)
	}
	if action != ReconcileNone {
		t.Fatalf("action = %v, want ReconcileNone", action)
	}
	if _, ok := store.PendingVersion(); ok {
		t.Fatal("expected stale pending activation to be cleared")
	}
}

func TestStorePruneReleasesKeepsOnlyCurrentAndPrevious(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.StageAndActivate("v1.0.0", []byte("binary-v1")); err != nil {
		t.Fatalf("StageAndActivate v1: %v", err)
	}
	if err := store.ConfirmHealthy("v1.0.0"); err != nil {
		t.Fatalf("ConfirmHealthy v1: %v", err)
	}
	if err := store.StageAndActivate("v2.0.0", []byte("binary-v2")); err != nil {
		t.Fatalf("StageAndActivate v2: %v", err)
	}
	if err := store.ConfirmHealthy("v2.0.0"); err != nil {
		t.Fatalf("ConfirmHealthy v2: %v", err)
	}
	if err := store.StageAndActivate("v3.0.0", []byte("binary-v3")); err != nil {
		t.Fatalf("StageAndActivate v3: %v", err)
	}

	entries, err := os.ReadDir(store.releasesDir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	got := map[string]bool{}
	for _, entry := range entries {
		got[entry.Name()] = true
	}
	want := map[string]bool{"v2.0.0": true, "v3.0.0": true}
	if len(got) != len(want) {
		t.Fatalf("releases dir entries = %v, want %v", got, want)
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("expected release %s to be retained, got entries %v", name, got)
		}
	}
}

func TestStoreStatusPersistence(t *testing.T) {
	store := NewStore(t.TempDir())

	status, err := store.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus on fresh store: %v", err)
	}
	if !status.SchedulerEnabled {
		t.Fatal("expected scheduler to default to enabled")
	}

	status.AvailableVersion = "v1.2.3"
	status.LastCheckError = "boom"
	if err := store.WriteStatus(status); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	reloaded, err := store.ReadStatus()
	if err != nil {
		t.Fatalf("ReadStatus after write: %v", err)
	}
	if reloaded.AvailableVersion != "v1.2.3" || reloaded.LastCheckError != "boom" {
		t.Fatalf("reloaded status = %+v, want AvailableVersion=v1.2.3 LastCheckError=boom", reloaded)
	}
}

func TestAtomicWriteFileIsDurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "file.json")
	if err := atomicWriteFile(path, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != `{"a":1}` {
		t.Fatalf("content = %q, want {\"a\":1}", content)
	}
}
