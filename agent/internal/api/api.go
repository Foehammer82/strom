package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Foehammer82/strom/agent/internal/nutconf"
	"github.com/Foehammer82/strom/agent/internal/updates"
	swguiV5 "github.com/swaggest/swgui/v5emb"
)

const (
	defaultCPUTempPath = "/sys/class/thermal/thermal_zone0/temp"
	defaultMemInfoPath = "/proc/meminfo"
	defaultCPUStatPath = "/proc/stat"
	defaultRootPath    = "/"
	defaultUPSCPath    = "upsc"
	csrfCookieName     = "strom_csrf"
	startingStatus     = "starting"
	unknownStatus      = "unknown"
	// agentSystemdUnit is the systemd unit restarted after a standalone
	// update install so the newly activated release takes effect.
	agentSystemdUnit = "strom-agent.service"
)

type commandRunner interface {
	CombinedOutput(ctx context.Context, path string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) CombinedOutput(ctx context.Context, path string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

type Options struct {
	Version         string
	Serial          string
	StartedAt       time.Time
	Runner          commandRunner
	UPSCPath        string
	UPSCmdPath      string
	UPSRWPath       string
	CPUTempPath     string
	MemInfoPath     string
	CPUStatPath     string
	RootPath        string
	AdoptionPath    string
	DisableAuth     bool
	AuthPath        string
	UPSMetadataPath string
	// UpdatesRoot is the persistent directory used for durable agent release
	// slots (see internal/updates.Store). Empty uses the package default.
	UpdatesRoot string
	// UpdatesChecker enables the standalone GitHub-based update check/install
	// API endpoints when non-nil. Nil disables them (they respond 404).
	UpdatesChecker *updates.Checker
	NUTUser        string
	NUTPassword    string
	Adopter        adoptionHandler
	SSHAccess      sshAccessManager
}

type adoptionHandler interface {
	ApplyAdoption(context.Context, adoptRequest) (adoptResponse, error)
}

type Service struct {
	logger          *log.Logger
	version         string
	serial          string
	startedAt       time.Time
	runner          commandRunner
	upscPath        string
	upscmdPath      string
	upsrwPath       string
	cpuTempPath     string
	memInfoPath     string
	cpuStatPath     string
	rootPath        string
	adoptionPath    string
	upsMetadataPath string
	updatesStore    *updates.Store
	updatesChecker  *updates.Checker
	nutUser         string
	nutPassword     string
	auth            *authManager
	adopter         adoptionHandler
	sshAccess       sshAccessManager

	mu            sync.RWMutex
	devices       []nutconf.DetectedUPS
	upsMetadata   map[string]upsMetadata
	cache         http.Handler
	lastCPUSample cpuStatSample
}

// cpuStatSample is a single reading of cumulative CPU jiffy counters (from
// /proc/stat), used to compute CPU usage as a percentage over the interval
// between two health requests.
type cpuStatSample struct {
	idle  uint64
	total uint64
}

type healthResponse struct {
	Version               string      `json:"version"`
	UptimeSeconds         int64       `json:"uptime_seconds"`
	Serial                string      `json:"serial"`
	CPUTemperatureCelsius *float64    `json:"cpu_temperature_celsius,omitempty"`
	CPUUsagePercent       *float64    `json:"cpu_usage_percent,omitempty"`
	MemoryUsedBytes       uint64      `json:"memory_used_bytes"`
	MemoryTotalBytes      uint64      `json:"memory_total_bytes"`
	DiskFreeBytes         uint64      `json:"disk_free_bytes"`
	UPSes                 []upsHealth `json:"upses"`
}

type diagnosticCheck struct {
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

type diagnosticsResponse struct {
	GeneratedAt time.Time             `json:"generated_at"`
	Inventory   []nutconf.DetectedUPS `json:"inventory"`
	USBDevices  diagnosticCheck       `json:"usb_devices"`
	Scanner     diagnosticCheck       `json:"scanner"`
	NUTConfig   diagnosticCheck       `json:"nut_config"`
	NUTServer   diagnosticCheck       `json:"nut_server"`
}

type statusResponse struct {
	Status   string `json:"status"`
	UPSCount int    `json:"ups_count"`
}

type upsHealth struct {
	Name                 string      `json:"name"`
	Driver               string      `json:"driver"`
	Metadata             upsMetadata `json:"metadata"`
	Status               string      `json:"status"`
	BatteryChargePercent *float64    `json:"battery_charge_percent,omitempty"`
	LoadPercent          *float64    `json:"load_percent,omitempty"`
	RuntimeSeconds       *int64      `json:"runtime_seconds,omitempty"`
	InputVoltage         *float64    `json:"input_voltage,omitempty"`
	OutputVoltage        *float64    `json:"output_voltage,omitempty"`
	BatteryVoltage       *float64    `json:"battery_voltage,omitempty"`
}

type upsCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Destructive bool   `json:"destructive"`
}

type upsDetailResponse struct {
	Name      string            `json:"name"`
	Driver    string            `json:"driver"`
	Metadata  upsMetadata       `json:"metadata"`
	Status    string            `json:"status"`
	Metrics   upsHealth         `json:"metrics"`
	Variables map[string]string `json:"variables"`
	Commands  []upsCommand      `json:"commands"`
	Writable  []upsWritableVar  `json:"writable"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type upsWritableVar struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Editor       string   `json:"editor"`
	CurrentValue string   `json:"current_value,omitempty"`
	Options      []string `json:"options,omitempty"`
	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
}

type upsCommandRequest struct {
	Command string `json:"cmd"`
}

type upsMetadataResponse struct {
	UPS      string      `json:"ups"`
	Metadata upsMetadata `json:"metadata"`
}

type upsCommandResponse struct {
	UPS     string `json:"ups"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

type upsSetVarRequest struct {
	Variable string `json:"var"`
	Value    string `json:"value"`
}

type upsSetVarResponse struct {
	UPS      string `json:"ups"`
	Variable string `json:"variable"`
	Value    string `json:"value"`
	Output   string `json:"output"`
}

type uiPolicyRequest struct {
	Managed bool `json:"managed"`
	Enabled bool `json:"enabled"`
}

type otaUpdateRequest struct {
	Version         string `json:"version"`
	BinaryBase64    string `json:"binary_base64"`
	SHA256          string `json:"sha256"`
	SignatureBase64 string `json:"signature_base64"`
}

type otaUpdateResponse struct {
	Status          string `json:"status"`
	Version         string `json:"version"`
	SHA256          string `json:"sha256"`
	RestartRequired bool   `json:"restart_required"`
}

type updatesStatusResponse struct {
	InstalledVersion    string `json:"installed_version"`
	PendingVersion      string `json:"pending_version,omitempty"`
	UpdatesSupported    bool   `json:"updates_supported"`
	SchedulerEnabled    bool   `json:"scheduler_enabled"`
	LastCheckedAt       string `json:"last_checked_at,omitempty"`
	LastCheckError      string `json:"last_check_error,omitempty"`
	AvailableVersion    string `json:"available_version,omitempty"`
	AvailableReleaseURL string `json:"available_release_url,omitempty"`
	LastInstallAt       string `json:"last_install_at,omitempty"`
	LastInstallError    string `json:"last_install_error,omitempty"`
}

type updatesInstallRequest struct {
	Version string `json:"version"`
}

type updatesInstallResponse struct {
	Status          string `json:"status"`
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

type adoptRequest struct {
	CAPEM         string `json:"ca_pem"`
	NUTUser       string `json:"nut_user"`
	NUTPassword   string `json:"nut_password"`
	APIToken      string `json:"api_token"`
	ControllerURL string `json:"controller_url"`
}

type adoptResponse struct {
	Serial         string `json:"serial"`
	Version        string `json:"version"`
	ControllerURL  string `json:"controller_url"`
	TLSPort        int    `json:"tls_port"`
	TLSFingerprint string `json:"tls_fingerprint"`
	TokenSHA256    string `json:"token_sha256"`
}

type AdoptRequest = adoptRequest
type AdoptResponse = adoptResponse

type indexViewModel struct {
	GeneratedAt time.Time
	Health      healthResponse
	AuthEnabled bool
	Username    string
	CSRFToken   string
}

type diagnosticsViewModel struct {
	Username    string
	CSRFToken   string
	Diagnostics diagnosticsResponse
}

type storedAdoption struct {
	TokenSHA256 string `json:"token_sha256"`
	CAPEM       string `json:"ca_pem"`
}

//go:embed web/*
var webAssets embed.FS

var assetFS = mustSubFS(webAssets, "web")

var swaggerUIHandler = swguiV5.New("Strom Node API", "/openapi.json", "/api/docs/")

var openAPISpecification = []byte(`{
	"openapi": "3.1.0",
	"info": {
		"title": "Strom Node API",
		"version": "1.0.0",
		"description": "Local Strom node status, diagnostics, and UPS-control API. Protected operations accept the current browser session or a node API key in the Authorization header."
	},
	"servers": [{"url": "/"}],
	"paths": {
		"/status": {"get": {"summary": "Public node summary", "responses": {"200": {"description": "Overall status and UPS count"}}}},
		"/status/details": {"get": {"summary": "Detailed node health", "security": [{"sessionCookie": []}, {"apiKeyAuth": []}], "responses": {"200": {"description": "Node and UPS telemetry"}}}},
		"/healthz": {"get": {"summary": "Health check", "security": [{"sessionCookie": []}, {"apiKeyAuth": []}], "responses": {"200": {"description": "Node and UPS telemetry"}}}},
		"/api/health": {"get": {"summary": "Detailed API health", "security": [{"sessionCookie": []}, {"apiKeyAuth": []}], "responses": {"200": {"description": "Node and UPS telemetry"}}}},
		"/api/diagnostics": {"get": {"summary": "Node diagnostics", "security": [{"sessionCookie": []}, {"apiKeyAuth": []}], "responses": {"200": {"description": "USB, scanner, NUT config, and NUT service diagnostics"}}}},
		"/api/ups": {"get": {"summary": "List UPS status", "security": [{"sessionCookie": []}, {"apiKeyAuth": []}], "responses": {"200": {"description": "UPS summaries"}}}},
		"/api/ups/{name}": {
			"get": {"summary": "Get UPS details", "security": [{"sessionCookie": []}, {"apiKeyAuth": []}], "parameters": [{"name": "name", "in": "path", "required": true, "schema": {"type": "string"}}], "responses": {"200": {"description": "UPS metrics, variables, commands, and writable settings"}}}
		},
		"/api/ups/{name}/command": {
			"post": {"summary": "Run a UPS command", "security": [{"sessionCookie": []}, {"apiKeyAuth": []}], "parameters": [{"name": "name", "in": "path", "required": true, "schema": {"type": "string"}}], "requestBody": {"required": true, "content": {"application/json": {"schema": {"type": "object", "required": ["cmd"], "properties": {"cmd": {"type": "string"}}}}}}, "responses": {"200": {"description": "Command result"}}}
		},
		"/api/ups/{name}/setvar": {
			"post": {"summary": "Set a writable UPS variable", "security": [{"sessionCookie": []}, {"apiKeyAuth": []}], "parameters": [{"name": "name", "in": "path", "required": true, "schema": {"type": "string"}}], "requestBody": {"required": true, "content": {"application/json": {"schema": {"type": "object", "required": ["var", "value"], "properties": {"var": {"type": "string"}, "value": {"type": "string"}}}}}}, "responses": {"200": {"description": "Updated variable result"}}}
		},
		"/api/about": {"get": {"summary": "Node software and platform details", "security": [{"sessionCookie": []}], "responses": {"200": {"description": "Node environment details"}}}}
	},
	"components": {
		"securitySchemes": {
			"sessionCookie": {"type": "apiKey", "in": "cookie", "name": "strom_session"},
			"apiKeyAuth": {"type": "http", "scheme": "bearer", "bearerFormat": "Strom API key"}
		}
	}
}`)

func mustSubFS(source fs.FS, dir string) fs.FS {
	subtree, err := fs.Sub(source, dir)
	if err != nil {
		panic(err)
	}
	return subtree
}

var indexTemplate = template.Must(template.New("index").Funcs(template.FuncMap{
	"formatTemp": func(value *float64) string {
		if value == nil {
			return "unavailable"
		}
		return fmt.Sprintf("%.1f C", *value)
	},
	"formatPercent": func(value *float64) string {
		if value == nil {
			return "unavailable"
		}
		return fmt.Sprintf("%.1f%%", *value)
	},
	"initials": func(name string) string {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return "?"
		}
		fields := strings.Fields(trimmed)
		first := []rune(fields[0])
		if len(fields) == 1 {
			if len(first) == 1 {
				return strings.ToUpper(string(first))
			}
			return strings.ToUpper(string(first[:2]))
		}
		last := []rune(fields[len(fields)-1])
		return strings.ToUpper(string(first[:1]) + string(last[:1]))
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Strom Node</title>
	<link rel="icon" href="/assets/favicon.svg" type="image/svg+xml">
	<link rel="stylesheet" href="/assets/styles.css">
</head>
<body>
	<main class="shell">
		<header class="topbar surface">
			<div class="brand">
				<img class="brand-mark" src="/assets/logo.svg" alt="Strom logo">
				<div class="brand-copy">
					<h1>Strom Node</h1>
				</div>
			</div>
			<nav id="topbar-toolbar" class="toolbar" aria-label="Dashboard actions">
				<button id="refresh-indicator" class="refresh-indicator" type="button" aria-label="Refresh dashboard now">
					<svg class="refresh-ring" viewBox="0 0 36 36" aria-hidden="true">
						<circle class="refresh-ring-track" cx="18" cy="18" r="15.5"></circle>
						<circle id="refresh-ring-progress" class="refresh-ring-progress" cx="18" cy="18" r="15.5"></circle>
					</svg>
					<span id="refresh-countdown" class="helper" role="status">Loading live metrics&hellip;</span>
				</button>
				<button
					id="topbar-menu-toggle"
					class="button button--ghost menu-toggle"
					type="button"
					data-menu-toggle
					aria-expanded="false"
					aria-haspopup="menu"
					aria-label="Toggle navigation menu"
				>
					☰
				</button>
				<div class="profile-menu" id="profile-menu">
					<div id="profile-menu-panel" class="menu-panel" role="menu" aria-label="Profile options" hidden>
						<div class="menu-section">
							<p class="menu-title">Appearance</p>
							<div class="appearance-segmented" role="radiogroup" aria-label="Color mode">
								<button class="appearance-option" type="button" role="radio" data-theme-option="system" aria-checked="true">System</button>
								<button class="appearance-option" type="button" role="radio" data-theme-option="light" aria-checked="false">Light</button>
								<button class="appearance-option" type="button" role="radio" data-theme-option="dark" aria-checked="false">Dark</button>
							</div>
						</div>
						<div class="menu-divider" role="separator"></div>
						<div class="menu-section">
							<a class="menu-link menu-link--active" href="/" role="menuitem" aria-current="page">Dashboard</a>
							{{if .AuthEnabled}}<a class="menu-link" href="/settings" role="menuitem">Settings</a>{{end}}
							{{if .AuthEnabled}}<a class="menu-link" href="/diagnostics" role="menuitem">Diagnostics</a>{{end}}
							<button class="menu-link" type="button" data-about-open role="menuitem">About Strom</button>
							<a class="menu-link menu-link--docs" href="https://foehammer82.github.io/strom/getting-started/" target="_blank" rel="noreferrer" role="menuitem" aria-label="Docs (opens in a new tab)">
								<span class="menu-link-icon-wrap" aria-hidden="true">
									<svg class="menu-link-icon" viewBox="0 0 24 24" focusable="false">
										<path d="M14 5h5v5M19 5l-9 9M19 14v5H5V5h5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"></path>
									</svg>
								</span>
								<span>Docs</span>
							</a>
							{{if .AuthEnabled}}<div class="menu-divider" role="separator"></div><form class="menu-form" method="post" action="/auth/logout"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button class="menu-link menu-link--sign-out" type="submit" role="menuitem">Sign out</button></form>{{end}}
						</div>
					</div>
				</div>
			</nav>
		</header>

		<section class="surface hero">
			<div class="section-head">
				<h2>Node overview</h2>
				<div class="footer-links">
					<a class="footer-link" href="/status" target="_blank" rel="noreferrer"><span>Public status</span><svg class="external-link-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M14 5h5v5M19 5l-9 9M19 14v5H5V5h5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"></path></svg></a>
					<a class="footer-link" href="/status/details" target="_blank" rel="noreferrer"><span>Detailed JSON</span><svg class="external-link-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M14 5h5v5M19 5l-9 9M19 14v5H5V5h5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"></path></svg></a>
					<a class="footer-link" href="/healthz" target="_blank" rel="noreferrer"><span>Health payload</span><svg class="external-link-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M14 5h5v5M19 5l-9 9M19 14v5H5V5h5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"></path></svg></a>
				</div>
			</div>
			<div id="metrics-grid" class="summary-grid">
				<article class="metric-card"><span class="eyebrow">Version</span><div class="metric-value">{{.Health.Version}}</div></article>
				<article class="metric-card"><span class="eyebrow">Uptime</span><div class="metric-value">{{.Health.UptimeSeconds}}s</div></article>
				<article class="metric-card"><span class="eyebrow">Disk free</span><div class="metric-value">{{.Health.DiskFreeBytes}} B</div></article>
				<article class="metric-card"><span class="eyebrow">CPU temp</span><div class="metric-value">{{formatTemp .Health.CPUTemperatureCelsius}}</div></article>
				<article class="metric-card"><span class="eyebrow">CPU usage</span><div class="metric-value">{{formatPercent .Health.CPUUsagePercent}}</div></article>
				<article class="metric-card"><span class="eyebrow">Memory used</span><div class="metric-value">{{.Health.MemoryUsedBytes}} / {{.Health.MemoryTotalBytes}} B</div></article>
				<article class="metric-card"><span class="eyebrow">UPS count</span><div class="metric-value">{{len .Health.UPSes}}</div></article>
			</div>
		</section>

		<section class="layout">
			<div class="stack">
				<section class="surface hero">
					<div id="ups-grid" class="ups-grid" aria-label="UPS inventory">
						{{if .Health.UPSes}}
							{{range .Health.UPSes}}
							<article class="ups-card">
								<header>
									<div>
										<h3>{{.Name}}</h3>
										<p>{{.Driver}}</p>
									</div>
									<span class="chip {{if or (eq .Status "starting") (eq .Status "unknown")}}chip--warn{{end}}">{{.Status}}</span>
								</header>
							</article>
							{{end}}
						{{else}}
							<div class="empty-state"><p>No UPS devices are currently discovered on this node.</p></div>
						{{end}}
					</div>
				</section>
			</div>
			<aside class="surface detail-shell" id="ups-detail">
				<div class="empty-state">
					<h3>Select a UPS</h3>
					<p>Pick a UPS card to inspect full telemetry, raw variables, and supported commands.</p>
				</div>
			</aside>
		</section>
	</main>
	<dialog id="about-dialog" class="about-dialog" aria-labelledby="about-dialog-title">
		<div class="about-dialog-content">
			<div class="about-dialog-head"><div><span class="eyebrow">About</span><h2 id="about-dialog-title">Strom Node</h2></div><button class="button button--ghost" type="button" data-about-close>Close</button></div>
			<div id="about-content" class="about-content"></div>
		</div>
	</dialog>
	<dialog id="acknowledgements-dialog" class="about-dialog" aria-labelledby="acknowledgements-dialog-title">
		<div class="about-dialog-content">
			<div class="about-dialog-head"><div><span class="eyebrow">Open source</span><h2 id="acknowledgements-dialog-title">All acknowledgments</h2></div><button class="button button--ghost" type="button" data-acknowledgements-close>Close</button></div>
			<div class="acknowledgements-controls"><input id="acknowledgements-search" type="search" placeholder="Search acknowledgments" aria-label="Search acknowledgments"><select id="acknowledgements-filter" aria-label="Acknowledgment category"><option value="all">All software</option><option value="go">Go modules</option><option value="debian">Debian packages</option></select></div>
			<div id="acknowledgements-content" class="about-content"></div>
			<div class="modal-actions"><button class="button button--ghost" type="button" data-acknowledgements-back>Back to About</button></div>
		</div>
	</dialog>

	<div id="toast" class="toast" role="status" aria-live="polite"></div>
	<div id="confirm-modal" class="modal" aria-hidden="true">
		<div class="surface modal-card">
			<span class="eyebrow">Destructive command</span>
			<h2>Confirm UPS action</h2>
			<p id="confirm-text" class="helper"></p>
			<div class="modal-actions">
				<button id="confirm-cancel" class="button button--ghost" type="button">Cancel</button>
				<button id="confirm-submit" class="button button--danger" type="button">Run command</button>
			</div>
		</div>
	</div>
	<div id="raw-json-modal" class="modal" aria-hidden="true">
		<div class="surface modal-card modal-card--wide">
			<div class="raw-json-head">
				<h2 id="raw-json-title">Raw NUT variables</h2>
				<span id="raw-json-name-badge" class="chip"></span>
			</div>
			<div class="json-card"><pre id="raw-json-content"><code id="raw-json-code"></code></pre></div>
			<div class="modal-actions">
				<button id="raw-json-copy" class="button button--ghost" type="button">Copy JSON</button>
				<button id="raw-json-close" class="button button--ghost" type="button">Close</button>
			</div>
		</div>
	</div>
	<div id="ups-metadata-modal" class="modal" aria-hidden="true">
		<div class="surface modal-card" role="dialog" aria-modal="true" aria-labelledby="ups-metadata-title">
			<span class="eyebrow">UPS details</span>
			<h2 id="ups-metadata-title">Edit UPS details</h2>
			<p id="ups-metadata-subtitle" class="helper"></p>
			<form id="ups-metadata-form" class="metadata-form">
				<label for="ups-metadata-display-name">Friendly name</label>
				<input id="ups-metadata-display-name" name="display_name" maxlength="120" autocomplete="off">
				<label for="ups-metadata-tags">Tags</label>
				<input id="ups-metadata-tags" name="tags" maxlength="120" autocomplete="off" aria-describedby="ups-metadata-tags-help">
				<p id="ups-metadata-tags-help" class="helper">Separate tags with commas.</p>
				<p id="ups-metadata-error" class="metadata-error" role="alert" hidden></p>
				<div class="modal-actions">
					<button id="ups-metadata-cancel" class="button button--ghost" type="button">Cancel</button>
					<button id="ups-metadata-save" class="button button--primary" type="submit">Save details</button>
				</div>
			</form>
		</div>
	</div>
	<script src="/assets/app.js" defer></script>
	<script src="/assets/about.js" defer></script>
</body>
</html>`))

var diagnosticsTemplate = template.Must(template.New("diagnostics").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Node Diagnostics</title>
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
</head>
<body>
	<main class="shell">
		<header class="topbar surface">
			<div class="brand"><img class="brand-mark" src="/assets/logo.svg" alt="Strom logo"><div class="brand-copy"><h1>Node Diagnostics</h1></div></div>
			<nav id="diagnostics-toolbar" class="toolbar" aria-label="Diagnostics actions">
				<button id="diagnostics-menu-toggle" class="button button--ghost menu-toggle" type="button" aria-expanded="false" aria-haspopup="menu" aria-label="Toggle navigation menu">☰</button>
				<div class="profile-menu" id="diagnostics-profile-menu">
					<div id="diagnostics-menu-panel" class="menu-panel" role="menu" aria-label="Node menu" hidden>
						<div class="menu-section">
							<a class="menu-link" href="/" role="menuitem">Dashboard</a>
							<a class="menu-link" href="/settings" role="menuitem">Settings</a>
							<a class="menu-link menu-link--active" href="/diagnostics" role="menuitem" aria-current="page">Diagnostics</a>
							<button class="menu-link" type="button" data-about-open role="menuitem">About Strom</button>
							<a class="menu-link menu-link--docs" href="https://foehammer82.github.io/strom/getting-started/" target="_blank" rel="noreferrer" role="menuitem" aria-label="Docs (opens in a new tab)">
								<span class="menu-link-icon-wrap" aria-hidden="true">
									<svg class="menu-link-icon" viewBox="0 0 24 24" focusable="false">
										<path d="M14 5h5v5M19 5l-9 9M19 14v5H5V5h5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"></path>
									</svg>
								</span>
								<span>Docs</span>
							</a>
							<div class="menu-divider" role="separator"></div>
							<form class="menu-form" method="post" action="/auth/logout"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button class="menu-link menu-link--sign-out" type="submit" role="menuitem">Sign out</button></form>
						</div>
					</div>
				</div>
			</nav>
		</header>
		<section class="surface hero">
			<div class="section-head"><div><h2>USB and NUT detection</h2><p class="helper">Captured {{.Diagnostics.GeneratedAt.Local.Format "2006-01-02 15:04:05 MST"}} for {{.Username}}.</p></div></div>
			<div class="diagnostics-grid">
				<article class="diagnostic-card"><h3>USB devices</h3>{{if .Diagnostics.USBDevices.Error}}<p class="diagnostic-error">{{.Diagnostics.USBDevices.Error}}</p>{{end}}<pre>{{.Diagnostics.USBDevices.Output}}</pre></article>
				<article class="diagnostic-card"><h3>NUT scanner</h3>{{if .Diagnostics.Scanner.Error}}<p class="diagnostic-error">{{.Diagnostics.Scanner.Error}}</p>{{end}}<pre>{{.Diagnostics.Scanner.Output}}</pre></article>
				<article class="diagnostic-card"><h3>NUT server</h3>{{if .Diagnostics.NUTServer.Error}}<p class="diagnostic-error">{{.Diagnostics.NUTServer.Error}}</p>{{end}}<pre>{{.Diagnostics.NUTServer.Output}}</pre></article>
				<article class="diagnostic-card"><h3>Generated ups.conf</h3>{{if .Diagnostics.NUTConfig.Error}}<p class="diagnostic-error">{{.Diagnostics.NUTConfig.Error}}</p>{{end}}<pre>{{.Diagnostics.NUTConfig.Output}}</pre></article>
			</div>
		</section>
	</main>
	<dialog id="about-dialog" class="about-dialog" aria-labelledby="about-dialog-title">
		<div class="about-dialog-content">
			<div class="about-dialog-head"><div><span class="eyebrow">About</span><h2 id="about-dialog-title">Strom Node</h2></div><button class="button button--ghost" type="button" data-about-close>Close</button></div>
			<div id="about-content" class="about-content"></div>
		</div>
	</dialog>
	<dialog id="acknowledgements-dialog" class="about-dialog" aria-labelledby="acknowledgements-dialog-title">
		<div class="about-dialog-content">
			<div class="about-dialog-head"><div><span class="eyebrow">Open source</span><h2 id="acknowledgements-dialog-title">All acknowledgments</h2></div><button class="button button--ghost" type="button" data-acknowledgements-close>Close</button></div>
			<div class="acknowledgements-controls"><input id="acknowledgements-search" type="search" placeholder="Search acknowledgments" aria-label="Search acknowledgments"><select id="acknowledgements-filter" aria-label="Acknowledgment category"><option value="all">All software</option><option value="go">Go modules</option><option value="debian">Debian packages</option></select></div>
			<div id="acknowledgements-content" class="about-content"></div>
			<div class="modal-actions"><button class="button button--ghost" type="button" data-acknowledgements-back>Back to About</button></div>
		</div>
	</dialog>
	<script>
		(() => {
			const toolbar = document.getElementById("diagnostics-toolbar");
			const menu = document.getElementById("diagnostics-profile-menu");
			const panel = document.getElementById("diagnostics-menu-panel");
			const toggles = [document.getElementById("diagnostics-menu-toggle")];
			const closeMenu = () => { panel.hidden = true; toolbar.classList.remove("is-open"); toggles.forEach((toggle) => toggle.setAttribute("aria-expanded", "false")); };
			const toggleMenu = () => { const open = panel.hidden; panel.hidden = !open; toolbar.classList.toggle("is-open", open); toggles.forEach((toggle) => toggle.setAttribute("aria-expanded", String(open))); };
			toggles.forEach((toggle) => toggle.addEventListener("click", (event) => { event.stopPropagation(); toggleMenu(); }));
			document.addEventListener("click", (event) => { if (!menu.contains(event.target)) closeMenu(); });
			document.addEventListener("keydown", (event) => { if (event.key === "Escape") closeMenu(); });
		})();
	</script>
	<script src="/assets/about.js" defer></script>
</body>
</html>`))

func New(logger *log.Logger, opts Options) *Service {
	startedAt := opts.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}

	upscPath := opts.UPSCPath
	if upscPath == "" {
		upscPath = defaultUPSCPath
	}

	upscmdPath := opts.UPSCmdPath
	if upscmdPath == "" {
		upscmdPath = "upscmd"
	}

	upsrwPath := opts.UPSRWPath
	if upsrwPath == "" {
		upsrwPath = "upsrw"
	}

	cpuTempPath := opts.CPUTempPath
	if cpuTempPath == "" {
		cpuTempPath = defaultCPUTempPath
	}

	memInfoPath := opts.MemInfoPath
	if memInfoPath == "" {
		memInfoPath = defaultMemInfoPath
	}

	cpuStatPath := opts.CPUStatPath
	if cpuStatPath == "" {
		cpuStatPath = defaultCPUStatPath
	}

	rootPath := opts.RootPath
	if rootPath == "" {
		rootPath = defaultRootPath
	}

	service := &Service{
		logger:          logger,
		version:         defaultString(opts.Version, "dev"),
		serial:          opts.Serial,
		startedAt:       startedAt,
		runner:          runner,
		upscPath:        upscPath,
		upscmdPath:      upscmdPath,
		upsrwPath:       upsrwPath,
		cpuTempPath:     cpuTempPath,
		memInfoPath:     memInfoPath,
		cpuStatPath:     cpuStatPath,
		rootPath:        rootPath,
		adoptionPath:    opts.AdoptionPath,
		upsMetadataPath: opts.UPSMetadataPath,
		updatesStore:    updates.NewStore(opts.UpdatesRoot),
		updatesChecker:  opts.UpdatesChecker,
		nutUser:         opts.NUTUser,
		nutPassword:     opts.NUTPassword,
		auth:            newAuthManager(opts.DisableAuth, opts.AuthPath, logger),
		adopter:         opts.Adopter,
		sshAccess:       opts.SSHAccess,
	}
	if service.sshAccess == nil {
		service.sshAccess = newSystemSSHAccessManager(rootPath)
	}
	metadata, err := loadUPSMetadata(service.upsMetadataPath)
	if err != nil && logger != nil {
		logger.Printf("load UPS metadata failed: %v", err)
	}
	if metadata == nil {
		metadata = map[string]upsMetadata{}
	}
	service.upsMetadata = metadata
	service.cache = service.loggingMiddleware(service.routes())
	return service
}

func (s *Service) Handler() http.Handler {
	return s.cache
}

func (s *Service) UpdateInventory(devices []nutconf.DetectedUPS) {
	cloned := make([]nutconf.DetectedUPS, len(devices))
	copy(cloned, devices)
	sort.Slice(cloned, func(i, j int) bool {
		return cloned[i].Name < cloned[j].Name
	})

	s.mu.Lock()
	s.devices = cloned
	s.mu.Unlock()
}

func (s *Service) UpdateNUTCredentials(username, password string) {
	s.mu.Lock()
	s.nutUser = username
	s.nutPassword = password
	s.mu.Unlock()
}

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assetFS))))
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/adopt", s.handleAdopt)
	mux.HandleFunc("/auth/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/logout", s.handleLogout)
	mux.HandleFunc("/auth/reset", s.handleReset)
	mux.HandleFunc("/api/about", s.handleAPIAbout)
	mux.HandleFunc("/api/health", s.handleAPIHealth)
	mux.HandleFunc("/api/diagnostics", s.handleAPIDiagnostics)
	mux.HandleFunc("/api/ups", s.handleAPIUPS)
	mux.HandleFunc("/api/ups/", s.handleAPIUPS)
	mux.HandleFunc("/diagnostics", s.handleDiagnostics)
	mux.HandleFunc("/settings", s.handleSettings)
	mux.HandleFunc("/settings/ui", s.handleUISetting)
	mux.HandleFunc("/settings/password", s.handleChangePassword)
	mux.HandleFunc("/settings/ssh", s.handleSSHSetting)
	mux.HandleFunc("/settings/api-key", s.handleAPIKeySetting)
	mux.HandleFunc("/settings/api-docs", s.handleAPIDocsSetting)
	mux.HandleFunc("/api/settings/ui/policy", s.handleUIPolicy)
	mux.HandleFunc("/api/agent/update", s.handleAgentUpdate)
	mux.HandleFunc("/api/agent/updates/status", s.handleUpdatesStatus)
	mux.HandleFunc("/api/agent/updates/check", s.handleUpdatesCheck)
	mux.HandleFunc("/api/agent/updates/install", s.handleUpdatesInstall)
	mux.HandleFunc("/api/docs", s.handleAPIDocsRedirect)
	mux.HandleFunc("/api/docs/", s.handleAPIDocs)
	mux.HandleFunc("/openapi.json", s.handleOpenAPISpecification)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/status/details", s.handleStatusDetails)
	mux.HandleFunc("/healthz", s.handleHealthz)
	return mux
}

