package mqtt

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
)

type fakePublishClient struct {
	publishes []*paho.Publish
	awaits    int
}

func (f *fakePublishClient) AwaitConnection(context.Context) error {
	f.awaits++
	return nil
}

func (f *fakePublishClient) Publish(_ context.Context, publish *paho.Publish) (*paho.PublishResponse, error) {
	cloned := *publish
	cloned.Payload = append([]byte(nil), publish.Payload...)
	f.publishes = append(f.publishes, &cloned)
	return &paho.PublishResponse{}, nil
}

func TestPublisherPublishesDiscoveryOnceAndStateOnChangeOrHeartbeat(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 4, 9, 0, 0, 0, time.UTC)
	client := &fakePublishClient{}
	battery := 98.0
	load := 34.0
	runtime := int64(1800)
	publisher := NewTestPublisher(RuntimeConfig{Heartbeat: 5 * time.Minute}, client, func() time.Time { return now })
	snapshot := []NodeSnapshot{{
		Node:  NodeInfo{ID: "serial-1234", DisplayName: "Lab Rack Node", Online: true, CommsState: "healthy", Version: "v0.3.0"},
		UPSes: []UPSInfo{{Name: "ups-a", DisplayName: "Rack UPS", Status: "OL", BatteryCharge: &battery, LoadPercent: &load, Runtime: &runtime}},
	}}
	if err := publisher.PublishSnapshots(context.Background(), snapshot); err != nil {
		t.Fatalf("PublishSnapshots() error = %v", err)
	}
	firstCount := len(client.publishes)
	if firstCount == 0 {
		t.Fatal("no mqtt publishes recorded")
	}
	now = now.Add(2 * time.Minute)
	if err := publisher.PublishSnapshots(context.Background(), snapshot); err != nil {
		t.Fatalf("PublishSnapshots() second error = %v", err)
	}
	if len(client.publishes) != firstCount {
		t.Fatalf("publish count = %d, want unchanged before heartbeat with no state change", len(client.publishes))
	}
	battery = 97
	now = now.Add(1 * time.Minute)
	if err := publisher.PublishSnapshots(context.Background(), snapshot); err != nil {
		t.Fatalf("PublishSnapshots() third error = %v", err)
	}
	if len(client.publishes) <= firstCount {
		t.Fatalf("publish count = %d, want additional state publish on payload change", len(client.publishes))
	}
	beforeHeartbeatCount := len(client.publishes)
	now = now.Add(6 * time.Minute)
	if err := publisher.PublishSnapshots(context.Background(), snapshot); err != nil {
		t.Fatalf("PublishSnapshots() heartbeat error = %v", err)
	}
	if len(client.publishes) == beforeHeartbeatCount {
		t.Fatalf("publish count = %d, want heartbeat republish after interval", len(client.publishes))
	}

	var discoveryCount int
	for _, publish := range client.publishes {
		if publish.Retain && len(publish.Payload) > 0 && bytes.Contains(publish.Payload, []byte(`"unique_id"`)) {
			discoveryCount++
		}
	}
	if discoveryCount != 8 {
		t.Fatalf("discovery publish count = %d, want 8 retained discovery publishes once", discoveryCount)
	}
}
