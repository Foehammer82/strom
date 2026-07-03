package hotplug

import (
	"context"
	"errors"
	"log"
	"testing"
	"time"
)

func TestWatcherEmitsSyntheticAndDebouncedEvents(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &fakeConn{
		messages: [][]byte{
			[]byte("ACTION=add\x00SUBSYSTEM=usb\x00"),
			[]byte("ACTION=add\x00SUBSYSTEM=usb\x00"),
			[]byte("ACTION=change\x00SUBSYSTEM=usb\x00"),
			[]byte("ACTION=remove\x00SUBSYSTEM=usb\x00"),
		},
		err: errors.New("done"),
	}

	watcher := NewWatcher(log.New(testWriter{t: t}, "", 0), Options{
		Debounce: 20 * time.Millisecond,
		OpenSocket: func() (messageConn, error) {
			return conn, nil
		},
	})

	events, err := watcher.Events(ctx)
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}

	first := waitForEvent(t, events)
	if !first.Synthetic {
		t.Fatalf("first event should be synthetic: %#v", first)
	}

	second := waitForEvent(t, events)
	if second.Synthetic {
		t.Fatalf("second event should be debounced hardware event: %#v", second)
	}

	select {
	case extra, ok := <-events:
		if ok {
			t.Fatalf("unexpected extra event: %#v", extra)
		}
	case <-time.After(100 * time.Millisecond):
		cancel()
	}
}

func TestRelevantMessageFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message []byte
		want    bool
	}{
		{name: "usb add", message: []byte("ACTION=add\x00SUBSYSTEM=usb\x00"), want: true},
		{name: "usb remove", message: []byte("ACTION=remove\x00SUBSYSTEM=usb\x00"), want: true},
		{name: "usb change", message: []byte("ACTION=change\x00SUBSYSTEM=usb\x00"), want: false},
		{name: "block add", message: []byte("ACTION=add\x00SUBSYSTEM=block\x00"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isRelevantMessage(tt.message); got != tt.want {
				t.Fatalf("isRelevantMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeConn struct {
	messages [][]byte
	err      error
	index    int
}

func (f *fakeConn) ReadMessage() ([]byte, error) {
	if f.index >= len(f.messages) {
		return nil, f.err
	}
	message := f.messages[f.index]
	f.index++
	return message, nil
}

func (f *fakeConn) Close() error {
	return nil
}

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(string(p))
	return len(p), nil
}

func waitForEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()

	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("event channel closed")
		}
		return event
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}