func (s *Service) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var username string
	var csrfToken string
	if s.auth.Enabled() {
		sessionUsername, ok := s.requireSession(w, r)
		if !ok {
			return
		}
		username = sessionUsername
		uiEnabled, err := s.auth.UIEnabled()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !uiEnabled {
			http.Redirect(w, r, "/settings?message=local-ui-disabled", http.StatusSeeOther)
			return
		}
		csrfToken, err = s.auth.SessionCSRFToken(r)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "load CSRF token")
			return
		}
	}

	response, err := s.buildHealthResponse(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.Execute(w, indexViewModel{GeneratedAt: time.Now(), Health: response, AuthEnabled: s.auth.Enabled(), Username: username, CSRFToken: csrfToken}); err != nil && s.logger != nil {
		s.logger.Printf("render index failed: %v", err)
	}
}

func (s *Service) handleAdopt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.adopter == nil {
		writeJSONError(w, http.StatusNotImplemented, "adoption unavailable")
		return
	}

	var request adoptRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode adopt request: %v", err))
		return
	}
	if err := validateAdoptRequest(request); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	response, err := s.adopter.ApplyAdoption(r.Context(), request)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errNodeAlreadyAdopted) {
			status = http.StatusConflict
		}
		writeJSONError(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Service) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeJSONError(w, http.StatusNotFound, "bootstrap unavailable when http auth is disabled")
		return
	}
	if s.auth.Bootstrapped() {
		if wantsHTML(r) {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		writeJSONError(w, http.StatusConflict, "node web auth already initialized")
		return
	}
	if r.Method == http.MethodGet {
		token, err := s.issueCSRFToken(w, r)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.renderBootstrapPage(w, http.StatusOK, token, "")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateAnonymousCSRF(r); err != nil {
			token, issueErr := s.issueCSRFToken(w, r)
			if issueErr != nil {
				writeJSONError(w, http.StatusInternalServerError, issueErr.Error())
				return
			}
			s.renderBootstrapPage(w, http.StatusForbidden, token, err.Error())
			return
		}
	}
	req, err := bootstrapRequestFromRequest(r)
	if err != nil {
		if wantsHTML(r) {
			token, issueErr := s.issueCSRFToken(w, r)
			if issueErr != nil {
				writeJSONError(w, http.StatusInternalServerError, issueErr.Error())
				return
			}
			s.renderBootstrapPage(w, http.StatusBadRequest, token, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.auth.Bootstrap(req.NewPassword, req.ConfirmPassword); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errAlreadyBootstrapped) {
			status = http.StatusConflict
		}
		if wantsHTML(r) {
			token, issueErr := s.issueCSRFToken(w, r)
			if issueErr != nil {
				writeJSONError(w, http.StatusInternalServerError, issueErr.Error())
				return
			}
			s.renderBootstrapPage(w, status, token, err.Error())
			return
		}
		writeJSONError(w, status, err.Error())
		return
	}
	if err := s.startSession(w, r, defaultAdminUsername); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if wantsHTML(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "bootstrapped"})
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeJSONError(w, http.StatusNotFound, "login unavailable when http auth is disabled")
		return
	}
	if !s.auth.Bootstrapped() {
		if wantsHTML(r) {
			http.Redirect(w, r, "/auth/bootstrap", http.StatusSeeOther)
			return
		}
		writeJSONError(w, http.StatusUnauthorized, "node web auth not initialized; complete setup at /auth/bootstrap")
		return
	}
	if r.Method == http.MethodGet {
		token, err := s.issueCSRFToken(w, r)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		uiEnabled, err := s.auth.UIEnabled()
		if err != nil && !errors.Is(err, errAuthNotConfigured) {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.renderLoginPage(w, http.StatusOK, token, "", !uiEnabled)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateAnonymousCSRF(r); err != nil {
			token, issueErr := s.issueCSRFToken(w, r)
			if issueErr != nil {
				writeJSONError(w, http.StatusInternalServerError, issueErr.Error())
				return
			}
			s.renderLoginPage(w, http.StatusForbidden, token, err.Error(), false)
			return
		}
	}
	req, err := loginRequestFromRequest(r)
	if err != nil {
		if wantsHTML(r) {
			token, issueErr := s.issueCSRFToken(w, r)
			if issueErr != nil {
				writeJSONError(w, http.StatusInternalServerError, issueErr.Error())
				return
			}
			s.renderLoginPage(w, http.StatusBadRequest, token, err.Error(), false)
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateLoginRequest(req); err != nil {
		if wantsHTML(r) {
			token, issueErr := s.issueCSRFToken(w, r)
			if issueErr != nil {
				writeJSONError(w, http.StatusInternalServerError, issueErr.Error())
				return
			}
			s.renderLoginPage(w, http.StatusBadRequest, token, err.Error(), false)
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.auth.Authenticate(req.Username, req.Password); err != nil {
		if wantsHTML(r) {
			token, issueErr := s.issueCSRFToken(w, r)
			if issueErr != nil {
				writeJSONError(w, http.StatusInternalServerError, issueErr.Error())
				return
			}
			s.renderLoginPage(w, http.StatusUnauthorized, token, "invalid username or password", false)
			return
		}
		writeJSONError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err := s.startSession(w, r, strings.TrimSpace(req.Username)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	uiEnabled, _ := s.auth.UIEnabled()
	if wantsHTML(r) {
		if uiEnabled {
			http.Redirect(w, r, "/", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/settings?message=local-ui-disabled", http.StatusSeeOther)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed-in"})
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeJSONError(w, http.StatusNotFound, "logout unavailable when http auth is disabled")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateSessionCSRF(r); err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.auth.ClearSession(cookie.Value)
	}
	s.clearSessionCookie(w, r)
	if wantsHTML(r) {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed-out"})
}

func (s *Service) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateSessionCSRF(r); err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	sshEnabled, err := s.auth.SSHEnabled()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sshEnabled {
		if err := s.sshAccess.Disable(r.Context()); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.auth.Reset(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.clearSessionCookie(w, r)
	if wantsHTML(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (s *Service) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.auth.Enabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	username, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	viewModel, err := s.buildSettingsViewModel(r, username, r.URL.Query().Get("message"), "")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.renderSettingsPage(w, http.StatusOK, viewModel)
}

// formatUpdatesCheckedAt renders a persisted RFC3339 last-checked timestamp
// as a short, human-readable string for the settings page. It returns an
// empty string (rather than an error) for an empty or unparsable input, so
// the caller can treat "" as "never checked" without special-casing parse
// failures.
func formatUpdatesCheckedAt(rfc3339 string) string {
	if rfc3339 == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return ""
	}
	return parsed.Local().Format("Jan 2, 2006 15:04")
}

// buildSettingsViewModel gathers everything the settings page needs to
// render. It is shared by handleSettings and handleChangePassword's error
// path so the page can be re-rendered consistently after a failed password
// change.
func (s *Service) buildSettingsViewModel(r *http.Request, username, message, errMessage string) (settingsViewModel, error) {
	uiEnabled, err := s.auth.UIEnabled()
	if err != nil {
		return settingsViewModel{}, err
	}
	uiManaged, err := s.auth.UIManaged()
	if err != nil {
		return settingsViewModel{}, err
	}
	sshEnabled, err := s.auth.SSHEnabled()
	if err != nil {
		return settingsViewModel{}, err
	}
	csrfToken, err := s.auth.SessionCSRFToken(r)
	if err != nil {
		return settingsViewModel{}, err
	}
	readAPIKeyExists, err := s.auth.APIKeyExists(apiKeyScopeRead)
	if err != nil {
		return settingsViewModel{}, err
	}
	writeAPIKeyExists, err := s.auth.APIKeyExists(apiKeyScopeWrite)
	if err != nil {
		return settingsViewModel{}, err
	}
	apiDocsEnabled, err := s.auth.APIDocsEnabled()
	if err != nil {
		return settingsViewModel{}, err
	}
	updatesStatus := s.buildUpdatesStatusResponse()
	return settingsViewModel{
		Username:                username,
		UIEnabled:               uiEnabled,
		UIManaged:               uiManaged,
		SSHEnabled:              sshEnabled,
		SSHCommand:              sshCommandForRequest(r),
		ReadAPIKeyExists:        readAPIKeyExists,
		WriteAPIKeyExists:       writeAPIKeyExists,
		APIDocsEnabled:          apiDocsEnabled,
		UpdatesSupported:        updatesStatus.UpdatesSupported,
		InstalledVersion:        updatesStatus.InstalledVersion,
		UpdatesPendingVersion:   updatesStatus.PendingVersion,
		UpdatesAvailableVersion: updatesStatus.AvailableVersion,
		UpdatesReleaseURL:       updatesStatus.AvailableReleaseURL,
		UpdatesLastCheckedAt:    formatUpdatesCheckedAt(updatesStatus.LastCheckedAt),
		UpdatesLastCheckError:   updatesStatus.LastCheckError,
		UpdatesLastInstallError: updatesStatus.LastInstallError,
		Message:                 message,
		Error:                   errMessage,
		CSRFToken:               csrfToken,
	}, nil
}

func sshCommandForRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.Host)
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	if host == "" || strings.ContainsAny(host, "\r\n\t ") {
		return ""
	}
	return "ssh " + defaultAdminUsername + "@" + host
}

func (s *Service) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateSessionCSRF(r); err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	username, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	req, err := changePasswordRequestFromRequest(r)
	if err != nil {
		s.respondSettingsError(w, r, username, http.StatusBadRequest, err.Error())
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		s.respondSettingsError(w, r, username, http.StatusBadRequest, "new passwords do not match")
		return
	}
	sshEnabled, err := s.auth.SSHEnabled()
	if err != nil {
		s.respondSettingsError(w, r, username, http.StatusInternalServerError, err.Error())
		return
	}
	if sshEnabled {
		passwordHash, err := s.sshAccess.SyncPassword(r.Context(), req.NewPassword)
		if err != nil {
			s.respondSettingsError(w, r, username, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.auth.SetSSHPasswordHash(passwordHash); err != nil {
			s.respondSettingsError(w, r, username, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.auth.ChangePassword(req.CurrentPassword, req.NewPassword); err != nil {
		status := http.StatusBadRequest
		message := err.Error()
		if errors.Is(err, errInvalidCredentials) {
			status = http.StatusUnauthorized
			message = "current password is incorrect"
		}
		s.respondSettingsError(w, r, username, status, message)
		return
	}
	if wantsHTML(r) {
		http.Redirect(w, r, "/settings?message=password-updated", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "password-updated"})
}

func (s *Service) handleSSHSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateSessionCSRF(r); err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	username, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	request, err := sshSettingRequestFromRequest(r)
	if err != nil {
		s.respondSettingsError(w, r, username, http.StatusBadRequest, err.Error())
		return
	}
	if request.Enabled {
		if err := s.auth.Authenticate(username, request.Password); err != nil {
			s.respondSettingsError(w, r, username, http.StatusUnauthorized, "current password is incorrect")
			return
		}
		var passwordHash string
		passwordHash, err = s.sshAccess.Enable(r.Context(), request.Password)
		if err == nil {
			err = s.auth.SetSSHEnabled(true, passwordHash)
		}
	} else {
		err = s.sshAccess.Disable(r.Context())
		if err == nil {
			err = s.auth.SetSSHEnabled(false, "")
		}
	}
	if err != nil {
		s.respondSettingsError(w, r, username, http.StatusInternalServerError, err.Error())
		return
	}
	message := "ssh-access-disabled"
	if request.Enabled {
		message = "ssh-access-enabled"
	}
	if wantsHTML(r) {
		http.Redirect(w, r, "/settings?message="+message, http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "ssh_enabled": request.Enabled})
}

func (s *Service) handleAPIKeySetting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.validateSessionCSRF(r); err != nil {
		writeJSONError(w, http.StatusForbidden, err.Error())
		return
	}
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	var request apiKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode API key request: %v", err))
		return
	}
	request.Scope = strings.TrimSpace(request.Scope)
	request.Action = strings.TrimSpace(request.Action)
	if request.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "current password is required")
		return
	}

	var (
		key string
		err error
	)
	switch request.Action {
	case "reveal":
		key, err = s.auth.RevealAPIKey(request.Scope, request.Password)
		if err == nil && key == "" {
			writeJSONError(w, http.StatusNotFound, "API key has not been generated")
			return
		}
	case "regenerate":
		key, err = s.auth.RegenerateAPIKey(request.Scope, request.Password)
	default:
		writeJSONError(w, http.StatusBadRequest, "action must be reveal or regenerate")
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		message := err.Error()
		if errors.Is(err, errInvalidCredentials) {
			status = http.StatusUnauthorized
			message = "current password is incorrect"
		}
		writeJSONError(w, status, message)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"scope": request.Scope, "key": key})
}

func (s *Service) handleAPIDocsSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateSessionCSRF(r); err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	enabled, err := enabledFlagFromRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.auth.SetAPIDocsEnabled(enabled); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if wantsHTML(r) {
		message := "API documentation disabled"
		if enabled {
			message = "API documentation enabled"
		}
		http.Redirect(w, r, "/settings?message="+url.QueryEscape(message), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "api_docs_enabled": enabled})
}

// respondSettingsError re-renders the settings page with an error message
// for HTML clients, or writes a plain JSON error for API clients.
func (s *Service) respondSettingsError(w http.ResponseWriter, r *http.Request, username string, status int, message string) {
	if !wantsHTML(r) {
		writeJSONError(w, status, message)
		return
	}
	viewModel, err := s.buildSettingsViewModel(r, username, "", message)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.renderSettingsPage(w, status, viewModel)
}

func (s *Service) handleUISetting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateSessionCSRF(r); err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	enabled, err := enabledFlagFromRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.auth.SetUIEnabled(enabled); err != nil {
		if errors.Is(err, errUIPolicyManaged) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	message := "local-ui-enabled"
	if !enabled {
		message = "local-ui-disabled"
	}
	if wantsHTML(r) {
		http.Redirect(w, r, "/settings?message="+message, http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "ui_enabled": enabled})
}

func (s *Service) handleUIPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireControllerToken(w, r) {
		return
	}
	var request uiPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode local UI policy request: %v", err))
		return
	}
	if err := s.auth.ApplyControllerUIPolicy(request.Managed, request.Enabled); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "ui_managed": request.Managed, "ui_enabled": request.Enabled})
}

