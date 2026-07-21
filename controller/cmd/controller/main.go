package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	"net"
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

	"github.com/Foehammer82/strom/controller/internal/aggregatenut"
	"github.com/Foehammer82/strom/controller/internal/alerts"
	"github.com/Foehammer82/strom/controller/internal/browse"
	"github.com/Foehammer82/strom/controller/internal/ca"
	controllermqtt "github.com/Foehammer82/strom/controller/internal/mqtt"
	"github.com/Foehammer82/strom/controller/internal/nutpoll"
	"github.com/Foehammer82/strom/controller/internal/registry"
	"github.com/Foehammer82/strom/controller/internal/securestore"
)

var version = "dev"

const defaultControllerDataDir = "/data"

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
	discoverySeeds      string
	aggregateNUTListen  string
	aggregateNUTEnabled bool
	aggregateNUTUser    string
	aggregateNUTPass    string
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
	aggregate *aggregatenut.Manager
}

type nodeResponse struct {
	ID             string               `json:"id"`
	Instance       string               `json:"instance"`
	Hostname       string               `json:"hostname"`
	Address        string               `json:"address"`
	Port           int                  `json:"port"`
	Version        string               `json:"version"`
	UPSCount       int                  `json:"ups_count"`
	DisplayName    string               `json:"display_name"`
	LocationLabel  string               `json:"location_label"`
	SiteLabel      string               `json:"site_label"`
	LocalUIManaged bool                 `json:"local_ui_policy_managed"`
	LocalUIEnabled bool                 `json:"local_ui_policy_enabled"`
	Adopted        bool                 `json:"adopted"`
	Live           bool                 `json:"live"`
	Status         string               `json:"status"`
	CommsState     string               `json:"comms_state"`
	PollFailures   int                  `json:"poll_failures"`
	LastPolledAt   time.Time            `json:"last_polled_at,omitempty"`
	LastPollError  string               `json:"last_poll_error,omitempty"`
	UPSSummaries   []upsSummaryResponse `json:"ups_summaries,omitempty"`
	LastSeen       time.Time            `json:"last_seen"`
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
	NodeID              string                   `json:"node_id"`
	Name                string                   `json:"name"`
	Driver              string                   `json:"driver"`
	Status              string                   `json:"status,omitempty"`
	Metrics             *upsSummaryResponse      `json:"metrics,omitempty"`
	BatteryRuntimeTrend *batteryTrendResponse    `json:"battery_runtime_trend,omitempty"`
	Variables           map[string]string        `json:"variables"`
	Commands            []upsCommandDescriptor   `json:"commands,omitempty"`
	Writable            []upsWritableVarResponse `json:"writable,omitempty"`
	Live                bool                     `json:"live"`
	CapturedAt          time.Time                `json:"captured_at,omitempty"`
}

type batteryTrendResponse struct {
	BaselineRuntimeSeconds  int64     `json:"baseline_runtime_seconds"`
	LatestRuntimeSeconds    int64     `json:"latest_runtime_seconds"`
	ReplacementThresholdSec int64     `json:"replacement_threshold_seconds"`
	TrendSecondsPer30Days   float64   `json:"trend_seconds_per_30d"`
	SamplesUsed             int       `json:"samples_used"`
	LatestSampledAt         time.Time `json:"latest_sampled_at"`
	EstimatedReplaceBy      time.Time `json:"estimated_replace_by,omitempty"`
}

type alertRulesResponse struct {
	Rules []registry.AlertRule `json:"rules"`
}

type alertEventsResponse struct {
	Events []registry.AlertEvent `json:"events"`
}

type controllerSettingsResponse struct {
	AggregateNUTEnabled bool   `json:"aggregate_nut_enabled"`
	AggregateNUTListen  string `json:"aggregate_nut_listen"`
	AggregateNUTActive  bool   `json:"aggregate_nut_active"`
}

