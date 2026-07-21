package mqtt

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultDiscoveryPrefix = "homeassistant"
	defaultStatePrefix     = "strom"
)

type Config struct {
	DiscoveryPrefix string
	StatePrefix     string
}

type NodeInfo struct {
	ID          string
	DisplayName string
	Hostname    string
	Location    string
	Online      bool
	CommsState  string
	Version     string
}

type UPSInfo struct {
	Name          string
	DisplayName   string
	Driver        string
	Status        string
	BatteryCharge *float64
	LoadPercent   *float64
	Runtime       *int64
	InputVoltage  *float64
	OnBattery     bool
	LowBattery    bool
	Commands      []CommandInfo
}

type CommandInfo struct {
	Name        string
	Description string
}

type PublishMessage struct {
	Topic   string
	Payload []byte
	Retain  bool
}

type discoveryPayload struct {
	Name                string            `json:"name"`
	UniqueID            string            `json:"unique_id"`
	StateTopic          string            `json:"state_topic,omitempty"`
	CommandTopic        string            `json:"command_topic,omitempty"`
	AvailabilityTopic   string            `json:"availability_topic"`
	PayloadAvailable    string            `json:"payload_available,omitempty"`
	PayloadNotAvailable string            `json:"payload_not_available,omitempty"`
	DeviceClass         string            `json:"device_class,omitempty"`
	StateClass          string            `json:"state_class,omitempty"`
	UnitOfMeasurement   string            `json:"unit_of_measurement,omitempty"`
	ValueTemplate       string            `json:"value_template,omitempty"`
	CommandTemplate     string            `json:"command_template,omitempty"`
	EntityCategory      string            `json:"entity_category,omitempty"`
	Icon                string            `json:"icon,omitempty"`
	Device              map[string]any    `json:"device"`
	Availability        []map[string]any  `json:"availability,omitempty"`
	PayloadPress        string            `json:"payload_press,omitempty"`
	JSONAttributesTopic string            `json:"json_attributes_topic,omitempty"`
	QoS                 int               `json:"qos,omitempty"`
	Extra               map[string]string `json:"-"`
}

