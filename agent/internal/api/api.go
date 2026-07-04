package api

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Foehammer82/wattkeeper/agent/internal/nutconf"
)

const (
	defaultCPUTempPath = "/sys/class/thermal/thermal_zone0/temp"
	defaultRootPath    = "/"
	defaultUPSCPath    = "upsc"
	startingStatus     = "starting"
	unknownStatus      = "unknown"
)

type commandRunner interface {
	CombinedOutput(ctx context.Context, path string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) CombinedOutput(ctx context.Context, path string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

type Options struct {
	Version      string
	Serial       string
	StartedAt    time.Time
	Runner       commandRunner
	UPSCPath     string
	UPSCmdPath   string
	UPSRWPath    string
	CPUTempPath  string
	RootPath     string
	AdoptionPath string
	DisableAuth  bool
	AuthPath     string
	NUTUser      string
	NUTPassword  string
	Adopter      adoptionHandler
}

type adoptionHandler interface {
	ApplyAdoption(context.Context, adoptRequest) (adoptResponse, error)
}

type Service struct {
	logger       *log.Logger
	version      string
	serial       string
	startedAt    time.Time
	runner       commandRunner
	upscPath     string
	upscmdPath   string
	upsrwPath    string
	cpuTempPath  string
	rootPath     string
	adoptionPath string
	nutUser      string
	nutPassword  string
	auth         *authManager
	adopter      adoptionHandler

	mu      sync.RWMutex
	devices []nutconf.DetectedUPS
	cache   http.Handler
}

type healthResponse struct {
	Version               string      `json:"version"`
	UptimeSeconds         int64       `json:"uptime_seconds"`
	Serial                string      `json:"serial"`
	CPUTemperatureCelsius *float64    `json:"cpu_temperature_celsius,omitempty"`
	DiskFreeBytes         uint64      `json:"disk_free_bytes"`
	UPSes                 []upsHealth `json:"upses"`
}

type statusResponse struct {
	Status   string `json:"status"`
	UPSCount int    `json:"ups_count"`
}

type upsHealth struct {
	Name                 string   `json:"name"`
	Driver               string   `json:"driver"`
	Status               string   `json:"status"`
	BatteryChargePercent *float64 `json:"battery_charge_percent,omitempty"`
	LoadPercent          *float64 `json:"load_percent,omitempty"`
	RuntimeSeconds       *int64   `json:"runtime_seconds,omitempty"`
	InputVoltage         *float64 `json:"input_voltage,omitempty"`
	OutputVoltage        *float64 `json:"output_voltage,omitempty"`
	BatteryVoltage       *float64 `json:"battery_voltage,omitempty"`
}

type upsCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Destructive bool   `json:"destructive"`
}

type upsDetailResponse struct {
	Name      string            `json:"name"`
	Driver    string            `json:"driver"`
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
}

type storedAdoption struct {
	TokenSHA256 string `json:"token_sha256"`
}

//go:embed web/*
var webAssets embed.FS

var assetFS = mustSubFS(webAssets, "web")

func mustSubFS(source fs.FS, dir string) fs.FS {
	subtree, err := fs.Sub(source, dir)
	if err != nil {
		panic(err)
	}
	return subtree
}

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Wattkeeper Node</title>
	<link rel="icon" href="/assets/favicon.svg" type="image/svg+xml">
	<link rel="stylesheet" href="/assets/styles.css">
</head>
<body>
	<main class="shell">
		<header class="topbar">
			<div class="brand">
				<img class="brand-mark" src="/assets/logo.svg" alt="Wattkeeper logo">
				<div class="brand-copy">
					<h1>Wattkeeper Node</h1>
					<p>Material-inspired local node dashboard for NUT-backed UPS monitoring and control.</p>
				</div>
			</div>
			<nav class="toolbar" aria-label="Dashboard actions">
				<button id="theme-toggle" class="button button--ghost" type="button">Dark mode</button>
				<a class="link-button" href="/settings">Settings</a>
				{{if .AuthEnabled}}<a class="link-button" href="/auth/logout">Sign out</a>{{end}}
			</nav>
		</header>

		<section class="surface hero">
			<div class="hero-grid">
				<div>
					<div class="section-head">
						<h2>Node overview</h2>
						<span class="helper">Live metrics, polling status, and per-UPS controls.</span>
					</div>
					<div id="metrics-grid" class="summary-grid">
						<article class="metric-card"><span class="eyebrow">Version</span><div class="metric-value">{{.Health.Version}}</div></article>
						<article class="metric-card"><span class="eyebrow">Serial</span><div class="metric-value">{{if .Health.Serial}}{{.Health.Serial}}{{else}}unknown{{end}}</div></article>
						<article class="metric-card"><span class="eyebrow">Uptime</span><div class="metric-value">{{.Health.UptimeSeconds}}s</div></article>
						<article class="metric-card"><span class="eyebrow">Disk free</span><div class="metric-value">{{.Health.DiskFreeBytes}} B</div></article>
						<article class="metric-card"><span class="eyebrow">CPU temp</span><div class="metric-value">{{if .Health.CPUTemperatureCelsius}}{{printf "%.1f C" .Health.CPUTemperatureCelsius}}{{else}}unavailable{{end}}</div></article>
						<article class="metric-card"><span class="eyebrow">UPS count</span><div class="metric-value">{{len .Health.UPSes}}</div></article>
					</div>
				</div>
				<aside class="refresh-panel metric-card">
					<div class="refresh-hero">
						<svg class="ring" viewBox="0 0 88 88" aria-hidden="true">
							<circle class="ring-track" cx="44" cy="44" r="34"></circle>
							<circle id="refresh-ring" class="ring-progress" cx="44" cy="44" r="34"></circle>
						</svg>
						<div class="refresh-copy">
							<span class="eyebrow">Refresh timer</span>
							<strong id="refresh-seconds">15s</strong>
							<p id="refresh-status">Polling node health and UPS telemetry every 15 seconds.</p>
						</div>
					</div>
					<div class="toolbar">
						<button id="refresh-now" class="button button--primary" type="button">Refresh now</button>
						<button id="refresh-toggle" class="button button--ghost" type="button">Pause auto refresh</button>
					</div>
				</aside>
			</div>
		</section>

		<section class="layout">
			<div class="stack">
				<section class="surface hero">
					<div class="section-head">
						<h2>UPS inventory</h2>
						<span class="helper">Select any UPS to inspect telemetry and available NUT controls.</span>
					</div>
					<div id="ups-grid" class="ups-grid">
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

	<div id="toast" class="toast" role="status" aria-live="polite"></div>
	<div id="confirm-modal" class="modal" aria-hidden="true">
		<div class="surface modal-card">
			<span class="eyebrow">Destructive command</span>
			<h2>Confirm UPS action</h2>
			<p id="confirm-text" class="helper"></p>
			<label class="field" for="confirm-input">
				<span>Type the command exactly to continue</span>
				<input id="confirm-input" type="text" autocomplete="off">
			</label>
			<div class="modal-actions">
				<button id="confirm-cancel" class="button button--ghost" type="button">Cancel</button>
				<button id="confirm-submit" class="button button--primary" type="button" disabled>Run command</button>
			</div>
		</div>
	</div>
	<script src="/assets/app.js" defer></script>
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

	rootPath := opts.RootPath
	if rootPath == "" {
		rootPath = defaultRootPath
	}

	service := &Service{
		logger:       logger,
		version:      defaultString(opts.Version, "dev"),
		serial:       opts.Serial,
		startedAt:    startedAt,
		runner:       runner,
		upscPath:     upscPath,
		upscmdPath:   upscmdPath,
		upsrwPath:    upsrwPath,
		cpuTempPath:  cpuTempPath,
		rootPath:     rootPath,
		adoptionPath: opts.AdoptionPath,
		nutUser:      opts.NUTUser,
		nutPassword:  opts.NUTPassword,
		auth:         newAuthManager(opts.DisableAuth, opts.AuthPath),
		adopter:      opts.Adopter,
	}
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
	mux.HandleFunc("/api/health", s.handleAPIHealth)
	mux.HandleFunc("/api/ups", s.handleAPIUPS)
	mux.HandleFunc("/api/ups/", s.handleAPIUPS)
	mux.HandleFunc("/settings", s.handleSettings)
	mux.HandleFunc("/settings/ui", s.handleUISetting)
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
	if s.auth.Enabled() {
		needsBootstrap, err := s.auth.NeedsBootstrap()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if needsBootstrap {
			s.renderBootstrapPage(w, http.StatusOK, "")
			return
		}
		if _, ok := s.requireSession(w, r); !ok {
			return
		}
		uiEnabled, err := s.auth.UIEnabled()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !uiEnabled {
			http.Redirect(w, r, "/settings?message=local-ui-disabled", http.StatusSeeOther)
			return
		}
	}

	response, err := s.buildHealthResponse(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.Execute(w, indexViewModel{GeneratedAt: time.Now(), Health: response, AuthEnabled: s.auth.Enabled()}); err != nil && s.logger != nil {
		s.logger.Printf("render index failed: %v", err)
	}
}

func (s *Service) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeJSONError(w, http.StatusNotFound, "bootstrap unavailable when http auth is disabled")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	req, err := bootstrapRequestFromRequest(r)
	if err != nil {
		if wantsHTML(r) {
			s.renderBootstrapPage(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.auth.Bootstrap(req); err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, errAuthAlreadyConfigured):
			status = http.StatusConflict
		case errors.Is(err, errAuthDisabled):
			status = http.StatusNotFound
		default:
			if !strings.Contains(err.Error(), ":") && !strings.Contains(err.Error(), "config") {
				status = http.StatusBadRequest
			}
		}
		if wantsHTML(r) && status == http.StatusBadRequest {
			s.renderBootstrapPage(w, status, err.Error())
			return
		}
		writeJSONError(w, status, err.Error())
		return
	}
	if err := s.startSession(w, strings.TrimSpace(req.Username)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if wantsHTML(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
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

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeJSONError(w, http.StatusNotFound, "login unavailable when http auth is disabled")
		return
	}
	if r.Method == http.MethodGet {
		uiEnabled, err := s.auth.UIEnabled()
		if err != nil && !errors.Is(err, errAuthNotConfigured) {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.renderLoginPage(w, http.StatusOK, "", !uiEnabled)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	req, err := loginRequestFromRequest(r)
	if err != nil {
		if wantsHTML(r) {
			s.renderLoginPage(w, http.StatusBadRequest, err.Error(), false)
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateLoginRequest(req); err != nil {
		if wantsHTML(r) {
			s.renderLoginPage(w, http.StatusBadRequest, err.Error(), false)
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.auth.Authenticate(req.Username, req.Password); err != nil {
		if wantsHTML(r) {
			s.renderLoginPage(w, http.StatusUnauthorized, "invalid username or password", false)
			return
		}
		writeJSONError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err := s.startSession(w, strings.TrimSpace(req.Username)); err != nil {
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
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.auth.ClearSession(cookie.Value)
	}
	s.clearSessionCookie(w)
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
	if _, ok := s.requireSession(w, r); !ok {
		return
	}
	if err := s.auth.Reset(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.clearSessionCookie(w)
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
	username, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	uiEnabled, err := s.auth.UIEnabled()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.renderSettingsPage(w, http.StatusOK, settingsViewModel{Username: username, UIEnabled: uiEnabled, Message: r.URL.Query().Get("message")})
}

func (s *Service) handleUISetting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
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
	if _, ok := s.requireSession(w, r); !ok {
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
	if !s.requireControllerOrSession(w, r) {
		return
	}

	response, err := s.buildHealthResponse(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Service) handleAPIUPS(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/ups")
	if path == "" || path == "/" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := s.requireSession(w, r); !ok {
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
		if _, ok := s.requireSession(w, r); !ok {
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
		if !s.requireControllerOrSession(w, r) {
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
	case len(parts) == 2 && parts[1] == "setvar":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireControllerOrSession(w, r) {
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

func (s *Service) renderBootstrapPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := bootstrapTemplate.Execute(w, bootstrapViewModel{Error: message}); err != nil && s.logger != nil {
		s.logger.Printf("render bootstrap failed: %v", err)
	}
}

func (s *Service) renderLoginPage(w http.ResponseWriter, status int, message string, uiDisabled bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := loginTemplate.Execute(w, loginViewModel{Error: message, UIDisabled: uiDisabled}); err != nil && s.logger != nil {
		s.logger.Printf("render login failed: %v", err)
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
	needsBootstrap, err := s.auth.NeedsBootstrap()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return "", false
	}
	if needsBootstrap {
		if wantsHTML(r) {
			s.renderBootstrapPage(w, http.StatusOK, "")
		} else {
			writeJSONError(w, http.StatusServiceUnavailable, errAuthNotConfigured.Error())
		}
		return "", false
	}
	username, err := s.auth.SessionUsername(r)
	if err != nil {
		if wantsHTML(r) {
			uiDisabled, _ := s.auth.UIEnabled()
			s.renderLoginPage(w, http.StatusUnauthorized, "sign in required", !uiDisabled)
		} else {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
		}
		return "", false
	}
	return username, true
}

func (s *Service) requireControllerOrSession(w http.ResponseWriter, r *http.Request) bool {
	if !s.auth.Enabled() {
		return true
	}
	matched, err := s.controllerTokenMatches(r)
	if err == nil && matched {
		return true
	}
	if err != nil && s.logger != nil {
		s.logger.Printf("controller bearer auth unavailable: %v", err)
	}
	_, ok := s.requireSession(w, r)
	return ok
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

func (s *Service) startSession(w http.ResponseWriter, username string) error {
	token, err := s.auth.CreateSession(username)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   false,
		MaxAge:   int(defaultSessionTTL.Seconds()),
	})
	return nil
}

func (s *Service) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: false, MaxAge: -1})
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

	diskFree, err := diskFreeBytes(s.rootPath)
	if err != nil {
		return healthResponse{}, fmt.Errorf("stat root filesystem: %w", err)
	}
	response.DiskFreeBytes = diskFree
	response.UPSes = s.buildUPSStatuses(ctx)

	return response, nil
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

		upses = append(upses, buildUPSHealth(device, snapshot))
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
	return bootstrapRequest{
		Username:        r.FormValue("username"),
		Password:        r.FormValue("password"),
		ConfirmPassword: r.FormValue("confirm_password"),
	}, nil
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

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