type updateControllerSettingsRequest struct {
	AggregateNUTEnabled *bool   `json:"aggregate_nut_enabled,omitempty"`
	AggregateNUTListen  *string `json:"aggregate_nut_listen,omitempty"`
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

type nodeLocalUIPolicyRequest struct {
	Managed bool `json:"managed"`
	Enabled bool `json:"enabled"`
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
	DisplayName    *string `json:"display_name"`
	LocationLabel  *string `json:"location_label"`
	SiteLabel      *string `json:"site_label"`
	LocalUIManaged *bool   `json:"local_ui_policy_managed"`
	LocalUIEnabled *bool   `json:"local_ui_policy_enabled"`
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

type agentOTAUpdateRequest struct {
	Version         string `json:"version"`
	BinaryBase64    string `json:"binary_base64"`
	SHA256          string `json:"sha256"`
	SignatureBase64 string `json:"signature_base64"`
}

type agentOTAUpdateResponse struct {
	Status          string `json:"status"`
	Version         string `json:"version"`
	SHA256          string `json:"sha256"`
	RestartRequired bool   `json:"restart_required"`
}

var (
	errNodeAlreadyAdopted          = errors.New("node already adopted")
	errTrustedNodeUnauthorized     = errors.New("node rejected controller credentials")
	errTrustedNodeFingerprintDrift = errors.New("node TLS fingerprint mismatch")
)

const (
	adoptRequestMaxAttempts = 3
	adoptRetryBackoff       = 400 * time.Millisecond
)

//go:embed assets/*
var webAssets embed.FS

var controllerAssetFS = mustSubFS(webAssets, "assets")

func main() {
	logger := log.New(os.Stdout, "strom-controller: ", log.LstdFlags)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "backup":
			if err := runBackupCommand(logger, os.Args[2:]); err != nil {
				logger.Fatalf("backup failed: %v", err)
			}
			return
		case "restore":
			if err := runRestoreCommand(logger, os.Args[2:]); err != nil {
				logger.Fatalf("restore failed: %v", err)
			}
			return
		case "ota":
			if err := runOTACommand(logger, os.Args[2:]); err != nil {
				logger.Fatalf("ota failed: %v", err)
			}
			return
		}
	}

	cfg := parseFlags()
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
	browser.ConfigureSeeds(cfg.discoverySeeds)
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
		aggregate: aggregatenut.NewManager(logger),
	}
	application.aggregate.SetBackend(&aggregateNUTBackend{app: application})
	application.aggregate.SetAuthenticator(func(username, password string) bool {
		return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(username)), []byte(strings.TrimSpace(cfg.aggregateNUTUser))) == 1 &&
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(password)), []byte(cfg.aggregateNUTPass)) == 1
	})

	settingsDefaults := registry.ControllerSettings{AggregateNUTEnabled: cfg.aggregateNUTEnabled, AggregateNUTListen: cfg.aggregateNUTListen}
	settings, err := store.LoadControllerSettings(ctx, settingsDefaults)
	if err != nil {
		logger.Fatalf("load controller settings: %v", err)
	}
	if err := application.aggregate.Apply(ctx, settings.AggregateNUTEnabled, settings.AggregateNUTListen); err != nil {
		logger.Fatalf("apply aggregate NUT settings: %v", err)
	}
	alertEngine := &alerts.Engine{Logger: logger, Store: store}
	application.alerts = alertEngine
	mqttPublisher, err := controllermqtt.NewPublisher(ctx, logger, controllermqtt.RuntimeConfig{
		BrokerURL:       cfg.mqttBrokerURL,
		Username:        cfg.mqttUsername,
		Password:        cfg.mqttPassword,
		DiscoveryPrefix: cfg.mqttDiscoveryPrefix,
		StatePrefix:     cfg.mqttStatePrefix,
		ClientID:        "strom-controller",
		CommandHandler: func(commandCtx context.Context, request controllermqtt.CommandRequest) error {
			_, runErr := application.runTrustedUPSCommand(commandCtx, request.NodeID, request.UPSName, request.Command)
			return runErr
		},
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
		application.aggregate.Close()
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
	flag.StringVar(&cfg.dataDir, "data-dir", defaultControllerDataDir, "directory for controller data")
	flag.StringVar(&cfg.listen, "listen", ":9000", "controller listen address")
	flag.StringVar(&cfg.logLevel, "log-level", "info", "log verbosity level")
	flag.DurationVar(&cfg.pollInterval, "poll-interval", 15*time.Second, "interval between adopted-node NUT polls")
	flag.DurationVar(&cfg.sampleRetention, "sample-retention", 30*24*time.Hour, "retention window for stored UPS samples")
	flag.StringVar(&cfg.mqttBrokerURL, "mqtt-broker", "", "optional MQTT broker URL used for the Home Assistant bridge")
	flag.StringVar(&cfg.mqttUsername, "mqtt-username", "", "MQTT username for the Home Assistant bridge")
	flag.StringVar(&cfg.mqttPassword, "mqtt-password", "", "MQTT password for the Home Assistant bridge")
	flag.StringVar(&cfg.mqttDiscoveryPrefix, "mqtt-discovery-prefix", "homeassistant", "Home Assistant MQTT discovery prefix")
	flag.StringVar(&cfg.mqttStatePrefix, "mqtt-state-prefix", "strom", "MQTT state topic prefix")
	flag.StringVar(&cfg.discoverySeeds, "discovery-seeds", "", "optional comma-separated host:port targets for discovery fallback when mDNS is unavailable")
	flag.StringVar(&cfg.aggregateNUTListen, "aggregate-nut-listen", ":3493", "controller aggregate NUT listen address")
	flag.BoolVar(&cfg.aggregateNUTEnabled, "aggregate-nut-enabled", true, "enable controller aggregate NUT listener")
	flag.StringVar(&cfg.aggregateNUTUser, "aggregate-nut-user", "controller", "username required for aggregate NUT listener")
	flag.StringVar(&cfg.aggregateNUTPass, "aggregate-nut-pass", "controller", "password required for aggregate NUT listener")
	flag.Parse()
	return cfg
}

type backupConfig struct {
	dataDir string
	output  string
}

type restoreConfig struct {
	dataDir string
	input   string
	force   bool
}

type otaConfig struct {
	dataDir    string
	nodeID     string
	binaryPath string
	version    string
}

func parseBackupFlags(args []string) (backupConfig, error) {
	var cfg backupConfig
	flags := flag.NewFlagSet("strom-controller backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.dataDir, "data-dir", defaultControllerDataDir, "directory containing controller.db")
	flags.StringVar(&cfg.output, "output", "", "destination path for backup copy of controller.db")
	if err := flags.Parse(args); err != nil {
		return backupConfig{}, err
	}
	return cfg, nil
}

func parseRestoreFlags(args []string) (restoreConfig, error) {
	var cfg restoreConfig
	flags := flag.NewFlagSet("strom-controller restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.dataDir, "data-dir", defaultControllerDataDir, "directory containing controller.db")
	flags.StringVar(&cfg.input, "input", "", "source backup file to restore into controller.db")
	flags.BoolVar(&cfg.force, "force", false, "overwrite existing controller.db")
	if err := flags.Parse(args); err != nil {
		return restoreConfig{}, err
	}
	return cfg, nil
}

func parseOTAFlags(args []string) (otaConfig, error) {
	var cfg otaConfig
	flags := flag.NewFlagSet("strom-controller ota", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.dataDir, "data-dir", defaultControllerDataDir, "directory containing controller.db and controller CA")
	flags.StringVar(&cfg.nodeID, "node-id", "", "adopted node ID to receive the update")
	flags.StringVar(&cfg.binaryPath, "binary", "", "path to the signed strom-agent binary payload")
	flags.StringVar(&cfg.version, "version", "", "version string advertised in OTA metadata")
	if err := flags.Parse(args); err != nil {
		return otaConfig{}, err
	}
	return cfg, nil
}

func controllerDBPath(dataDir string) string {
	return filepath.Join(strings.TrimSpace(dataDir), "controller.db")
}

func runBackupCommand(logger *log.Logger, args []string) error {
	cfg, err := parseBackupFlags(args)
	if err != nil {
		return err
	}
	output := strings.TrimSpace(cfg.output)
	if output == "" {
		return errors.New("backup output path is required")
	}
	source := controllerDBPath(cfg.dataDir)
	info, err := os.Stat(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("controller database not found: %s", source)
		}
		return fmt.Errorf("stat controller database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("controller database is not a regular file: %s", source)
	}
	if err := copyFileAtomic(source, output, 0o600); err != nil {
		return fmt.Errorf("backup controller database: %w", err)
	}
	if logger != nil {
		logger.Printf("backup complete source=%s output=%s", source, output)
	}
	return nil
}

func runRestoreCommand(logger *log.Logger, args []string) error {
	cfg, err := parseRestoreFlags(args)
	if err != nil {
		return err
	}
	input := strings.TrimSpace(cfg.input)
	if input == "" {
		return errors.New("restore input path is required")
	}
	info, err := os.Stat(input)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("restore input not found: %s", input)
		}
		return fmt.Errorf("stat restore input: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("restore input is not a regular file: %s", input)
	}
	destination := controllerDBPath(cfg.dataDir)
	if !cfg.force {
		if _, statErr := os.Stat(destination); statErr == nil {
			return fmt.Errorf("controller database already exists at %s (use --force to overwrite)", destination)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("stat destination controller database: %w", statErr)
		}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create controller data dir: %w", err)
	}
	if err := copyFileAtomic(input, destination, 0o600); err != nil {
		return fmt.Errorf("restore controller database: %w", err)
	}
	if logger != nil {
		logger.Printf("restore complete input=%s destination=%s", input, destination)
	}
	return nil
}

func runOTACommand(logger *log.Logger, args []string) error {
	cfg, err := parseOTAFlags(args)
	if err != nil {
		return err
	}
	nodeID := strings.TrimSpace(cfg.nodeID)
	if nodeID == "" {
		return errors.New("node-id is required")
	}
	binaryPath := strings.TrimSpace(cfg.binaryPath)
	if binaryPath == "" {
		return errors.New("binary path is required")
	}
	versionText := strings.TrimSpace(cfg.version)
	if versionText == "" {
		return errors.New("version is required")
	}

	authority, err := ca.Ensure(cfg.dataDir)
	if err != nil {
		return fmt.Errorf("ensure controller CA: %w", err)
	}
	vault, err := securestore.Ensure(cfg.dataDir)
	if err != nil {
		return fmt.Errorf("ensure secure store: %w", err)
	}
	store, err := registry.Open(controllerDBPath(cfg.dataDir))
	if err != nil {
		return fmt.Errorf("open registry: %w", err)
	}
	defer store.Close()

	application := &app{
		logger:   logger,
		registry: store,
		ca:       authority,
		vault:    vault,
	}
	response, err := application.pushTrustedAgentUpdate(context.Background(), nodeID, binaryPath, versionText)
	if err != nil {
		return err
	}
	if logger != nil {
		logger.Printf("ota update pushed node=%s version=%s sha256=%s restart_required=%t", nodeID, response.Version, response.SHA256, response.RestartRequired)
	}
	return nil
}

func (a *app) pushTrustedAgentUpdate(ctx context.Context, nodeID, binaryPath, versionText string) (agentOTAUpdateResponse, error) {
	info, err := os.Stat(binaryPath)
	if err != nil {
		return agentOTAUpdateResponse{}, fmt.Errorf("stat binary payload: %w", err)
	}
	if !info.Mode().IsRegular() {
		return agentOTAUpdateResponse{}, fmt.Errorf("binary payload is not a regular file: %s", binaryPath)
	}
	binaryPayload, err := os.ReadFile(binaryPath)
	if err != nil {
		return agentOTAUpdateResponse{}, fmt.Errorf("read binary payload: %w", err)
	}
	if len(binaryPayload) == 0 {
		return agentOTAUpdateResponse{}, fmt.Errorf("binary payload is empty: %s", binaryPath)
	}
	digest := sha256.Sum256(binaryPayload)
	signature, err := a.ca.SignSHA256Digest(digest[:])
	if err != nil {
		return agentOTAUpdateResponse{}, fmt.Errorf("sign ota payload digest: %w", err)
	}
	request := agentOTAUpdateRequest{
		Version:         versionText,
		BinaryBase64:    base64.StdEncoding.EncodeToString(binaryPayload),
		SHA256:          fmt.Sprintf("%x", digest[:]),
		SignatureBase64: base64.StdEncoding.EncodeToString(signature),
	}
	var response agentOTAUpdateResponse
	if err := a.doTrustedNodeJSON(ctx, nodeID, http.MethodPost, "/api/agent/update", request, &response); err != nil {
		return agentOTAUpdateResponse{}, err
	}
	return response, nil
}

func copyFileAtomic(sourcePath, destinationPath string, mode os.FileMode) (err error) {
	resolvedSource, err := filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	resolvedDestination, err := filepath.Abs(strings.TrimSpace(destinationPath))
	if err != nil {
		return fmt.Errorf("resolve destination path: %w", err)
	}
	if resolvedSource == resolvedDestination {
		return errors.New("source and destination paths must differ")
	}

	source, err := os.Open(resolvedSource)
	if err != nil {
		return err
	}
	defer source.Close()

	destinationDir := filepath.Dir(resolvedDestination)
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return fmt.Errorf("ensure destination directory: %w", err)
	}

	tempFile, err := os.CreateTemp(destinationDir, ".controller-db-*")
	if err != nil {
		return fmt.Errorf("create temporary destination file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err = io.Copy(tempFile, source); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("copy database file: %w", err)
	}
	if err = tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("sync database backup: %w", err)
	}
	if err = tempFile.Chmod(mode); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("chmod database backup: %w", err)
	}
	if err = tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary destination file: %w", err)
	}
	if err = os.Rename(tempPath, resolvedDestination); err != nil {
		return fmt.Errorf("rename temporary destination file: %w", err)
	}
	return nil
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
	mux.HandleFunc("/api/settings/controller", a.handleControllerSettings)
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