func DiscoveryMessages(cfg Config, node NodeInfo, ups UPSInfo) ([]PublishMessage, error) {
	if strings.TrimSpace(node.ID) == "" {
		return nil, fmt.Errorf("node id is required")
	}
	if strings.TrimSpace(ups.Name) == "" {
		return nil, fmt.Errorf("ups name is required")
	}
	discoveryPrefix := defaultString(strings.TrimSpace(cfg.DiscoveryPrefix), defaultDiscoveryPrefix)
	statePrefix := defaultString(strings.TrimSpace(cfg.StatePrefix), defaultStatePrefix)
	baseID := slug(node.ID + "-" + ups.Name)
	availabilityTopic := fmt.Sprintf("%s/nodes/%s/availability", statePrefix, slug(node.ID))
	stateTopic := fmt.Sprintf("%s/nodes/%s/ups/%s/state", statePrefix, slug(node.ID), slug(ups.Name))
	commandTopic := fmt.Sprintf("%s/nodes/%s/ups/%s/command", statePrefix, slug(node.ID), slug(ups.Name))
	device := map[string]any{
		"identifiers":  []string{"strom-node-" + node.ID},
		"manufacturer": "Strom",
		"model":        firstNonEmpty(node.DisplayName, node.Hostname, node.ID),
		"name":         firstNonEmpty(node.DisplayName, node.Hostname, node.ID),
		"sw_version":   node.Version,
	}
	if strings.TrimSpace(node.Location) != "" {
		device["suggested_area"] = node.Location
	}
	availability := []map[string]any{{
		"topic": availabilityTopic,
	}}

	specs := []struct {
		component string
		objectID  string
		name      string
		deviceCls string
		stateCls  string
		unit      string
		template  string
		icon      string
	}{
		{component: "sensor", objectID: "battery_charge", name: upsEntityName(ups, "Battery charge"), deviceCls: "battery", stateCls: "measurement", unit: "%", template: "{{ value_json.battery_charge }}"},
		{component: "sensor", objectID: "load", name: upsEntityName(ups, "UPS load"), stateCls: "measurement", unit: "%", template: "{{ value_json.load_percent }}"},
		{component: "sensor", objectID: "runtime", name: upsEntityName(ups, "Battery runtime"), deviceCls: "duration", stateCls: "measurement", unit: "s", template: "{{ value_json.runtime_seconds }}"},
		{component: "sensor", objectID: "input_voltage", name: upsEntityName(ups, "Input voltage"), deviceCls: "voltage", stateCls: "measurement", unit: "V", template: "{{ value_json.input_voltage }}"},
		{component: "sensor", objectID: "status", name: upsEntityName(ups, "UPS status"), template: "{{ value_json.status }}"},
		{component: "binary_sensor", objectID: "on_battery", name: upsEntityName(ups, "On battery"), deviceCls: "power", template: "{{ value_json.on_battery }}"},
		{component: "binary_sensor", objectID: "low_battery", name: upsEntityName(ups, "Low battery"), deviceCls: "battery", template: "{{ value_json.low_battery }}"},
		{component: "binary_sensor", objectID: "online", name: upsEntityName(ups, "Online"), deviceCls: "connectivity", template: "{{ value_json.online }}"},
	}

	messages := make([]PublishMessage, 0, len(specs)+len(ups.Commands))
	for _, spec := range specs {
		payload := discoveryPayload{
			Name:                spec.name,
			UniqueID:            baseID + "-" + spec.objectID,
			StateTopic:          stateTopic,
			AvailabilityTopic:   availabilityTopic,
			PayloadAvailable:    "online",
			PayloadNotAvailable: "offline",
			DeviceClass:         spec.deviceCls,
			StateClass:          spec.stateCls,
			UnitOfMeasurement:   spec.unit,
			ValueTemplate:       spec.template,
			Icon:                spec.icon,
			Device:              device,
			Availability:        availability,
			QoS:                 1,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		messages = append(messages, PublishMessage{Topic: fmt.Sprintf("%s/%s/%s/config", discoveryPrefix, spec.component, baseID+"_"+spec.objectID), Payload: encoded, Retain: true})
	}

	sortedCommands := append([]CommandInfo(nil), ups.Commands...)
	sort.Slice(sortedCommands, func(i, j int) bool { return sortedCommands[i].Name < sortedCommands[j].Name })
	for _, command := range sortedCommands {
		payload := discoveryPayload{
			Name:                upsEntityName(ups, command.Name),
			UniqueID:            baseID + "-cmd-" + slug(command.Name),
			CommandTopic:        commandTopic,
			AvailabilityTopic:   availabilityTopic,
			PayloadAvailable:    "online",
			PayloadNotAvailable: "offline",
			CommandTemplate:     command.Name,
			Device:              device,
			Availability:        availability,
			PayloadPress:        command.Name,
			QoS:                 1,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		messages = append(messages, PublishMessage{Topic: fmt.Sprintf("%s/button/%s_cmd_%s/config", discoveryPrefix, baseID, slug(command.Name)), Payload: encoded, Retain: true})
	}

	return messages, nil
}

func StateMessage(cfg Config, node NodeInfo, ups UPSInfo) (PublishMessage, error) {
	if strings.TrimSpace(node.ID) == "" || strings.TrimSpace(ups.Name) == "" {
		return PublishMessage{}, fmt.Errorf("node id and ups name are required")
	}
	statePrefix := defaultString(strings.TrimSpace(cfg.StatePrefix), defaultStatePrefix)
	payload := map[string]any{
		"name":        ups.Name,
		"status":      ups.Status,
		"on_battery":  ups.OnBattery,
		"low_battery": ups.LowBattery,
		"online":      node.Online && node.CommsState != "offline",
		"comms_state": node.CommsState,
		"node_id":     node.ID,
		"node_name":   firstNonEmpty(node.DisplayName, node.Hostname, node.ID),
		"updated_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if ups.BatteryCharge != nil {
		payload["battery_charge"] = *ups.BatteryCharge
	}
	if ups.LoadPercent != nil {
		payload["load_percent"] = *ups.LoadPercent
	}
	if ups.Runtime != nil {
		payload["runtime_seconds"] = *ups.Runtime
	}
	if ups.InputVoltage != nil {
		payload["input_voltage"] = *ups.InputVoltage
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return PublishMessage{}, err
	}
	return PublishMessage{Topic: fmt.Sprintf("%s/nodes/%s/ups/%s/state", statePrefix, slug(node.ID), slug(ups.Name)), Payload: encoded, Retain: true}, nil
}

func AvailabilityMessage(cfg Config, node NodeInfo) (PublishMessage, error) {
	if strings.TrimSpace(node.ID) == "" {
		return PublishMessage{}, fmt.Errorf("node id is required")
	}
	statePrefix := defaultString(strings.TrimSpace(cfg.StatePrefix), defaultStatePrefix)
	status := "offline"
	if node.Online {
		status = "online"
	}
	return PublishMessage{Topic: fmt.Sprintf("%s/nodes/%s/availability", statePrefix, slug(node.ID)), Payload: []byte(status), Retain: true}, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "_", "/", "_", ".", "_", ":", "_", "-", "_")
	value = replacer.Replace(value)
	value = strings.Trim(value, "_")
	if value == "" {
		return "item"
	}
	return value
}

func upsEntityName(ups UPSInfo, suffix string) string {
	base := firstNonEmpty(ups.DisplayName, ups.Name, "UPS")
	return base + " " + suffix
}
