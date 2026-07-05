package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Foehammer82/wattkeeper/agent/internal/api"
	"github.com/Foehammer82/wattkeeper/agent/internal/discovery"
	"github.com/Foehammer82/wattkeeper/agent/internal/hotplug"
	"github.com/Foehammer82/wattkeeper/agent/internal/nutconf"
	"github.com/Foehammer82/wattkeeper/agent/internal/services"
	"github.com/Foehammer82/wattkeeper/agent/internal/sim"
	"github.com/Foehammer82/wattkeeper/agent/nodeapi"
	"gopkg.in/yaml.v3"
)

const (
	defaultAgentConfigPath = "/etc/wattkeeper/agent.yaml"
	defaultNamesPath       = "/var/lib/wattkeeper/names.json"
	defaultAdoptionPath    = "/var/lib/wattkeeper/adoption.json"
	defaultTLSCertPath     = "/var/lib/wattkeeper/node-api.crt"
	defaultTLSKeyPath      = "/var/lib/wattkeeper/node-api.key"
	factoryResetMarkerPath = "/boot/firmware/wattkeeper-factory-reset"
	factoryResetLegacyPath = "/boot/wattkeeper-factory-reset"
)

var version = "dev"

type config struct {
	configDir string
	listen    string
	tlsListen string
	logLevel  string
	devUI     bool
	demoMode  bool
	simulate  string
	nodeID    string
	httpAuth  bool
	authPath  string
}

type hotplugWatcher interface {
	Events(context.Context) (<-chan hotplug.Event, error)
}

type scanner interface {
	Scan(context.Context) ([]nutconf.DetectedUPS, error)
}

type reloader interface {
	Reload(context.Context, bool, []string) error
}

type inventoryUpdater interface {
	UpdateInventory([]nutconf.DetectedUPS)
}

type upsCountUpdater interface {
	UpdateUPSCount(int)
}

type agentRuntime struct {
	watcher         hotplugWatcher
	scanner         scanner
	reloader        reloader
	inventory       inventoryUpdater
	upsCount        upsCountUpdater
	adopted         nodeapi.AdoptedUpdater
	logger          *log.Logger
	configDir       string
	agentConfigPath string
	namesPath       string
	adoptionPath    string
}

type adoptionState struct {
	CAPEM          string    `json:"ca_pem"`
	NUTUser        string    `json:"nut_user"`
	NUTPassword    string    `json:"nut_password"`
	TokenSHA256    string    `json:"token_sha256"`
	ControllerURL  string    `json:"controller_url"`
	TLSPort        int       `json:"tls_port"`
	TLSFingerprint string    `json:"tls_fingerprint"`
	AdoptedAt      time.Time `json:"adopted_at"`
}

type appliedConfig struct {
	devices        []nutconf.DetectedUPS
	changed        bool
	restartUPSName []string
	user           nutconf.UPSDUser
}

