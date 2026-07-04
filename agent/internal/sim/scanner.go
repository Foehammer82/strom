package sim

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Foehammer82/wattkeeper/agent/internal/nutconf"
)

type Scanner struct {
	logger   *log.Logger
	dir      string
	demoMode bool
}

func NewScanner(logger *log.Logger, dir string, demoMode bool) *Scanner {
	return &Scanner{logger: logger, dir: strings.TrimSpace(dir), demoMode: demoMode}
}

func (s *Scanner) Scan(context.Context) ([]nutconf.DetectedUPS, error) {
	if s.dir == "" {
		return nil, fmt.Errorf("simulation directory is required")
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read simulation directory: %w", err)
	}

	fileNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), ".dev") {
			fileNames = append(fileNames, name)
		}
	}
	sort.Strings(fileNames)

	devices := make([]nutconf.DetectedUPS, 0, len(fileNames))
	for _, name := range fileNames {
		device, err := s.detectedUPSForFile(filepath.Join(s.dir, name), strings.TrimSuffix(name, filepath.Ext(name)))
		if err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	return devices, nil
}

func (s *Scanner) detectedUPSForFile(path, fallbackID string) (nutconf.DetectedUPS, error) {
	content, err := os.Open(path)
	if err != nil {
		return nutconf.DetectedUPS{}, fmt.Errorf("open fixture %s: %w", path, err)
	}
	defer content.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(content)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		values[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nutconf.DetectedUPS{}, fmt.Errorf("scan fixture %s: %w", path, err)
	}

	vendor := firstNonEmpty(values["device.mfr"], values["ups.mfr"])
	product := firstNonEmpty(values["device.model"], values["ups.model"])
	serial := firstNonEmpty(values["device.serial"], values["ups.serial"])
	if serial == "" {
		serial = fallbackID
	}
	if vendor == "" && s.demoMode {
		vendor = "APC"
	}
	if product == "" && s.demoMode {
		product = "Back-UPS BE1050G3"
	}

	return nutconf.DetectedUPS{
		Driver:    "dummy-ups",
		Port:      path,
		VendorID:  "sim",
		ProductID: "dummy",
		Product:   product,
		Serial:    serial,
		Vendor:    vendor,
		Bus:       "sim",
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
