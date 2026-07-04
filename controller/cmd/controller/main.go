package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Foehammer82/wattkeeper/controller/internal/alerts"
	"github.com/Foehammer82/wattkeeper/controller/internal/browse"
	"github.com/Foehammer82/wattkeeper/controller/internal/ca"
	controllermqtt "github.com/Foehammer82/wattkeeper/controller/internal/mqtt"
	"github.com/Foehammer82/wattkeeper/controller/internal/nutpoll"
	"github.com/Foehammer82/wattkeeper/controller/internal/registry"
	"github.com/Foehammer82/wattkeeper/controller/internal/securestore"
)

var version = "dev"

type config struct {
	dataDir             string
	listen              string
	logLevel            string
	pollInterval        time.Duration
	sampleRetention     time.Duration
	mqttBrokerURL       string
	mqttUsername        string
	mqttPassword        string
	mqttDiscoveryPrefix string
	mqttStatePrefix     string
}

type app struct {
	logger    *log.Logger
	config    config
	startedAt time.Time
	registry  *registry.Store
	browser   *browse.Browser
	ca        *ca.Authority
	client    *http.Client
	vault     *securestore.Store
	alerts    *alerts.Engine
}

type nodeResponse struct {
	ID            string               `json:"id"`
	Instance      string               `json:"instance"`
	Hostname      string               `json:"hostname"`
	Address       string               `json:"address"`
	Port          int                  `json:"port"`
	Version       string               `json:"version"`
	UPSCount      int                  `json:"ups_count"`
	DisplayName   string               `json:"display_name"`
	LocationLabel string               `json:"location_label"`
	SiteLabel     string               `json:"site_label"`
	Adopted       bool                 `json:"adopted"`
	Live          bool                 `json:"live"`
	Status        string               `json:"status"`
	CommsState    string               `json:"comms_state"`
	PollFailures  int                  `json:"poll_failures"`
	LastPolledAt  time.Time            `json:"last_polled_at,omitempty"`
	LastPollError string               `json:"last_poll_error,omitempty"`
	UPSSummaries  []upsSummaryResponse `json:"ups_summaries,omitempty"`
	LastSeen      time.Time            `json:"last_seen"`
}

type upsSummaryResponse struct {
	Name                 string    `json:"name"`
	Driver               string    `json:"driver"`
	Status               string    `json:"status,omitempty"`
	BatteryChargePercent *float64  `json:"battery_charge_percent,omitempty"`
	LoadPercent          *float64  `json:"load_percent,omitempty"`
	RuntimeSeconds       *int64    `json:"runtime_seconds,omitempty"`
	CapturedAt           time.Time `json:"captured_at,omitempty"`
}

type upsCommandDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Destructive bool   `json:"destructive"`
}

type upsWritableVarResponse struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Editor       string   `json:"editor"`
	CurrentValue string   `json:"current_value,omitempty"`
	Options      []string `json:"options,omitempty"`
	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
}

type nodesResponse struct {
	Nodes       []nodeResponse `json:"nodes"`
	GeneratedAt time.Time      `json:"generated_at"`
}

type adoptNodeResponse struct {
	Node        nodeResponse `json:"node"`
	TokenSHA256 string       `json:"token_sha256"`
	NUTUser     string       `json:"nut_user"`
}

type trustedNodeHealthResponse struct {
	NodeID string         `json:"node_id"`
	Health map[string]any `json:"health"`
}

type nodeUPSListResponse struct {
	NodeID string               `json:"node_id"`
	UPSes  []upsSummaryResponse `json:"upses"`
}

type nodeUPSDetailResponse struct {
	NodeID     string                   `json:"node_id"`
	Name       string                   `json:"name"`
	Driver     string                   `json:"driver"`
	Status     string                   `json:"status,omitempty"`
	Metrics    *upsSummaryResponse      `json:"metrics,omitempty"`
	Variables  map[string]string        `json:"variables"`
	Commands   []upsCommandDescriptor   `json:"commands,omitempty"`
	Writable   []upsWritableVarResponse `json:"writable,omitempty"`
	Live       bool                     `json:"live"`
	CapturedAt time.Time                `json:"captured_at,omitempty"`
}

type alertRulesResponse struct {
	Rules []registry.AlertRule `json:"rules"`
}

type alertEventsResponse struct {
	Events []registry.AlertEvent `json:"events"`
}

type createAlertRuleRequest struct {
	Kind            string   `json:"kind"`
	UPSVar          string   `json:"ups_var"`
	Threshold       *float64 `json:"threshold,omitempty"`
	WebhookURL      string   `json:"webhook_url"`
	DebounceSeconds int      `json:"debounce_seconds"`
	Enabled         *bool    `json:"enabled,omitempty"`
}