func (s *Service) handleAgentUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireControllerToken(w, r) {
		return
	}

	var request otaUpdateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96*1024*1024))
	if err := decoder.Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode ota update request: %v", err))
		return
	}
	if strings.TrimSpace(request.Version) == "" || strings.TrimSpace(request.BinaryBase64) == "" || strings.TrimSpace(request.SHA256) == "" || strings.TrimSpace(request.SignatureBase64) == "" {
		writeJSONError(w, http.StatusBadRequest, "version, binary_base64, sha256, and signature_base64 are required")
		return
	}

	binaryPayload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.BinaryBase64))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode binary_base64: %v", err))
		return
	}
	if len(binaryPayload) == 0 {
		writeJSONError(w, http.StatusBadRequest, "binary payload is empty")
		return
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.SignatureBase64))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode signature_base64: %v", err))
		return
	}
	digest := sha256.Sum256(binaryPayload)
	providedDigest, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(request.SHA256)))
	if err != nil || len(providedDigest) != len(digest) {
		writeJSONError(w, http.StatusBadRequest, "sha256 must be a 64-character lowercase hex digest")
		return
	}
	if subtle.ConstantTimeCompare(digest[:], providedDigest) != 1 {
		writeJSONError(w, http.StatusBadRequest, "sha256 does not match binary payload")
		return
	}

	adoption, err := s.loadAdoption()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if adoption == nil || strings.TrimSpace(adoption.CAPEM) == "" {
		writeJSONError(w, http.StatusConflict, "node adoption CA is unavailable for signature verification")
		return
	}
	if err := verifySignedUpdate(adoption.CAPEM, digest[:], signature); err != nil {
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return
	}

	if err := s.updatesStore.StageAndActivate(strings.TrimSpace(request.Version), binaryPayload); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, otaUpdateResponse{Status: "applied", Version: strings.TrimSpace(request.Version), SHA256: strings.ToLower(strings.TrimSpace(request.SHA256)), RestartRequired: true})
	s.restartAgentAsync()
}

