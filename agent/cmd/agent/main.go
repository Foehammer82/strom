package main

import (
	"context"
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
)

var version = "dev"

type config struct {
	configDir string
	listen    string
	logLevel  string
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
	logger          *log.Logger
	configDir       string
	agentConfigPath string
	namesPath       string
}

type appliedConfig struct {
	devices        []nutconf.DetectedUPS
	changed        bool
	restartUPSName []string
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
	logger.Printf("starting config_dir=%s listen=%s log_level=%s", cfg.configDir, cfg.listen, cfg.logLevel)

	if err := run(ctx, logger, cfg); err != nil {
		logger.Printf("fatal error: %v", err)
		os.Exit(1)
	}

	logger.Print("shutdown complete")
}

func parseFlags() config {
	var cfg config

	flag.StringVar(&cfg.configDir, "config-dir", "/etc/nut", "directory containing NUT configuration")
	flag.StringVar(&cfg.listen, "listen", ":8080", "agent listen address")
	flag.StringVar(&cfg.logLevel, "log-level", "info", "log verbosity level")
	flag.Parse()

	return cfg
}

func run(ctx context.Context, logger *log.Logger, cfg config) error {
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

	healthAPI := api.New(logger, api.Options{
		Version:   version,
		Serial:    identity.Serial,
		StartedAt: time.Now(),
	})
	httpServer := &http.Server{Handler: healthAPI.Handler()}
	httpErr := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr <- err
		}
	}()

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
