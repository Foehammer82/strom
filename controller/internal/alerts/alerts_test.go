package alerts

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Foehammer82/strom/controller/internal/registry"
)

func TestEngineEvaluateOnceCreatesDebouncedEvents(t *testing.T) {
	t.Parallel()
	store, err := registry.Open(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 3, 18, 0, 0, 0, time.UTC)
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{ID: "serial-1234", Instance: "strom-node-1234", Hostname: "strom-node-1234.local", Address: "192.168.1.50", Port: 80, Version: "v0.3.0", UPSCount: 1, LastSeen: now.Add(-10 * time.Second)}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() error = %v", err)
	}
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	if err := store.UpdateNodePollState(context.Background(), "serial-1234", registry.PollState{CommsState: registry.CommsStateOffline, PollFailures: 3, LastPolledAt: now, LastPollError: "dial timeout"}); err != nil {
		t.Fatalf("UpdateNodePollState() error = %v", err)
	}
	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", now, []registry.UPSSnapshot{{Name: "ups-a", Driver: "usbhid-ups", Variables: map[string]string{"ups.status": "OB DISCHRG", "battery.charge": "19"}}}); err != nil {
		t.Fatalf("RecordUPSSnapshots() error = %v", err)
	}
	threshold := 20.0
	if _, err := store.CreateAlertRule(context.Background(), registry.AlertRule{Kind: KindOnBattery, WebhookURL: "http://example.invalid/onbattery", DebounceSeconds: 300, Enabled: true}); err != nil {
		t.Fatalf("CreateAlertRule() on_battery error = %v", err)
	}
	if _, err := store.CreateAlertRule(context.Background(), registry.AlertRule{Kind: KindLowBattery, Threshold: &threshold, WebhookURL: "http://example.invalid/lowbattery", DebounceSeconds: 300, Enabled: true}); err != nil {
		t.Fatalf("CreateAlertRule() low_battery error = %v", err)
	}
	if _, err := store.CreateAlertRule(context.Background(), registry.AlertRule{Kind: KindCommsLost, WebhookURL: "http://example.invalid/comms", DebounceSeconds: 300, Enabled: true}); err != nil {
		t.Fatalf("CreateAlertRule() comms_lost error = %v", err)
	}
	fake := &fakeDeliverer{}
	engine := &Engine{Store: store, Deliverer: fake, Now: func() time.Time { return now }, NodeOfflineAfter: 45 * time.Second}
	if err := engine.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("EvaluateOnce() error = %v", err)
	}
	if got := len(fake.events); got != 3 {
		t.Fatalf("delivered event count = %d, want 3", got)
	}
	events, err := store.ListAlertEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListAlertEvents() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("stored alert event count = %d, want 3", len(events))
	}
	if err := engine.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("EvaluateOnce() second error = %v", err)
	}
	events, err = store.ListAlertEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListAlertEvents() second error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("stored alert event count after debounce = %d, want 3", len(events))
	}
}

func TestEngineNodeOfflineUsesLastSeenThreshold(t *testing.T) {
	t.Parallel()
	store, err := registry.Open(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 3, 18, 0, 0, 0, time.UTC)
	if err := store.UpsertDiscoveredNode(context.Background(), registry.Node{ID: "serial-1234", Instance: "strom-node-1234", Hostname: "strom-node-1234.local", Address: "192.168.1.50", Port: 80, Version: "v0.3.0", UPSCount: 1, LastSeen: now.Add(-1 * time.Minute)}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() error = %v", err)
	}
	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}
	rule, err := store.CreateAlertRule(context.Background(), registry.AlertRule{Kind: KindNodeOffline, WebhookURL: "http://example.invalid/offline", DebounceSeconds: 300, Enabled: true})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	fake := &fakeDeliverer{}
	engine := &Engine{Store: store, Deliverer: fake, Now: func() time.Time { return now }, NodeOfflineAfter: 45 * time.Second}
	if err := engine.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("EvaluateOnce() error = %v", err)
	}
	if len(fake.events) != 1 || fake.events[0].RuleID != rule.ID {
		t.Fatalf("delivered events = %#v, want one node_offline event", fake.events)
	}
}

type fakeDeliverer struct {
	events []registry.AlertEvent
	err    error
}

func (f *fakeDeliverer) Deliver(_ context.Context, _ registry.AlertRule, event registry.AlertEvent) error {
	f.events = append(f.events, event)
	return f.err
}

func (f *fakeDeliverer) String() string {
	return fmt.Sprintf("events=%d", len(f.events))
}