func verifySignedUpdate(caPEM string, digest, signature []byte) error {
	if len(digest) != 32 {
		return fmt.Errorf("invalid digest size")
	}
	block, _ := pem.Decode([]byte(caPEM))
	if block == nil {
		return fmt.Errorf("decode adopted controller CA certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse adopted controller CA certificate: %w", err)
	}
	publicKey, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("unsupported controller CA public key type %T", certificate.PublicKey)
	}
	if !ecdsa.VerifyASN1(publicKey, digest, signature) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

// restartAgentAsync restarts the agent systemd unit shortly after a
// successful update activation, giving the current HTTP response time to
// flush before the process is terminated and re-executed by systemd against
// the newly activated "current" slot. Failures are logged but otherwise
// ignored: an operator can always restart manually, and the update has
// already been durably staged regardless.
func (s *Service) restartAgentAsync() {
	go func() {
		time.Sleep(300 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := s.runner.CombinedOutput(ctx, "systemctl", "restart", agentSystemdUnit); err != nil && s.logger != nil {
			s.logger.Printf("restart %s after update failed: %v", agentSystemdUnit, err)
		}
	}()
}

// buildUpdatesStatusResponse assembles the current standalone update status
// from the durable store plus this process's own build version.
func (s *Service) buildUpdatesStatusResponse() updatesStatusResponse {
	response := updatesStatusResponse{
		InstalledVersion: s.version,
		UpdatesSupported: s.updatesChecker != nil,
	}
	if s.updatesStore == nil {
		return response
	}
	if pending, ok := s.updatesStore.PendingVersion(); ok {
		response.PendingVersion = pending
	}
	status, err := s.updatesStore.ReadStatus()
	if err != nil {
		return response
	}
	response.SchedulerEnabled = status.SchedulerEnabled
	response.AvailableVersion = status.AvailableVersion
	response.AvailableReleaseURL = status.AvailableReleaseURL
	response.LastCheckError = status.LastCheckError
	response.LastInstallError = status.LastInstallError
	if !status.LastCheckedAt.IsZero() {
		response.LastCheckedAt = status.LastCheckedAt.Format(time.RFC3339)
	}
	if !status.LastInstallAt.IsZero() {
		response.LastInstallAt = status.LastInstallAt.Format(time.RFC3339)
	}
	return response
}

func (s *Service) handleUpdatesStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireReadAccess(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.buildUpdatesStatusResponse())
}

