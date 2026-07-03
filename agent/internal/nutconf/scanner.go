package nutconf

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
)

const defaultScannerPath = "nut-scanner"

type DetectedUPS struct {
	Driver    string
	Port      string
	VendorID  string
	ProductID string
	Product   string
	Serial    string
	Vendor    string
	Bus       string
}

func (d DetectedUPS) StableKey() string {
	if d.Serial != "" {
		return "serial:" + strings.ToLower(d.Serial)
	}

	return fmt.Sprintf("fallback:%s:%s:%s", strings.ToLower(d.Bus), strings.ToLower(d.VendorID), strings.ToLower(d.ProductID))
}

type commandRunner interface {
	CombinedOutput(ctx context.Context, path string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) CombinedOutput(ctx context.Context, path string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

type Scanner struct {
	Path   string
	Logger *log.Logger
	Runner commandRunner
}

func NewScanner(logger *log.Logger) *Scanner {
	return &Scanner{
		Path:   defaultScannerPath,
		Logger: logger,
		Runner: execRunner{},
	}
}

func (s *Scanner) Scan(ctx context.Context) ([]DetectedUPS, error) {
	path := s.Path
	if path == "" {
		path = defaultScannerPath
	}

	runner := s.Runner
	if runner == nil {
		runner = execRunner{}
	}

	output, err := runner.CombinedOutput(ctx, path, "-U", "-q")
	if err != nil {
		return nil, fmt.Errorf("run %s -U -q: %w: %s", path, err, strings.TrimSpace(string(output)))
	}

	devices, err := parseScannerOutput(bytes.NewReader(output), s.Logger)
	if err != nil {
		return nil, fmt.Errorf("parse scanner output: %w", err)
	}

	return devices, nil
}

func parseScannerOutput(reader io.Reader, logger *log.Logger) ([]DetectedUPS, error) {
	scanner := bufio.NewScanner(reader)
	devices := make([]DetectedUPS, 0)
	current := map[string]string{}
	inSection := false
	lineNumber := 0

	flush := func() error {
		if !inSection {
			return nil
		}

		device, err := buildDetectedUPS(current, logger)
		if err != nil {
			return err
		}
		devices = append(devices, device)
		current = map[string]string{}
		return nil
	}

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if err := flush(); err != nil {
				return nil, err
			}
			inSection = true
			continue
		}

		if !inSection {
			return nil, fmt.Errorf("line %d: key/value outside section", lineNumber)
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: malformed entry %q", lineNumber, line)
		}

		current[strings.ToLower(strings.TrimSpace(key))] = trimScannerValue(value)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if err := flush(); err != nil {
		return nil, err
	}

	return devices, nil
}

func buildDetectedUPS(values map[string]string, logger *log.Logger) (DetectedUPS, error) {
	device := DetectedUPS{
		Driver:    values["driver"],
		Port:      values["port"],
		VendorID:  values["vendorid"],
		ProductID: values["productid"],
		Product:   values["product"],
		Serial:    values["serial"],
		Vendor:    values["vendor"],
		Bus:       values["bus"],
	}

	if device.Driver == "" {
		return DetectedUPS{}, errors.New("scanner result missing driver")
	}
	if device.Port == "" {
		return DetectedUPS{}, errors.New("scanner result missing port")
	}
	if device.Serial == "" {
		if device.Bus == "" || device.VendorID == "" || device.ProductID == "" {
			return DetectedUPS{}, errors.New("scanner result missing serial and fallback identity fields")
		}
		if logger != nil {
			logger.Printf("nut-scanner device missing serial, using fallback identity bus=%s vendorid=%s productid=%s", device.Bus, device.VendorID, device.ProductID)
		}
	}

	return device, nil
}

func trimScannerValue(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, `"`)
	trimmed = strings.TrimSuffix(trimmed, `"`)
	return trimmed
}