type fileAgentConfig struct {
	NUT struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"nut"`
}

func main() {
	logger := log.New(os.Stdout, "wattkeeper-agent: ", log.LstdFlags)

	if len(os.Args) > 1 && os.Args[1] == "reset" {
		if err := runResetCommand(logger, os.Args[2:]); err != nil {
			logger.Printf("fatal error: %v", err)
			os.Exit(1)
		}
		logger.Print("reset complete")
		return
	}

	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		logger.Printf("fatal error: %v", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Printf("starting config_dir=%s listen=%s log_level=%s dev_ui=%t http_auth=%t", cfg.configDir, cfg.listen, cfg.logLevel, cfg.devUI, cfg.httpAuth)

	if err := run(ctx, logger, cfg); err != nil {
		logger.Printf("fatal error: %v", err)
		os.Exit(1)
	}

	logger.Print("shutdown complete")
}

func parseFlags(args []string) (config, error) {
	var cfg config
	flags := flag.NewFlagSet("wattkeeper-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	flags.StringVar(&cfg.configDir, "config-dir", "/etc/nut", "directory containing NUT configuration")
	flags.StringVar(&cfg.listen, "listen", ":80", "agent listen address")
	flags.StringVar(&cfg.tlsListen, "tls-listen", ":8443", "controller API TLS listen address")
	flags.StringVar(&cfg.logLevel, "log-level", "info", "log verbosity level")
	flags.BoolVar(&cfg.devUI, "dev-ui", false, "serve the node UI and API with sample data only")
	flags.BoolVar(&cfg.demoMode, "demo-mode", false, "run deterministic simulation helpers intended for demo and evaluation workflows")
	flags.StringVar(&cfg.simulate, "simulate", "", "directory containing simulated *.dev fixtures; bypasses nut-scanner and hotplug netlink")
	flags.StringVar(&cfg.nodeID, "node-id", "", "optional node identity override for container and simulation replicas")
	flags.BoolVar(&cfg.httpAuth, "http-auth", true, "require bootstrap and Basic Auth for the node dashboard and detailed status routes")
	flags.StringVar(&cfg.authPath, "http-auth-file", "/var/lib/wattkeeper/webui-auth.json", "path to the node web auth file")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}

	return cfg, nil
}

type resetConfig struct {
	adoptionPath string
	tlsCertPath  string
	tlsKeyPath   string
}

func runResetCommand(logger *log.Logger, args []string) error {
	cfg, err := parseResetFlags(args)
	if err != nil {
		return err
	}
	if err := resetNodeState(cfg.adoptionPath, cfg.tlsCertPath, cfg.tlsKeyPath); err != nil {
		return err
	}
	if logger != nil {
		logger.Printf("cleared adoption state path=%s", cfg.adoptionPath)
	}
	return nil
}

func parseResetFlags(args []string) (resetConfig, error) {
	var cfg resetConfig
	flags := flag.NewFlagSet("wattkeeper-agent reset", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.adoptionPath, "adoption-file", defaultAdoptionPath, "path to the node adoption state file")
	flags.StringVar(&cfg.tlsCertPath, "tls-cert-file", defaultTLSCertPath, "path to the controller API TLS certificate")
	flags.StringVar(&cfg.tlsKeyPath, "tls-key-file", defaultTLSKeyPath, "path to the controller API TLS private key")
	if err := flags.Parse(args); err != nil {
		return resetConfig{}, err
	}
	return cfg, nil
}

func resetNodeState(paths ...string) error {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}

func run(ctx context.Context, logger *log.Logger, cfg config) error {
	if cfg.devUI {
		return runDevUI(ctx, logger, cfg)
	}

	if _, err := applyFactoryResetIfRequested(logger, []string{factoryResetMarkerPath, factoryResetLegacyPath}, factoryResetStatePaths(cfg.authPath)); err != nil {
		return fmt.Errorf("apply factory reset: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	override := strings.TrimSpace(cfg.nodeID)
	var identity discovery.Identity
	var err error
	if override != "" {
		identity = discovery.IdentityForSerial(override)
	} else {
		identity, err = discovery.ResolveIdentity()
		if err != nil {
			return fmt.Errorf("resolve node identity: %w", err)
		}
	}

	listener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.listen, err)
	}

	adopter := &nodeapi.RuntimeAdopter{
		Logger:       logger,
		ConfigDir:    cfg.configDir,
		AdoptionPath: defaultAdoptionPath,
		Version:      version,
		Serial:       identity.Serial,
		TLSCertPath:  defaultTLSCertPath,
		TLSKeyPath:   defaultTLSKeyPath,
	}
	tlsPort := 0
	if cfg.tlsListen != "" {
		if _, portText, err := net.SplitHostPort(cfg.tlsListen); err == nil {
			parsedPort, parseErr := strconv.Atoi(portText)
			if parseErr == nil {
				tlsPort = parsedPort
			}
		}
	}
	adopter.TLSPort = tlsPort

	healthAPI := api.New(logger, api.Options{
		Version:      version,
		Serial:       identity.Serial,
		StartedAt:    time.Now(),
		AdoptionPath: defaultAdoptionPath,
		DisableAuth:  !cfg.httpAuth,
		AuthPath:     cfg.authPath,
		Adopter:      adopter,
	})
	httpServer := &http.Server{Handler: healthAPI.Handler()}
	httpErr := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr <- err
		}
	}()

	tlsErr := make(chan error, 1)
	var tlsServer *http.Server
	if cfg.tlsListen != "" {
		tlsListener, err := net.Listen("tcp", cfg.tlsListen)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.tlsListen, err)
		}
		tlsServer = &http.Server{Handler: healthAPI.Handler(), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: nodeapi.DynamicCertificateLoader(defaultTLSCertPath, defaultTLSKeyPath)}}
		go func() {
			if err := tlsServer.ServeTLS(tlsListener, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				tlsErr <- err
			}
		}()
	}

	advertiser := discovery.NewAdvertiser(logger, discovery.Metadata{
		Serial:   identity.Serial,
		Instance: identity.Instance,
		Version:  version,
		Port:     listener.Addr().(*net.TCPAddr).Port,
	})
	if err := advertiser.Start(); err != nil {
		_ = listener.Close()
		return err
	}
	defer advertiser.Close()

	runtime := newAgentRuntime(cfg, logger)
	runtime.inventory = healthAPI
	runtime.upsCount = advertiser
	runtime.adopted = advertiser
	adopter.Advertiser = advertiser
	adopter.Inventory = healthAPI
	adopter.Reloader = runtime.reloader

	logger.Printf("node identity serial=%s instance=%s", identity.Serial, identity.Instance)

	runtimeErr := make(chan error, 1)
	go func() {
		runtimeErr <- runtime.run(runCtx)
	}()

	var result error
	select {
	case err := <-runtimeErr:
		result = err
	case err := <-httpErr:
		cancel()
		result = fmt.Errorf("serve http: %w", err)
		<-runtimeErr
	case err := <-tlsErr:
		cancel()
		result = fmt.Errorf("serve https: %w", err)
		<-runtimeErr
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		if result == nil {
			result = fmt.Errorf("shutdown http server: %w", err)
		} else {
			logger.Printf("http shutdown failed: %v", err)
		}
	}
	if tlsServer != nil {
		if err := tlsServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if result == nil {
				result = fmt.Errorf("shutdown https server: %w", err)
			} else {
				logger.Printf("https shutdown failed: %v", err)
			}
		}
	}

	return result
}

func factoryResetStatePaths(authPath string) []string {
	paths := []string{defaultAdoptionPath, defaultTLSCertPath, defaultTLSKeyPath, defaultNamesPath}
	if strings.TrimSpace(authPath) != "" {
		paths = append(paths, authPath)
	}
	return paths
}

func applyFactoryResetIfRequested(logger *log.Logger, markerPaths []string, statePaths []string) (bool, error) {
	markerPath, found, err := firstExistingRegularFile(markerPaths)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	if err := resetNodeState(statePaths...); err != nil {
		return false, err
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		if logger != nil {
			logger.Printf("warning: factory reset marker cleanup failed path=%s err=%v", markerPath, err)
		}
	}
	if logger != nil {
		logger.Printf("factory reset applied marker=%s", markerPath)
	}

	return true, nil
}

func firstExistingRegularFile(paths []string) (string, bool, error) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("stat %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		return path, true, nil
	}

	return "", false, nil
}

type sampleRunner struct {
	variables map[string]map[string]string
	commands  map[string][]string
	writable  map[string][]string
}

func (s sampleRunner) CombinedOutput(_ context.Context, path string, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("missing UPS name")
	}
	switch path {
	case "upsc":
		if args[0] == "-j" {
			if len(args) < 2 {
				return nil, errors.New("missing UPS name")
			}
			variables, ok := s.variables[args[1]]
			if !ok {
				return nil, fmt.Errorf("unknown UPS %q", args[1])
			}
			payload, err := json.Marshal(variables)
			if err != nil {
				return nil, err
			}
			return payload, nil
		}
		variables, ok := s.variables[args[0]]
		if !ok {
			return nil, fmt.Errorf("unknown UPS %q", args[0])
		}
		var builder strings.Builder
		keys := make([]string, 0, len(variables))
		for key := range variables {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(variables[key])
			builder.WriteString("\n")
		}
		return []byte(builder.String()), nil
	case "upscmd":
		if args[0] == "-l" {
			if len(args) < 2 {
				return nil, errors.New("missing UPS name")
			}
			commands, ok := s.commands[args[1]]
			if !ok {
				return nil, fmt.Errorf("unknown UPS %q", args[1])
			}
			return []byte(strings.Join(commands, "\n") + "\n"), nil
		}
		if len(args) < 7 {
			return nil, errors.New("missing command arguments")
		}
		upsName := args[5]
		command := args[6]
		return []byte("OK: executed " + command + " on " + upsName + "\n"), nil
	case "upsrw":
		if args[0] == "-l" {
			if len(args) < 2 {
				return nil, errors.New("missing UPS name")
			}
			writable, ok := s.writable[args[1]]
			if !ok {
				return nil, fmt.Errorf("unknown UPS %q", args[1])
			}
			return []byte(strings.Join(writable, "\n") + "\n"), nil
		}
		if len(args) < 8 {
			return nil, errors.New("missing writable variable arguments")
		}
		assignment := args[1]
		parts := strings.SplitN(assignment, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid assignment %q", assignment)
		}
		upsName := args[7]
		variables, ok := s.variables[upsName]
		if !ok {
			return nil, fmt.Errorf("unknown UPS %q", upsName)
		}
		variables[parts[0]] = parts[1]
		return []byte("OK: set " + parts[0] + " to " + parts[1] + " on " + upsName + "\n"), nil
	default:
		return nil, fmt.Errorf("unexpected path %q", path)
	}
}

func runDevUI(ctx context.Context, logger *log.Logger, cfg config) error {
	listener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.listen, err)
	}

	devices := []nutconf.DetectedUPS{
		{Name: "ups-lab-a", Driver: "usbhid-ups", Vendor: "APC", Product: "Back-UPS Pro 1500"},
		{Name: "ups-lab-b", Driver: "blazer_usb", Vendor: "CyberPower", Product: "CP1500AVRLCD3"},
	}
	service := api.New(logger, api.Options{
		Version:     version,
		Serial:      "dev-node-0000",
		StartedAt:   time.Now(),
		NUTUser:     "agent",
		NUTPassword: "dev-secret",
		Runner: sampleRunner{
			variables: map[string]map[string]string{
				"ups-lab-a": {
					"ups.status":          "OL",
					"battery.charge":      "100",
					"battery.runtime":     "3420",
					"battery.voltage":     "27.2",
					"input.voltage":       "120.4",
					"output.voltage":      "120.1",
					"ups.load":            "31",
					"device.model":        "Back-UPS Pro 1500",
					"device.mfr":          "APC",
					"battery.test.status": "Idle",
				},
				"ups-lab-b": {
					"ups.status":      "OB DISCHRG",
					"battery.charge":  "74",
					"battery.runtime": "1180",
					"battery.voltage": "25.6",
					"input.voltage":   "0.0",
					"output.voltage":  "118.7",
					"ups.load":        "48",
					"device.model":    "CP1500AVRLCD3",
					"device.mfr":      "CyberPower",
				},
			},
			commands: map[string][]string{
				"ups-lab-a": {
					"beeper.toggle - Toggle the audible alarm",
					"test.battery.start.quick - Start a quick battery self-test",
					"shutdown.return - Cut load and restore when utility power returns",
				},
				"ups-lab-b": {
					"test.panel.start - Flash panel indicators",
					"load.off - Turn the UPS load off",
				},
			},
			writable: map[string][]string{
				"ups-lab-a": {
					"input.transfer.high: High transfer voltage",
					"Type: RANGE",
					"Range: 127..144",
					"Value: 136",
					"",
					"ups.delay.shutdown: Shutdown delay seconds",
					"Type: RANGE",
					"Range: 20..600",
					"Value: 120",
				},
				"ups-lab-b": {
					"ups.beeper.status: Audible alarm setting",
					"Type: ENUM",
					"Option: enabled",
					"Option: disabled",
					"Value: enabled",
				},
			},
		},
		DisableAuth: !cfg.httpAuth,
		AuthPath:    cfg.authPath,
	})
	service.UpdateInventory(devices)

	httpServer := &http.Server{Handler: service.Handler()}
	httpErr := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr <- err
		}
	}()

	if logger != nil {
		logger.Printf("dev UI mode serving on http://%s", listener.Addr().String())
	}

	var result error
	select {
	case <-ctx.Done():
		result = nil
	case err := <-httpErr:
		result = fmt.Errorf("serve http: %w", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		if result == nil {
			result = fmt.Errorf("shutdown http server: %w", err)
		} else if logger != nil {
			logger.Printf("http shutdown failed: %v", err)
		}
	}

	return result
}

func newAgentRuntime(cfg config, logger *log.Logger) *agentRuntime {
	var watcher hotplugWatcher = hotplug.NewWatcher(logger, hotplug.Options{Debounce: 3 * time.Second})
	scanner := scanner(nutconf.NewScanner(logger))
	reloader := reloader(services.NewManager(logger))
	if cfg.simulate != "" {
		watcher = sim.NewWatcher(logger, sim.Options{Dir: cfg.simulate, Debounce: 3 * time.Second})
		scanner = sim.NewScanner(logger, cfg.simulate, cfg.demoMode)
		reloader = services.NewLocalNUTManager(logger, services.LocalNUTOptions{ConfigDir: cfg.configDir})
	}

	return &agentRuntime{
		watcher:         watcher,
		scanner:         scanner,
		reloader:        reloader,
		logger:          logger,
		configDir:       cfg.configDir,
		agentConfigPath: defaultAgentConfigPath,
		namesPath:       defaultNamesPath,
		adoptionPath:    defaultAdoptionPath,
	}
}

func (r *agentRuntime) run(ctx context.Context) error {
	events, err := r.watcher.Events(ctx)
	if err != nil {
		return err
	}

	var previous []nutconf.DetectedUPS

	r.logger.Print("run loop started")

	for {
		select {
		case <-ctx.Done():
			r.logger.Printf("received shutdown signal: %v", ctx.Err())
			return nil
		case event, ok := <-events:
			if !ok {
				return errors.New("hotplug watcher stopped")
			}

			current, err := r.scanner.Scan(ctx)
			if err != nil {
				r.logger.Printf("scan failed synthetic=%t: %v", event.Synthetic, err)
				continue
			}

			applied, err := r.apply(current)
			if err != nil {
				r.logger.Printf("config apply failed synthetic=%t: %v", event.Synthetic, err)
				continue
			}

			logScanDiff(r.logger, previous, applied.devices, event)
			if r.inventory != nil {
				r.inventory.UpdateInventory(applied.devices)
				if credentialsUpdater, ok := r.inventory.(nodeapi.InventoryCredentialsUpdater); ok {
					credentialsUpdater.UpdateNUTCredentials(applied.user.Username, applied.user.Password)
				}
			}
			if r.upsCount != nil {
				r.upsCount.UpdateUPSCount(len(applied.devices))
			}
			if err := r.reloader.Reload(ctx, applied.changed, applied.restartUPSName); err != nil {
				r.logger.Printf("service reload failed synthetic=%t: %v", event.Synthetic, err)
			}
			previous = applied.devices
		}
	}
}

func (r *agentRuntime) apply(devices []nutconf.DetectedUPS) (appliedConfig, error) {
	user, err := loadAgentUser(r.agentConfigPath)
	if err != nil {
		return appliedConfig{}, err
	}

	persistedNames, err := nutconf.LoadNameMap(r.namesPath)
	if err != nil {
		return appliedConfig{}, err
	}

	namedDevices, nextMap := nutconf.AssignStableNames(devices, persistedNames)

	changed := false
	if namesChanged, err := nutconf.SaveNameMap(r.namesPath, nextMap); err != nil {
		return appliedConfig{}, err
	} else if namesChanged {
		changed = true
	}

	upsChanged, err := nutconf.WriteIfChanged(filepath.Join(r.configDir, "ups.conf"), nutconf.RenderUPSConf(namedDevices))
	if err != nil {
		return appliedConfig{}, fmt.Errorf("write ups.conf: %w", err)
	}
	changed = changed || upsChanged

	for _, file := range []struct {
		name    string
		content string
	}{
		{name: "nut.conf", content: nutconf.RenderNutConf()},
		{name: "upsd.conf", content: nutconf.RenderUPSDConf()},
		{name: "upsd.users", content: nutconf.RenderUPSDUsers(user)},
	} {
		fileChanged, err := nutconf.WriteIfChanged(filepath.Join(r.configDir, file.name), file.content)
		if err != nil {
			return appliedConfig{}, fmt.Errorf("write %s: %w", file.name, err)
		}
		changed = changed || fileChanged
	}

	return appliedConfig{
		devices:        namedDevices,
		changed:        changed,
		restartUPSName: restartUnitsForUPSChange(upsChanged, namedDevices),
		user:           user,
	}, nil
}

func loadAgentUser(path string) (nutconf.UPSDUser, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nutconf.UPSDUser{}, fmt.Errorf("read agent config: %w", err)
	}

	var cfg fileAgentConfig
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nutconf.UPSDUser{}, fmt.Errorf("decode agent config: %w", err)
	}
	if cfg.NUT.Username == "" || cfg.NUT.Password == "" {
		return nutconf.UPSDUser{}, errors.New("agent config missing nut.username or nut.password")
	}

	return nutconf.UPSDUser{Username: cfg.NUT.Username, Password: cfg.NUT.Password}, nil
}

func restartUnitsForUPSChange(upsChanged bool, devices []nutconf.DetectedUPS) []string {
	if !upsChanged {
		return nil
	}

	seen := make(map[string]struct{}, len(devices))
	names := make([]string, 0, len(devices))
	for _, device := range devices {
		if device.Name == "" {
			continue
		}
		if _, exists := seen[device.Name]; exists {
			continue
		}
		seen[device.Name] = struct{}{}
		names = append(names, device.Name)
	}
	sort.Strings(names)
	return names
}

func logScanDiff(logger *log.Logger, previous, current []nutconf.DetectedUPS, event hotplug.Event) {
	added, removed := diffUPS(previous, current)
	if len(added) == 0 && len(removed) == 0 {
		logger.Printf("scan complete synthetic=%t ups_count=%d no inventory changes", event.Synthetic, len(current))
		return
	}

	if len(added) > 0 {
		logger.Printf("scan complete synthetic=%t ups_count=%d added=%s", event.Synthetic, len(current), strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		logger.Printf("scan complete synthetic=%t ups_count=%d removed=%s", event.Synthetic, len(current), strings.Join(removed, ", "))
	}
}

func diffUPS(previous, current []nutconf.DetectedUPS) ([]string, []string) {
	previousByKey := make(map[string]nutconf.DetectedUPS, len(previous))
	currentByKey := make(map[string]nutconf.DetectedUPS, len(current))

	for _, device := range previous {
		previousByKey[device.StableKey()] = device
	}
	for _, device := range current {
		currentByKey[device.StableKey()] = device
	}

	added := make([]string, 0)
	removed := make([]string, 0)

	for key, device := range currentByKey {
		if _, ok := previousByKey[key]; ok {
			continue
		}
		added = append(added, formatUPS(device))
	}

	for key, device := range previousByKey {
		if _, ok := currentByKey[key]; ok {
			continue
		}
		removed = append(removed, formatUPS(device))
	}

	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func formatUPS(device nutconf.DetectedUPS) string {
	if device.Name != "" {
		return device.Name + "(" + device.Driver + "," + device.Port + ")"
	}

	identity := device.Serial
	if identity == "" {
		identity = strings.TrimPrefix(device.StableKey(), "fallback:")
	}
	return identity + "(" + device.Driver + "," + device.Port + ")"
}

func newTestLogger(output io.Writer) *log.Logger {
	return log.New(output, "", 0)
}