func (s *Service) handleUpdatesCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateSessionCSRF(r); err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	if !s.requireWriteAccess(w, r) {
		return
	}
	if s.updatesChecker == nil {
		writeJSONError(w, http.StatusNotFound, "standalone update checking is not enabled on this node")
		return
	}
	if _, err := s.updatesChecker.Check(r.Context()); err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.buildUpdatesStatusResponse())
}

func (s *Service) handleUpdatesInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if wantsHTML(r) {
		if err := s.validateSessionCSRF(r); err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	// Installing new software is a sensitive, disruptive action: only an
	// authenticated node session may trigger it. Unlike read/write API
	// keys, no local API key can perform an install.
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	if s.updatesChecker == nil {
		writeJSONError(w, http.StatusNotFound, "standalone update checking is not enabled on this node")
		return
	}

	var request updatesInstallRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024))
	if err := decoder.Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode install request: %v", err))
		return
	}
	requestedVersion := strings.TrimSpace(request.Version)
	if requestedVersion == "" {
		writeJSONError(w, http.StatusBadRequest, "version is required")
		return
	}

	result, err := s.updatesChecker.Install(r.Context(), requestedVersion)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updatesInstallResponse{Status: "installed", Version: result.Version, RestartRequired: result.RestartRequired})
	s.restartAgentAsync()
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	response, err := s.buildStatusResponse(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Service) handleStatusDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireReadAccess(w, r) {
		return
	}

	response, err := s.buildHealthResponse(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Service) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.handleStatusDetails(w, r)
}

func (s *Service) handleAPIHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireReadAccess(w, r) {
		return
	}

	response, err := s.buildHealthResponse(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Service) apiDocsEnabled(w http.ResponseWriter, r *http.Request) bool {
	enabled, err := s.auth.APIDocsEnabled()
	if err != nil || !enabled {
		http.NotFound(w, r)
		return false
	}
	return true
}

func (s *Service) handleAPIDocsRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.apiDocsEnabled(w, r) {
		return
	}
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	http.Redirect(w, r, "/api/docs/", http.StatusTemporaryRedirect)
}

func (s *Service) handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.apiDocsEnabled(w, r) {
		return
	}
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	swaggerUIHandler.ServeHTTP(w, r)
}

func (s *Service) handleOpenAPISpecification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.apiDocsEnabled(w, r) {
		return
	}
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(openAPISpecification)
}

func (s *Service) handleAPIAbout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.buildAboutResponse())
}

func (s *Service) handleAPIDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireReadAccess(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.buildDiagnosticsResponse(r.Context()))
}

func (s *Service) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	username, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	csrfToken, err := s.auth.SessionCSRFToken(r)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "load CSRF token")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := diagnosticsTemplate.Execute(w, diagnosticsViewModel{Username: username, CSRFToken: csrfToken, Diagnostics: s.buildDiagnosticsResponse(r.Context())}); err != nil && s.logger != nil {
		s.logger.Printf("render diagnostics failed: %v", err)
	}
}

func (s *Service) handleAPIUPS(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/ups")
	if path == "" || path == "/" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireReadAccess(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, s.buildUPSStatuses(r.Context()))
		return
	}

	trimmed := strings.TrimPrefix(path, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	name := parts[0]
	switch {
	case len(parts) == 1:
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireReadAccess(w, r) {
			return
		}
		response, err := s.buildUPSDetailResponse(r.Context(), name)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errUPSNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, response)
	case len(parts) == 2 && parts[1] == "command":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireWriteAccess(w, r) {
			return
		}
		var request upsCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode command request: %v", err))
			return
		}
		request.Command = strings.TrimSpace(request.Command)
		if request.Command == "" {
			writeJSONError(w, http.StatusBadRequest, "cmd is required")
			return
		}
		response, err := s.runUPSCommand(r.Context(), name, request.Command)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, errUPSNotFound):
				status = http.StatusNotFound
			case errors.Is(err, errUPSCommandNotFound), errors.Is(err, errUPSControlUnavailable):
				status = http.StatusBadRequest
			}
			writeJSONError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, response)
	case len(parts) == 2 && parts[1] == "metadata":
		if r.Method != http.MethodPatch {
			w.Header().Set("Allow", http.MethodPatch)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireWriteAccess(w, r) {
			return
		}
		var metadata upsMetadata
		if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode UPS metadata: %v", err))
			return
		}
		response, err := s.updateUPSMetadata(name, metadata)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errUPSNotFound) || strings.Contains(err.Error(), "must be") || strings.Contains(err.Error(), "metadata cannot") || strings.Contains(err.Error(), "tags are allowed") {
				status = http.StatusBadRequest
			}
			writeJSONError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, response)
	case len(parts) == 2 && parts[1] == "setvar":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireWriteAccess(w, r) {
			return
		}
		var request upsSetVarRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode setvar request: %v", err))
			return
		}
		request.Variable = strings.TrimSpace(request.Variable)
		request.Value = strings.TrimSpace(request.Value)
		if request.Variable == "" {
			writeJSONError(w, http.StatusBadRequest, "var is required")
			return
		}
		response, err := s.runUPSSetVariable(r.Context(), name, request.Variable, request.Value)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, errUPSNotFound):
				status = http.StatusNotFound
			case errors.Is(err, errUPSVariableNotFound), errors.Is(err, errUPSControlUnavailable):
				status = http.StatusBadRequest
			}
			writeJSONError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, response)
	default:
		http.NotFound(w, r)
	}
}