func (a *app) handleControllerSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := a.loadControllerSettings(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a.controllerSettingsResponse(settings))
		return
	case http.MethodPatch:
		var request updateControllerSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode controller settings patch: %v", err))
			return
		}
		if request.AggregateNUTEnabled == nil && request.AggregateNUTListen == nil {
			writeJSONError(w, http.StatusBadRequest, "controller settings patch is empty")
			return
		}
		current, err := a.loadControllerSettings(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		next := current
		if request.AggregateNUTEnabled != nil {
			next.AggregateNUTEnabled = *request.AggregateNUTEnabled
		}
		if request.AggregateNUTListen != nil {
			next.AggregateNUTListen = strings.TrimSpace(*request.AggregateNUTListen)
		}
		if strings.TrimSpace(next.AggregateNUTListen) == "" {
			writeJSONError(w, http.StatusBadRequest, "aggregate_nut_listen is required")
			return
		}
		if _, _, splitErr := net.SplitHostPort(normalizeListenAddress(next.AggregateNUTListen)); splitErr != nil {
			writeJSONError(w, http.StatusBadRequest, "aggregate_nut_listen must be host:port or :port")
			return
		}
		if err := a.aggregate.Apply(r.Context(), next.AggregateNUTEnabled, next.AggregateNUTListen); err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		if err := a.registry.SaveControllerSettings(r.Context(), next); err != nil {
			_ = a.aggregate.Apply(r.Context(), current.AggregateNUTEnabled, current.AggregateNUTListen)
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a.controllerSettingsResponse(next))
		return
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *app) loadControllerSettings(ctx context.Context) (registry.ControllerSettings, error) {
	defaults := registry.ControllerSettings{AggregateNUTEnabled: a.config.aggregateNUTEnabled, AggregateNUTListen: a.config.aggregateNUTListen}
	settings, err := a.registry.LoadControllerSettings(ctx, defaults)
	if err != nil {
		return registry.ControllerSettings{}, err
	}
	if strings.TrimSpace(settings.AggregateNUTListen) == "" {
		settings.AggregateNUTListen = defaults.AggregateNUTListen
	}
	if strings.TrimSpace(settings.AggregateNUTListen) == "" {
		settings.AggregateNUTListen = ":3493"
	}
	return settings, nil
}

