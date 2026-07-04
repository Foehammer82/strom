package sim

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherEmitsSyntheticAndFileEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	watcher := NewWatcher(nil, Options{Dir: dir, Debounce: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := watcher.Events(ctx)
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}

	first := <-events
	if !first.Synthetic {
		t.Fatalf("first event Synthetic = %t, want true", first.Synthetic)
	}

	if err := os.WriteFile(filepath.Join(dir, "demo.dev"), []byte("ups.status: OL\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	select {
	case event := <-events:
		if event.Synthetic {
			t.Fatalf("second event Synthetic = true, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fs event")
	}
}
