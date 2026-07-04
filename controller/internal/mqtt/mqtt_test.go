package mqtt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiscoveryMessagesPublishExpectedHomeAssistantEntities(t *testing.T) {
	t.Parallel()
	batteryCharge := 98.0
	load := 34.0
	runtime := int64(1800)
	inputVoltage := 121.3
	messages, err := DiscoveryMessages(Config{}, NodeInfo{
		ID:          "serial-1234",
		DisplayName: "Lab Rack Node",
		Hostname:    "wkeeper-node-1234.local",
		Location:    "Utility Closet",
		Online:      true,
		CommsState:  "healthy",
		Version:     "v0.3.0",
	}, UPSInfo{
		Name:          "ups-a",
		DisplayName:   "Rack UPS",
		Driver:        "usbhid-ups",
		Status:        "OL",
		BatteryCharge: &batteryCharge,
		LoadPercent:   &load,
		Runtime:       &runtime,
		InputVoltage:  &inputVoltage,
		Commands:      []CommandInfo{{Name: "test.battery.start.quick", Description: "Quick self test"}},
	})
	if err != nil {
		t.Fatalf("DiscoveryMessages() error = %v", err)
	}
	if len(messages) != 9 {
		t.Fatalf("len(messages) = %d, want 9", len(messages))
	}
	if !strings.Contains(messages[0].Topic, "homeassistant/sensor/") {
		t.Fatalf("topic = %q, want sensor discovery prefix", messages[0].Topic)
	}
	var payload map[string]any
	if err := json.Unmarshal(messages[0].Payload, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload["availability_topic"] != "wattkeeper/nodes/serial_1234/availability" {
		t.Fatalf("payload = %#v, want availability_topic", payload)
	}
	if payload["state_topic"] != "wattkeeper/nodes/serial_1234/ups/ups_a/state" {
		t.Fatalf("payload = %#v, want state_topic", payload)
	}
}

func TestStateAndAvailabilityMessagesUseStableTopics(t *testing.T) {
	t.Parallel()
	batteryCharge := 91.0
	load := 52.0
	runtime := int64(1200)
	message, err := StateMessage(Config{}, NodeInfo{ID: "serial-1234", DisplayName: "Lab Rack Node", Online: true, CommsState: "healthy"}, UPSInfo{Name: "ups-a", Status: "OB DISCHRG", BatteryCharge: &batteryCharge, LoadPercent: &load, Runtime: &runtime, OnBattery: true, LowBattery: false})
	if err != nil {
		t.Fatalf("StateMessage() error = %v", err)
	}
	if message.Topic != "wattkeeper/nodes/serial_1234/ups/ups_a/state" {
		t.Fatalf("topic = %q, want stable state topic", message.Topic)
	}
	var payload map[string]any
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload["status"] != "OB DISCHRG" || payload["on_battery"] != true {
		t.Fatalf("payload = %#v, want status/on_battery", payload)
	}
	availability, err := AvailabilityMessage(Config{}, NodeInfo{ID: "serial-1234", Online: true})
	if err != nil {
		t.Fatalf("AvailabilityMessage() error = %v", err)
	}
	if availability.Topic != "wattkeeper/nodes/serial_1234/availability" || string(availability.Payload) != "online" {
		t.Fatalf("availability = %#v, want online availability topic", availability)
	}
}
