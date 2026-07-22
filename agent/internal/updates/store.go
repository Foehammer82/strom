package updates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// defaultUpdatesRoot is the persistent directory used for durable agent
	// release slots. It lives on the same persistent partition as adoption
	// state and local auth, so it survives reboots and the RAM-backed root
	// OverlayFS used by the flashable node image.
	defaultUpdatesRoot = "/var/lib/strom/agent"

	releasesDirName  = "releases"
	currentLinkName  = "current"
	previousLinkName = "previous"
	pendingFileName  = "pending.json"
	statusFileName   = "status.json"

	// defaultMaxActivationAttempts caps how many times a newly activated
	// version is allowed to restart without confirming healthy before the
	// installer treats it as failed and rolls back to the previous slot.
	defaultMaxActivationAttempts = 3
	// defaultMaxActivationAge caps how long a pending activation may remain
	// unconfirmed (across any number of restarts) before it is rolled back.
	defaultMaxActivationAge = 3 * time.Minute
)

// ReconcileAction describes what ReconcileStartup determined should happen
// at agent startup.
type ReconcileAction int

const (
	// ReconcileNone means there was no pending activation to reconcile;
	// startup should proceed normally.
	ReconcileNone ReconcileAction = iota
	// ReconcileAwaitingHealth means this process is running a version with
	// a pending activation that has not yet exceeded its attempt/age
	// budget. The caller should proceed with startup and confirm healthy
	// once its own health check succeeds, by calling ConfirmHealthy.
	ReconcileAwaitingHealth
	// ReconcileRolledBack means the pending activation exceeded its
	// attempt/age budget and the store has already restored "current" to
	// the previous known-good slot. The caller must exit immediately
	// without serving traffic so systemd's Restart=on-failure re-execs the
	// now-restored "current" symlink.
	ReconcileRolledBack
)

// pendingActivation records an in-progress activation awaiting a confirmed
// health check from the newly activated version.
type pendingActivation struct {
	Version        string    `json:"version"`
	Attempts       int       `json:"attempts"`
	FirstAttemptAt time.Time `json:"first_attempt_at"`
}

// PersistedStatus is the durable, node-local record of update check/install
// activity, independent of any one process's in-memory state.
type PersistedStatus struct {
	SchedulerEnabled    bool      `json:"scheduler_enabled"`
	LastCheckedAt       time.Time `json:"last_checked_at,omitempty"`
	LastCheckError      string    `json:"last_check_error,omitempty"`
	AvailableVersion    string    `json:"available_version,omitempty"`
	AvailableReleaseURL string    `json:"available_release_url,omitempty"`
	LastInstallAt       time.Time `json:"last_install_at,omitempty"`
	LastInstallError    string    `json:"last_install_error,omitempty"`
}

// Store manages the persistent, durable release-slot layout under a root
// directory (normally /var/lib/strom/agent) and the small status/pending
// records alongside it.
type Store struct {
	root string
}

// NewStore returns a Store rooted at root. An empty root uses the default
// persistent updates directory.
func NewStore(root string) *Store {
	if strings.TrimSpace(root) == "" {
		root = defaultUpdatesRoot
	}
	return &Store{root: root}
}

// Root returns the store's root directory.
func (s *Store) Root() string { return s.root }

func (s *Store) releasesDir() string  { return filepath.Join(s.root, releasesDirName) }
func (s *Store) currentLink() string  { return filepath.Join(s.root, currentLinkName) }
func (s *Store) previousLink() string { return filepath.Join(s.root, previousLinkName) }
func (s *Store) pendingPath() string  { return filepath.Join(s.root, pendingFileName) }
func (s *Store) statusPath() string   { return filepath.Join(s.root, statusFileName) }

// InstalledVersion returns the version currently pointed to by the "current"
// slot, or false if no version has ever been staged (a fresh node running
// only its image-provided recovery binary).
func (s *Store) InstalledVersion() (string, bool) {
	return s.versionAtLink(s.currentLink())
}

func (s *Store) versionAtLink(link string) (string, bool) {
	target, err := os.Readlink(link)
	if err != nil {
		return "", false
	}
	// Expect ".../releases/<version>/strom-agent".
	dir := filepath.Dir(target)
	version := filepath.Base(dir)
	if version == "" || version == "." || version == "/" {
		return "", false
	}
	return version, true
}

