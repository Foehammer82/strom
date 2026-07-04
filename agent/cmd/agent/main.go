package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
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
	"gopkg.in/yaml.v3"
)

const (
	defaultAgentConfigPath = "/etc/wattkeeper/agent.yaml"
	defaultNamesPath       = "/var/lib/wattkeeper/names.json"
	defaultAdoptionPath    = "/var/lib/wattkeeper/adoption.json"
	defaultTLSCertPath     = "/var/lib/wattkeeper/node-api.crt"
	defaultTLSKeyPath      = "/var/lib/wattkeeper/node-api.key"
)

var version = "dev"

type config struct {
	configDir string
	listen    string
	tlsListen string
	logLevel  string
	devUI     bool
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

type inventoryCredentialsUpdater interface {
	UpdateNUTCredentials(string, string)
}

type upsCountUpdater interface {
	UpdateUPSCount(int)
}

type adoptedUpdater interface {
	UpdateAdopted(bool)
}

type agentRuntime struct {
	watcher         hotplugWatcher
	scanner         scanner
	reloader        reloader
	inventory       inventoryUpdater
	upsCount        upsCountUpdater
	adopted         adoptedUpdater
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

type runtimeAdopter struct {
	logger       *log.Logger
	configDir    string
	adoptionPath string
	reloader     reloader
	advertiser   adoptedUpdater
	inventory    inventoryUpdater
	version      string
	serial       string
	tlsPort      int
	tlsCertPath  string
	tlsKeyPath   string
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
	cfg := parseFlags()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stdout, "wattkeeper-agent: ", log.LstdFlags)
	logger.Printf("starting config_dir=%s listen=%s log_level=%s dev_ui=%t http_auth=%t", cfg.configDir, cfg.listen, cfg.logLevel, cfg.devUI, cfg.httpAuth)

	if err := run(ctx, logger, cfg); err != nil {
		logger.Printf("fatal error: %v", err)
		os.Exit(1)
	}

	logger.Print("shutdown complete")
}

func parseFlags() config {
	var cfg config

	flag.StringVar(&cfg.configDir, "config-dir", "/etc/nut", "directory containing NUT configuration")
	flag.StringVar(&cfg.listen, "listen", ":80", "agent listen address")
	flag.StringVar(&cfg.tlsListen, "tls-listen", ":8443", "controller API TLS listen address")
	flag.StringVar(&cfg.logLevel, "log-level", "info", "log verbosity level")
	flag.BoolVar(&cfg.devUI, "dev-ui", false, "serve the node UI and API with sample data only")
	flag.BoolVar(&cfg.httpAuth, "http-auth", true, "require bootstrap and Basic Auth for the node dashboard and detailed status routes")
	flag.StringVar(&cfg.authPath, "http-auth-file", "/var/lib/wattkeeper/webui-auth.json", "path to the node web auth file")
	flag.Parse()

	return cfg
}

func run(ctx context.Context, logger *log.Logger, cfg config) error {
	if cfg.devUI {
		return runDevUI(ctx, logger, cfg)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	identity, err := discovery.ResolveIdentity()
	if err != nil {
		return fmt.Errorf("resolve node identity: %w", err)
	}

	listener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.listen, err)
	}

	adopter := &runtimeAdopter{
		logger:       logger,
		configDir:    cfg.configDir,
		adoptionPath: defaultAdoptionPath,
		version:      version,
		serial:       identity.Serial,
		tlsCertPath:  defaultTLSCertPath,
		tlsKeyPath:   defaultTLSKeyPath,
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
	adopter.tlsPort = tlsPort

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
		tlsServer = &http.Server{Handler: healthAPI.Handler(), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: dynamicCertificateLoader(defaultTLSCertPath, defaultTLSKeyPath)}}
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
	adopter.advertiser = advertiser
	adopter.inventory = healthAPI
	adopter.reloader = runtime.reloader

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
	return &agentRuntime{
		watcher:         hotplug.NewWatcher(logger, hotplug.Options{Debounce: 3 * time.Second}),
		scanner:         nutconf.NewScanner(logger),
		reloader:        services.NewManager(logger),
		logger:          logger,
		configDir:       cfg.configDir,
		agentConfigPath: defaultAgentConfigPath,
		namesPath:       defaultNamesPath,
		adoptionPath:    defaultAdoptionPath,
	}
}

