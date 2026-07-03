package hotplug

import (
	"context"
	"log"
	"time"
)

const defaultDebounce = 3 * time.Second

type Event struct {
	Time      time.Time
	Synthetic bool
}

type Options struct {
	Debounce   time.Duration
	Logger     *log.Logger
	OpenSocket func() (messageConn, error)
	Now        func() time.Time
}

type Watcher struct {
	debounce   time.Duration
	logger     *log.Logger
	openSocket func() (messageConn, error)
	now        func() time.Time
}

type messageConn interface {
	ReadMessage() ([]byte, error)
	Close() error
}

func NewWatcher(logger *log.Logger, options Options) *Watcher {
	debounce := options.Debounce
	if debounce <= 0 {
		debounce = defaultDebounce
	}

	openSocket := options.OpenSocket
	if openSocket == nil {
		openSocket = openSocketForPlatform
	}

	now := options.Now
	if now == nil {
		now = time.Now
	}

	return &Watcher{
		debounce:   debounce,
		logger:     logger,
		openSocket: openSocket,
		now:        now,
	}
}

func (w *Watcher) Events(ctx context.Context) (<-chan Event, error) {
	conn, err := w.openSocket()
	if err != nil {
		return nil, err
	}

	out := make(chan Event)
	triggers := make(chan struct{}, 1)

	go w.runDebounceLoop(ctx, out, triggers)
	go w.runReadLoop(ctx, conn, triggers)

	return out, nil
}

func (w *Watcher) runDebounceLoop(ctx context.Context, out chan<- Event, triggers <-chan struct{}) {
	defer close(out)
	triggersCh := triggers

	select {
	case out <- Event{Time: w.now(), Synthetic: true}:
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
			case out <- Event{Time: w.now()}:
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

func (w *Watcher) runReadLoop(ctx context.Context, conn messageConn, triggers chan<- struct{}) {
	defer close(triggers)
	defer conn.Close()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if w.logger != nil {
				w.logger.Printf("hotplug watcher read failed: %v", err)
			}
			return
		}

		if !isRelevantMessage(message) {
			continue
		}

		select {
		case triggers <- struct{}{}:
		default:
		}
	}
}