func (a *app) controllerSettingsResponse(settings registry.ControllerSettings) controllerSettingsResponse {
	_, listen, active := a.aggregate.Status()
	if strings.TrimSpace(listen) == "" {
		listen = settings.AggregateNUTListen
	}
	if strings.TrimSpace(listen) == "" {
		listen = ":3493"
	}
	return controllerSettingsResponse{AggregateNUTEnabled: settings.AggregateNUTEnabled, AggregateNUTListen: listen, AggregateNUTActive: active}
}

func normalizeListenAddress(listen string) string {
	trimmed := strings.TrimSpace(listen)
	if strings.HasPrefix(trimmed, ":") {
		return "0.0.0.0" + trimmed
	}
	return trimmed
}

type aggregateNUTBackend struct {
	app *app
}

func (b *aggregateNUTBackend) List(ctx context.Context) ([]aggregatenut.UPS, error) {
	if b == nil || b.app == nil || b.app.registry == nil {
		return nil, nil
	}
	nodes, err := b.app.registry.ListAdoptedNodes(ctx)
	if err != nil {
		return nil, err
	}
	upses := make([]aggregatenut.UPS, 0)
	for _, node := range nodes {
		summaries, err := b.app.registry.ListNodeUPSSummaries(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		for _, summary := range summaries {
			detail, err := b.app.registry.GetUPSDetail(ctx, node.ID, summary.Name)
			if err != nil {
				if errors.Is(err, registry.ErrUPSNotFound) {
					continue
				}
				return nil, err
			}
			variables := make(map[string]string, len(detail.Variables)+4)
			for key, value := range detail.Variables {
				variables[key] = value
			}
			if strings.TrimSpace(variables["ups.status"]) == "" {
				variables["ups.status"] = summary.Status
			}
			if _, ok := variables["battery.charge"]; !ok && summary.BatteryChargePercent != nil {
				variables["battery.charge"] = strconv.FormatFloat(*summary.BatteryChargePercent, 'f', -1, 64)
			}
			if _, ok := variables["ups.load"]; !ok && summary.LoadPercent != nil {
				variables["ups.load"] = strconv.FormatFloat(*summary.LoadPercent, 'f', -1, 64)
			}
			if _, ok := variables["battery.runtime"]; !ok && summary.RuntimeSeconds != nil {
				variables["battery.runtime"] = strconv.FormatInt(*summary.RuntimeSeconds, 10)
			}
			upses = append(upses, aggregatenut.UPS{
				Name:        aggregateUPSAlias(node.ID, summary.Name),
				Description: fmt.Sprintf("%s on %s", summary.Name, node.ID),
				Variables:   variables,
			})
		}
	}
	sort.Slice(upses, func(i, j int) bool { return upses[i].Name < upses[j].Name })
	return upses, nil
}

func (b *aggregateNUTBackend) RunCommand(ctx context.Context, aggregateUPSName, command string) error {
	if b == nil || b.app == nil || b.app.registry == nil {
		return aggregatenut.ErrUnknownUPS
	}
	target, ok, err := b.resolve(ctx, aggregateUPSName)
	if err != nil {
		return err
	}
	if !ok {
		return aggregatenut.ErrUnknownUPS
	}
	_, err = b.app.runTrustedUPSCommand(ctx, target.nodeID, target.upsName, command)
	if errors.Is(err, registry.ErrUPSNotFound) {
		return aggregatenut.ErrUnknownUPS
	}
	return err
}

func (b *aggregateNUTBackend) ListCommands(ctx context.Context, aggregateUPSName string) ([]aggregatenut.Command, error) {
	if b == nil || b.app == nil || b.app.registry == nil {
		return nil, aggregatenut.ErrUnknownUPS
	}
	target, ok, err := b.resolve(ctx, aggregateUPSName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, aggregatenut.ErrUnknownUPS
	}
	detail, err := b.app.fetchTrustedUPSDetail(ctx, target.nodeID, target.upsName)
	if err != nil {
		if errors.Is(err, registry.ErrUPSNotFound) {
			return nil, aggregatenut.ErrUnknownUPS
		}
		return nil, err
	}
	commands := make([]aggregatenut.Command, 0, len(detail.Commands))
	for _, command := range detail.Commands {
		trimmedName := strings.TrimSpace(command.Name)
		if trimmedName == "" {
			continue
		}
		commands = append(commands, aggregatenut.Command{Name: trimmedName, Description: strings.TrimSpace(command.Description)})
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commands, nil
}

type aggregateUPSTarget struct {
	nodeID  string
	upsName string
}

func (b *aggregateNUTBackend) resolve(ctx context.Context, aggregateUPSName string) (aggregateUPSTarget, bool, error) {
	nodes, err := b.app.registry.ListAdoptedNodes(ctx)
	if err != nil {
		return aggregateUPSTarget{}, false, err
	}
	for _, node := range nodes {
		summaries, err := b.app.registry.ListNodeUPSSummaries(ctx, node.ID)
		if err != nil {
			return aggregateUPSTarget{}, false, err
		}
		for _, summary := range summaries {
			if aggregateUPSAlias(node.ID, summary.Name) == aggregateUPSName {
				return aggregateUPSTarget{nodeID: node.ID, upsName: summary.Name}, true, nil
			}
		}
	}
	return aggregateUPSTarget{}, false, nil
}

func aggregateUPSAlias(nodeID, upsName string) string {
	return aggregateSlug(nodeID) + "__" + aggregateSlug(upsName)
}

func aggregateSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "_", "/", "_", ".", "_", ":", "_", "-", "_")
	value = replacer.Replace(value)
	value = strings.Trim(value, "_")
	if value == "" {
		return "item"
	}
	return value
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
		trend, trendErr := a.buildBatteryRuntimeTrend(r.Context(), nodeID, upsName)
		if trendErr != nil {
			if !errors.Is(trendErr, registry.ErrUPSNotFound) && a.logger != nil {
				a.logger.Printf("battery trend unavailable node=%s ups=%s: %v", nodeID, upsName, trendErr)
			}
		} else {
			response.BatteryRuntimeTrend = trend
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
	if patch.DisplayName == nil && patch.LocationLabel == nil && patch.SiteLabel == nil && request.LocalUIManaged == nil && request.LocalUIEnabled == nil {
		writeJSONError(w, http.StatusBadRequest, "at least one metadata field is required")
		return
	}
	current, err := a.registry.GetNode(r.Context(), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, registry.ErrNodeNotFound) {
			status = http.StatusNotFound
		}
		writeJSONError(w, status, err.Error())
		return
	}
	if patch.DisplayName != nil || patch.LocationLabel != nil || patch.SiteLabel != nil {
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
	}
	if request.LocalUIEnabled != nil && request.LocalUIManaged == nil {
		writeJSONError(w, http.StatusBadRequest, "local_ui_policy_managed is required when updating local_ui_policy_enabled")
		return
	}
	if request.LocalUIManaged != nil {
		if !current.Adopted {
			writeJSONError(w, http.StatusBadRequest, "node local UI policy can only be managed after adoption")
			return
		}
		managed := *request.LocalUIManaged
		enabled := current.LocalUIEnabled
		if request.LocalUIEnabled != nil {
			enabled = *request.LocalUIEnabled
		} else if managed && !current.LocalUIManaged {
			writeJSONError(w, http.StatusBadRequest, "local_ui_policy_enabled is required when enabling controller-managed policy")
			return
		}
		if err := a.registry.UpdateNodeLocalUIPolicy(r.Context(), id, managed, enabled); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, registry.ErrNodeNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		liveNode, err := a.buildNodeResponse(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, registry.ErrNodeNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		if liveNode.Live {
			if err := a.applyTrustedNodeLocalUIPolicy(r.Context(), id, managed, enabled); err != nil {
				_ = a.registry.UpdateNodeLocalUIPolicy(r.Context(), id, current.LocalUIManaged, current.LocalUIEnabled)
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
		}
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
	existingNodes, err := a.registry.ListNodes(ctx)
	if err != nil {
		return err
	}
	existingAdoptedByID := make(map[string]bool, len(existingNodes))
	for _, node := range existingNodes {
		existingAdoptedByID[node.ID] = node.Adopted
	}

	for _, live := range a.browser.Snapshot() {
		adopted := live.Adopted
		if existingAdoptedByID[live.ID] {
			adopted = true
		}
		if err := a.registry.UpsertDiscoveredNode(ctx, registry.Node{
			ID:       live.ID,
			Instance: live.Instance,
			Hostname: live.Hostname,
			Address:  live.Address,
			Port:     live.Port,
			Version:  live.Version,
			UPSCount: live.UPSCount,
			Adopted:  adopted,
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
		ID:             node.ID,
		Instance:       node.Instance,
		Hostname:       node.Hostname,
		Address:        node.Address,
		Port:           node.Port,
		Version:        node.Version,
		UPSCount:       node.UPSCount,
		DisplayName:    node.DisplayName,
		LocationLabel:  node.LocationLabel,
		SiteLabel:      node.SiteLabel,
		LocalUIManaged: node.LocalUIManaged,
		LocalUIEnabled: node.LocalUIEnabled,
		Adopted:        node.Adopted,
		Live:           isLive,
		Status:         status,
		CommsState:     node.CommsState,
		PollFailures:   node.PollFailures,
		LastPolledAt:   node.LastPolledAt,
		LastPollError:  node.LastPollError,
		UPSSummaries:   toUPSSummaryResponses(summaries),
		LastSeen:       node.LastSeen,
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
			storedDetail, detailErr := a.registry.GetUPSDetail(ctx, node.ID, summary.Name)
			if detailErr != nil && !errors.Is(detailErr, registry.ErrUPSNotFound) {
				return nil, detailErr
			}
			liveDetail, liveErr := a.fetchTrustedUPSDetail(ctx, node.ID, summary.Name)
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
			if liveErr == nil {
				if strings.TrimSpace(liveDetail.Status) != "" {
					upsInfo.Status = liveDetail.Status
					upsInfo.OnBattery = strings.Contains(strings.ToUpper(liveDetail.Status), "OB")
				}
				if liveDetail.Metrics.BatteryChargePercent != nil {
					upsInfo.BatteryCharge = liveDetail.Metrics.BatteryChargePercent
					upsInfo.LowBattery = *liveDetail.Metrics.BatteryChargePercent <= 20
				}
				if liveDetail.Metrics.LoadPercent != nil {
					upsInfo.LoadPercent = liveDetail.Metrics.LoadPercent
				}
				if liveDetail.Metrics.RuntimeSeconds != nil {
					upsInfo.Runtime = liveDetail.Metrics.RuntimeSeconds
				}
				if inputVoltage := parseFloatVariable(liveDetail.Variables["input.voltage"]); inputVoltage != nil {
					upsInfo.InputVoltage = inputVoltage
				}
				for _, command := range liveDetail.Commands {
					trimmedName := strings.TrimSpace(command.Name)
					if trimmedName == "" {
						continue
					}
					upsInfo.Commands = append(upsInfo.Commands, controllermqtt.CommandInfo{Name: trimmedName, Description: strings.TrimSpace(command.Description)})
				}
			} else if detailErr == nil {
				if inputVoltage := parseFloatVariable(storedDetail.Variables["input.voltage"]); inputVoltage != nil {
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

type batteryTrendSnapshot struct {
	CapturedAt     time.Time
	RuntimeSeconds *int64
	ChargePercent  *float64
	Status         string
}

type batteryTrendPoint struct {
	CapturedAt     time.Time
	RuntimeSeconds int64
}

func (a *app) buildBatteryRuntimeTrend(ctx context.Context, nodeID, upsName string) (*batteryTrendResponse, error) {
	history, err := a.registry.ListUPSHistoryFiltered(ctx, nodeID, upsName, "", time.Now().UTC().Add(-45*24*time.Hour), 5000)
	if err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return nil, nil
	}

	snapshotsByUnix := make(map[int64]*batteryTrendSnapshot)
	for _, sample := range history {
		unixTS := sample.CapturedAt.UTC().Unix()
		snapshot, ok := snapshotsByUnix[unixTS]
		if !ok {
			snapshot = &batteryTrendSnapshot{CapturedAt: sample.CapturedAt.UTC()}
			snapshotsByUnix[unixTS] = snapshot
		}
		switch strings.TrimSpace(sample.Variable) {
		case "battery.runtime":
			parsed, parseErr := strconv.ParseInt(strings.TrimSpace(sample.Value), 10, 64)
			if parseErr == nil && parsed > 0 {
				snapshot.RuntimeSeconds = &parsed
			}
		case "battery.charge":
			parsed, parseErr := strconv.ParseFloat(strings.TrimSpace(sample.Value), 64)
			if parseErr == nil && parsed >= 0 && parsed <= 100 {
				snapshot.ChargePercent = &parsed
			}
		case "ups.status":
			snapshot.Status = strings.ToUpper(strings.TrimSpace(sample.Value))
		}
	}

	points := make([]batteryTrendPoint, 0, len(snapshotsByUnix))
	for _, snapshot := range snapshotsByUnix {
		if snapshot.RuntimeSeconds == nil || snapshot.ChargePercent == nil {
			continue
		}
		if *snapshot.ChargePercent < 85 {
			continue
		}
		if strings.Contains(snapshot.Status, "OB") || strings.Contains(snapshot.Status, "DISCHRG") {
			continue
		}
		points = append(points, batteryTrendPoint{CapturedAt: snapshot.CapturedAt, RuntimeSeconds: *snapshot.RuntimeSeconds})
	}
	if len(points) < 3 {
		return nil, nil
	}

	sort.Slice(points, func(i, j int) bool {
		return points[i].CapturedAt.Before(points[j].CapturedAt)
	})

	baseline := points[0].RuntimeSeconds
	for _, point := range points {
		if point.RuntimeSeconds > baseline {
			baseline = point.RuntimeSeconds
		}
	}
	latest := points[len(points)-1]

	var sumX float64
	var sumY float64
	var sumXY float64
	var sumXX float64
	for _, point := range points {
		x := point.CapturedAt.Sub(points[0].CapturedAt).Hours() / 24
		y := float64(point.RuntimeSeconds)
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	n := float64(len(points))
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return nil, nil
	}
	slopeSecondsPerDay := (n*sumXY - sumX*sumY) / denominator
	trendSecondsPer30Days := slopeSecondsPerDay * 30

	replaceThreshold := int64(float64(baseline) * 0.70)
	response := &batteryTrendResponse{
		BaselineRuntimeSeconds:  baseline,
		LatestRuntimeSeconds:    latest.RuntimeSeconds,
		ReplacementThresholdSec: replaceThreshold,
		TrendSecondsPer30Days:   trendSecondsPer30Days,
		SamplesUsed:             len(points),
		LatestSampledAt:         latest.CapturedAt,
	}
	if latest.RuntimeSeconds <= replaceThreshold {
		response.EstimatedReplaceBy = latest.CapturedAt
		return response, nil
	}
	if slopeSecondsPerDay >= -0.01 {
		return response, nil
	}
	daysUntilThreshold := (float64(replaceThreshold) - float64(latest.RuntimeSeconds)) / slopeSecondsPerDay
	if daysUntilThreshold <= 0 || daysUntilThreshold > 3650 {
		return response, nil
	}
	response.EstimatedReplaceBy = latest.CapturedAt.Add(time.Duration(daysUntilThreshold * 24 * float64(time.Hour)))
	return response, nil
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

func (a *app) applyTrustedNodeLocalUIPolicy(ctx context.Context, nodeID string, managed, enabled bool) error {
	return a.doTrustedNodeJSON(ctx, nodeID, http.MethodPost, "/api/settings/ui/policy", nodeLocalUIPolicyRequest{Managed: managed, Enabled: enabled}, nil)
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
		if strings.HasPrefix(path, "/api/ups/") {
			return registry.ErrUPSNotFound
		}
		return fmt.Errorf("trusted node request rejected with status %d", resp.StatusCode)
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
	var resp *http.Response
	for attempt := 1; attempt <= adoptRequestMaxAttempts; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if reqErr != nil {
			return adoptNodeResponse{}, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err = a.client.Do(req)
		if err != nil {
			if attempt < adoptRequestMaxAttempts && shouldRetryAdoptTransportError(ctx, err) {
				if waitErr := waitForRetry(ctx, time.Duration(attempt)*adoptRetryBackoff); waitErr != nil {
					return adoptNodeResponse{}, fmt.Errorf("call node adopt endpoint: %w", err)
				}
				continue
			}
			return adoptNodeResponse{}, fmt.Errorf("call node adopt endpoint: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			break
		}

		message := decodeNodeError(resp)
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusConflict:
			if message == "" {
				message = errNodeAlreadyAdopted.Error()
			}
			return adoptNodeResponse{}, fmt.Errorf("%w: %s", errNodeAlreadyAdopted, message)
		default:
			if attempt < adoptRequestMaxAttempts && shouldRetryAdoptStatus(resp.StatusCode) {
				if waitErr := waitForRetry(ctx, time.Duration(attempt)*adoptRetryBackoff); waitErr != nil {
					if message != "" {
						return adoptNodeResponse{}, fmt.Errorf("node adopt rejected: %s", message)
					}
					return adoptNodeResponse{}, fmt.Errorf("node adopt rejected with status %d", resp.StatusCode)
				}
				continue
			}
			if message != "" {
				return adoptNodeResponse{}, fmt.Errorf("node adopt rejected: %s", message)
			}
			return adoptNodeResponse{}, fmt.Errorf("node adopt rejected with status %d", resp.StatusCode)
		}
	}
	defer resp.Body.Close()
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

func shouldRetryAdoptTransportError(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return true
}

func shouldRetryAdoptStatus(status int) bool {
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
