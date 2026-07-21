package api

import (
	"crypto/rand"
	"crypto/subtle"
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
	defaultAuthPath   = "/var/lib/strom/webui-auth.json"
	defaultSessionTTL = 12 * time.Hour
	sessionCookieName = "strom_session"
	apiKeyScopeRead   = "read"
	apiKeyScopeWrite  = "write"

	// defaultAdminUsername is the fixed local admin account name. This node
	// only ever has a single local admin identity, so there is no need to
	// let operators choose a username.
	defaultAdminUsername = "admin"
)

var (
	errAuthDisabled        = errors.New("node web auth disabled")
	errAuthNotConfigured   = errors.New("node web auth not initialized")
	errAlreadyBootstrapped = errors.New("node web auth already initialized")
	errInvalidCredentials  = errors.New("invalid credentials")
	errUIPolicyManaged     = errors.New("local UI is controller-managed")
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
	ReadAPIKey   string    `json:"read_api_key,omitempty"`
	WriteAPIKey  string    `json:"write_api_key,omitempty"`
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

type apiKeyRequest struct {
	Scope    string `json:"scope"`
	Action   string `json:"action"`
	Password string `json:"password"`
}

type loginViewModel struct {
	Error      string
	UIDisabled bool
	CSRFToken  string
}

type bootstrapRequest struct {
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

type bootstrapViewModel struct {
	Error     string
	CSRFToken string
}

type settingsViewModel struct {
	Username          string
	UIEnabled         bool
	UIManaged         bool
	ReadAPIKeyExists  bool
	WriteAPIKeyExists bool
	Error             string
	Message           string
	CSRFToken         string
}

var loginTemplate = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Sign In - Strom Node</title>
  <style>
	:root { color-scheme:light dark; --bg:#f4efe7; --panel:#fffaf2; --input:#fff; --ink:#1f2933; --muted:#5f6c7b; --line:#d7c8b3; --accent:#0f766e; --danger:#b91c1c; --shadow:rgba(31,41,51,.08); }
	@media (prefers-color-scheme: dark) { :root { --bg:#1f2529; --panel:#2a3339; --input:#243036; --ink:#e8ece6; --muted:#b8c1bb; --line:#435159; --accent:#55b4a6; --danger:#f87171; --shadow:rgba(0,0,0,.32); } }
    * { box-sizing:border-box; }
	body { margin:0; min-height:100vh; display:grid; place-items:center; padding:20px; background:var(--bg); color:var(--ink); font-family:"Segoe UI",Tahoma,sans-serif; }
	.panel { width:min(100%,480px); background:var(--panel); border:1px solid var(--line); border-radius:20px; padding:28px; box-shadow:0 18px 40px var(--shadow); }
    h1 { margin:0 0 10px; font-size:clamp(2rem,5vw,2.8rem); line-height:1; }
    p { margin:0 0 16px; color:var(--muted); }
    .error { margin-bottom:16px; padding:12px 14px; border-radius:12px; border:1px solid rgba(185,28,28,.18); background:rgba(185,28,28,.08); color:var(--danger); }
    label { display:block; margin:14px 0 6px; font-size:.92rem; font-weight:600; }
	input { width:100%; padding:12px 14px; border-radius:12px; border:1px solid var(--line); background:var(--input); color:var(--ink); font:inherit; }
    button { margin-top:18px; width:100%; padding:12px 16px; border:0; border-radius:999px; background:var(--accent); color:#fff; font:inherit; font-weight:700; cursor:pointer; }
  </style>
</head>
<body>
  <main class="panel">
    <h1>Sign In</h1>
    <p>{{if .UIDisabled}}The local node dashboard is currently disabled. You can still sign in to review settings.{{else}}Sign in to reach the node dashboard and detailed status routes.{{end}}</p>
    {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
    <form method="post" action="/auth/login">
			<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
      <label for="username">Username</label>
      <input id="username" name="username" type="text" autocomplete="username" required>
      <label for="password">Password</label>
      <input id="password" name="password" type="password" autocomplete="current-password" required>
      <button type="submit">Sign in</button>
    </form>
  </main>
</body>
</html>`))

var bootstrapTemplate = template.Must(template.New("bootstrap").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Set Admin Password - Strom Node</title>
  <style>
	:root { color-scheme:light dark; --bg:#f4efe7; --panel:#fffaf2; --input:#fff; --ink:#1f2933; --muted:#5f6c7b; --line:#d7c8b3; --accent:#0f766e; --danger:#b91c1c; --shadow:rgba(31,41,51,.08); }
	@media (prefers-color-scheme: dark) { :root { --bg:#1f2529; --panel:#2a3339; --input:#243036; --ink:#e8ece6; --muted:#b8c1bb; --line:#435159; --accent:#55b4a6; --danger:#f87171; --shadow:rgba(0,0,0,.32); } }
    * { box-sizing:border-box; }
	body { margin:0; min-height:100vh; display:grid; place-items:center; padding:20px; background:var(--bg); color:var(--ink); font-family:"Segoe UI",Tahoma,sans-serif; }
	.panel { width:min(100%,480px); background:var(--panel); border:1px solid var(--line); border-radius:20px; padding:28px; box-shadow:0 18px 40px var(--shadow); }
    h1 { margin:0 0 10px; font-size:clamp(2rem,5vw,2.8rem); line-height:1; }
    p { margin:0 0 16px; color:var(--muted); }
    .error { margin-bottom:16px; padding:12px 14px; border-radius:12px; border:1px solid rgba(185,28,28,.18); background:rgba(185,28,28,.08); color:var(--danger); }
    label { display:block; margin:14px 0 6px; font-size:.92rem; font-weight:600; }
	input { width:100%; padding:12px 14px; border-radius:12px; border:1px solid var(--line); background:var(--input); color:var(--ink); font:inherit; }
    button { margin-top:18px; width:100%; padding:12px 16px; border:0; border-radius:999px; background:var(--accent); color:#fff; font:inherit; font-weight:700; cursor:pointer; }
  </style>
</head>
<body>
  <main class="panel">
    <h1>Set Admin Password</h1>
    <p>This node has a single local admin account (<strong>admin</strong>). Choose a password for it to finish setting up this node.</p>
    {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
    <form method="post" action="/auth/bootstrap">
			<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
      <label for="new_password">Admin password</label>
      <input id="new_password" name="new_password" type="password" autocomplete="new-password" minlength="8" required>
      <label for="confirm_password">Confirm password</label>
      <input id="confirm_password" name="confirm_password" type="password" autocomplete="new-password" minlength="8" required>
      <button type="submit">Set password and sign in</button>
    </form>
  </main>
</body>
</html>`))

var settingsTemplate = template.Must(template.New("settings").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Node Settings</title>
	<link rel="icon" href="/assets/favicon.svg" type="image/svg+xml">
	<link rel="stylesheet" href="/assets/styles.css">
	<script>
		(() => {
			try {
				const preference = localStorage.getItem("strom-theme-preference") || localStorage.getItem("strom-theme");
				const theme = preference === "dark" || (preference !== "light" && matchMedia("(prefers-color-scheme: dark)").matches) ? "dark" : "light";
				document.documentElement.dataset.theme = theme;
			} catch (_) {}
		})();
	</script>
  <style>
		:root { color-scheme:light; --bg:#f4efe7; --panel:#fffaf2; --input:#fff; --ink:#1f2933; --muted:#5f6c7b; --line:#d7c8b3; --accent:#0f766e; --accent-soft:rgba(15,118,110,.08); --danger:#b91c1c; --danger-soft:rgba(185,28,28,.08); --shadow:rgba(31,41,51,.08); }
		:root[data-theme="dark"] { color-scheme:dark; --bg:#1f2529; --panel:#2a3339; --input:#243036; --ink:#e8ece6; --muted:#b8c1bb; --line:#435159; --accent:#55b4a6; --accent-soft:rgba(85,180,166,.12); --danger:#f87171; --danger-soft:rgba(248,113,113,.12); --shadow:rgba(0,0,0,.32); }
    * { box-sizing:border-box; }
		body { margin:0; min-height:100vh; background:var(--bg); color:var(--ink); font-family:"Segoe UI",Tahoma,sans-serif; }
		main { max-width:760px; margin:0 auto; }
		.settings-shell { width:min(760px,calc(100vw - 32px)); margin:0 auto; padding:18px 0 32px; display:grid; gap:20px; }
		.panel { background:var(--panel); border:1px solid var(--line); border-radius:20px; padding:28px; box-shadow:0 18px 40px var(--shadow); }
		h1,h2 { margin:0; }
		h1 { font-size:clamp(1.7rem,4vw,2.2rem); }
		h2 { font-size:1.1rem; }
		p { color:var(--muted); }
		.page-head { display:flex; gap:16px; justify-content:space-between; align-items:flex-start; padding-bottom:20px; border-bottom:1px solid var(--line); }
		.page-head p { margin:6px 0 0; }
		.section-list { display:grid; gap:16px; margin-top:20px; }
		.settings-section { padding:20px; border:1px solid var(--line); border-radius:14px; background:color-mix(in srgb,var(--panel) 88%,transparent); }
		.section-head { display:flex; gap:12px; justify-content:space-between; align-items:flex-start; }
		.section-head p { margin:6px 0 0; max-width:58ch; }
		.status { display:inline-flex; align-items:center; flex:0 0 auto; padding:5px 9px; border-radius:999px; background:var(--accent-soft); color:var(--accent); font-size:.78rem; font-weight:700; text-transform:uppercase; letter-spacing:.04em; }
    .message, .error { margin:16px 0; padding:12px 14px; border-radius:12px; }
		.message { background:var(--accent-soft); color:var(--accent); border:1px solid color-mix(in srgb,var(--accent) 28%,var(--line)); }
		.error { background:var(--danger-soft); color:var(--danger); border:1px solid color-mix(in srgb,var(--danger) 28%,var(--line)); }
		button, .link { min-height:44px; padding:10px 16px; border-radius:999px; border:1px solid transparent; background:var(--accent); color:#fff; font:inherit; font-weight:700; cursor:pointer; text-decoration:none; display:inline-flex; align-items:center; justify-content:center; }
		.link { background:transparent; border-color:var(--line); color:var(--ink); }
		.button--secondary { background:transparent; border-color:var(--line); color:var(--ink); }
		.danger-zone { border-color:color-mix(in srgb,var(--danger) 38%,var(--line)); background:var(--danger-soft); }
		.danger { background:var(--danger); }
		form { margin:16px 0 0; }
    label { display:block; margin:14px 0 6px; font-size:.92rem; font-weight:600; }
		input { width:100%; padding:12px 14px; border-radius:12px; border:1px solid var(--line); background:var(--input); color:var(--ink); font:inherit; }
		.password-dialog { width:min(480px,calc(100vw - 32px)); margin:auto; padding:0; border:1px solid var(--line); border-radius:16px; background:var(--panel); color:var(--ink); box-shadow:0 24px 56px var(--shadow); }
		.password-dialog::backdrop { background:rgba(15,23,28,.58); }
		.password-dialog-content { padding:24px; }
		.password-dialog-head { display:flex; justify-content:space-between; gap:16px; align-items:flex-start; }
		.password-dialog-head p { margin:6px 0 0; }
		.password-dialog form { margin-top:18px; }
		.dialog-actions { display:flex; justify-content:flex-end; gap:10px; margin-top:20px; }
		.api-key-list { display:grid; gap:14px; margin-top:16px; }
		.api-key-row { padding-top:14px; border-top:1px solid var(--line); }
		.api-key-row:first-child { padding-top:0; border-top:0; }
		.api-key-label { display:flex; justify-content:space-between; gap:12px; align-items:baseline; }
		.api-key-label p { margin:4px 0 0; font-size:.9rem; }
		.api-key-controls { display:grid; grid-template-columns:minmax(0,1fr) auto auto; gap:8px; margin-top:10px; }
		.api-key-controls input { min-width:0; font-family:ui-monospace,SFMono-Regular,Consolas,monospace; }
		.api-key-result { display:grid; grid-template-columns:minmax(0,1fr) auto; gap:8px; margin-top:16px; }
		.api-key-result[hidden] { display:none; }
		.dialog-error[hidden] { display:none; }
		@media (max-width:720px) { .api-key-controls { grid-template-columns:1fr 1fr; } .api-key-controls input { grid-column:1 / -1; } .api-key-result { grid-template-columns:1fr; } }
		@media (max-width:720px) { .settings-shell { width:min(100vw - 20px,760px); gap:16px; } .panel { padding:18px; } .page-head { flex-direction:column; } .settings-section button { width:100%; } .section-head { flex-direction:column; } .settings-section { padding:18px; } }
  </style>
</head>
<body>
	<main class="settings-shell">
		<header class="topbar surface">
			<div class="brand">
				<img class="brand-mark" src="/assets/logo.svg" alt="Strom logo">
				<div class="brand-copy"><h1>Strom Node</h1></div>
			</div>
			<nav id="settings-toolbar" class="toolbar" aria-label="Settings actions">
				<button id="settings-menu-toggle" class="button button--ghost menu-toggle" type="button" aria-expanded="false" aria-haspopup="menu" aria-label="Toggle navigation menu">☰</button>
				<div class="profile-menu" id="settings-profile-menu">
					<div id="settings-menu-panel" class="menu-panel" role="menu" aria-label="Node menu" hidden>
						<div class="menu-section">
							<p class="menu-title">Appearance</p>
							<div class="appearance-segmented" role="radiogroup" aria-label="Color mode">
								<button class="appearance-option" type="button" role="radio" data-theme-option="system">System</button>
								<button class="appearance-option" type="button" role="radio" data-theme-option="light">Light</button>
								<button class="appearance-option" type="button" role="radio" data-theme-option="dark">Dark</button>
							</div>
						</div>
						<div class="menu-divider" role="separator"></div>
						<div class="menu-section">
							<a class="menu-link" href="/" role="menuitem">Dashboard</a>
							<a class="menu-link menu-link--active" href="/settings" role="menuitem" aria-current="page">Settings</a>
							<a class="menu-link" href="/diagnostics" role="menuitem">Diagnostics</a>
							<a class="menu-link menu-link--docs" href="https://foehammer82.github.io/strom/getting-started/" target="_blank" rel="noreferrer" role="menuitem">Docs</a>
							<div class="menu-divider" role="separator"></div>
							<a class="menu-link menu-link--sign-out" href="/auth/logout" role="menuitem">Sign out</a>
						</div>
					</div>
				</div>
			</nav>
		</header>
    <div class="panel">
			<header class="page-head">
				<div>
					<h1>Settings</h1>
					<p>Signed in as <strong>{{.Username}}</strong>.</p>
				</div>
			</header>
			{{if .Message}}<div class="message">{{.Message}}</div>{{end}}
			{{if .Error}}<div class="error">{{.Error}}</div>{{end}}

			<div class="section-list">
				<section class="settings-section">
					<div class="section-head">
						<div>
							<h2>Password</h2>
							<p>Change the local admin password used to sign in to this node's dashboard.</p>
						</div>
					</div>
					<button id="change-password-button" type="button">Change password</button>
				</section>

				<section class="settings-section">
					<div class="section-head">
						<div>
							<h2>API access</h2>
							<p>Create separate credentials for telemetry integrations and UPS controls. Revealing or replacing a key requires your local admin password.</p>
						</div>
					</div>
					<div class="api-key-list">
						<div class="api-key-row" data-api-key-scope="read">
							<div class="api-key-label">
								<div><strong>Read API key</strong><p>Detailed node health, diagnostics, and UPS telemetry.</p></div>
								<span class="status" data-api-key-status>{{if .ReadAPIKeyExists}}Generated{{else}}Not generated{{end}}</span>
							</div>
							<div class="api-key-controls">
								<input aria-label="Read API key" readonly value="{{if .ReadAPIKeyExists}}****************{{else}}Not generated{{end}}">
								{{if .ReadAPIKeyExists}}<button class="button--secondary api-key-action" type="button" data-api-key-action="reveal">Reveal</button>{{end}}
								<button class="api-key-action" type="button" data-api-key-action="regenerate">{{if .ReadAPIKeyExists}}Regenerate{{else}}Generate{{end}}</button>
							</div>
						</div>
						<div class="api-key-row" data-api-key-scope="write">
							<div class="api-key-label">
								<div><strong>Write API key</strong><p>All read access plus UPS commands and writable variables.</p></div>
								<span class="status" data-api-key-status>{{if .WriteAPIKeyExists}}Generated{{else}}Not generated{{end}}</span>
							</div>
							<div class="api-key-controls">
								<input aria-label="Write API key" readonly value="{{if .WriteAPIKeyExists}}****************{{else}}Not generated{{end}}">
								{{if .WriteAPIKeyExists}}<button class="button--secondary api-key-action" type="button" data-api-key-action="reveal">Reveal</button>{{end}}
								<button class="api-key-action" type="button" data-api-key-action="regenerate">{{if .WriteAPIKeyExists}}Regenerate{{else}}Generate{{end}}</button>
							</div>
						</div>
					</div>
				</section>

				<section class="settings-section">
					<div class="section-head">
						<div>
							<h2>Local UI</h2>
					<p>{{if .UIManaged}}This setting is managed by the controller for adopted operation.{{else}}Controls whether this node serves its local dashboard.{{end}}</p>
						</div>
						<span class="status">{{if .UIEnabled}}Enabled{{else}}Disabled{{end}}</span>
					</div>
					<form method="post" action="/settings/ui">
				<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
						<input type="hidden" name="enabled" value="{{if .UIEnabled}}false{{else}}true{{end}}">
				<button class="button--secondary" type="submit" {{if .UIManaged}}disabled{{end}}>{{if .UIEnabled}}Disable local UI{{else}}Enable local UI{{end}}</button>
					</form>
				</section>

				<section class="settings-section">
					<div class="section-head">
						<div>
							<h2>Session</h2>
							<p>End this local browser session.</p>
						</div>
					</div>
					<form method="post" action="/auth/logout">
				<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
						<button class="button--secondary" type="submit">Sign out</button>
					</form>
				</section>

				<section class="settings-section danger-zone">
					<div class="section-head">
						<div>
							<h2>Reset local access</h2>
							<p>Clears the local admin account and all current sessions. The next visit requires a new admin password.</p>
						</div>
					</div>
					<form method="post" action="/auth/reset" onsubmit="return confirm('Reset local web access? This signs out every session and requires a new admin password.');">
				<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
						<button class="danger" type="submit">Reset local web auth</button>
					</form>
				</section>
      </div>
    </div>
		<dialog id="password-dialog" class="password-dialog" aria-labelledby="password-dialog-title">
			<div class="password-dialog-content">
				<div class="password-dialog-head">
					<div>
						<h2 id="password-dialog-title">Change password</h2>
						<p>Enter your current password, then choose a new one.</p>
					</div>
				</div>
				<form method="post" action="/settings/password">
					<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
					<label for="current_password">Current password</label>
					<input id="current_password" name="current_password" type="password" autocomplete="current-password" required>
					<label for="new_password">New password</label>
					<input id="new_password" name="new_password" type="password" autocomplete="new-password" required>
					<label for="confirm_new_password">Confirm new password</label>
					<input id="confirm_new_password" name="confirm_password" type="password" autocomplete="new-password" required>
					<div class="dialog-actions">
						<button id="cancel-password-change" class="button--secondary" type="button">Cancel</button>
						<button type="submit">Update password</button>
					</div>
				</form>
			</div>
		</dialog>
		<dialog id="api-key-dialog" class="password-dialog" aria-labelledby="api-key-dialog-title">
			<div class="password-dialog-content">
				<div class="password-dialog-head">
					<div>
						<h2 id="api-key-dialog-title">API key</h2>
						<p id="api-key-dialog-description"></p>
					</div>
				</div>
				<form id="api-key-form">
					<label for="api-key-password">Current password</label>
					<input id="api-key-password" type="password" autocomplete="current-password" required>
					<div id="api-key-error" class="error dialog-error" role="alert" hidden></div>
					<div id="api-key-result" class="api-key-result" hidden>
						<input id="api-key-value" aria-label="API key value" readonly>
						<button id="copy-api-key" class="button--secondary" type="button">Copy</button>
					</div>
					<div class="dialog-actions">
						<button id="cancel-api-key" class="button--secondary" type="button">Close</button>
						<button id="submit-api-key" type="submit">Continue</button>
					</div>
				</form>
			</div>
		</dialog>
  </main>
	<script>
		(() => {
			const storageKey = "strom-theme-preference";
			const legacyKey = "strom-theme";
			const toolbar = document.getElementById("settings-toolbar");
			const menu = document.getElementById("settings-profile-menu");
			const panel = document.getElementById("settings-menu-panel");
			const passwordDialog = document.getElementById("password-dialog");
			const changePasswordButton = document.getElementById("change-password-button");
			const cancelPasswordChange = document.getElementById("cancel-password-change");
			const apiKeyDialog = document.getElementById("api-key-dialog");
			const apiKeyForm = document.getElementById("api-key-form");
			const apiKeyPassword = document.getElementById("api-key-password");
			const apiKeyTitle = document.getElementById("api-key-dialog-title");
			const apiKeyDescription = document.getElementById("api-key-dialog-description");
			const apiKeyError = document.getElementById("api-key-error");
			const apiKeyResult = document.getElementById("api-key-result");
			const apiKeyValue = document.getElementById("api-key-value");
			const apiKeySubmit = document.getElementById("submit-api-key");
			const apiKeyCopy = document.getElementById("copy-api-key");
			const cancelAPIKey = document.getElementById("cancel-api-key");
			const csrfToken = {{printf "%q" .CSRFToken}};
			let apiKeyOperation = null;
			const toggles = [document.getElementById("settings-menu-toggle")];
			const options = Array.from(document.querySelectorAll("[data-theme-option]"));
			const preference = () => localStorage.getItem(storageKey) || localStorage.getItem(legacyKey) || "system";
			const resolved = (value) => value === "system" ? (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light") : value;
			const applyTheme = (value, persist) => {
				const next = ["system", "light", "dark"].includes(value) ? value : "system";
				document.documentElement.dataset.theme = resolved(next);
				options.forEach((option) => option.setAttribute("aria-checked", String(option.dataset.themeOption === next)));
				if (persist) { localStorage.setItem(storageKey, next); localStorage.setItem(legacyKey, resolved(next)); }
			};
			const closeMenu = () => { panel.hidden = true; toolbar.classList.remove("is-open"); toggles.forEach((toggle) => toggle.setAttribute("aria-expanded", "false")); };
			const toggleMenu = () => { const open = panel.hidden; panel.hidden = !open; toolbar.classList.toggle("is-open", open); toggles.forEach((toggle) => toggle.setAttribute("aria-expanded", String(open))); };
			applyTheme(preference(), false);
			toggles.forEach((toggle) => toggle.addEventListener("click", (event) => { event.stopPropagation(); toggleMenu(); }));
			options.forEach((option) => option.addEventListener("click", () => { applyTheme(option.dataset.themeOption, true); closeMenu(); }));
			changePasswordButton.addEventListener("click", () => { passwordDialog.showModal(); document.getElementById("current_password").focus(); });
			cancelPasswordChange.addEventListener("click", () => passwordDialog.close());
			passwordDialog.addEventListener("click", (event) => { if (event.target === passwordDialog) passwordDialog.close(); });
			const resetAPIKeyDialog = () => {
				apiKeyForm.reset();
				apiKeyError.hidden = true;
				apiKeyError.textContent = "";
				apiKeyResult.hidden = true;
				apiKeyValue.value = "";
				apiKeyOperation = null;
			};
			document.querySelectorAll(".api-key-action").forEach((button) => button.addEventListener("click", () => {
				const row = button.closest("[data-api-key-scope]");
				const scope = row.dataset.apiKeyScope;
				const action = button.dataset.apiKeyAction;
				apiKeyOperation = { scope, action, row };
				apiKeyTitle.textContent = (scope === "read" ? "Read" : "Write") + " API key";
				apiKeyDescription.textContent = action === "reveal"
					? "Enter your current password to reveal this API key."
					: "Enter your current password to generate a new API key. The previous key stops working immediately.";
				apiKeySubmit.textContent = action === "reveal" ? "Reveal key" : "Generate key";
				apiKeyForm.reset();
				apiKeyError.hidden = true;
				apiKeyResult.hidden = true;
				apiKeyValue.value = "";
				apiKeyDialog.showModal();
				apiKeyPassword.focus();
			}));
			cancelAPIKey.addEventListener("click", () => apiKeyDialog.close());
			apiKeyDialog.addEventListener("click", (event) => { if (event.target === apiKeyDialog) apiKeyDialog.close(); });
			apiKeyDialog.addEventListener("close", resetAPIKeyDialog);
			apiKeyForm.addEventListener("submit", async (event) => {
				event.preventDefault();
				if (!apiKeyOperation) return;
				apiKeyError.hidden = true;
				apiKeySubmit.disabled = true;
				try {
					const response = await fetch("/settings/api-key", {
						method: "POST",
						headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken },
						body: JSON.stringify({ scope: apiKeyOperation.scope, action: apiKeyOperation.action, password: apiKeyPassword.value }),
					});
					const payload = await response.json();
					if (!response.ok) throw new Error(payload.error || "Unable to update API key");
					apiKeyValue.value = payload.key;
					apiKeyResult.hidden = false;
					apiKeyOperation.row.querySelector("input").value = "****************";
					apiKeyOperation.row.querySelector("[data-api-key-status]").textContent = "Generated";
				} catch (error) {
					apiKeyError.textContent = error.message;
					apiKeyError.hidden = false;
				} finally {
					apiKeySubmit.disabled = false;
				}
			});
			apiKeyCopy.addEventListener("click", async () => {
				if (!apiKeyValue.value) return;
				try {
					await navigator.clipboard.writeText(apiKeyValue.value);
				} catch (_) {
					apiKeyValue.select();
					document.execCommand("copy");
				}
				apiKeyCopy.textContent = "Copied";
				setTimeout(() => { apiKeyCopy.textContent = "Copy"; }, 1500);
			});
			document.addEventListener("click", (event) => {
				if (menu.contains(event.target) || toggles.some((toggle) => toggle.contains(event.target))) return;
				closeMenu();
			});
			document.addEventListener("keydown", (event) => { if (event.key === "Escape") closeMenu(); });
		})();
	</script>
</body>
</html>`))

func newAuthManager(disableAuth bool, path string, logger *log.Logger) *authManager {
	if path == "" {
		path = defaultAuthPath
	}
	return &authManager{enabled: !disableAuth, path: path, sessionTTL: defaultSessionTTL, logger: logger, sessions: make(map[string]authSession)}
}

func (a *authManager) Enabled() bool {
	return a != nil && a.enabled
}

// Bootstrapped reports whether the local admin account has already been
// created. Nodes start with no stored auth config at all; the first browser
// client to reach the node must choose the admin password via Bootstrap
// before any session can be created.
func (a *authManager) Bootstrapped() bool {
	if !a.Enabled() {
		return true
	}
	_, err := a.load()
	return err == nil
}

// Bootstrap creates the local admin account on a fresh node by setting its
// initial password. It fails if the account already exists, so it can only
// ever be used once per auth config (use Reset to start over).
func (a *authManager) Bootstrap(newPassword, confirmPassword string) error {
	if !a.Enabled() {
		return errAuthDisabled
	}
	if a.Bootstrapped() {
		return errAlreadyBootstrapped
	}
	if newPassword != confirmPassword {
		return errors.New("passwords do not match")
	}
	if len(newPassword) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	enabled := true
	stored := &storedAuth{Username: defaultAdminUsername, PasswordHash: string(hash), CreatedAt: time.Now().UTC(), UIEnabled: &enabled}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked(stored)
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

func (a *authManager) APIKeyExists(scope string) (bool, error) {
	stored, err := a.load()
	if err != nil {
		return false, err
	}
	key, err := stored.apiKey(scope)
	if err != nil {
		return false, err
	}
	return key != "", nil
}

func (a *authManager) RevealAPIKey(scope, password string) (string, error) {
	stored, err := a.load()
	if err != nil {
		return "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(password)) != nil {
		return "", errInvalidCredentials
	}
	return stored.apiKey(scope)
}

func (a *authManager) RegenerateAPIKey(scope, password string) (string, error) {
	stored, err := a.load()
	if err != nil {
		return "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(password)) != nil {
		return "", errInvalidCredentials
	}
	key, err := generateAPIKey(scope)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	updated := *stored
	if err := updated.setAPIKey(scope, key); err != nil {
		return "", err
	}
	if err := a.saveLocked(&updated); err != nil {
		return "", err
	}
	return key, nil
}

func (a *authManager) MatchAPIKey(value string) (string, bool, error) {
	if value == "" {
		return "", false, nil
	}
	stored, err := a.load()
	if err != nil {
		return "", false, err
	}
	for _, scope := range []string{apiKeyScopeWrite, apiKeyScopeRead} {
		key, keyErr := stored.apiKey(scope)
		if keyErr != nil {
			return "", false, keyErr
		}
		if key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(value)) == 1 {
			return scope, true, nil
		}
	}
	return "", false, nil
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

// Reset clears the local admin account and all sessions, returning the node
// to its pending first-run state. The next visit must complete Bootstrap
// again to choose a new admin password before signing in.
func (a *authManager) Reset() error {
	a.mu.Lock()
	a.cached = nil
	a.sessions = make(map[string]authSession)
	if err := os.Remove(a.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.mu.Unlock()
		return fmt.Errorf("remove auth config: %w", err)
	}
	a.mu.Unlock()
	return nil
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

func (s *storedAuth) apiKey(scope string) (string, error) {
	switch scope {
	case apiKeyScopeRead:
		return s.ReadAPIKey, nil
	case apiKeyScopeWrite:
		return s.WriteAPIKey, nil
	default:
		return "", fmt.Errorf("unknown API key scope %q", scope)
	}
}

func (s *storedAuth) setAPIKey(scope, key string) error {
	switch scope {
	case apiKeyScopeRead:
		s.ReadAPIKey = key
	case apiKeyScopeWrite:
		s.WriteAPIKey = key
	default:
		return fmt.Errorf("unknown API key scope %q", scope)
	}
	return nil
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

func generateAPIKey(scope string) (string, error) {
	prefix := ""
	switch scope {
	case apiKeyScopeRead:
		prefix = "strom_ro_"
	case apiKeyScopeWrite:
		prefix = "strom_rw_"
	default:
		return "", fmt.Errorf("unknown API key scope %q", scope)
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	return prefix + token, nil
}
