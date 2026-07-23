package api

import (
	"context"
	"sync"
	"time"
)

// historySampleInterval controls how often a metricSample is recorded into
// the in-memory ring buffer used for the node dashboard's trend sparklines.
const historySampleInterval = 10 * time.Second

// historyCapacity bounds the ring buffer to a 10 minute trailing window at
// historySampleInterval granularity (10 min / 10s = 60 samples).
const historyCapacity = 60

// longSampleInterval controls how often a metricSample is additionally
// recorded into the longer-retention ring buffer used by the metric detail
// dialog's 1h/6h/24h window options. It reuses the sample already computed
// on the historySampleInterval tick (see RunMetricsSampler) rather than
// issuing extra buildHealthResponse calls.
const longSampleInterval = time.Minute

// longHistoryCapacity bounds the long-retention ring buffer to a 24 hour
// trailing window at longSampleInterval granularity (24h / 1min = 1440
// samples).
const longHistoryCapacity = 24 * 60

// liveStreamInterval controls how often the SSE endpoint pushes a fresh
// healthResponse snapshot to connected dashboard clients, independent of how
// often a sample is recorded into the trend history.
const liveStreamInterval = 2 * time.Second

// metricSample is a single point-in-time recording of the metrics tracked
// for the node dashboard's trend sparklines (CPU usage, CPU temperature,
// memory usage, disk free). It intentionally mirrors the relevant subset of
// healthResponse fields.
type metricSample struct {
	Timestamp             time.Time `json:"timestamp"`
	CPUUsagePercent       *float64  `json:"cpu_usage_percent,omitempty"`
	CPUTemperatureCelsius *float64  `json:"cpu_temperature_celsius,omitempty"`
	MemoryUsedBytes       uint64    `json:"memory_used_bytes"`
	MemoryTotalBytes      uint64    `json:"memory_total_bytes"`
	DiskFreeBytes         uint64    `json:"disk_free_bytes"`
}

// metricHistory is a fixed-size, mutex-guarded ring buffer of metricSample
// values. It is safe for concurrent use by the sampler goroutine (writer)
// and any number of SSE handler goroutines (readers).
type metricHistory struct {
	mu       sync.Mutex
	samples  []metricSample
	capacity int
}

func newMetricHistory(capacity int) *metricHistory {
	return &metricHistory{capacity: capacity}
}

// add appends a sample, trimming the oldest entries once capacity is
// exceeded.
func (h *metricHistory) add(sample metricSample) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.samples = append(h.samples, sample)
	if overflow := len(h.samples) - h.capacity; overflow > 0 {
		h.samples = h.samples[overflow:]
	}
}

// snapshot returns a defensive copy of the currently buffered samples,
// oldest first.
func (h *metricHistory) snapshot() []metricSample {
	h.mu.Lock()
	defer h.mu.Unlock()

	samples := make([]metricSample, len(h.samples))
	copy(samples, h.samples)
	return samples
}

// RunMetricsSampler periodically records a metricSample derived from
// buildHealthResponse into the node's in-memory trend history. It blocks
// until ctx is done, so callers should run it in its own goroutine, e.g.
// `go service.RunMetricsSampler(ctx)`.
func (s *Service) RunMetricsSampler(ctx context.Context) {
	ticker := time.NewTicker(historySampleInterval)
	defer ticker.Stop()

	var lastLongSample time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			response, err := s.buildHealthResponse(ctx)
			if err != nil {
				if s.logger != nil {
					s.logger.Printf("metrics sampler: build health response: %v", err)
				}
				continue
			}
			now := time.Now()
			sample := metricSample{
				Timestamp:             now,
				CPUUsagePercent:       response.CPUUsagePercent,
				CPUTemperatureCelsius: response.CPUTemperatureCelsius,
				MemoryUsedBytes:       response.MemoryUsedBytes,
				MemoryTotalBytes:      response.MemoryTotalBytes,
				DiskFreeBytes:         response.DiskFreeBytes,
			}
			s.history.add(sample)
			// Piggyback the same sample onto the long-retention buffer at a
			// coarser cadence instead of sampling it independently, since
			// they'd otherwise both shell out to the same health build.
			if lastLongSample.IsZero() || now.Sub(lastLongSample) >= longSampleInterval {
				s.longHistory.add(sample)
				lastLongSample = now
			}
		}
	}
}
