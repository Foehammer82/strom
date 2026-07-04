package sim

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Foehammer82/wattkeeper/agent/internal/hotplug"
	"github.com/fsnotify/fsnotify"
)

type Options struct {
	Dir      string
	Debounce time.Duration
	Logger   *log.Logger
	Now      func() time.Time
}

type Watcher struct {
	dir      string
	debounce time.Duration
	logger   *log.Logger
	now      func() time.Time
}

func NewWatcher(logger *log.Logger, options Options) *Watcher {
	debounce := options.Debounce
	if debounce <= 0 {
		debounce = 3 * time.Second
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	dir := strings.TrimSpace(options.Dir)
	if dir == "" {
		dir = strings.TrimSpace(options.Dir)
	}
	if options.Logger != nil {
		logger = options.Logger
	}
	return &Watcher{dir: dir, debounce: debounce, logger: logger, now: now}
}

func (w *Watcher) Events(ctx context.Context) (<-chan hotplug.Event, error) {
	if w.dir == "" {
		return nil, fmt.Errorf("simulation directory is required")
	}
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return nil, fmt.Errorf("create simulation directory: %w", err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}
	if err := watcher.Add(w.dir); err != nil {
		_ = watcher.Close()
		return nil, fmt.Errorf("watch simulation directory: %w", err)
	}

	out := make(chan hotplug.Event)
	triggers := make(chan struct{}, 1)
	go w.runDebounceLoop(ctx, out, triggers)
	go w.runReadLoop(ctx, watcher, triggers)
	return out, nil
}

func (w *Watcher) runDebounceLoop(ctx context.Context, out chan<- hotplug.Event, triggers <-chan struct{}) {
	defer close(out)
	triggersCh := triggers

	select {
	case out <- hotplug.Event{Time: w.now(), Synthetic: true}:
	case <-ctx.Done():
		return
	}

	var timer *time.Timer
	var timerChannel <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case _, ok := <-triggersCh:
			if !ok {
				if timer == nil {
					return
				}
				triggersCh = nil
				continue
			}
			if timer == nil {
				timer = time.NewTimer(w.debounce)
				timerChannel = timer.C
				continue
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.debounce)
		case <-timerChannel:
			select {
			case out <- hotplug.Event{Time: w.now()}:
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			}
			timerChannel = nil
			timer = nil
			if triggersCh == nil {
				return
			}
		}
	}
}

func (w *Watcher) runReadLoop(ctx context.Context, watcher *fsnotify.Watcher, triggers chan<- struct{}) {
	defer close(triggers)
	defer watcher.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !w.relevantEvent(event) {
				continue
			}
			select {
			case triggers <- struct{}{}:
			default:
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			if ctx.Err() != nil {
				return
			}
			if w.logger != nil {
				w.logger.Printf("simulation watcher error: %v", err)
			}
		}
	}
}

func (w *Watcher) relevantEvent(event fsnotify.Event) bool {
	if filepath.Ext(strings.ToLower(event.Name)) != ".dev" {
		return false
	}
	return event.Op&fsnotify.Create != 0 || event.Op&fsnotify.Remove != 0 || event.Op&fsnotify.Write != 0 || event.Op&fsnotify.Rename != 0
}