func (s *Service) renderLoginPage(w http.ResponseWriter, status int, csrfToken, message string, uiDisabled bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	viewModel := loginViewModel{Error: message, UIDisabled: uiDisabled, CSRFToken: csrfToken}
	if err := loginTemplate.Execute(w, viewModel); err != nil && s.logger != nil {
		s.logger.Printf("render login failed: %v", err)
	}
}

func (s *Service) renderBootstrapPage(w http.ResponseWriter, status int, csrfToken, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	viewModel := bootstrapViewModel{Error: message, CSRFToken: csrfToken}
	if err := bootstrapTemplate.Execute(w, viewModel); err != nil && s.logger != nil {
		s.logger.Printf("render bootstrap failed: %v", err)
	}
}

func (s *Service) renderSettingsPage(w http.ResponseWriter, status int, viewModel settingsViewModel) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := settingsTemplate.Execute(w, viewModel); err != nil && s.logger != nil {
		s.logger.Printf("render settings failed: %v", err)
	}
}

func (s *Service) requireSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.auth.Enabled() {
		return "", true
	}
	if !s.auth.Bootstrapped() {
		if wantsHTML(r) {
			http.Redirect(w, r, "/auth/bootstrap", http.StatusSeeOther)
		} else {
			writeJSONError(w, http.StatusUnauthorized, "node web auth not initialized; complete setup at /auth/bootstrap")
		}
		return "", false
	}
	username, err := s.auth.SessionUsername(r)
	if err != nil {
		if wantsHTML(r) {
			uiDisabled, _ := s.auth.UIEnabled()
			token, issueErr := s.issueCSRFToken(w, r)
			if issueErr != nil {
				writeJSONError(w, http.StatusInternalServerError, issueErr.Error())
				return "", false
			}
			s.renderLoginPage(w, http.StatusUnauthorized, token, "sign in required", !uiDisabled)
		} else {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
		}
		return "", false
	}
	return username, true
}

func (s *Service) requireReadAccess(w http.ResponseWriter, r *http.Request) bool {
	if !s.auth.Enabled() {
		return true
	}
	if matched, err := s.controllerTokenMatches(r); err == nil && matched {
		return true
	} else if err != nil && s.logger != nil {
		s.logger.Printf("controller bearer auth unavailable: %v", err)
	}
	scope, matched, err := s.localAPIKeyMatches(r)
	if err == nil && matched && (scope == apiKeyScopeRead || scope == apiKeyScopeWrite) {
		return true
	} else if err != nil && s.logger != nil {
		s.logger.Printf("local API key auth unavailable: %v", err)
	}
	_, ok := s.requireSession(w, r)
	return ok
}

func (s *Service) requireWriteAccess(w http.ResponseWriter, r *http.Request) bool {
	if !s.auth.Enabled() {
		return true
	}
	if matched, err := s.controllerTokenMatches(r); err == nil && matched {
		return true
	} else if err != nil && s.logger != nil {
		s.logger.Printf("controller bearer auth unavailable: %v", err)
	}
	scope, matched, err := s.localAPIKeyMatches(r)
	if err == nil && matched && scope == apiKeyScopeWrite {
		return true
	} else if err != nil && s.logger != nil {
		s.logger.Printf("local API key auth unavailable: %v", err)
	}
	_, ok := s.requireSession(w, r)
	return ok
}

func (s *Service) requireControllerToken(w http.ResponseWriter, r *http.Request) bool {
	if !s.auth.Enabled() {
		writeJSONError(w, http.StatusNotFound, "controller token auth unavailable when http auth is disabled")
		return false
	}
	matched, err := s.controllerTokenMatches(r)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("controller bearer auth unavailable: %v", err)
		}
		writeJSONError(w, http.StatusUnauthorized, "controller authentication required")
		return false
	}
	if !matched {
		writeJSONError(w, http.StatusUnauthorized, "controller authentication required")
		return false
	}
	return true
}

func (s *Service) controllerTokenMatches(r *http.Request) (bool, error) {
	if s.adoptionPath == "" || r == nil {
		return false, nil
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		return false, nil
	}
	token := strings.TrimSpace(authorization[len("Bearer "):])
	if token == "" {
		return false, nil
	}
	adoption, err := s.loadAdoption()
	if err != nil {
		return false, err
	}
	if adoption == nil || adoption.TokenSHA256 == "" {
		return false, nil
	}
	return adoption.TokenSHA256 == tokenSHA256Hex(token), nil
}

func (s *Service) localAPIKeyMatches(r *http.Request) (string, bool, error) {
	if r == nil {
		return "", false, nil
	}
	token := bearerToken(r)
	if token == "" {
		return "", false, nil
	}
	return s.auth.MatchAPIKey(token)
}

func bearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		return ""
	}
	return strings.TrimSpace(authorization[len("Bearer "):])
}

func (s *Service) loadAdoption() (*storedAdoption, error) {
	content, err := os.ReadFile(s.adoptionPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read adoption config: %w", err)
	}
	var adoption storedAdoption
	if err := json.Unmarshal(content, &adoption); err != nil {
		return nil, fmt.Errorf("decode adoption config: %w", err)
	}
	return &adoption, nil
}

func (s *Service) startSession(w http.ResponseWriter, r *http.Request, username string) error {
	if r != nil {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			s.auth.ClearSession(strings.TrimSpace(cookie.Value))
		}
	}
	token, csrfToken, err := s.auth.CreateSession(username)
	if err != nil {
		return err
	}
	secure := requestIsSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		MaxAge:   int(defaultSessionTTL.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrfToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		MaxAge:   int(defaultSessionTTL.Seconds()),
	})
	return nil
}

func (s *Service) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	secure := requestIsSecure(r)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure, MaxAge: -1})
}

// requestIsSecure reports whether the request should be treated as arriving
// over a secure transport, so Secure cookies are only set when the browser
// will actually persist them (plain HTTP deployments must still work).
func requestIsSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (s *Service) buildStatusResponse(ctx context.Context) (statusResponse, error) {
	upses := s.buildUPSStatuses(ctx)
	return statusResponse{
		Status:   summarizeStatus(upses),
		UPSCount: len(upses),
	}, nil
}

func (s *Service) buildHealthResponse(ctx context.Context) (healthResponse, error) {
	response := healthResponse{
		Version:       s.version,
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		Serial:        s.serial,
	}

	if cpuTemp, err := readCPUTemperature(s.cpuTempPath); err == nil {
		response.CPUTemperatureCelsius = cpuTemp
	} else if s.logger != nil {
		s.logger.Printf("health cpu temperature unavailable: %v", err)
	}

	if cpuUsage, err := s.cpuUsagePercent(); err != nil {
		if s.logger != nil {
			s.logger.Printf("health cpu usage unavailable: %v", err)
		}
	} else {
		response.CPUUsagePercent = cpuUsage
	}

	if usedBytes, totalBytes, err := readMemoryUsage(s.memInfoPath); err == nil {
		response.MemoryUsedBytes = usedBytes
		response.MemoryTotalBytes = totalBytes
	} else if s.logger != nil {
		s.logger.Printf("health memory usage unavailable: %v", err)
	}

	diskFree, err := diskFreeBytes(s.rootPath)
	if err != nil {
		return healthResponse{}, fmt.Errorf("stat root filesystem: %w", err)
	}
	response.DiskFreeBytes = diskFree
	response.UPSes = s.buildUPSStatuses(ctx)

	return response, nil
}

func (s *Service) buildDiagnosticsResponse(ctx context.Context) diagnosticsResponse {
	return diagnosticsResponse{
		GeneratedAt: time.Now(),
		Inventory:   s.inventory(),
		USBDevices:  s.runDiagnosticCommand(ctx, "lsusb"),
		Scanner:     s.runDiagnosticCommand(ctx, "nut-scanner", "-U", "-q"),
		NUTConfig:   s.readDiagnosticFile(filepath.Join(s.rootPath, "etc", "nut", "ups.conf")),
		NUTServer:   s.runDiagnosticCommand(ctx, "systemctl", "is-active", "nut-server"),
	}
}

func (s *Service) runDiagnosticCommand(ctx context.Context, path string, args ...string) diagnosticCheck {
	output, err := s.runner.CombinedOutput(ctx, path, args...)
	check := diagnosticCheck{Output: strings.TrimSpace(string(output))}
	if err != nil {
		check.Error = fmt.Sprintf("%s: %v", strings.Join(append([]string{path}, args...), " "), err)
	}
	return check
}

func (s *Service) readDiagnosticFile(path string) diagnosticCheck {
	content, err := os.ReadFile(path)
	if err != nil {
		return diagnosticCheck{Error: fmt.Sprintf("read %s: %v", path, err)}
	}
	return diagnosticCheck{Output: strings.TrimSpace(string(content))}
}
func (s *Service) buildUPSStatuses(ctx context.Context) []upsHealth {
	devices := s.inventory()
	upses := make([]upsHealth, 0, len(devices))
	for _, device := range devices {
		snapshot, err := s.queryUPSSnapshot(ctx, device.Name)
		if err != nil {
			if s.logger != nil {
				s.logger.Printf("health upsc failed ups=%s: %v", device.Name, err)
			}
		}

		health := buildUPSHealth(device, snapshot)
		health.Metadata = s.metadataForUPS(device.Name)
		upses = append(upses, health)
	}

	return upses
}

func (s *Service) buildUPSDetailResponse(ctx context.Context, name string) (upsDetailResponse, error) {
	device, ok := s.lookupUPS(name)
	if !ok {
		return upsDetailResponse{}, fmt.Errorf("%w: %s", errUPSNotFound, name)
	}

	snapshot, err := s.queryUPSSnapshot(ctx, name)
	if err != nil {
		return upsDetailResponse{}, err
	}

	commands, err := s.listUPSCommands(ctx, name)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("list upscmd failed ups=%s: %v", name, err)
		}
		commands = nil
	}

	writable, err := s.listUPSWritableVars(ctx, name, snapshot.Variables)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("list upsrw failed ups=%s: %v", name, err)
		}
		writable = nil
	}

	metrics := buildUPSHealth(device, snapshot)
	return upsDetailResponse{
		Name:      device.Name,
		Driver:    device.Driver,
		Metadata:  s.metadataForUPS(device.Name),
		Status:    metrics.Status,
		Metrics:   metrics,
		Variables: snapshot.Variables,
		Commands:  commands,
		Writable:  writable,
		UpdatedAt: time.Now(),
	}, nil
}

func summarizeStatus(upses []upsHealth) string {
	if len(upses) == 0 {
		return "empty"
	}

	for _, device := range upses {
		if device.Status == startingStatus || device.Status == unknownStatus {
			return "degraded"
		}
	}

	return "ok"
}

var (
	errNodeAlreadyAdopted    = errors.New("node already adopted")
	errUPSNotFound           = errors.New("ups not found")
	errUPSCommandNotFound    = errors.New("ups command not supported")
	errUPSVariableNotFound   = errors.New("ups variable not supported")
	errUPSControlUnavailable = errors.New("ups control credentials unavailable")
)

