package mqtt

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

type fakePublishClient struct {
	mu           sync.Mutex
	publishes    []*paho.Publish
	subscribes   []*paho.Subscribe
	receivers    []func(autopaho.PublishReceived) (bool, error)
	awaits       int
	subscribeErr error
	publishErr   error
}

func (f *fakePublishClient) AwaitConnection(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.awaits++
	return nil
}

func (f *fakePublishClient) Publish(_ context.Context, publish *paho.Publish) (*paho.PublishResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	cloned := *publish
	cloned.Payload = append([]byte(nil), publish.Payload...)
	f.publishes = append(f.publishes, &cloned)
	return &paho.PublishResponse{}, nil
}

func (f *fakePublishClient) Subscribe(_ context.Context, subscribe *paho.Subscribe) (*paho.Suback, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}
	cloned := *subscribe
	cloned.Subscriptions = append([]paho.SubscribeOptions(nil), subscribe.Subscriptions...)
	f.subscribes = append(f.subscribes, &cloned)
	return &paho.Suback{}, nil
}

func (f *fakePublishClient) AddOnPublishReceived(receiver func(autopaho.PublishReceived) (bool, error)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receivers = append(f.receivers, receiver)
	index := len(f.receivers) - 1
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if index >= 0 && index < len(f.receivers) {
			f.receivers[index] = nil
		}
	}
}

func (f *fakePublishClient) emit(topic string, payload []byte) {
	f.mu.Lock()
	receivers := append([]func(autopaho.PublishReceived) (bool, error){}, f.receivers...)
	f.mu.Unlock()
	received := autopaho.PublishReceived{PublishReceived: paho.PublishReceived{Packet: &paho.Publish{Topic: topic, Payload: payload}}}
	for _, receiver := range receivers {
		if receiver == nil {
			continue
		}
		_, _ = receiver(received)
	}
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

	controllerAvailabilityTopic := "strom/controller/availability"
	if countPublishesForTopic(client.publishes, controllerAvailabilityTopic) < 2 {
		t.Fatalf("controller availability publishes = %d, want heartbeat republish", countPublishesForTopic(client.publishes, controllerAvailabilityTopic))
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

func countPublishesForTopic(publishes []*paho.Publish, topic string) int {
	count := 0
	for _, publish := range publishes {
		if publish.Topic == topic {
			count++
		}
	}
	return count
}

func TestPublisherSubscribesToCommandTopicsAndDispatchesKnownCommands(t *testing.T) {
	t.Parallel()
	client := &fakePublishClient{}
	now := time.Date(2026, 7, 4, 9, 0, 0, 0, time.UTC)
	commandCalls := make(chan CommandRequest, 1)
	publisher := NewTestPublisher(RuntimeConfig{
		StatePrefix: "strom",
		CommandHandler: func(_ context.Context, request CommandRequest) error {
			commandCalls <- request
			return nil
		},
	}, client, func() time.Time { return now })

	battery := 98.0
	load := 34.0
	runtime := int64(1800)
	snapshot := []NodeSnapshot{{
		Node: NodeInfo{ID: "serial-1234", DisplayName: "Lab Rack Node", Online: true, CommsState: "healthy", Version: "v0.3.0"},
		UPSes: []UPSInfo{{
			Name:          "ups-a",
			DisplayName:   "Rack UPS",
			Status:        "OL",
			BatteryCharge: &battery,
			LoadPercent:   &load,
			Runtime:       &runtime,
			Commands:      []CommandInfo{{Name: "test.battery.start.quick"}},
		}},
	}}

	if err := publisher.PublishSnapshots(context.Background(), snapshot); err != nil {
		t.Fatalf("PublishSnapshots() error = %v", err)
	}
	if len(client.subscribes) != 1 {
		t.Fatalf("subscribe count = %d, want 1", len(client.subscribes))
	}
	if len(client.subscribes[0].Subscriptions) != 1 || client.subscribes[0].Subscriptions[0].Topic != "strom/nodes/+/ups/+/command" {
		t.Fatalf("subscriptions = %#v, want command wildcard topic", client.subscribes[0].Subscriptions)
	}

	client.emit("strom/nodes/serial_1234/ups/ups_a/command", []byte("test.battery.start.quick"))
	select {
	case call := <-commandCalls:
		if call.NodeID != "serial-1234" || call.UPSName != "ups-a" || call.Command != "test.battery.start.quick" {
			t.Fatalf("command call = %#v, want mapped node/ups/command", call)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for command handler call")
	}

	client.emit("strom/nodes/serial_1234/ups/ups_a/command", []byte("unknown.command"))
	select {
	case unexpected := <-commandCalls:
		t.Fatalf("unexpected command handler call = %#v", unexpected)
	case <-time.After(200 * time.Millisecond):
	}
}