type updateAlertRuleRequest struct {
	Kind            *string  `json:"kind,omitempty"`
	UPSVar          *string  `json:"ups_var,omitempty"`
	Threshold       *float64 `json:"threshold,omitempty"`
	WebhookURL      *string  `json:"webhook_url,omitempty"`
	DebounceSeconds *int     `json:"debounce_seconds,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
}

type nodeUPSHistoryResponse struct {
	NodeID  string             `json:"node_id"`
	Name    string             `json:"name"`
	Samples []upsHistoryRecord `json:"samples"`
}

type upsHistoryRecord struct {
	Variable   string    `json:"variable"`
	Value      string    `json:"value"`
	CapturedAt time.Time `json:"captured_at"`
}

type nodeUPSCommandRequest struct {
	Command string `json:"cmd"`
}

type nodeUPSCommandResponse struct {
	UPS     string `json:"ups"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

type agentUPSDetailResponse struct {
	Name      string                   `json:"name"`
	Driver    string                   `json:"driver"`
	Status    string                   `json:"status"`
	Metrics   upsSummaryResponse       `json:"metrics"`
	Variables map[string]string        `json:"variables"`
	Commands  []upsCommandDescriptor   `json:"commands"`
	Writable  []upsWritableVarResponse `json:"writable"`
	UpdatedAt time.Time                `json:"updated_at"`
}

type agentUPSCommandResponse struct {
	UPS     string `json:"ups"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

type updateNodeMetadataRequest struct {
	DisplayName   *string `json:"display_name"`
	LocationLabel *string `json:"location_label"`
	SiteLabel     *string `json:"site_label"`
}

type agentAdoptRequest struct {
	CAPEM         string `json:"ca_pem"`
	NUTUser       string `json:"nut_user"`
	NUTPassword   string `json:"nut_password"`
	APIToken      string `json:"api_token"`
	ControllerURL string `json:"controller_url"`
}

type agentAdoptResponse struct {
	Serial         string `json:"serial"`
	Version        string `json:"version"`
	ControllerURL  string `json:"controller_url"`
	TLSPort        int    `json:"tls_port"`
	TLSFingerprint string `json:"tls_fingerprint"`
	TokenSHA256    string `json:"token_sha256"`
}

var (
	errNodeAlreadyAdopted          = errors.New("node already adopted")
	errTrustedNodeUnauthorized     = errors.New("node rejected controller credentials")
	errTrustedNodeFingerprintDrift = errors.New("node TLS fingerprint mismatch")
)

//go:embed assets/*
var webAssets embed.FS

var controllerAssetFS = mustSubFS(webAssets, "assets")

func main() {
	cfg := parseFlags()
	logger := log.New(os.Stdout, "wattkeeper-controller: ", log.LstdFlags)
	logger.Printf("starting listen=%s data_dir=%s log_level=%s", cfg.listen, cfg.dataDir, cfg.logLevel)

	if err := os.MkdirAll(cfg.dataDir, 0o700); err != nil {
		logger.Fatalf("ensure data dir: %v", err)
	}
	authority, err := ca.Ensure(cfg.dataDir)
	if err != nil {
		logger.Fatalf("ensure controller CA: %v", err)
	}
	vault, err := securestore.Ensure(cfg.dataDir)
	if err != nil {
		logger.Fatalf("ensure secure store: %v", err)
	}
	store, err := registry.Open(filepath.Join(cfg.dataDir, "controller.db"))
	if err != nil {
		logger.Fatalf("open registry: %v", err)
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	browser := browse.New(logger)
	if err := browser.Start(ctx); err != nil {
		logger.Fatalf("start discovery browser: %v", err)
	}

	application := &app{
		logger:    logger,
		config:    cfg,
		startedAt: time.Now().UTC(),
		registry:  store,
		browser:   browser,
		ca:        authority,
		client:    &http.Client{Timeout: 10 * time.Second},
		vault:     vault,
	}
	alertEngine := &alerts.Engine{Logger: logger, Store: store}
	application.alerts = alertEngine
	mqttPublisher, err := controllermqtt.NewPublisher(ctx, logger, controllermqtt.RuntimeConfig{
		BrokerURL:       cfg.mqttBrokerURL,
		Username:        cfg.mqttUsername,
		Password:        cfg.mqttPassword,
		DiscoveryPrefix: cfg.mqttDiscoveryPrefix,
		StatePrefix:     cfg.mqttStatePrefix,
		ClientID:        "wattkeeper-controller",
	})
	if err != nil {
		logger.Fatalf("create mqtt publisher: %v", err)
	}
	postPoll := func(ctx context.Context) error {
		if err := alertEngine.EvaluateOnce(ctx); err != nil {
			return err
		}
		if mqttPublisher != nil {
			snapshots, snapshotErr := buildMQTTSnapshots(ctx, application)
			if snapshotErr != nil {
				return snapshotErr
			}
			if err := mqttPublisher.PublishSnapshots(ctx, snapshots); err != nil {
				return err
			}
		}
		return nil
	}

	poller := &nutpoll.Poller{
		Logger:          logger,
		Store:           store,
		Vault:           vault,
		Interval:        cfg.pollInterval,
		Retention:       cfg.sampleRetention,
		OnCycleComplete: postPoll,
	}
	go func() {
		if err := poller.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Printf("NUT poller stopped: %v", err)
		}
	}()

	server := &http.Server{
		Addr:              cfg.listen,
		Handler:           loggingMiddleware(logger, corsMiddleware(application.routes())),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("http shutdown failed: %v", err)
		}
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("serve http: %v", err)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.dataDir, "data-dir", "/data", "directory for controller data")
	flag.StringVar(&cfg.listen, "listen", ":9000", "controller listen address")
	flag.StringVar(&cfg.logLevel, "log-level", "info", "log verbosity level")
	flag.DurationVar(&cfg.pollInterval, "poll-interval", 15*time.Second, "interval between adopted-node NUT polls")
	flag.DurationVar(&cfg.sampleRetention, "sample-retention", 30*24*time.Hour, "retention window for stored UPS samples")
	flag.StringVar(&cfg.mqttBrokerURL, "mqtt-broker", "", "optional MQTT broker URL used for the Home Assistant bridge")
	flag.StringVar(&cfg.mqttUsername, "mqtt-username", "", "MQTT username for the Home Assistant bridge")
	flag.StringVar(&cfg.mqttPassword, "mqtt-password", "", "MQTT password for the Home Assistant bridge")
	flag.StringVar(&cfg.mqttDiscoveryPrefix, "mqtt-discovery-prefix", "homeassistant", "Home Assistant MQTT discovery prefix")
	flag.StringVar(&cfg.mqttStatePrefix, "mqtt-state-prefix", "wattkeeper", "MQTT state topic prefix")
	flag.Parse()
	return cfg
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(mustSubFS(controllerAssetFS, "assets")))))
	mux.Handle("/favicon.svg", http.FileServer(http.FS(controllerAssetFS)))
	mux.Handle("/logo.svg", http.FileServer(http.FS(controllerAssetFS)))
	mux.HandleFunc("/healthz", a.handleHealthz)
	mux.HandleFunc("/api/alerts/rules", a.handleAlertRules)
	mux.HandleFunc("/api/alerts/rules/", a.handleAlertRules)
	mux.HandleFunc("/api/alerts/events", a.handleAlertEvents)
	mux.HandleFunc("/api/nodes", a.handleNodes)
	mux.HandleFunc("/api/nodes/", a.handleNodes)
	mux.HandleFunc("/", a.handleIndex)
	return mux
}

func (a *app) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"version":    version,
		"data_dir":   a.config.dataDir,
		"started_at": a.startedAt.Format(time.RFC3339),
	})
}

func (a *app) handleAlertRules(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/alerts/rules" {
		switch r.Method {
		case http.MethodGet:
			rules, err := a.registry.ListAlertRules(r.Context())
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, alertRulesResponse{Rules: rules})
			return
		case http.MethodPost:
			var request createAlertRuleRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode alert rule request: %v", err))
				return
			}
			enabled := true
			if request.Enabled != nil {
				enabled = *request.Enabled
			}
			rule, err := a.registry.CreateAlertRule(r.Context(), registry.AlertRule{
				Kind:            request.Kind,
				UPSVar:          request.UPSVar,
				Threshold:       request.Threshold,
				WebhookURL:      request.WebhookURL,
				DebounceSeconds: request.DebounceSeconds,
				Enabled:         enabled,
			})
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, rule)
			return
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/alerts/rules/")
	rest = strings.TrimSuffix(rest, "/")
	isTest := strings.HasSuffix(rest, "test")
	if isTest {
		rest = strings.TrimSuffix(rest, "/test")
		rest = strings.TrimSuffix(rest, "/")
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid alert rule id")
		return
	}
	if isTest {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		rule, err := a.registry.GetAlertRule(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, registry.ErrAlertRuleNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		event, err := a.alerts.TestRule(r.Context(), rule)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, event)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var request updateAlertRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode alert rule patch: %v", err))
			return
		}
		patch := registry.AlertRulePatch{
			Kind:            request.Kind,
			UPSVar:          request.UPSVar,
			Threshold:       request.Threshold,
			ThresholdSet:    request.Threshold != nil,
			WebhookURL:      request.WebhookURL,
			DebounceSeconds: request.DebounceSeconds,
			Enabled:         request.Enabled,
		}
		rule, err := a.registry.UpdateAlertRule(r.Context(), id, patch)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, registry.ErrAlertRuleNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rule)
	case http.MethodDelete:
		if err := a.registry.DeleteAlertRule(r.Context(), id); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, registry.ErrAlertRuleNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", http.MethodPatch+", "+http.MethodDelete+", "+http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *app) handleAlertEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 100
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			writeJSONError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	events, err := a.registry.ListAlertEvents(r.Context(), limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, alertEventsResponse{Events: events})
}

func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	content, err := fs.ReadFile(controllerAssetFS, "index.html")
	if err != nil {
		http.Error(w, "frontend not built", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(content)
}

func (a *app) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/adopt") {
		a.handleAdoptNode(w, r)
		return
	}
	if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/nodes/") {
		a.handleUpdateNodeMetadata(w, r)
		return
	}
	if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/nodes/") {
		a.handleForgetNode(w, r)
		return
	}
	if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/ups/") {
		a.handleNodeUPS(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost+", "+http.MethodPatch+", "+http.MethodDelete)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.Path == "/api/nodes" {
		nodes, err := a.buildNodeResponses(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, nodesResponse{Nodes: nodes, GeneratedAt: time.Now().UTC()})
		return
	}
	if strings.Contains(r.URL.Path, "/ups") {
		a.handleNodeUPS(w, r)
		return
	}
	id := r.URL.Path[len("/api/nodes/"):]
	if strings.HasSuffix(r.URL.Path, "/health") {
		a.handleTrustedNodeHealth(w, r)
		return
	}
	if id == "" {
		http.NotFound(w, r)
		return
	}
	node, err := a.buildNodeResponse(r.Context(), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, registry.ErrNodeNotFound) {
			status = http.StatusNotFound
		}
		writeJSONError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (a *app) handleNodeUPS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != "ups" {
		http.NotFound(w, r)
		return
	}
	nodeID := parts[0]
	if len(parts) == 2 {
		node, err := a.buildNodeResponse(r.Context(), nodeID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, registry.ErrNodeNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, nodeUPSListResponse{NodeID: nodeID, UPSes: node.UPSSummaries})
		return
	}
	upsName, err := url.PathUnescape(parts[2])
	if err != nil || strings.TrimSpace(upsName) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid UPS name")
		return
	}
	if len(parts) == 3 {
		node, err := a.buildNodeResponse(r.Context(), nodeID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, registry.ErrNodeNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		detail, err := a.registry.GetUPSDetail(r.Context(), nodeID, upsName)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, registry.ErrUPSNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		response := nodeUPSDetailResponse{NodeID: nodeID, Name: detail.Name, Driver: detail.Driver, Variables: detail.Variables, CapturedAt: detail.CapturedAt, Live: node.Live}
		if len(node.UPSSummaries) > 0 {
			for _, summary := range node.UPSSummaries {
				if summary.Name == upsName {
					copied := summary
					response.Metrics = &copied
					response.Status = summary.Status
					break
				}
			}
		}
		if node.Live {
			liveDetail, liveErr := a.fetchTrustedUPSDetail(r.Context(), nodeID, upsName)
			if liveErr != nil {
				if errors.Is(liveErr, errTrustedNodeUnauthorized) || errors.Is(liveErr, errTrustedNodeFingerprintDrift) {
					status := http.StatusBadGateway
					if errors.Is(liveErr, errTrustedNodeUnauthorized) {
						status = http.StatusUnauthorized
					} else if errors.Is(liveErr, errTrustedNodeFingerprintDrift) {
						status = http.StatusConflict
					}
					writeJSONError(w, status, trustedNodeErrorMessage(liveErr))
					return
				}
			} else {
				response.Driver = firstNonEmpty(response.Driver, liveDetail.Driver)
				response.Status = liveDetail.Status
				response.Metrics = &liveDetail.Metrics
				response.Variables = liveDetail.Variables
				response.Commands = liveDetail.Commands
				response.Writable = liveDetail.Writable
			}
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 4 && parts[3] == "command" {
		var request nodeUPSCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode UPS command request: %v", err))
			return
		}
		request.Command = strings.TrimSpace(request.Command)
		if request.Command == "" {
			writeJSONError(w, http.StatusBadRequest, "cmd is required")
			return
		}
		response, err := a.runTrustedUPSCommand(r.Context(), nodeID, upsName, request.Command)
		if err != nil {
			status := http.StatusBadGateway
			switch {
			case errors.Is(err, registry.ErrNodeNotFound), errors.Is(err, registry.ErrUPSNotFound):
				status = http.StatusNotFound
			case errors.Is(err, errTrustedNodeUnauthorized):
				status = http.StatusUnauthorized
			case errors.Is(err, errTrustedNodeFingerprintDrift):
				status = http.StatusConflict
			}
			writeJSONError(w, status, trustedNodeErrorMessage(err))
			return
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 4 && parts[3] == "history" {
		limit := 200
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			parsed, parseErr := strconv.Atoi(rawLimit)
			if parseErr != nil || parsed <= 0 {
				writeJSONError(w, http.StatusBadRequest, "limit must be a positive integer")
				return
			}
			limit = parsed
		}
		variable := strings.TrimSpace(r.URL.Query().Get("var"))
		var since time.Time
		if rawHours := strings.TrimSpace(r.URL.Query().Get("hours")); rawHours != "" {
			parsedHours, parseErr := strconv.Atoi(rawHours)
			if parseErr != nil || parsedHours <= 0 {
				writeJSONError(w, http.StatusBadRequest, "hours must be a positive integer")
				return
			}
			since = time.Now().UTC().Add(-time.Duration(parsedHours) * time.Hour)
		}
		history, err := a.registry.ListUPSHistoryFiltered(r.Context(), nodeID, upsName, variable, since, limit)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, registry.ErrUPSNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, nodeUPSHistoryResponse{NodeID: nodeID, Name: upsName, Samples: toUPSHistoryRecords(history)})
		return
	}
	http.NotFound(w, r)
}

func (a *app) handleAdoptNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), "/adopt")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	node, err := a.buildNodeResponse(r.Context(), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, registry.ErrNodeNotFound) {
			status = http.StatusNotFound
		}
		writeJSONError(w, status, err.Error())
		return
	}
	if !node.Live || node.Address == "" || node.Port == 0 {
		writeJSONError(w, http.StatusBadGateway, "node is not currently reachable for adoption")
		return
	}
	adopted, err := a.adoptNode(r.Context(), r, node)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errNodeAlreadyAdopted) {
			status = http.StatusConflict
		}
		writeJSONError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, adopted)
}

func (a *app) handleForgetNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodDelete)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	id = strings.TrimSuffix(id, "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if err := a.registry.DeleteNode(r.Context(), id); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, registry.ErrNodeNotFound) {
			status = http.StatusNotFound
		}
		writeJSONError(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) handleUpdateNodeMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", http.MethodPatch)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	id = strings.TrimSuffix(id, "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var request updateNodeMetadataRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode node metadata request: %v", err))
		return
	}
	patch := registry.NodeMetadataPatch{
		DisplayName:   request.DisplayName,
		LocationLabel: request.LocationLabel,
		SiteLabel:     request.SiteLabel,
	}
	if patch.DisplayName == nil && patch.LocationLabel == nil && patch.SiteLabel == nil {
		writeJSONError(w, http.StatusBadRequest, "at least one metadata field is required")
		return
	}
	if err := a.registry.UpdateNodeMetadata(r.Context(), id, patch); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, registry.ErrNodeNotFound) {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "empty") {
			status = http.StatusBadRequest
		}
		writeJSONError(w, status, err.Error())
		return
	}
	updated, err := a.buildNodeResponse(r.Context(), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, registry.ErrNodeNotFound) {
			status = http.StatusNotFound
		}
		writeJSONError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *app) handleTrustedNodeHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), "/health")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	health, err := a.fetchTrustedNodeHealth(r.Context(), id)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, registry.ErrNodeNotFound):
			status = http.StatusNotFound
		case errors.Is(err, errTrustedNodeUnauthorized):
			status = http.StatusUnauthorized
		case errors.Is(err, errTrustedNodeFingerprintDrift):
			status = http.StatusConflict
		}
		writeJSONError(w, status, trustedNodeErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, health)
}

func (a *app) buildNodeResponses(ctx context.Context) ([]nodeResponse, error) {
	if err := a.syncLiveNodes(ctx); err != nil {
		return nil, err
	}
	nodes, err := a.registry.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	liveByID := make(map[string]browse.LiveNode)
	for _, live := range a.browser.Snapshot() {
		liveByID[live.ID] = live
	}
	responses := make([]nodeResponse, 0, len(nodes))
	for _, node := range nodes {
		live, ok := liveByID[node.ID]
		summaries, err := a.registry.ListNodeUPSSummaries(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		responses = append(responses, toNodeResponse(node, live, ok, summaries))
	}
	sort.Slice(responses, func(i, j int) bool {
		left := statusRank(responses[i].Status)
		right := statusRank(responses[j].Status)
		if left != right {
			return left < right
		}
		if !responses[i].LastSeen.Equal(responses[j].LastSeen) {
			return responses[i].LastSeen.After(responses[j].LastSeen)
		}
		return responses[i].ID < responses[j].ID
	})
	return responses, nil
}

func (a *app) buildNodeResponse(ctx context.Context, id string) (nodeResponse, error) {
	if err := a.syncLiveNodes(ctx); err != nil {
		return nodeResponse{}, err
	}
	node, err := a.registry.GetNode(ctx, id)
	if err != nil {
		return nodeResponse{}, err
	}
	for _, live := range a.browser.Snapshot() {
		if live.ID == id {
			summaries, err := a.registry.ListNodeUPSSummaries(ctx, node.ID)
			if err != nil {
				return nodeResponse{}, err
			}
			return toNodeResponse(node, live, true, summaries), nil
		}
	}
	summaries, err := a.registry.ListNodeUPSSummaries(ctx, node.ID)
	if err != nil {
		return nodeResponse{}, err
	}
	return toNodeResponse(node, browse.LiveNode{}, false, summaries), nil
}

func (a *app) syncLiveNodes(ctx context.Context) error {
	for _, live := range a.browser.Snapshot() {
		if err := a.registry.UpsertDiscoveredNode(ctx, registry.Node{
			ID:       live.ID,
			Instance: live.Instance,
			Hostname: live.Hostname,
			Address:  live.Address,
			Port:     live.Port,
			Version:  live.Version,
			UPSCount: live.UPSCount,
			Adopted:  live.Adopted,
			LastSeen: live.LastSeen,
		}); err != nil {
			return err
		}
	}
	return nil
}

func toNodeResponse(node registry.Node, live browse.LiveNode, isLive bool, summaries []registry.UPSLatestSample) nodeResponse {
	if isLive {
		node.Instance = live.Instance
		node.Hostname = live.Hostname
		node.Address = live.Address
		node.Port = live.Port
		node.Version = live.Version
		node.UPSCount = live.UPSCount
		node.LastSeen = live.LastSeen
	}
	status := "pending"
	if node.Adopted {
		if isLive {
			status = "adopted-online"
		} else {
			status = "adopted-offline"
		}
	}
	return nodeResponse{
		ID:            node.ID,
		Instance:      node.Instance,
		Hostname:      node.Hostname,
		Address:       node.Address,
		Port:          node.Port,
		Version:       node.Version,
		UPSCount:      node.UPSCount,
		DisplayName:   node.DisplayName,
		LocationLabel: node.LocationLabel,
		SiteLabel:     node.SiteLabel,
		Adopted:       node.Adopted,
		Live:          isLive,
		Status:        status,
		CommsState:    node.CommsState,
		PollFailures:  node.PollFailures,
		LastPolledAt:  node.LastPolledAt,
		LastPollError: node.LastPollError,
		UPSSummaries:  toUPSSummaryResponses(summaries),
		LastSeen:      node.LastSeen,
	}
}

func toUPSSummaryResponses(summaries []registry.UPSLatestSample) []upsSummaryResponse {
	if len(summaries) == 0 {
		return nil
	}
	responses := make([]upsSummaryResponse, 0, len(summaries))
	for _, summary := range summaries {
		responses = append(responses, upsSummaryResponse{
			Name:                 summary.Name,
			Driver:               summary.Driver,
			Status:               summary.Status,
			BatteryChargePercent: summary.BatteryChargePercent,
			LoadPercent:          summary.LoadPercent,
			RuntimeSeconds:       summary.RuntimeSeconds,
			CapturedAt:           summary.CapturedAt,
		})
	}
	return responses
}

func toUPSHistoryRecords(samples []registry.UPSHistorySample) []upsHistoryRecord {
	if len(samples) == 0 {
		return nil
	}
	records := make([]upsHistoryRecord, 0, len(samples))
	for _, sample := range samples {
		records = append(records, upsHistoryRecord{Variable: sample.Variable, Value: sample.Value, CapturedAt: sample.CapturedAt})
	}
	return records
}

func buildMQTTSnapshots(ctx context.Context, a *app) ([]controllermqtt.NodeSnapshot, error) {
	if a == nil || a.registry == nil {
		return nil, nil
	}
	nodes, err := a.registry.ListAdoptedNodes(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	snapshots := make([]controllermqtt.NodeSnapshot, 0, len(nodes))
	for _, node := range nodes {
		summaries, err := a.registry.ListNodeUPSSummaries(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		nodeInfo := controllermqtt.NodeInfo{
			ID:          node.ID,
			DisplayName: node.DisplayName,
			Hostname:    node.Hostname,
			Location:    strings.TrimSpace(strings.Join([]string{node.LocationLabel, node.SiteLabel}, " / ")),
			Online:      !node.LastSeen.IsZero() && now.Sub(node.LastSeen) <= 45*time.Second,
			CommsState:  node.CommsState,
			Version:     node.Version,
		}
		upsInfos := make([]controllermqtt.UPSInfo, 0, len(summaries))
		for _, summary := range summaries {
			detail, detailErr := a.registry.GetUPSDetail(ctx, node.ID, summary.Name)
			if detailErr != nil && !errors.Is(detailErr, registry.ErrUPSNotFound) {
				return nil, detailErr
			}
			upsInfo := controllermqtt.UPSInfo{
				Name:          summary.Name,
				DisplayName:   summary.Name,
				Driver:        summary.Driver,
				Status:        summary.Status,
				BatteryCharge: summary.BatteryChargePercent,
				LoadPercent:   summary.LoadPercent,
				Runtime:       summary.RuntimeSeconds,
				OnBattery:     strings.Contains(strings.ToUpper(summary.Status), "OB"),
				LowBattery:    summary.BatteryChargePercent != nil && *summary.BatteryChargePercent <= 20,
			}
			if detailErr == nil {
				if inputVoltage := parseFloatVariable(detail.Variables["input.voltage"]); inputVoltage != nil {
					upsInfo.InputVoltage = inputVoltage
				}
			}
			upsInfos = append(upsInfos, upsInfo)
		}
		snapshots = append(snapshots, controllermqtt.NodeSnapshot{Node: nodeInfo, UPSes: upsInfos})
	}
	return snapshots, nil
}

func parseFloatVariable(raw string) *float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (a *app) fetchTrustedUPSDetail(ctx context.Context, nodeID, upsName string) (agentUPSDetailResponse, error) {
	var response agentUPSDetailResponse
	err := a.doTrustedNodeJSON(ctx, nodeID, http.MethodGet, "/api/ups/"+url.PathEscape(upsName), nil, &response)
	return response, err
}

func (a *app) runTrustedUPSCommand(ctx context.Context, nodeID, upsName, command string) (agentUPSCommandResponse, error) {
	var response agentUPSCommandResponse
	err := a.doTrustedNodeJSON(ctx, nodeID, http.MethodPost, "/api/ups/"+url.PathEscape(upsName)+"/command", nodeUPSCommandRequest{Command: command}, &response)
	return response, err
}

func (a *app) doTrustedNodeJSON(ctx context.Context, nodeID, method, path string, payload any, out any) error {
	node, err := a.registry.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	trust, err := a.registry.LoadNodeTrust(ctx, nodeID)
	if err != nil {
		return err
	}
	apiToken, err := a.vault.OpenString(trust.APITokenEnc)
	if err != nil {
		return fmt.Errorf("open stored node api token: %w", err)
	}
	client := pinnedNodeClient(trust.TLSFingerprint)
	var bodyReader *bytes.Reader
	if payload != nil {
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return marshalErr
		}
		bodyReader = bytes.NewReader(body)
		_ = bodyReader
	}
	var requestBody io.Reader
	if payload != nil {
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return marshalErr
		}
		requestBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("https://%s:%d%s", node.Address, trust.TLSPort, path), requestBody)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: unexpected status %d", errTrustedNodeUnauthorized, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		return registry.ErrUPSNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := decodeNodeError(resp)
		if message != "" {
			return fmt.Errorf("trusted node request rejected: %s", message)
		}
		return fmt.Errorf("trusted node request rejected with status %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode trusted node response: %w", err)
		}
	}
	return nil
}

func pinnedNodeClient(fingerprint string) *http.Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return errors.New("no peer certificate presented")
				}
				sum := sha256.Sum256(rawCerts[0])
				if fmt.Sprintf("%x", sum[:]) != fingerprint {
					return fmt.Errorf("%w", errTrustedNodeFingerprintDrift)
				}
				return nil
			},
		},
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: transport}
}

func (a *app) adoptNode(ctx context.Context, r *http.Request, node nodeResponse) (adoptNodeResponse, error) {
	nutPassword, err := randomSecret(24)
	if err != nil {
		return adoptNodeResponse{}, err
	}
	apiToken, err := randomSecret(32)
	if err != nil {
		return adoptNodeResponse{}, err
	}
	payload := agentAdoptRequest{
		CAPEM:         a.ca.CAPEM(),
		NUTUser:       "controller",
		NUTPassword:   nutPassword,
		APIToken:      apiToken,
		ControllerURL: controllerURLFromRequest(r),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return adoptNodeResponse{}, err
	}
	url := fmt.Sprintf("http://%s:%d/adopt", node.Address, node.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return adoptNodeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return adoptNodeResponse{}, fmt.Errorf("call node adopt endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message := decodeNodeError(resp)
		switch resp.StatusCode {
		case http.StatusConflict:
			if message == "" {
				message = errNodeAlreadyAdopted.Error()
			}
			return adoptNodeResponse{}, fmt.Errorf("%w: %s", errNodeAlreadyAdopted, message)
		default:
			if message != "" {
				return adoptNodeResponse{}, fmt.Errorf("node adopt rejected: %s", message)
			}
			return adoptNodeResponse{}, fmt.Errorf("node adopt rejected with status %d", resp.StatusCode)
		}
	}
	var adoptResp agentAdoptResponse
	if err := json.NewDecoder(resp.Body).Decode(&adoptResp); err != nil {
		return adoptNodeResponse{}, fmt.Errorf("decode node adopt response: %w", err)
	}
	if err := a.verifyPinnedNodeHealth(ctx, node.Address, adoptResp.TLSPort, apiToken, adoptResp.TLSFingerprint); err != nil {
		return adoptNodeResponse{}, fmt.Errorf("verify node TLS API: %w", err)
	}
	sealedToken, err := a.vault.SealString(apiToken)
	if err != nil {
		return adoptNodeResponse{}, fmt.Errorf("seal node api token: %w", err)
	}
	sealedPassword, err := a.vault.SealString(nutPassword)
	if err != nil {
		return adoptNodeResponse{}, fmt.Errorf("seal node NUT password: %w", err)
	}
	if err := a.registry.SaveNodeTrust(ctx, node.ID, registry.Trust{
		ControllerURL:  payload.ControllerURL,
		TLSPort:        adoptResp.TLSPort,
		TLSFingerprint: adoptResp.TLSFingerprint,
		NUTUser:        payload.NUTUser,
		APITokenEnc:    sealedToken,
		NUTPasswordEnc: sealedPassword,
	}); err != nil {
		return adoptNodeResponse{}, err
	}
	if err := a.registry.SetNodeAdopted(ctx, node.ID, true); err != nil {
		return adoptNodeResponse{}, err
	}
	updated, err := a.buildNodeResponse(ctx, node.ID)
	if err != nil {
		return adoptNodeResponse{}, err
	}
	updated.Adopted = true
	updated.Status = "adopted-online"
	return adoptNodeResponse{Node: updated, TokenSHA256: adoptResp.TokenSHA256, NUTUser: payload.NUTUser}, nil
}

func (a *app) fetchTrustedNodeHealth(ctx context.Context, id string) (trustedNodeHealthResponse, error) {
	node, err := a.registry.GetNode(ctx, id)
	if err != nil {
		return trustedNodeHealthResponse{}, err
	}
	trust, err := a.registry.LoadNodeTrust(ctx, id)
	if err != nil {
		return trustedNodeHealthResponse{}, err
	}
	apiToken, err := a.vault.OpenString(trust.APITokenEnc)
	if err != nil {
		return trustedNodeHealthResponse{}, fmt.Errorf("open stored node api token: %w", err)
	}
	payload, err := a.fetchPinnedNodeHealthPayload(ctx, node.Address, trust.TLSPort, apiToken, trust.TLSFingerprint)
	if err != nil {
		return trustedNodeHealthResponse{}, err
	}
	return trustedNodeHealthResponse{NodeID: id, Health: payload}, nil
}

func (a *app) verifyPinnedNodeHealth(ctx context.Context, address string, port int, apiToken, fingerprint string) error {
	_, err := a.fetchPinnedNodeHealthPayload(ctx, address, port, apiToken, fingerprint)
	return err
}

func (a *app) fetchPinnedNodeHealthPayload(ctx context.Context, address string, port int, apiToken, fingerprint string) (map[string]any, error) {
	if address == "" || port == 0 || apiToken == "" || fingerprint == "" {
		return nil, errors.New("missing TLS verification inputs")
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return errors.New("no peer certificate presented")
				}
				sum := sha256.Sum256(rawCerts[0])
				if fmt.Sprintf("%x", sum[:]) != fingerprint {
					return fmt.Errorf("%w", errTrustedNodeFingerprintDrift)
				}
				return nil
			},
		},
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s:%d/api/health", address, port), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("%w: unexpected status %d", errTrustedNodeUnauthorized, resp.StatusCode)
		}
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode pinned node health: %w", err)
	}
	return payload, nil
}

func decodeNodeError(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	var errPayload map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errPayload); err != nil {
		return ""
	}
	return strings.TrimSpace(errPayload["error"])
}

func trustedNodeErrorMessage(err error) string {
	switch {
	case errors.Is(err, errTrustedNodeUnauthorized):
		return "node rejected the stored controller bearer token"
	case errors.Is(err, errTrustedNodeFingerprintDrift):
		return "node certificate fingerprint does not match stored trust material"
	default:
		return err.Error()
	}
}

func statusRank(status string) int {
	switch status {
	case "pending":
		return 0
	case "adopted-online":
		return 1
	case "adopted-offline":
		return 2
	default:
		return 3
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func mustSubFS(source fs.FS, dir string) fs.FS {
	subtree, err := fs.Sub(source, dir)
	if err != nil {
		panic(err)
	}
	return subtree
}

func loggingMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Printf("http method=%s path=%s duration=%s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func randomSecret(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func controllerURLFromRequest(r *http.Request) string {
	if r == nil || r.Host == "" {
		return ""
	}
	return "http://" + r.Host
}
