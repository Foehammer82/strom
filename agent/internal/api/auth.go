package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	defaultAuthPath   = "/var/lib/wattkeeper/webui-auth.json"
	defaultSessionTTL = 12 * time.Hour
	sessionCookieName = "wattkeeper_session"

	// defaultAdminUsername is the fixed local admin account name. This node
	// only ever has a single local admin identity, so there is no need to
	// let operators choose a username.
	defaultAdminUsername = "admin"
	// adminPasswordEnvVar overrides the built-in default admin password. It
	// is read directly from the process environment (not piped through a
	// CLI flag) so the secret never appears in argv/process listings.
	adminPasswordEnvVar = "AGENT_ADMIN_PASSWORD"
	// fallbackAdminPassword is the built-in password used only when
	// adminPasswordEnvVar is not set. It is intentionally public, so nodes
	// relying on it are flagged with startup, login, and settings warnings
	// until the operator sets adminPasswordEnvVar or changes the password.
	fallbackAdminPassword = "wattkeeper-admin"
)

var (
	errAuthDisabled       = errors.New("node web auth disabled")
	errAuthNotConfigured  = errors.New("node web auth not initialized")
	errInvalidCredentials = errors.New("invalid credentials")
	errUIPolicyManaged    = errors.New("local UI is controller-managed")
)

type authManager struct {
	enabled    bool
	path       string
	sessionTTL time.Duration
	logger     *log.Logger

	mu       sync.RWMutex
	cached   *storedAuth
	sessions map[string]authSession
}

type storedAuth struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
	UIEnabled    *bool     `json:"ui_enabled,omitempty"`
	UIManaged    *bool     `json:"ui_managed,omitempty"`
}