// StageAndActivate durably writes the given binary content as a new release
// slot, promotes the current activation to "previous" (rollback target), and
// activates the new version as "current". It records a pending activation
// so the next process to run this version must confirm health via
// ConfirmHealthy or ReconcileStartup will eventually roll it back.
//
// StageAndActivate refuses to reinstall the currently active version and
// refuses implicit downgrades: callers that need to force a specific
// version (e.g. recovery tooling) must remove the conflicting release
// directory out of band first.
func (s *Store) StageAndActivate(version string, binary []byte) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("version is required")
	}
	if len(binary) == 0 {
		return fmt.Errorf("binary content is empty")
	}
	if _, err := ParseStableVersion(version); err != nil {
		return fmt.Errorf("refusing to activate non-stable version: %w", err)
	}

	if currentVersion, ok := s.InstalledVersion(); ok {
		if currentVersion == version {
			return fmt.Errorf("version %s is already active", version)
		}
		if newer, err := IsNewerStableVersion(version, currentVersion); err == nil && !newer {
			return fmt.Errorf("refusing to downgrade from %s to %s", currentVersion, version)
		}
	}

	releaseDir := filepath.Join(s.releasesDir(), version)
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return fmt.Errorf("create release directory: %w", err)
	}
	binaryPath := filepath.Join(releaseDir, agentBinaryName)
	if err := atomicWriteExecutable(binaryPath, binary); err != nil {
		return fmt.Errorf("stage release binary: %w", err)
	}

	// Promote the existing "current" target to "previous" before
	// repointing "current", so a rollback always has somewhere to go back
	// to. If there was no current slot yet (fresh node, still running the
	// image recovery binary), leave "previous" unset: rollback in that case
	// means removing "current" so the launcher falls back to recovery.
	if existingTarget, err := os.Readlink(s.currentLink()); err == nil {
		if err := atomicSymlink(existingTarget, s.previousLink()); err != nil {
			return fmt.Errorf("update previous slot: %w", err)
		}
	}
	if err := atomicSymlink(binaryPath, s.currentLink()); err != nil {
		return fmt.Errorf("activate new slot: %w", err)
	}

	if err := s.writePending(pendingActivation{Version: version, Attempts: 0, FirstAttemptAt: time.Now().UTC()}); err != nil {
		return fmt.Errorf("record pending activation: %w", err)
	}

	if err := s.pruneReleases(); err != nil {
		return fmt.Errorf("prune old releases: %w", err)
	}
	return nil
}

// ReconcileStartup must be called once, early, at agent process startup. It
// inspects any pending activation and decides whether this process should
// proceed normally, proceed while awaiting a health confirmation, or must
// exit immediately because the pending activation already exhausted its
// attempt/age budget and has been rolled back.
func (s *Store) ReconcileStartup(myVersion string) (ReconcileAction, error) {
	pending, ok, err := s.readPending()
	if err != nil {
		return ReconcileNone, fmt.Errorf("read pending activation: %w", err)
	}
	if !ok {
		return ReconcileNone, nil
	}
	if pending.Version != myVersion {
		// A pending activation exists for a different version than the one
		// currently executing. That means a previous rollback already
		// happened (current now points elsewhere) but pending.json was not
		// cleared, or this is unrelated leftover state; clear it defensively
		// rather than leaving stale pending state around forever.
		if err := s.clearPending(); err != nil {
			return ReconcileNone, fmt.Errorf("clear stale pending activation: %w", err)
		}
		return ReconcileNone, nil
	}

	pending.Attempts++
	expired := time.Since(pending.FirstAttemptAt) > defaultMaxActivationAge
	exhausted := pending.Attempts > defaultMaxActivationAttempts
	if expired || exhausted {
		if err := s.rollbackToPrevious(); err != nil {
			return ReconcileNone, fmt.Errorf("roll back failed activation: %w", err)
		}
		if err := s.clearPending(); err != nil {
			return ReconcileNone, fmt.Errorf("clear pending activation after rollback: %w", err)
		}
		if err := s.recordInstallOutcome("", fmt.Sprintf("update to %s failed to become healthy and was rolled back", pending.Version)); err != nil {
			return ReconcileNone, err
		}
		return ReconcileRolledBack, nil
	}

	if err := s.writePending(pending); err != nil {
		return ReconcileNone, fmt.Errorf("record activation attempt: %w", err)
	}
	return ReconcileAwaitingHealth, nil
}

