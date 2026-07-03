package api

import (
	"context"
	"encoding/json"
	"fmt"
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
	Version     string
	Serial      string
	StartedAt   time.Time
	Runner      commandRunner
	UPSCPath    string
	CPUTempPath string
	RootPath    string
}

type Service struct {
	logger      *log.Logger
	version     string
	serial      string
	startedAt   time.Time
	runner      commandRunner
	upscPath    string
	cpuTempPath string
	rootPath    string

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

type upsHealth struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Status string `json:"status"`
}

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

	cpuTempPath := opts.CPUTempPath
	if cpuTempPath == "" {
		cpuTempPath = defaultCPUTempPath
	}

	rootPath := opts.RootPath
	if rootPath == "" {
		rootPath = defaultRootPath
	}

	service := &Service{
		logger:      logger,
		version:     defaultString(opts.Version, "dev"),
		serial:      opts.Serial,
		startedAt:   startedAt,
		runner:      runner,
		upscPath:    upscPath,
		cpuTempPath: cpuTempPath,
		rootPath:    rootPath,
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

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	return mux
}

func (s *Service) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	response, err := s.buildHealthResponse(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, response)
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

	devices := s.inventory()
	response.UPSes = make([]upsHealth, 0, len(devices))
	for _, device := range devices {
		status := unknownStatus
		upsStatus, err := s.queryUPSStatus(ctx, device.Name)
		if err != nil {
			if s.logger != nil {
				s.logger.Printf("health upsc failed ups=%s: %v", device.Name, err)
			}
		} else {
			status = upsStatus
		}

		response.UPSes = append(response.UPSes, upsHealth{
			Name:   device.Name,
			Driver: device.Driver,
			Status: status,
		})
	}

	return response, nil
}

func (s *Service) queryUPSStatus(ctx context.Context, name string) (string, error) {
	output, err := s.runner.CombinedOutput(ctx, s.upscPath, name)
	status, parseErr := parseUPSStatus(output)
	if parseErr == nil {
		return status, nil
	}
	if err != nil && isDriverStarting(output, err) {
		return startingStatus, nil
	}
	if err != nil {
		return "", fmt.Errorf("run %s %s: %w: %s", s.upscPath, name, err, strings.TrimSpace(string(output)))
	}
	return "", parseErr
}

func (s *Service) inventory() []nutconf.DetectedUPS {
	s.mu.RLock()
	defer s.mu.RUnlock()

	devices := make([]nutconf.DetectedUPS, len(s.devices))
	copy(devices, s.devices)
	return devices
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

		if strings.TrimSpace(key) != "ups.status" {
			continue
		}

		status := strings.TrimSpace(value)
		if status == "" {
			break
		}
		return status, nil
	}

	return "", fmt.Errorf("ups.status not found")
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