var ErrNodeAlreadyAdopted = errNodeAlreadyAdopted

func validateAdoptRequest(req adoptRequest) error {
	if strings.TrimSpace(req.CAPEM) == "" {
		return errors.New("ca_pem is required")
	}
	if strings.TrimSpace(req.NUTUser) == "" {
		return errors.New("nut_user is required")
	}
	if strings.TrimSpace(req.NUTPassword) == "" {
		return errors.New("nut_password is required")
	}
	if strings.TrimSpace(req.APIToken) == "" {
		return errors.New("api_token is required")
	}
	if strings.TrimSpace(req.ControllerURL) == "" {
		return errors.New("controller_url is required")
	}
	return nil
}

type upsSnapshot struct {
	Status    string
	Variables map[string]string
}

func (s *Service) queryUPSSnapshot(ctx context.Context, name string) (upsSnapshot, error) {
	jsonOutput, jsonErr := s.runner.CombinedOutput(ctx, s.upscPath, "-j", name)
	if jsonErr == nil {
		variables, err := parseUPSVariablesJSON(jsonOutput)
		if err == nil {
			return buildUPSSnapshot(variables)
		}
		if s.logger != nil {
			s.logger.Printf("parse upsc json failed ups=%s: %v", name, err)
		}
	}

	output, err := s.runner.CombinedOutput(ctx, s.upscPath, name)
	variables, parseErr := parseUPSVariablesText(output)
	if parseErr == nil {
		snapshot, snapshotErr := buildUPSSnapshot(variables)
		if snapshotErr == nil {
			return snapshot, nil
		}
		if err != nil && isDriverStarting(output, err) {
			return upsSnapshot{Status: startingStatus}, nil
		}
		return upsSnapshot{}, snapshotErr
	}
	if err != nil && isDriverStarting(output, err) {
		return upsSnapshot{Status: startingStatus}, nil
	}
	if err != nil {
		return upsSnapshot{}, fmt.Errorf("run %s %s: %w: %s", s.upscPath, name, err, strings.TrimSpace(string(output)))
	}
	return upsSnapshot{}, parseErr
}

func buildUPSSnapshot(variables map[string]string) (upsSnapshot, error) {
	status := strings.TrimSpace(variables["ups.status"])
	if status == "" {
		return upsSnapshot{}, fmt.Errorf("ups.status not found")
	}
	return upsSnapshot{Status: status, Variables: variables}, nil
}

func (s *Service) listUPSCommands(ctx context.Context, name string) ([]upsCommand, error) {
	output, err := s.runner.CombinedOutput(ctx, s.upscmdPath, "-l", name)
	if err != nil {
		return nil, fmt.Errorf("run %s -l %s: %w: %s", s.upscmdPath, name, err, strings.TrimSpace(string(output)))
	}
	return parseUPSCommands(output), nil
}

func (s *Service) listUPSWritableVars(ctx context.Context, name string, snapshot map[string]string) ([]upsWritableVar, error) {
	output, err := s.runner.CombinedOutput(ctx, s.upsrwPath, "-l", name)
	if err != nil {
		return nil, fmt.Errorf("run %s -l %s: %w: %s", s.upsrwPath, name, err, strings.TrimSpace(string(output)))
	}
	return parseUPSWritableVars(output, snapshot), nil
}

func (s *Service) runUPSCommand(ctx context.Context, name, command string) (upsCommandResponse, error) {
	if _, ok := s.lookupUPS(name); !ok {
		return upsCommandResponse{}, fmt.Errorf("%w: %s", errUPSNotFound, name)
	}
	if strings.TrimSpace(s.currentNUTUser()) == "" || strings.TrimSpace(s.currentNUTPassword()) == "" {
		return upsCommandResponse{}, errUPSControlUnavailable
	}

	commands, err := s.listUPSCommands(ctx, name)
	if err == nil {
		found := false
		for _, candidate := range commands {
			if candidate.Name == command {
				found = true
				break
			}
		}
		if !found {
			return upsCommandResponse{}, fmt.Errorf("%w: %s", errUPSCommandNotFound, command)
		}
	}

	output, err := s.runner.CombinedOutput(ctx, s.upscmdPath, "-u", s.currentNUTUser(), "-p", s.currentNUTPassword(), "-w", name, command)
	if err != nil {
		return upsCommandResponse{}, fmt.Errorf("run %s %s %s: %w: %s", s.upscmdPath, name, command, err, strings.TrimSpace(string(output)))
	}

	return upsCommandResponse{
		UPS:     name,
		Command: command,
		Output:  strings.TrimSpace(string(output)),
	}, nil
}

func (s *Service) runUPSSetVariable(ctx context.Context, name, variable, value string) (upsSetVarResponse, error) {
	if _, ok := s.lookupUPS(name); !ok {
		return upsSetVarResponse{}, fmt.Errorf("%w: %s", errUPSNotFound, name)
	}
	if strings.TrimSpace(s.currentNUTUser()) == "" || strings.TrimSpace(s.currentNUTPassword()) == "" {
		return upsSetVarResponse{}, errUPSControlUnavailable
	}

	snapshot, err := s.queryUPSSnapshot(ctx, name)
	if err != nil {
		return upsSetVarResponse{}, err
	}
	writable, err := s.listUPSWritableVars(ctx, name, snapshot.Variables)
	if err == nil {
		found := false
		for _, candidate := range writable {
			if candidate.Name == variable {
				found = true
				break
			}
		}
		if !found {
			return upsSetVarResponse{}, fmt.Errorf("%w: %s", errUPSVariableNotFound, variable)
		}
	}

	assignment := variable + "=" + value
	output, err := s.runner.CombinedOutput(ctx, s.upsrwPath, "-s", assignment, "-u", s.currentNUTUser(), "-p", s.currentNUTPassword(), "-w", name)
	if err != nil {
		return upsSetVarResponse{}, fmt.Errorf("run %s %s %s: %w: %s", s.upsrwPath, name, assignment, err, strings.TrimSpace(string(output)))
	}

	return upsSetVarResponse{
		UPS:      name,
		Variable: variable,
		Value:    value,
		Output:   strings.TrimSpace(string(output)),
	}, nil
}

func (s *Service) inventory() []nutconf.DetectedUPS {
	s.mu.RLock()
	defer s.mu.RUnlock()

	devices := make([]nutconf.DetectedUPS, len(s.devices))
	copy(devices, s.devices)
	return devices
}

func (s *Service) lookupUPS(name string) (nutconf.DetectedUPS, bool) {
	for _, device := range s.inventory() {
		if device.Name == name {
			return device, true
		}
	}
	return nutconf.DetectedUPS{}, false
}

func (s *Service) metadataForUPS(name string) upsMetadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.upsMetadata[name]
}

func (s *Service) updateUPSMetadata(name string, metadata upsMetadata) (upsMetadataResponse, error) {
	if _, ok := s.lookupUPS(name); !ok {
		return upsMetadataResponse{}, fmt.Errorf("%w: %s", errUPSNotFound, name)
	}
	normalized, err := normalizeUPSMetadata(metadata)
	if err != nil {
		return upsMetadataResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]upsMetadata, len(s.upsMetadata)+1)
	for key, value := range s.upsMetadata {
		next[key] = value
	}
	if isEmptyUPSMetadata(normalized) {
		delete(next, name)
	} else {
		next[name] = normalized
	}
	if _, err := saveUPSMetadata(s.upsMetadataPath, next); err != nil {
		return upsMetadataResponse{}, err
	}
	s.upsMetadata = next
	return upsMetadataResponse{UPS: name, Metadata: normalized}, nil
}

func isEmptyUPSMetadata(metadata upsMetadata) bool {
	return metadata.DisplayName == "" && len(metadata.Tags) == 0
}

func (s *Service) currentNUTUser() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nutUser
}

func (s *Service) currentNUTPassword() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nutPassword
}

func (s *Service) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		if s.logger != nil {
			s.logger.Printf("http method=%s path=%s status=%d duration=%s", r.Method, r.URL.Path, wrapped.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

func readCPUTemperature(path string) (*float64, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	value, err := strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
	if err != nil {
		return nil, fmt.Errorf("parse cpu temperature: %w", err)
	}

	temperature := value / 1000.0
	return &temperature, nil
}

func diskFreeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

// readMemoryUsage parses /proc/meminfo-format content and returns the
// currently used and total memory in bytes. "Used" is derived as
// total-available (MemAvailable already accounts for reclaimable caches,
// unlike the naive total-free calculation).
func readMemoryUsage(path string) (usedBytes, totalBytes uint64, err error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}

	var total, available uint64
	var foundTotal, foundAvailable bool
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, 0, fmt.Errorf("parse MemTotal in %s: %w", path, err)
			}
			total *= 1024
			foundTotal = true
		case "MemAvailable":
			available, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, 0, fmt.Errorf("parse MemAvailable in %s: %w", path, err)
			}
			available *= 1024
			foundAvailable = true
		}

		if foundTotal && foundAvailable {
			break
		}
	}

	if !foundTotal || !foundAvailable {
		return 0, 0, fmt.Errorf("MemTotal/MemAvailable not found in %s", path)
	}
	if available > total {
		available = total
	}
	return total - available, total, nil
}

// readCPUStatTotals parses the aggregate "cpu" line of /proc/stat-format
// content, returning cumulative idle and total jiffy counters since boot.
func readCPUStatTotals(path string) (idle, total uint64, err error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}

	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}

		values := make([]uint64, 0, len(fields)-1)
		for _, field := range fields[1:] {
			value, parseErr := strconv.ParseUint(field, 10, 64)
			if parseErr != nil {
				return 0, 0, fmt.Errorf("parse %s field %q: %w", path, field, parseErr)
			}
			values = append(values, value)
		}

		for _, value := range values {
			total += value
		}
		// fields: user nice system idle iowait ... - idle time is the sum
		// of "idle" and "iowait" (both count as CPU not doing work).
		idle = values[3]
		if len(values) > 4 {
			idle += values[4]
		}
		return idle, total, nil
	}

	return 0, 0, fmt.Errorf("cpu totals not found in %s", path)
}

// cpuUsagePercent computes CPU utilization as a percentage of the interval
// since the previous call, using cumulative jiffy counters from
// s.cpuStatPath. The first call after startup has no prior sample to diff
// against, so it returns (nil, nil) rather than an error.
func (s *Service) cpuUsagePercent() (*float64, error) {
	idle, total, err := readCPUStatTotals(s.cpuStatPath)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	previous := s.lastCPUSample
	s.lastCPUSample = cpuStatSample{idle: idle, total: total}
	s.mu.Unlock()

	if previous.total == 0 || total <= previous.total {
		return nil, nil
	}

	totalDelta := total - previous.total
	idleDelta := idle - previous.idle
	if idleDelta > totalDelta {
		idleDelta = totalDelta
	}

	usage := float64(totalDelta-idleDelta) / float64(totalDelta) * 100.0
	return &usage, nil
}

func parseUPSStatus(output []byte) (string, error) {
	variables, err := parseUPSVariablesText(output)
	if err != nil {
		return "", err
	}
	status := strings.TrimSpace(variables["ups.status"])
	if status == "" {
		return "", fmt.Errorf("ups.status not found")
	}
	return status, nil
}

func parseUPSVariablesJSON(output []byte) (map[string]string, error) {
	var raw map[string]any
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, fmt.Errorf("decode upsc json: %w", err)
	}
	variables := make(map[string]string, len(raw))
	for key, value := range raw {
		variables[key] = fmt.Sprint(value)
	}
	return variables, nil
}