// ConfirmHealthy must be called by a process once it has verified its own
// health (e.g. its HTTP server is serving and reports the expected version)
// after ReconcileStartup returned ReconcileAwaitingHealth for myVersion. It
// permanently clears the pending activation so future restarts of this same
// version are treated as ordinary restarts, not update attempts.
func (s *Store) ConfirmHealthy(myVersion string) error {
	pending, ok, err := s.readPending()
	if err != nil {
		return fmt.Errorf("read pending activation: %w", err)
	}
	if !ok || pending.Version != myVersion {
		return nil
	}
	if err := s.clearPending(); err != nil {
		return err
	}
	return s.recordInstallOutcome(myVersion, "")
}

func (s *Store) rollbackToPrevious() error {
	previousTarget, err := os.Readlink(s.previousLink())
	if err != nil {
		// No previous slot recorded: this was the first-ever activation, so
		// rolling back means removing "current" entirely and letting the
		// image-provided recovery binary launcher fallback take over.
		if removeErr := os.Remove(s.currentLink()); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove failed first activation: %w", removeErr)
		}
		return nil
	}
	return atomicSymlink(previousTarget, s.currentLink())
}

func (s *Store) pruneReleases() error {
	keep := map[string]bool{}
	if version, ok := s.versionAtLink(s.currentLink()); ok {
		keep[version] = true
	}
	if version, ok := s.versionAtLink(s.previousLink()); ok {
		keep[version] = true
	}
	entries, err := os.ReadDir(s.releasesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || keep[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.releasesDir(), entry.Name())); err != nil {
			return fmt.Errorf("remove superseded release %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) readPending() (pendingActivation, bool, error) {
	content, err := os.ReadFile(s.pendingPath())
	if err != nil {
		if os.IsNotExist(err) {
			return pendingActivation{}, false, nil
		}
		return pendingActivation{}, false, err
	}
	var pending pendingActivation
	if err := json.Unmarshal(content, &pending); err != nil {
		return pendingActivation{}, false, fmt.Errorf("decode pending activation: %w", err)
	}
	return pending, true, nil
}

func (s *Store) writePending(pending pendingActivation) error {
	content, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.pendingPath(), content, 0o644)
}

func (s *Store) clearPending() error {
	if err := os.Remove(s.pendingPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ReadStatus returns the persisted update status, or a zero-value status if
// none has ever been recorded.
func (s *Store) ReadStatus() (PersistedStatus, error) {
	content, err := os.ReadFile(s.statusPath())
	if err != nil {
		if os.IsNotExist(err) {
			return PersistedStatus{SchedulerEnabled: true}, nil
		}
		return PersistedStatus{}, err
	}
	var status PersistedStatus
	if err := json.Unmarshal(content, &status); err != nil {
		return PersistedStatus{}, fmt.Errorf("decode update status: %w", err)
	}
	return status, nil
}

// WriteStatus persists the given update status.
func (s *Store) WriteStatus(status PersistedStatus) error {
	content, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("create updates root: %w", err)
	}
	return atomicWriteFile(s.statusPath(), content, 0o644)
}

// PendingVersion returns the version awaiting health confirmation, if any.
func (s *Store) PendingVersion() (string, bool) {
	pending, ok, err := s.readPending()
	if err != nil || !ok {
		return "", false
	}
	return pending.Version, true
}

func (s *Store) recordInstallOutcome(succeededVersion, failureMessage string) error {
	status, err := s.ReadStatus()
	if err != nil {
		return err
	}
	status.LastInstallAt = time.Now().UTC()
	status.LastInstallError = failureMessage
	if succeededVersion != "" {
		// A confirmed-healthy install means the previously "available"
		// candidate is now installed; clear it so the UI does not keep
		// offering an already-installed version.
		if status.AvailableVersion == succeededVersion {
			status.AvailableVersion = ""
			status.AvailableReleaseURL = ""
		}
	}
	return s.WriteStatus(status)
}

func atomicWriteFile(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	tempFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()
	if _, err := tempFile.Write(content); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tempFile.Chmod(perm); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("rename temp file into place: %w", err)
	}
	return nil
}

func atomicWriteExecutable(path string, content []byte) error {
	return atomicWriteFile(path, content, 0o755)
}

// atomicSymlink creates or replaces a symlink at linkPath pointing at
// target, atomically via a temp symlink + rename.
func atomicSymlink(target, linkPath string) error {
	dir := filepath.Dir(linkPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	tempPath := linkPath + ".tmp-symlink"
	_ = os.Remove(tempPath)
	if err := os.Symlink(target, tempPath); err != nil {
		return fmt.Errorf("create temp symlink: %w", err)
	}
	if err := os.Rename(tempPath, linkPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename temp symlink into place: %w", err)
	}
	return nil
}
