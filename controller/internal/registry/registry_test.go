package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUpsertAndListNodes(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertDiscoveredNode(context.Background(), Node{
		ID:       "serial-1234",
		Instance: "wkeeper-node-1234",
		Hostname: "wkeeper-node-1234.local",
		Address:  "192.168.1.50",
		Port:     80,
		Version:  "v0.3.0",
		UPSCount: 2,
		LastSeen: now,
	}); err != nil {
		t.Fatalf("UpsertDiscoveredNode() error = %v", err)
	}

	nodes, err := store.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].ID != "serial-1234" || nodes[0].UPSCount != 2 || nodes[0].Adopted {
		t.Fatalf("node = %#v, want discovered pending node", nodes[0])
	}

	loaded, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if !loaded.LastSeen.Equal(now) {
		t.Fatalf("LastSeen = %v, want %v", loaded.LastSeen, now)
	}

	if err := store.SetNodeAdopted(context.Background(), "serial-1234", true); err != nil {
		t.Fatalf("SetNodeAdopted() error = %v", err)
	}

	adopted, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() after adopt error = %v", err)
	}
	if !adopted.Adopted {
		t.Fatalf("Adopted = %t, want true", adopted.Adopted)
	}
	if adopted.AdoptedAt.IsZero() {
		t.Fatal("AdoptedAt = zero, want timestamp")
	}

	pollState := PollState{
		CommsState:    CommsStateHealthy,
		PollFailures:  0,
		LastPolledAt:  now.Add(2 * time.Minute),
		LastPollError: "",
	}
	if err := store.UpdateNodePollState(context.Background(), "serial-1234", pollState); err != nil {
		t.Fatalf("UpdateNodePollState() error = %v", err)
	}
	polledNode, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() after poll state update error = %v", err)
	}
	if polledNode.CommsState != CommsStateHealthy || polledNode.PollFailures != 0 || !polledNode.LastPolledAt.Equal(pollState.LastPolledAt) {
		t.Fatalf("polled node = %#v, want updated poll state", polledNode)
	}

	displayName := "Lab Rack Node"
	locationLabel := "Utility Closet"
	siteLabel := "Home"
	if err := store.UpdateNodeMetadata(context.Background(), "serial-1234", NodeMetadataPatch{
		DisplayName:   &displayName,
		LocationLabel: &locationLabel,
		SiteLabel:     &siteLabel,
	}); err != nil {
		t.Fatalf("UpdateNodeMetadata() error = %v", err)
	}
	metadataNode, err := store.GetNode(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("GetNode() after metadata update error = %v", err)
	}
	if metadataNode.DisplayName != displayName || metadataNode.LocationLabel != locationLabel || metadataNode.SiteLabel != siteLabel {
		t.Fatalf("metadata node = %#v, want updated controller metadata", metadataNode)
	}

	adoptedNodes, err := store.ListAdoptedNodes(context.Background())
	if err != nil {
		t.Fatalf("ListAdoptedNodes() error = %v", err)
	}
	if len(adoptedNodes) != 1 || adoptedNodes[0].ID != "serial-1234" {
		t.Fatalf("adopted nodes = %#v, want serial-1234", adoptedNodes)
	}

	capturedAt := now.Add(5 * time.Minute)
	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", capturedAt, []UPSSnapshot{
		{
			Name:   "ups-a",
			Driver: "usbhid-ups",
			Variables: map[string]string{
				"battery.charge": "100",
				"ups.status":     "OL",
			},
		},
		{
			Name:   "ups-b",
			Driver: "blazer_usb",
			Variables: map[string]string{
				"battery.charge": "76",
			},
		},
	}); err != nil {
		t.Fatalf("RecordUPSSnapshots() error = %v", err)
	}

	var upsCount int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM ups WHERE node_id = ?`, "serial-1234").Scan(&upsCount); err != nil {
		t.Fatalf("count ups rows error = %v", err)
	}
	if upsCount != 2 {
		t.Fatalf("ups row count = %d, want 2", upsCount)
	}
	var sampleCount int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM samples`).Scan(&sampleCount); err != nil {
		t.Fatalf("count sample rows error = %v", err)
	}
	if sampleCount != 3 {
		t.Fatalf("sample row count = %d, want 3", sampleCount)
	}

	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", capturedAt.Add(5*time.Minute), []UPSSnapshot{
		{
			Name:   "ups-a",
			Driver: "usbhid-ups",
			Variables: map[string]string{
				"battery.charge": "95",
			},
		},
	}); err != nil {
		t.Fatalf("RecordUPSSnapshots() second error = %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM ups WHERE node_id = ?`, "serial-1234").Scan(&upsCount); err != nil {
		t.Fatalf("count ups rows after prune error = %v", err)
	}
	if upsCount != 1 {
		t.Fatalf("ups row count after prune = %d, want 1", upsCount)
	}

	if err := store.PruneSamplesBefore(context.Background(), capturedAt.Add(1*time.Minute)); err != nil {
		t.Fatalf("PruneSamplesBefore() error = %v", err)
	}
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM samples`).Scan(&sampleCount); err != nil {
		t.Fatalf("count sample rows after prune error = %v", err)
	}
	if sampleCount != 1 {
		t.Fatalf("sample row count after prune = %d, want 1", sampleCount)
	}

	summaries, err := store.ListNodeUPSSummaries(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("ListNodeUPSSummaries() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}
	if summaries[0].Name != "ups-a" || summaries[0].Driver != "usbhid-ups" {
		t.Fatalf("summary = %#v, want ups-a/usbhid-ups", summaries[0])
	}
	if summaries[0].BatteryChargePercent == nil || *summaries[0].BatteryChargePercent != 95 {
		t.Fatalf("summary = %#v, want battery charge 95", summaries[0])
	}
	detail, err := store.GetUPSDetail(context.Background(), "serial-1234", "ups-a")
	if err != nil {
		t.Fatalf("GetUPSDetail() error = %v", err)
	}
	if detail.Name != "ups-a" || detail.Driver != "usbhid-ups" {
		t.Fatalf("detail = %#v, want ups-a/usbhid-ups", detail)
	}
	if got := detail.Variables["battery.charge"]; got != "95" {
		t.Fatalf("detail variables = %#v, want battery.charge 95", detail.Variables)
	}
	history, err := store.ListUPSHistory(context.Background(), "serial-1234", "ups-a", 10)
	if err != nil {
		t.Fatalf("ListUPSHistory() error = %v", err)
	}
	if len(history) != 1 || history[0].Variable != "battery.charge" || history[0].Value != "95" {
		t.Fatalf("history = %#v, want latest ups-a sample history", history)
	}

	if err := store.RecordUPSSnapshots(context.Background(), "serial-1234", capturedAt.Add(10*time.Minute), []UPSSnapshot{
		{
			Name:   "ups-a",
			Driver: "usbhid-ups",
			Variables: map[string]string{
				"battery.charge":  "93",
				"battery.runtime": "1700",
			},
		},
	}); err != nil {
		t.Fatalf("RecordUPSSnapshots() filtered history seed error = %v", err)
	}
	filtered, err := store.ListUPSHistoryFiltered(context.Background(), "serial-1234", "ups-a", "battery.charge", capturedAt.Add(6*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListUPSHistoryFiltered() error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].Variable != "battery.charge" || filtered[0].Value != "93" {
		t.Fatalf("filtered history = %#v, want battery.charge=93 after cutoff", filtered)
	}

	trust := Trust{
		ControllerURL:  "https://controller.local",
		TLSPort:        8443,
		TLSFingerprint: "fingerprint",
		NUTUser:        "controller",
		APITokenEnc:    "enc-token",
		NUTPasswordEnc: "enc-pass",
	}
	if err := store.SaveNodeTrust(context.Background(), "serial-1234", trust); err != nil {
		t.Fatalf("SaveNodeTrust() error = %v", err)
	}
	loadedTrust, err := store.LoadNodeTrust(context.Background(), "serial-1234")
	if err != nil {
		t.Fatalf("LoadNodeTrust() error = %v", err)
	}
	if loadedTrust != trust {
		t.Fatalf("trust = %#v, want %#v", loadedTrust, trust)
	}

	threshold := 20.0
	rule, err := store.CreateAlertRule(context.Background(), AlertRule{Kind: "low_battery", UPSVar: "battery.charge", Threshold: &threshold, WebhookURL: "http://example.invalid/hook", DebounceSeconds: 120, Enabled: true})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if rule.ID == 0 || rule.Kind != "low_battery" || rule.Threshold == nil || *rule.Threshold != threshold {
		t.Fatalf("rule = %#v, want persisted low_battery rule", rule)
	}
	rules, err := store.ListAlertRules(context.Background())
	if err != nil {
		t.Fatalf("ListAlertRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("rules = %#v, want single created rule", rules)
	}
	updatedDebounce := 300
	enabled := false
	updatedRule, err := store.UpdateAlertRule(context.Background(), rule.ID, AlertRulePatch{DebounceSeconds: &updatedDebounce, Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdateAlertRule() error = %v", err)
	}
	if updatedRule.DebounceSeconds != 300 || updatedRule.Enabled {
		t.Fatalf("updated rule = %#v, want debounce 300 enabled false", updatedRule)
	}
	event, err := store.InsertAlertEvent(context.Background(), AlertEvent{RuleID: rule.ID, NodeID: "serial-1234", UPSID: "serial-1234:ups-a", SubjectKey: "serial-1234:ups-a", Kind: "low_battery", Message: "battery low", CreatedAt: capturedAt.Add(20 * time.Minute), Delivered: true})
	if err != nil {
		t.Fatalf("InsertAlertEvent() error = %v", err)
	}
	events, err := store.ListAlertEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListAlertEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("events = %#v, want inserted event", events)
	}
	lastEvent, found, err := store.LastAlertEvent(context.Background(), rule.ID, "serial-1234:ups-a")
	if err != nil {
		t.Fatalf("LastAlertEvent() error = %v", err)
	}
	if !found || lastEvent.ID != event.ID {
		t.Fatalf("last event = %#v found=%t, want inserted event", lastEvent, found)
	}
	if err := store.DeleteAlertRule(context.Background(), rule.ID); err != nil {
		t.Fatalf("DeleteAlertRule() error = %v", err)
	}
	rules, err = store.ListAlertRules(context.Background())
	if err != nil {
		t.Fatalf("ListAlertRules() after delete error = %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules after delete = %#v, want none", rules)
	}

	if err := store.DeleteNode(context.Background(), "serial-1234"); err != nil {
		t.Fatalf("DeleteNode() error = %v", err)
	}
	if _, err := store.GetNode(context.Background(), "serial-1234"); err != ErrNodeNotFound {
		t.Fatalf("GetNode() after delete error = %v, want ErrNodeNotFound", err)
	}
	if _, err := store.LoadNodeTrust(context.Background(), "serial-1234"); err != ErrNodeNotFound {
		t.Fatalf("LoadNodeTrust() after delete error = %v, want ErrNodeNotFound", err)
	}
}