func parseUPSVariablesText(output []byte) (map[string]string, error) {
	variables := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			key, value, ok = strings.Cut(trimmed, "=")
		}
		if !ok {
			continue
		}

		variables[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if len(variables) == 0 {
		return nil, fmt.Errorf("ups variables not found")
	}
	return variables, nil
}

// upsCommandNamePattern matches a real NUT instant-command token: a dotted
// identifier such as "load.off" or "test.battery.start.quick" (occasionally an
// all-caps token such as "FSD"). It intentionally rejects anything containing
// whitespace or brackets, which filters out `upscmd -l`'s prose header line.
var upsCommandNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)*$`)

func parseUPSCommands(output []byte) []upsCommand {
	commands := make([]upsCommand, 0)
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		name := trimmed
		description := ""
		if head, tail, ok := strings.Cut(trimmed, " - "); ok {
			name = strings.TrimSpace(head)
			description = strings.TrimSpace(tail)
		} else if head, tail, ok := strings.Cut(trimmed, ":"); ok {
			name = strings.TrimSpace(head)
			description = strings.TrimSpace(tail)
		}

		// `upscmd -l <ups>` prints a leading prose header, e.g.
		// "Instant commands supported on UPS [name]:", before the actual command
		// list. That header line contains a trailing colon and so falls into the
		// ":" split above, which previously turned it into a bogus command entry.
		// A real NUT command name is a dotted lowercase identifier (occasionally
		// uppercase, e.g. "FSD") with no spaces or brackets, so filter anything
		// else out here rather than trusting every non-blank line.
		if !upsCommandNamePattern.MatchString(name) {
			continue
		}

		commands = append(commands, upsCommand{
			Name:        name,
			Description: description,
			Destructive: isDestructiveUPSCommand(name),
		})
	}
	return commands
}

func parseUPSWritableVars(output []byte, snapshot map[string]string) []upsWritableVar {
	lines := strings.Split(string(output), "\n")
	blocks := make([][]string, 0)
	current := make([]string, 0)
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			if len(current) > 0 {
				blocks = append(blocks, current)
				current = nil
			}
			continue
		}
		current = append(current, trimmed)
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}

	vars := make([]upsWritableVar, 0, len(blocks))
	seen := map[string]struct{}{}
	for _, block := range blocks {
		variable, ok := parseUPSWritableBlock(block, snapshot)
		if !ok || variable.Name == "" {
			continue
		}
		if _, exists := seen[variable.Name]; exists {
			continue
		}
		seen[variable.Name] = struct{}{}
		vars = append(vars, variable)
	}
	return vars
}

func parseUPSWritableBlock(block []string, snapshot map[string]string) (upsWritableVar, bool) {
	var variable upsWritableVar
	variable.Editor = "text"

	name, description := parseWritableHeader(block[0])
	if name == "" {
		return upsWritableVar{}, false
	}
	variable.Name = name
	variable.Description = description
	variable.CurrentValue = snapshot[name]

	for _, line := range block[1:] {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch {
		case strings.Contains(key, "value") && variable.CurrentValue == "":
			variable.CurrentValue = value
		case strings.Contains(key, "desc") && variable.Description == "":
			variable.Description = value
		case strings.Contains(key, "option") || strings.Contains(key, "enum") || strings.Contains(key, "possible"):
			variable.Options = append(variable.Options, value)
		case strings.Contains(key, "range"):
			min, max := parseNumericRange(value)
			if min != nil {
				variable.Min = min
			}
			if max != nil {
				variable.Max = max
			}
		case strings.Contains(key, "type"):
			typeValue := strings.ToLower(value)
			if strings.Contains(typeValue, "enum") {
				variable.Editor = "select"
			}
			if strings.Contains(typeValue, "range") || strings.Contains(typeValue, "number") || strings.Contains(typeValue, "integer") {
				variable.Editor = "number"
			}
		}
	}

	if len(variable.Options) > 0 {
		variable.Editor = "select"
	}
	if variable.Min != nil || variable.Max != nil {
		variable.Editor = "number"
	}
	return variable, true
}

func parseWritableHeader(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		return strings.Trim(trimmed, "[]"), ""
	}
	if head, tail, ok := strings.Cut(trimmed, ":"); ok {
		name := strings.TrimSpace(head)
		if strings.Contains(name, ".") || strings.Contains(name, "_") {
			return name, strings.TrimSpace(tail)
		}
	}
	fields := strings.Fields(trimmed)
	if len(fields) > 0 && (strings.Contains(fields[0], ".") || strings.Contains(fields[0], "_")) {
		return fields[0], strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
	}
	return "", ""
}

func parseNumericRange(value string) (*float64, *float64) {
	replacer := strings.NewReplacer("to", "-", "..", "-", "—", "-", "–", "-", ",", " ")
	parts := strings.Fields(replacer.Replace(strings.ToLower(value)))
	if len(parts) == 1 {
		pieces := strings.Split(parts[0], "-")
		if len(pieces) == 2 {
			parts = pieces
		}
	}
	if len(parts) < 2 {
		return nil, nil
	}
	min, errMin := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	max, errMax := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if errMin != nil || errMax != nil {
		return nil, nil
	}
	return &min, &max
}

func buildUPSHealth(device nutconf.DetectedUPS, snapshot upsSnapshot) upsHealth {
	metrics := upsHealth{
		Name:   device.Name,
		Driver: device.Driver,
		Status: snapshot.Status,
	}
	if metrics.Status == "" {
		metrics.Status = unknownStatus
	}
	if len(snapshot.Variables) == 0 {
		return metrics
	}
	metrics.BatteryChargePercent = parseUPSFloat(snapshot.Variables, "battery.charge")
	metrics.LoadPercent = parseUPSFloat(snapshot.Variables, "ups.load")
	metrics.BatteryVoltage = parseUPSFloat(snapshot.Variables, "battery.voltage")
	metrics.InputVoltage = parseUPSFloat(snapshot.Variables, "input.voltage")
	metrics.OutputVoltage = parseUPSFloat(snapshot.Variables, "output.voltage")
	metrics.RuntimeSeconds = parseUPSInt(snapshot.Variables, "battery.runtime")
	return metrics
}

func parseUPSFloat(variables map[string]string, key string) *float64 {
	value := strings.TrimSpace(variables[key])
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseUPSInt(variables map[string]string, key string) *int64 {
	value := strings.TrimSpace(variables[key])
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	rounded := int64(parsed)
	return &rounded
}

func isDestructiveUPSCommand(name string) bool {
	return strings.HasPrefix(name, "shutdown.") ||
		strings.HasPrefix(name, "load.off") ||
		name == "driver.killpower" ||
		name == "shutdown.return" ||
		name == "shutdown.stayoff" ||
		name == "shutdown.reboot" ||
		name == "shutdown.reboot.graceful" ||
		name == "FSD"
}

func tokenSHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func TokenSHA256Hex(value string) string {
	return tokenSHA256Hex(value)
}

func isDriverStarting(output []byte, err error) bool {
	combined := strings.ToLower(strings.TrimSpace(string(output)))
	if err != nil {
		combined += " " + strings.ToLower(err.Error())
	}

	for _, marker := range []string{
		"data stale",
		"driver not connected",
		"connection refused",
		"connection failure",
		"initializing",
		"driver is not connected",
	} {
		if strings.Contains(combined, marker) {
			return true
		}
	}

	return false
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func wantsHTML(r *http.Request) bool {
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html") {
		return true
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	return strings.HasPrefix(contentType, "application/x-www-form-urlencoded") || strings.HasPrefix(contentType, "multipart/form-data")
}

func loginRequestFromRequest(r *http.Request) (loginRequest, error) {
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return loginRequest{}, fmt.Errorf("decode login request: %w", err)
		}
		return req, nil
	}
	if err := r.ParseForm(); err != nil {
		return loginRequest{}, fmt.Errorf("parse login form: %w", err)
	}
	return loginRequest{Username: r.FormValue("username"), Password: r.FormValue("password")}, nil
}

func bootstrapRequestFromRequest(r *http.Request) (bootstrapRequest, error) {
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		var req bootstrapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return bootstrapRequest{}, fmt.Errorf("decode bootstrap request: %w", err)
		}
		return req, nil
	}
	if err := r.ParseForm(); err != nil {
		return bootstrapRequest{}, fmt.Errorf("parse bootstrap form: %w", err)
	}
	return bootstrapRequest{NewPassword: r.FormValue("new_password"), ConfirmPassword: r.FormValue("confirm_password")}, nil
}

func enabledFlagFromRequest(r *http.Request) (bool, error) {
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		var payload struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return false, fmt.Errorf("decode ui setting request: %w", err)
		}
		return payload.Enabled, nil
	}
	if err := r.ParseForm(); err != nil {
		return false, fmt.Errorf("parse ui setting form: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(r.FormValue("enabled"))) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, errors.New("enabled must be true or false")
	}
}

func sshSettingRequestFromRequest(r *http.Request) (sshSettingRequest, error) {
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		var request sshSettingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return sshSettingRequest{}, fmt.Errorf("decode SSH setting request: %w", err)
		}
		return request, nil
	}
	enabled, err := enabledFlagFromRequest(r)
	if err != nil {
		return sshSettingRequest{}, err
	}
	return sshSettingRequest{Enabled: enabled, Password: r.FormValue("password")}, nil
}

func changePasswordRequestFromRequest(r *http.Request) (changePasswordRequest, error) {
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		var req changePasswordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return changePasswordRequest{}, fmt.Errorf("decode change password request: %w", err)
		}
		return req, nil
	}
	if err := r.ParseForm(); err != nil {
		return changePasswordRequest{}, fmt.Errorf("parse change password form: %w", err)
	}
	return changePasswordRequest{
		CurrentPassword: r.FormValue("current_password"),
		NewPassword:     r.FormValue("new_password"),
		ConfirmPassword: r.FormValue("confirm_password"),
	}, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (s *Service) issueCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	token, err := randomToken(24)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   requestIsSecure(r),
		MaxAge:   int(defaultSessionTTL.Seconds()),
	})
	return token, nil
}

func (s *Service) validateAnonymousCSRF(r *http.Request) error {
	requestToken, err := csrfTokenFromRequest(r)
	if err != nil {
		return err
	}
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return errors.New("csrf token cookie is required")
	}
	if subtle.ConstantTimeCompare([]byte(requestToken), []byte(cookie.Value)) != 1 {
		return errors.New("csrf token mismatch")
	}
	return nil
}

func (s *Service) validateSessionCSRF(r *http.Request) error {
	requestToken, err := csrfTokenFromRequest(r)
	if err != nil {
		return err
	}
	sessionToken, err := s.auth.SessionCSRFToken(r)
	if err != nil {
		return errors.New("csrf session token is required")
	}
	if subtle.ConstantTimeCompare([]byte(requestToken), []byte(sessionToken)) != 1 {
		return errors.New("csrf token mismatch")
	}
	return nil
}

func csrfTokenFromRequest(r *http.Request) (string, error) {
	if r == nil {
		return "", errors.New("csrf token is required")
	}
	headerToken := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
	if headerToken != "" {
		return headerToken, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", fmt.Errorf("parse csrf form token: %w", err)
	}
	formToken := strings.TrimSpace(r.FormValue("csrf_token"))
	if formToken == "" {
		return "", errors.New("csrf token is required")
	}
	return formToken, nil
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
