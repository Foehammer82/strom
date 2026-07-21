package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Foehammer82/strom/controller/internal/registry"
)

const (
	KindOnBattery   = "on_battery"
	KindLowBattery  = "low_battery"
	KindNodeOffline = "node_offline"
	KindCommsLost   = "comms_lost"
)

type deliveryPayload struct {
	RuleID     int64     `json:"rule_id"`
	Kind       string    `json:"kind"`
	NodeID     string    `json:"node_id"`
	UPSID      string    `json:"ups_id,omitempty"`
	SubjectKey string    `json:"subject_key"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

type Deliverer interface {
	Deliver(context.Context, registry.AlertRule, registry.AlertEvent) error
}

type WebhookDeliverer struct {
	Client *http.Client
}

func (d *WebhookDeliverer) Deliver(ctx context.Context, rule registry.AlertRule, event registry.AlertEvent) error {
	if strings.TrimSpace(rule.WebhookURL) == "" {
		return fmt.Errorf("webhook_url is required")
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(deliveryPayload{RuleID: rule.ID, Kind: event.Kind, NodeID: event.NodeID, UPSID: event.UPSID, SubjectKey: event.SubjectKey, Message: event.Message, CreatedAt: event.CreatedAt})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rule.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

type Engine struct {
	Logger           *log.Logger
	Store            *registry.Store
	Deliverer        Deliverer
	Now              func() time.Time
	NodeOfflineAfter time.Duration
}

func (e *Engine) EvaluateOnce(ctx context.Context) error {
	if e.Store == nil {
		return fmt.Errorf("registry store is required")
	}
	now := time.Now().UTC()
	if e.Now != nil {
		now = e.Now().UTC()
	}
	rules, err := e.Store.ListAlertRules(ctx)
	if err != nil {
		return err
	}
	nodes, err := e.Store.ListAdoptedNodes(ctx)
	if err != nil {
		return err
	}
	nodeOfflineAfter := e.NodeOfflineAfter
	if nodeOfflineAfter <= 0 {
		nodeOfflineAfter = 45 * time.Second
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		for _, node := range nodes {
			var events []registry.AlertEvent
			switch rule.Kind {
			case KindNodeOffline:
				if !node.LastSeen.IsZero() && now.Sub(node.LastSeen) > nodeOfflineAfter {
					events = append(events, registry.AlertEvent{RuleID: rule.ID, NodeID: node.ID, SubjectKey: node.ID, Kind: rule.Kind, Message: fmt.Sprintf("node %s has not been seen since %s", node.ID, node.LastSeen.Format(time.RFC3339)), CreatedAt: now})
				}
			case KindCommsLost:
				if node.CommsState == registry.CommsStateDegraded || node.CommsState == registry.CommsStateOffline {
					events = append(events, registry.AlertEvent{RuleID: rule.ID, NodeID: node.ID, SubjectKey: node.ID, Kind: rule.Kind, Message: fmt.Sprintf("node %s communications state is %s", node.ID, node.CommsState), CreatedAt: now})
				}
			case KindOnBattery, KindLowBattery:
				summaries, summaryErr := e.Store.ListNodeUPSSummaries(ctx, node.ID)
				if summaryErr != nil {
					if e.Logger != nil {
						e.Logger.Printf("alert summary load failed node=%s: %v", node.ID, summaryErr)
					}
					continue
				}
				for _, summary := range summaries {
					subjectKey := node.ID + ":" + summary.Name
					if rule.Kind == KindOnBattery && strings.Contains(strings.ToUpper(summary.Status), "OB") {
						events = append(events, registry.AlertEvent{RuleID: rule.ID, NodeID: node.ID, UPSID: subjectKey, SubjectKey: subjectKey, Kind: rule.Kind, Message: fmt.Sprintf("UPS %s on node %s is on battery (%s)", summary.Name, node.ID, summary.Status), CreatedAt: now})
					}
					if rule.Kind == KindLowBattery && rule.Threshold != nil && summary.BatteryChargePercent != nil && *summary.BatteryChargePercent <= *rule.Threshold {
						events = append(events, registry.AlertEvent{RuleID: rule.ID, NodeID: node.ID, UPSID: subjectKey, SubjectKey: subjectKey, Kind: rule.Kind, Message: fmt.Sprintf("UPS %s on node %s battery is %.1f%%", summary.Name, node.ID, *summary.BatteryChargePercent), CreatedAt: now})
					}
				}
			}
			for _, event := range events {
				if err := e.maybeFireEvent(ctx, rule, event); err != nil && e.Logger != nil {
					e.Logger.Printf("alert evaluation failed rule=%d subject=%s: %v", rule.ID, event.SubjectKey, err)
				}
			}
		}
	}
	return nil
}

func (e *Engine) maybeFireEvent(ctx context.Context, rule registry.AlertRule, event registry.AlertEvent) error {
	if last, found, err := e.Store.LastAlertEvent(ctx, rule.ID, event.SubjectKey); err != nil {
		return err
	} else if found && event.CreatedAt.Sub(last.CreatedAt) < time.Duration(rule.DebounceSeconds)*time.Second {
		return nil
	}
	deliverer := e.Deliverer
	if deliverer == nil {
		deliverer = &WebhookDeliverer{}
	}
	deliveryErr := deliverer.Deliver(ctx, rule, event)
	event.Delivered = deliveryErr == nil
	if deliveryErr != nil {
		event.DeliveryError = deliveryErr.Error()
	}
	_, err := e.Store.InsertAlertEvent(ctx, event)
	return err
}

func (e *Engine) TestRule(ctx context.Context, rule registry.AlertRule) (registry.AlertEvent, error) {
	now := time.Now().UTC()
	if e.Now != nil {
		now = e.Now().UTC()
	}
	event := registry.AlertEvent{RuleID: rule.ID, NodeID: "test-node", SubjectKey: fmt.Sprintf("test:%d", rule.ID), Kind: rule.Kind, Message: fmt.Sprintf("test event for %s", rule.Kind), CreatedAt: now}
	deliverer := e.Deliverer
	if deliverer == nil {
		deliverer = &WebhookDeliverer{}
	}
	deliveryErr := deliverer.Deliver(ctx, rule, event)
	event.Delivered = deliveryErr == nil
	if deliveryErr != nil {
		event.DeliveryError = deliveryErr.Error()
	}
	return e.Store.InsertAlertEvent(ctx, event)
}
