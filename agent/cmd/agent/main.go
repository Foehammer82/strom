package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Foehammer82/wattkeeper/agent/internal/hotplug"
	"github.com/Foehammer82/wattkeeper/agent/internal/nutconf"
)

type config struct {
	configDir string
	listen    string
	logLevel  string
}

func main() {
	cfg := parseFlags()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stdout, "wattkeeper-agent: ", log.LstdFlags)
	logger.Printf("starting config_dir=%s listen=%s log_level=%s", cfg.configDir, cfg.listen, cfg.logLevel)

	if err := run(ctx, logger); err != nil {
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

func run(ctx context.Context, logger *log.Logger) error {
	watcher := hotplug.NewWatcher(logger, hotplug.Options{Debounce: 3 * time.Second})
	events, err := watcher.Events(ctx)
	if err != nil {
		return err
	}

	scanner := nutconf.NewScanner(logger)
	var previous []nutconf.DetectedUPS

	logger.Print("run loop started")

	for {
		select {
		case <-ctx.Done():
			logger.Printf("received shutdown signal: %v", ctx.Err())
			return nil
		case event, ok := <-events:
			if !ok {
				return errors.New("hotplug watcher stopped")
			}

			current, err := scanner.Scan(ctx)
			if err != nil {
				logger.Printf("scan failed synthetic=%t: %v", event.Synthetic, err)
				continue
			}

			logScanDiff(logger, previous, current, event)
			previous = current
		}
	}
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
	identity := device.Serial
	if identity == "" {
		identity = strings.TrimPrefix(device.StableKey(), "fallback:")
	}
	return identity + "(" + device.Driver + "," + device.Port + ")"
}