func (a *runtimeAdopter) ApplyAdoption(ctx context.Context, req api.AdoptRequest) (api.AdoptResponse, error) {
	if _, err := os.Stat(a.adoptionPath); err == nil {
		return api.AdoptResponse{}, fmt.Errorf("%w: %s", api.ErrNodeAlreadyAdopted, a.serial)
	} else if !errors.Is(err, os.ErrNotExist) {
		return api.AdoptResponse{}, fmt.Errorf("stat adoption config: %w", err)
	}

	state := adoptionState{
		CAPEM:         req.CAPEM,
		NUTUser:       req.NUTUser,
		NUTPassword:   req.NUTPassword,
		TokenSHA256:   api.TokenSHA256Hex(req.APIToken),
		ControllerURL: req.ControllerURL,
		TLSPort:       a.tlsPort,
		AdoptedAt:     time.Now().UTC(),
	}
	fingerprint, err := ensureNodeCertificate(a.serial, a.tlsCertPath, a.tlsKeyPath)
	if err != nil {
		return api.AdoptResponse{}, fmt.Errorf("ensure node TLS certificate: %w", err)
	}
	state.TLSFingerprint = fingerprint
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return api.AdoptResponse{}, fmt.Errorf("marshal adoption config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(a.adoptionPath), 0o755); err != nil {
		return api.AdoptResponse{}, fmt.Errorf("create adoption dir: %w", err)
	}
	if err := os.WriteFile(a.adoptionPath, payload, 0o600); err != nil {
		return api.AdoptResponse{}, fmt.Errorf("write adoption config: %w", err)
	}

	if _, err := nutconf.WriteIfChanged(filepath.Join(a.configDir, "upsd.users"), nutconf.RenderUPSDUsers(nutconf.UPSDUser{Username: req.NUTUser, Password: req.NUTPassword})); err != nil {
		return api.AdoptResponse{}, fmt.Errorf("write upsd.users: %w", err)
	}
	if a.reloader != nil {
		if err := a.reloader.Reload(ctx, true, nil); err != nil {
			return api.AdoptResponse{}, fmt.Errorf("reload NUT services: %w", err)
		}
	}
	if a.inventory != nil {
		if updater, ok := a.inventory.(inventoryCredentialsUpdater); ok {
			updater.UpdateNUTCredentials(req.NUTUser, req.NUTPassword)
		}
	}
	if a.advertiser != nil {
		a.advertiser.UpdateAdopted(true)
	}

	return api.AdoptResponse{
		Serial:         a.serial,
		Version:        a.version,
		ControllerURL:  req.ControllerURL,
		TLSPort:        a.tlsPort,
		TLSFingerprint: fingerprint,
		TokenSHA256:    api.TokenSHA256Hex(req.APIToken),
	}, nil
}

func dynamicCertificateLoader(certPath, keyPath string) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		if certPath == "" || keyPath == "" {
			return nil, fmt.Errorf("TLS certificate paths unavailable")
		}
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, err
		}
		return &certificate, nil
	}
}

func ensureNodeCertificate(serial, certPath, keyPath string) (string, error) {
	if _, certErr := os.Stat(certPath); certErr == nil {
		if _, keyErr := os.Stat(keyPath); keyErr == nil {
			return certificateFingerprint(certPath)
		}
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return "", fmt.Errorf("create TLS dir: %w", err)
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate node TLS key: %w", err)
	}
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", fmt.Errorf("generate node TLS serial: %w", err)
	}
	certificateTemplate := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{CommonName: "wattkeeper-node-" + serial},
		NotBefore:             time.Now().Add(-1 * time.Hour).UTC(),
		NotAfter:              time.Now().AddDate(5, 0, 0).UTC(),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", fmt.Errorf("create node TLS certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("marshal node TLS key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return "", fmt.Errorf("write node TLS cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", fmt.Errorf("write node TLS key: %w", err)
	}
	return certificateFingerprint(certPath)
}

func certificateFingerprint(certPath string) (string, error) {
	content, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("read node TLS cert: %w", err)
	}
	block, _ := pem.Decode(content)
	if block == nil {
		return "", fmt.Errorf("decode node TLS cert PEM")
	}
	sum := sha256.Sum256(block.Bytes)
	return fmt.Sprintf("%x", sum[:]), nil
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
				if credentialsUpdater, ok := r.inventory.(inventoryCredentialsUpdater); ok {
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
