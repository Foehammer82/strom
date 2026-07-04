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
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Foehammer82/wattkeeper/controller/internal/browse"
	"github.com/Foehammer82/wattkeeper/controller/internal/ca"
	"github.com/Foehammer82/wattkeeper/controller/internal/registry"
	"github.com/Foehammer82/wattkeeper/controller/internal/securestore"
)

var version = "dev"

type config struct {
	dataDir  string
	listen   string
	logLevel string
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
}

type indexViewModel struct {
	Version string
	Listen  string
	DataDir string
}

type nodeResponse struct {
	ID       string    `json:"id"`
	Instance string    `json:"instance"`
	Hostname string    `json:"hostname"`
	Address  string    `json:"address"`
	Port     int       `json:"port"`
	Version  string    `json:"version"`
	UPSCount int       `json:"ups_count"`
	Adopted  bool      `json:"adopted"`
	Live     bool      `json:"live"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
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

//go:embed web/*
var webAssets embed.FS

var controllerAssetFS = mustSubFS(webAssets, "web")

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Wattkeeper Controller</title>
	<link rel="icon" href="/assets/favicon.svg" type="image/svg+xml">
	<link rel="stylesheet" href="/assets/styles.css">
</head>
<body>
	<main class="shell">
		<header class="topbar">
			<div class="brand">
				<img class="brand-mark" src="/assets/logo.svg" alt="Wattkeeper logo">
				<div class="brand-copy">
					<p class="eyebrow">Controller</p>
					<h1>Wattkeeper Fleet</h1>
					<p class="lede">Discovery-first controller shell for pending nodes, adopted inventory, and the next fleet workflows.</p>
				</div>
			</div>
			<div class="toolbar">
				<button id="theme-toggle" class="button button--ghost" type="button">Dark mode</button>
				<div class="chip">Version {{.Version}}</div>
			</div>
		</header>

		<section class="surface hero">
			<div class="hero-grid">
				<div>
					<div class="section-head">
						<h2>Controller overview</h2>
						<span class="helper">Persistent node registry, live discovery, and grouped fleet status.</span>
					</div>
					<div class="cards cards--summary">
						<article class="card"><span class="eyebrow">Listen</span><strong>{{.Listen}}</strong><p>HTTP address for browser access and controller APIs.</p></article>
						<article class="card"><span class="eyebrow">Data dir</span><strong>{{.DataDir}}</strong><p>SQLite registry and future adoption material persist here.</p></article>
						<article class="card"><span class="eyebrow">API</span><strong>/api/nodes</strong><p>Live JSON used by the fleet page and future React migration.</p></article>
					</div>
				</div>
				<aside class="refresh-panel card">
					<div class="refresh-hero">
						<svg class="ring" viewBox="0 0 88 88" aria-hidden="true">
							<circle class="ring-track" cx="44" cy="44" r="34"></circle>
							<circle id="refresh-ring" class="ring-progress" cx="44" cy="44" r="34"></circle>
						</svg>
						<div class="refresh-copy">
							<span class="eyebrow">Refresh timer</span>
							<strong id="refresh-seconds">5s</strong>
							<p id="refresh-status">Polling the fleet inventory every 5 seconds.</p>
						</div>
					</div>
					<div class="toolbar toolbar--tight">
						<button id="refresh-now" class="button button--primary" type="button">Refresh now</button>
						<button id="refresh-toggle" class="button button--ghost" type="button">Pause auto refresh</button>
					</div>
				</aside>
			</div>
		</section>

		<section class="surface fleet-shell">
			<div class="section-head">
				<h2>Fleet inventory</h2>
				<span class="helper">Nodes are grouped by pending, adopted online, and adopted offline state.</span>
			</div>
			<div id="fleet-groups" class="fleet-groups">
				<div class="empty-state"><p>Loading controller inventory...</p></div>
			</div>
		</section>
	</main>
	<div id="toast" class="toast" role="status" aria-live="polite"></div>
	<script src="/assets/app.js" defer></script>
</body>
</html>`))

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
	flag.Parse()
	return cfg
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(controllerAssetFS))))
	mux.HandleFunc("/healthz", a.handleHealthz)
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

func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTemplate.Execute(w, indexViewModel{Version: version, Listen: a.config.listen, DataDir: filepath.Clean(a.config.dataDir)})
}

func (a *app) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/adopt") {
		a.handleAdoptNode(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
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
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, adopted)
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
		if errors.Is(err, registry.ErrNodeNotFound) {
			status = http.StatusNotFound
		}
		writeJSONError(w, status, err.Error())
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
		responses = append(responses, toNodeResponse(node, live, ok))
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
			return toNodeResponse(node, live, true), nil
		}
	}
	return toNodeResponse(node, browse.LiveNode{}, false), nil
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

func toNodeResponse(node registry.Node, live browse.LiveNode, isLive bool) nodeResponse {
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
		ID:       node.ID,
		Instance: node.Instance,
		Hostname: node.Hostname,
		Address:  node.Address,
		Port:     node.Port,
		Version:  node.Version,
		UPSCount: node.UPSCount,
		Adopted:  node.Adopted,
		Live:     isLive,
		Status:   status,
		LastSeen: node.LastSeen,
	}
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
		var errPayload map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errPayload)
		if message := errPayload["error"]; message != "" {
			return adoptNodeResponse{}, fmt.Errorf("node adopt rejected: %s", message)
		}
		return adoptNodeResponse{}, fmt.Errorf("node adopt rejected with status %d", resp.StatusCode)
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
					return fmt.Errorf("unexpected TLS fingerprint")
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
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode pinned node health: %w", err)
	}
	return payload, nil
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
