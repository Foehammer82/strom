package api

import (
	"sync"
	"testing"
	"time"
)

func floatPtr(value float64) *float64 {
	return &value
}

func TestMetricHistoryTrimsToCapacity(t *testing.T) {
	t.Parallel()

	history := newMetricHistory(3)
	for i := 0; i < 5; i++ {
		history.add(metricSample{
			Timestamp:       time.Unix(int64(i), 0),
			MemoryUsedBytes: uint64(i),
		})
	}

	samples := history.snapshot()
	if len(samples) != 3 {
		t.Fatalf("len(samples) = %d, want 3", len(samples))
	}
	// The oldest two samples (MemoryUsedBytes 0 and 1) should have been
	// trimmed, leaving 2, 3, 4 in order.
	for index, want := range []uint64{2, 3, 4} {
		if samples[index].MemoryUsedBytes != want {
			t.Fatalf("samples[%d].MemoryUsedBytes = %d, want %d", index, samples[index].MemoryUsedBytes, want)
		}
	}
}

func TestMetricHistorySnapshotIsDefensiveCopy(t *testing.T) {
	t.Parallel()

	history := newMetricHistory(5)
	history.add(metricSample{CPUUsagePercent: floatPtr(12.5)})

	samples := history.snapshot()
	samples[0].CPUUsagePercent = floatPtr(99)

	again := history.snapshot()
	if *again[0].CPUUsagePercent != 12.5 {
		t.Fatalf("mutating a snapshot leaked into the ring buffer: got %v, want 12.5", *again[0].CPUUsagePercent)
	}
}

func TestMetricHistoryConcurrentAccess(t *testing.T) {
	t.Parallel()

	history := newMetricHistory(10)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			history.add(metricSample{DiskFreeBytes: uint64(i)})
			_ = history.snapshot()
		}(i)
	}
	wg.Wait()

	if len(history.snapshot()) != 10 {
		t.Fatalf("len(snapshot) = %d, want 10 (capacity)", len(history.snapshot()))
	}
}