type authSession struct {
	Username  string
	CSRFToken string
	ExpiresAt time.Time
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

type loginViewModel struct {
	Error                  string
	UIDisabled             bool
	DefaultPasswordWarning bool
	CSRFToken              string
}

type settingsViewModel struct {
	Username             string
	UIEnabled            bool
	UIManaged            bool
	UsingDefaultPassword bool
	Error                string
	Message              string
	CSRFToken            string
}

var loginTemplate = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Sign In - Wattkeeper Node</title>
  <style>
    :root { --bg:#f4efe7; --panel:#fffaf2; --ink:#1f2933; --muted:#5f6c7b; --line:#d7c8b3; --accent:#0f766e; --danger:#b91c1c; }
    * { box-sizing:border-box; }
    body { margin:0; min-height:100vh; display:grid; place-items:center; padding:20px; background:linear-gradient(180deg,#efe7da 0%,var(--bg) 55%,#efe9df 100%); color:var(--ink); font-family:"Segoe UI",Tahoma,sans-serif; }
    .panel { width:min(100%,480px); background:rgba(255,250,242,.96); border:1px solid var(--line); border-radius:20px; padding:28px; box-shadow:0 18px 40px rgba(31,41,51,.08); }
    h1 { margin:0 0 10px; font-size:clamp(2rem,5vw,2.8rem); line-height:1; }
    p { margin:0 0 16px; color:var(--muted); }
    .error { margin-bottom:16px; padding:12px 14px; border-radius:12px; border:1px solid rgba(185,28,28,.18); background:rgba(185,28,28,.08); color:var(--danger); }
    label { display:block; margin:14px 0 6px; font-size:.92rem; font-weight:600; }
    input { width:100%; padding:12px 14px; border-radius:12px; border:1px solid var(--line); background:#fff; font:inherit; }
    button { margin-top:18px; width:100%; padding:12px 16px; border:0; border-radius:999px; background:var(--accent); color:#fff; font:inherit; font-weight:700; cursor:pointer; }
    .note { margin-top:14px; }
  </style>
</head>
<body>
  <main class="panel">
    <h1>Sign In</h1>
    <p>{{if .UIDisabled}}The local node dashboard is currently disabled. You can still sign in to review settings.{{else}}Sign in to reach the node dashboard and detailed status routes.{{end}}</p>
    {{if .DefaultPasswordWarning}}<div class="error"><strong>Security warning:</strong> this node is using the built-in default admin password. Set <code>AGENT_ADMIN_PASSWORD</code> to a strong secret, or sign in and change the password immediately.</div>{{end}}
    {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
    <form method="post" action="/auth/login">
			<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
      <label for="username">Username</label>
      <input id="username" name="username" type="text" autocomplete="username" required>
      <label for="password">Password</label>
      <input id="password" name="password" type="password" autocomplete="current-password" required>
      <button type="submit">Sign in</button>
    </form>
    <p class="note">Public status remains available at <strong>/status</strong>.</p>
  </main>
</body>
</html>`))

var settingsTemplate = template.Must(template.New("settings").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Node Settings</title>
  <style>
    :root { --bg:#f4efe7; --panel:#fffaf2; --ink:#1f2933; --muted:#5f6c7b; --line:#d7c8b3; --accent:#0f766e; --danger:#b91c1c; }
    * { box-sizing:border-box; }
    body { margin:0; min-height:100vh; padding:24px; background:linear-gradient(180deg,#efe7da 0%,var(--bg) 55%,#efe9df 100%); color:var(--ink); font-family:"Segoe UI",Tahoma,sans-serif; }
    main { max-width:720px; margin:0 auto; }
    .panel { background:rgba(255,250,242,.96); border:1px solid var(--line); border-radius:20px; padding:24px; box-shadow:0 18px 40px rgba(31,41,51,.08); }
    h1,h2 { margin:0 0 10px; }
    p { color:var(--muted); }
    .row { display:flex; gap:12px; flex-wrap:wrap; align-items:center; }
    .message, .error { margin:16px 0; padding:12px 14px; border-radius:12px; }
    .message { background:rgba(15,118,110,.08); color:var(--accent); border:1px solid rgba(15,118,110,.18); }
    .error { background:rgba(185,28,28,.08); color:var(--danger); border:1px solid rgba(185,28,28,.18); }
    button, .link { padding:12px 16px; border-radius:999px; border:0; background:var(--accent); color:#fff; font:inherit; font-weight:700; cursor:pointer; text-decoration:none; display:inline-block; }
    .danger { background:var(--danger); }
    form { margin:16px 0 0; }
    label { display:block; margin:14px 0 6px; font-size:.92rem; font-weight:600; }
    input { width:100%; padding:12px 14px; border-radius:12px; border:1px solid var(--line); background:#fff; font:inherit; }
  </style>
</head>
<body>
  <main>
    <div class="panel">
      <div class="row">
        <h1>Node Settings</h1>
        <a class="link" href="/">Dashboard</a>
      </div>
      <p>Signed in as <strong>{{.Username}}</strong>.</p>
      {{if .Message}}<div class="message">{{.Message}}</div>{{end}}
      {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
      {{if .UsingDefaultPassword}}<div class="error"><strong>Security warning:</strong> this account is still using the built-in default admin password. Set <code>AGENT_ADMIN_PASSWORD</code> to a strong secret, or change the password below now.</div>{{end}}

      <h2>Password</h2>
      <p>Change the local admin password used to sign in to this node's dashboard.</p>
      <form method="post" action="/settings/password">
				<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <label for="current_password">Current password</label>
        <input id="current_password" name="current_password" type="password" autocomplete="current-password" required>
        <label for="new_password">New password</label>
        <input id="new_password" name="new_password" type="password" autocomplete="new-password" required>
        <label for="confirm_new_password">Confirm new password</label>
        <input id="confirm_new_password" name="confirm_password" type="password" autocomplete="new-password" required>
        <button type="submit">Update password</button>
      </form>

      <h2>Local UI</h2>
			<p>Current state: <strong>{{if .UIEnabled}}enabled{{else}}disabled{{end}}</strong>. {{if .UIManaged}}This setting is currently managed by the controller for adopted operation.{{else}}This only affects the local dashboard surface and is the future controller-managed toggle point.{{end}}</p>
      <form method="post" action="/settings/ui">
				<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <input type="hidden" name="enabled" value="{{if .UIEnabled}}false{{else}}true{{end}}">
				<button type="submit" {{if .UIManaged}}disabled{{end}}>{{if .UIEnabled}}Disable local UI{{else}}Enable local UI{{end}}</button>
      </form>

      <h2>Session</h2>
      <form method="post" action="/auth/logout">
				<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <button type="submit">Sign out</button>
      </form>

      <h2>Reset</h2>
      <p>Resetting clears the local admin account and all current sessions, then immediately re-provisions the default admin account (username <code>admin</code>).</p>
      <form method="post" action="/auth/reset">
				<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <button class="danger" type="submit">Reset local web auth</button>
      </form>
    </div>
  </main>
</body>
</html>`))

func newAuthManager(disableAuth bool, path string, logger *log.Logger) *authManager {
	if path == "" {
		path = defaultAuthPath
	}
	a := &authManager{enabled: !disableAuth, path: path, sessionTTL: defaultSessionTTL, logger: logger, sessions: make(map[string]authSession)}
	if err := a.ensureProvisioned(); err != nil && logger != nil {
		logger.Printf("provision default admin account failed: %v", err)
	}
	return a
}

func (a *authManager) Enabled() bool {
	return a != nil && a.enabled
}

// resolveAdminPassword returns the admin password to provision, preferring
// adminPasswordEnvVar over the built-in fallback. usingFallback reports
// whether the insecure built-in default is in effect.
func resolveAdminPassword() (password string, usingFallback bool) {
	if v := strings.TrimSpace(os.Getenv(adminPasswordEnvVar)); v != "" {
		return v, false
	}
	return fallbackAdminPassword, true
}

// ensureProvisioned auto-creates the local admin account the first time this
// node runs, so operators never have to complete a manual bootstrap step. If
// adminPasswordEnvVar is unset, it provisions a known default password and
// logs a startup warning; UsingDefaultPassword surfaces the same warning in
// the login and settings pages until the password is changed.
func (a *authManager) ensureProvisioned() error {
	if !a.Enabled() {
		return nil
	}
	a.mu.Lock()
	if _, err := os.Stat(a.path); err == nil {
		a.mu.Unlock()
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		a.mu.Unlock()
		return fmt.Errorf("stat auth config: %w", err)
	}
	password, usingFallback := resolveAdminPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("hash password: %w", err)
	}
	enabled := true
	stored := &storedAuth{Username: defaultAdminUsername, PasswordHash: string(hash), CreatedAt: time.Now().UTC(), UIEnabled: &enabled}
	saveErr := a.saveLocked(stored)
	a.mu.Unlock()
	if saveErr != nil {
		return saveErr
	}
	if usingFallback && a.logger != nil {
		a.logger.Printf("WARNING: node web admin account provisioned with the built-in default password; set %s to a strong secret to remove this warning", adminPasswordEnvVar)
	}
	return nil
}

// UsingDefaultPassword reports whether the current admin password still
// matches the built-in fallback. It is recomputed on every call instead of
// persisted, so it self-corrects as soon as the password is changed.
func (a *authManager) UsingDefaultPassword() bool {
	if !a.Enabled() {
		return false
	}
	stored, err := a.load()
	if err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(fallbackAdminPassword)) == nil
}

func (a *authManager) Authenticate(username, password string) error {
	if !a.Enabled() {
		return nil
	}
	stored, err := a.load()
	if err != nil {
		return err
	}
	if stored.Username != strings.TrimSpace(username) {
		return errInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(password)); err != nil {
		return errInvalidCredentials
	}
	return nil
}

func (a *authManager) CreateSession(username string) (string, string, error) {
	if !a.Enabled() {
		return "", "", errAuthDisabled
	}
	token, err := randomToken(32)
	if err != nil {
		return "", "", err
	}
	csrfToken, err := randomToken(24)
	if err != nil {
		return "", "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cleanupExpiredSessionsLocked(time.Now().UTC())
	a.sessions[token] = authSession{Username: username, CSRFToken: csrfToken, ExpiresAt: time.Now().UTC().Add(a.sessionTTL)}
	return token, csrfToken, nil
}

func (a *authManager) SessionUsername(r *http.Request) (string, error) {
	if !a.Enabled() {
		return "", nil
	}
	session, err := a.sessionFromRequest(r)
	if err != nil {
		return "", errInvalidCredentials
	}
	return session.Username, nil
}

func (a *authManager) SessionCSRFToken(r *http.Request) (string, error) {
	if !a.Enabled() {
		return "", nil
	}
	session, err := a.sessionFromRequest(r)
	if err != nil {
		return "", errInvalidCredentials
	}
	if strings.TrimSpace(session.CSRFToken) == "" {
		return "", errInvalidCredentials
	}
	return session.CSRFToken, nil
}

func (a *authManager) ClearSession(token string) {
	if token == "" {
		return
	}
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

// ChangePassword verifies currentPassword against the stored hash and, if it
// matches, replaces the stored password with newPassword.
func (a *authManager) ChangePassword(currentPassword, newPassword string) error {
	if !a.Enabled() {
		return errAuthDisabled
	}
	if len(newPassword) < 8 {
		return errors.New("new password must be at least 8 characters")
	}
	stored, err := a.load()
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(currentPassword)) != nil {
		return errInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	updated := *stored
	updated.PasswordHash = string(hash)
	return a.saveLocked(&updated)
}

func (a *authManager) UIEnabled() (bool, error) {
	stored, err := a.load()
	if err != nil {
		return false, err
	}
	return stored.isUIEnabled(), nil
}

func (a *authManager) SetUIEnabled(enabled bool) error {
	stored, err := a.load()
	if err != nil {
		return err
	}
	if stored.isUIManaged() {
		return errUIPolicyManaged
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stored.UIEnabled = &enabled
	return a.saveLocked(stored)
}

func (a *authManager) UIManaged() (bool, error) {
	stored, err := a.load()
	if err != nil {
		return false, err
	}
	return stored.isUIManaged(), nil
}

func (a *authManager) ApplyControllerUIPolicy(managed, enabled bool) error {
	stored, err := a.load()
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stored.UIManaged = &managed
	stored.UIEnabled = &enabled
	return a.saveLocked(stored)
}

// Reset clears the local admin account and all sessions, then immediately
// re-provisions the default admin account so the node never requires a
// manual bootstrap step to regain access.
func (a *authManager) Reset() error {
	a.mu.Lock()
	a.cached = nil
	a.sessions = make(map[string]authSession)
	if err := os.Remove(a.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.mu.Unlock()
		return fmt.Errorf("remove auth config: %w", err)
	}
	a.mu.Unlock()
	return a.ensureProvisioned()
}

func (a *authManager) load() (*storedAuth, error) {
	a.mu.RLock()
	if a.cached != nil {
		stored := *a.cached
		a.mu.RUnlock()
		return &stored, nil
	}
	a.mu.RUnlock()
	content, err := os.ReadFile(a.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errAuthNotConfigured
		}
		return nil, fmt.Errorf("read auth config: %w", err)
	}
	var stored storedAuth
	if err := json.Unmarshal(content, &stored); err != nil {
		return nil, fmt.Errorf("decode auth config: %w", err)
	}
	if stored.Username == "" || stored.PasswordHash == "" {
		return nil, fmt.Errorf("decode auth config: missing username or password hash")
	}
	a.mu.Lock()
	a.cached = &stored
	a.mu.Unlock()
	return &stored, nil
}

func (a *authManager) saveLocked(stored *storedAuth) error {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return fmt.Errorf("create auth config dir: %w", err)
	}
	content, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auth config: %w", err)
	}
	tempPath := a.path + ".tmp"
	if err := os.WriteFile(tempPath, append(content, '\n'), 0o600); err != nil {
		return fmt.Errorf("write auth config: %w", err)
	}
	if err := os.Rename(tempPath, a.path); err != nil {
		return fmt.Errorf("rename auth config: %w", err)
	}
	copyStored := *stored
	a.cached = &copyStored
	return nil
}

func (a *authManager) cleanupExpiredSessionsLocked(now time.Time) {
	for token, session := range a.sessions {
		if now.After(session.ExpiresAt) {
			delete(a.sessions, token)
		}
	}
}

func (a *authManager) sessionFromRequest(r *http.Request) (authSession, error) {
	if r == nil {
		return authSession{}, errInvalidCredentials
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return authSession{}, errInvalidCredentials
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cleanupExpiredSessionsLocked(time.Now().UTC())
	session, ok := a.sessions[cookie.Value]
	if !ok {
		return authSession{}, errInvalidCredentials
	}
	return session, nil
}

func (s *storedAuth) isUIEnabled() bool {
	return s.UIEnabled == nil || *s.UIEnabled
}

func (s *storedAuth) isUIManaged() bool {
	return s.UIManaged != nil && *s.UIManaged
}

func validateLoginRequest(req loginRequest) error {
	if strings.TrimSpace(req.Username) == "" || req.Password == "" {
		return errors.New("username and password are required")
	}
	return nil
}

func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
