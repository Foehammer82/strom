package registry

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

var ErrNodeNotFound = errors.New("node not found")
var ErrUPSNotFound = errors.New("ups not found")
var ErrAlertRuleNotFound = errors.New("alert rule not found")

const (
	CommsStateUnknown  = "unknown"
	CommsStateHealthy  = "healthy"
	CommsStateDegraded = "degraded"
	CommsStateOffline  = "offline"
)

type Node struct {
	ID            string    `json:"id"`
	Instance      string    `json:"instance"`
	Hostname      string    `json:"hostname"`
	Address       string    `json:"address"`
	Port          int       `json:"port"`
	Version       string    `json:"version"`
	UPSCount      int       `json:"ups_count"`
	DisplayName   string    `json:"display_name"`
	LocationLabel string    `json:"location_label"`
	SiteLabel     string    `json:"site_label"`
	Adopted       bool      `json:"adopted"`
	AdoptedAt     time.Time `json:"adopted_at"`
	CommsState    string    `json:"comms_state"`
	PollFailures  int       `json:"poll_failures"`
	LastPolledAt  time.Time `json:"last_polled_at"`
	LastPollError string    `json:"last_poll_error"`
	LastSeen      time.Time `json:"last_seen"`
}

type NodeMetadataPatch struct {
	DisplayName   *string
	LocationLabel *string
	SiteLabel     *string
}

type Trust struct {
	ControllerURL  string
	TLSPort        int
	TLSFingerprint string
	NUTUser        string
	APITokenEnc    string
	NUTPasswordEnc string
}

type UPSSnapshot struct {
	Name      string
	Driver    string
	Variables map[string]string
}

type UPSLatestSample struct {
	Name                 string
	Driver               string
	Status               string
	BatteryChargePercent *float64
	LoadPercent          *float64
	RuntimeSeconds       *int64
	CapturedAt           time.Time
}

type UPSDetail struct {
	Name       string
	Driver     string
	Variables  map[string]string
	CapturedAt time.Time
}

type UPSHistorySample struct {
	Variable   string
	Value      string
	CapturedAt time.Time
}

type PollState struct {
	CommsState    string
	PollFailures  int
	LastPolledAt  time.Time
	LastPollError string
}

type AlertRule struct {
	ID              int64     `json:"id"`
	Kind            string    `json:"kind"`
	UPSVar          string    `json:"ups_var,omitempty"`
	Threshold       *float64  `json:"threshold,omitempty"`
	WebhookURL      string    `json:"webhook_url"`
	DebounceSeconds int       `json:"debounce_seconds"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
}

type AlertRulePatch struct {
	Kind            *string
	UPSVar          *string
	Threshold       *float64
	ThresholdSet    bool
	WebhookURL      *string
	DebounceSeconds *int
	Enabled         *bool
}

type AlertEvent struct {
	ID            int64     `json:"id"`
	RuleID        int64     `json:"rule_id"`
	NodeID        string    `json:"node_id"`
	UPSID         string    `json:"ups_id,omitempty"`
	SubjectKey    string    `json:"subject_key"`
	Kind          string    `json:"kind"`
	Message       string    `json:"message"`
	CreatedAt     time.Time `json:"created_at"`
	Delivered     bool      `json:"delivered"`
	DeliveryError string    `json:"delivery_error,omitempty"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if err := applySchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func applySchema(db *sql.DB) error {
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	for _, statement := range []string{
		`ALTER TABLE nodes ADD COLUMN adopted_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN location_label TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN site_label TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN controller_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN tls_port INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN tls_fingerprint TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN nut_user TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN api_token_enc TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN nut_password_enc TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN comms_state TEXT NOT NULL DEFAULT 'unknown'`,
		`ALTER TABLE nodes ADD COLUMN poll_failures INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN last_polled_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN last_poll_error TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(statement); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("apply node schema migration: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) UpsertDiscoveredNode(ctx context.Context, node Node) error {
	if node.ID == "" {
		return errors.New("node id is required")
	}
	if node.LastSeen.IsZero() {
		node.LastSeen = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO nodes (id, instance, hostname, address, port, version, ups_count, adopted, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT adopted FROM nodes WHERE id = ?), 0), ?)
		ON CONFLICT(id) DO UPDATE SET
			instance = excluded.instance,
			hostname = excluded.hostname,
			address = excluded.address,
			port = excluded.port,
			version = excluded.version,
			ups_count = excluded.ups_count,
			last_seen = excluded.last_seen
	`, node.ID, node.Instance, node.Hostname, node.Address, node.Port, node.Version, node.UPSCount, node.ID, node.LastSeen.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("upsert discovered node %s: %w", node.ID, err)
	}
	return nil
}

func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, instance, hostname, address, port, version, ups_count, display_name, location_label, site_label, adopted, adopted_at, comms_state, poll_failures, last_polled_at, last_poll_error, last_seen
		FROM nodes
		ORDER BY adopted DESC, last_seen DESC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	return nodes, nil
}

func (s *Store) ListAdoptedNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, instance, hostname, address, port, version, ups_count, display_name, location_label, site_label, adopted, adopted_at, comms_state, poll_failures, last_polled_at, last_poll_error, last_seen
		FROM nodes
		WHERE adopted = 1
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list adopted nodes: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate adopted nodes: %w", err)
	}
	return nodes, nil
}

func (s *Store) GetNode(ctx context.Context, id string) (Node, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, instance, hostname, address, port, version, ups_count, display_name, location_label, site_label, adopted, adopted_at, comms_state, poll_failures, last_polled_at, last_poll_error, last_seen
		FROM nodes
		WHERE id = ?
	`, id)
	node, err := scanNode(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Node{}, ErrNodeNotFound
		}
		return Node{}, err
	}
	return node, nil
}

func (s *Store) SetNodeAdopted(ctx context.Context, id string, adopted bool) error {
	adoptedAt := ""
	if adopted {
		adoptedAt = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET adopted = ?, adopted_at = ? WHERE id = ?`, boolToInt(adopted), adoptedAt, id)
	if err != nil {
		return fmt.Errorf("set adopted on node %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read adoption update count: %w", err)
	}
	if rows == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (s *Store) SaveNodeTrust(ctx context.Context, id string, trust Trust) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE nodes
		SET controller_url = ?, tls_port = ?, tls_fingerprint = ?, nut_user = ?, api_token_enc = ?, nut_password_enc = ?
		WHERE id = ?
	`, trust.ControllerURL, trust.TLSPort, trust.TLSFingerprint, trust.NUTUser, trust.APITokenEnc, trust.NUTPasswordEnc, id)
	if err != nil {
		return fmt.Errorf("save node trust %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read trust update count: %w", err)
	}
	if rows == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (s *Store) LoadNodeTrust(ctx context.Context, id string) (Trust, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT controller_url, tls_port, tls_fingerprint, nut_user, api_token_enc, nut_password_enc
		FROM nodes WHERE id = ?
	`, id)
	var trust Trust
	if err := row.Scan(&trust.ControllerURL, &trust.TLSPort, &trust.TLSFingerprint, &trust.NUTUser, &trust.APITokenEnc, &trust.NUTPasswordEnc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Trust{}, ErrNodeNotFound
		}
		return Trust{}, fmt.Errorf("load node trust %s: %w", id, err)
	}
	return trust, nil
}

func (s *Store) UpdateNodePollState(ctx context.Context, id string, state PollState) error {
	commsState := strings.TrimSpace(state.CommsState)
	if commsState == "" {
		commsState = CommsStateUnknown
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE nodes
		SET comms_state = ?, poll_failures = ?, last_polled_at = ?, last_poll_error = ?
		WHERE id = ?
	`, commsState, state.PollFailures, formatOptionalTime(state.LastPolledAt), strings.TrimSpace(state.LastPollError), id)
	if err != nil {
		return fmt.Errorf("update node poll state %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read poll state update count: %w", err)
	}
	if rows == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (s *Store) UpdateNodeMetadata(ctx context.Context, id string, patch NodeMetadataPatch) error {
	assignments := make([]string, 0, 3)
	args := make([]any, 0, 4)
	if patch.DisplayName != nil {
		assignments = append(assignments, "display_name = ?")
		args = append(args, strings.TrimSpace(*patch.DisplayName))
	}
	if patch.LocationLabel != nil {
		assignments = append(assignments, "location_label = ?")
		args = append(args, strings.TrimSpace(*patch.LocationLabel))
	}
	if patch.SiteLabel != nil {
		assignments = append(assignments, "site_label = ?")
		args = append(args, strings.TrimSpace(*patch.SiteLabel))
	}
	if len(assignments) == 0 {
		return errors.New("node metadata patch is empty")
	}
	args = append(args, id)
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET `+strings.Join(assignments, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("update node metadata %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read metadata update count: %w", err)
	}
	if rows == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (s *Store) DeleteNode(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete node %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read delete count: %w", err)
	}
	if rows == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (s *Store) RecordUPSSnapshots(ctx context.Context, nodeID string, capturedAt time.Time, snapshots []UPSSnapshot) error {
	if strings.TrimSpace(nodeID) == "" {
		return errors.New("node id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin UPS sample transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	activeIDs := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		upsName := strings.TrimSpace(snapshot.Name)
		if upsName == "" {
			continue
		}
		upsID := nodeID + ":" + upsName
		activeIDs = append(activeIDs, upsID)
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO ups (id, node_id, name, driver)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				node_id = excluded.node_id,
				name = excluded.name,
				driver = excluded.driver
		`, upsID, nodeID, upsName, strings.TrimSpace(snapshot.Driver)); err != nil {
			return fmt.Errorf("upsert ups %s: %w", upsID, err)
		}

		keys := make([]string, 0, len(snapshot.Variables))
		for key := range snapshot.Variables {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err = tx.ExecContext(ctx, `
				INSERT INTO samples (ups_id, variable, value, captured_at)
				VALUES (?, ?, ?, ?)
			`, upsID, key, snapshot.Variables[key], capturedAt.UTC().Format(time.RFC3339)); err != nil {
				return fmt.Errorf("insert sample %s/%s: %w", upsID, key, err)
			}
		}
	}

	if len(activeIDs) == 0 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM ups WHERE node_id = ?`, nodeID); err != nil {
			return fmt.Errorf("clear UPS inventory for node %s: %w", nodeID, err)
		}
	} else {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(activeIDs)), ",")
		args := make([]any, 0, len(activeIDs)+1)
		args = append(args, nodeID)
		for _, id := range activeIDs {
			args = append(args, id)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM ups WHERE node_id = ? AND id NOT IN (`+placeholders+`)`, args...); err != nil {
			return fmt.Errorf("prune stale UPS inventory for node %s: %w", nodeID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit UPS sample transaction: %w", err)
	}
	return nil
}

func (s *Store) PruneSamplesBefore(ctx context.Context, cutoff time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE captured_at < ?`, cutoff.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("prune samples before %s: %w", cutoff.UTC().Format(time.RFC3339), err)
	}
	return nil
}

func (s *Store) CountUPSForNode(ctx context.Context, nodeID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ups WHERE node_id = ?`, nodeID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count UPS rows for node %s: %w", nodeID, err)
	}
	return count, nil
}

func (s *Store) CountSamples(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM samples`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count sample rows: %w", err)
	}
	return count, nil
}

func (s *Store) CreateAlertRule(ctx context.Context, rule AlertRule) (AlertRule, error) {
	if strings.TrimSpace(rule.Kind) == "" {
		return AlertRule{}, errors.New("alert rule kind is required")
	}
	if strings.TrimSpace(rule.WebhookURL) == "" {
		return AlertRule{}, errors.New("alert rule webhook_url is required")
	}
	if rule.DebounceSeconds <= 0 {
		rule.DebounceSeconds = 300
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now().UTC()
	}
	threshold := sql.NullFloat64{}
	if rule.Threshold != nil {
		threshold = sql.NullFloat64{Float64: *rule.Threshold, Valid: true}
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO alert_rules (kind, ups_var, threshold, webhook_url, debounce_seconds, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, strings.TrimSpace(rule.Kind), strings.TrimSpace(rule.UPSVar), threshold, strings.TrimSpace(rule.WebhookURL), rule.DebounceSeconds, boolToInt(rule.Enabled), rule.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return AlertRule{}, fmt.Errorf("create alert rule: %w", err)
	}
	ruleID, err := result.LastInsertId()
	if err != nil {
		return AlertRule{}, fmt.Errorf("read alert rule id: %w", err)
	}
	return s.GetAlertRule(ctx, ruleID)
}

func (s *Store) ListAlertRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, ups_var, threshold, webhook_url, debounce_seconds, enabled, created_at
		FROM alert_rules
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}
	defer rows.Close()
	var rules []AlertRule
	for rows.Next() {
		rule, err := scanAlertRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alert rules: %w", err)
	}
	return rules, nil
}

func (s *Store) GetAlertRule(ctx context.Context, id int64) (AlertRule, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, kind, ups_var, threshold, webhook_url, debounce_seconds, enabled, created_at
		FROM alert_rules WHERE id = ?
	`, id)
	rule, err := scanAlertRule(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AlertRule{}, ErrAlertRuleNotFound
		}
		return AlertRule{}, err
	}
	return rule, nil
}

func (s *Store) UpdateAlertRule(ctx context.Context, id int64, patch AlertRulePatch) (AlertRule, error) {
	assignments := make([]string, 0, 6)
	args := make([]any, 0, 7)
	if patch.Kind != nil {
		assignments = append(assignments, "kind = ?")
		args = append(args, strings.TrimSpace(*patch.Kind))
	}
	if patch.UPSVar != nil {
		assignments = append(assignments, "ups_var = ?")
		args = append(args, strings.TrimSpace(*patch.UPSVar))
	}
	if patch.ThresholdSet {
		assignments = append(assignments, "threshold = ?")
		if patch.Threshold == nil {
			args = append(args, nil)
		} else {
			args = append(args, *patch.Threshold)
		}
	}
	if patch.WebhookURL != nil {
		assignments = append(assignments, "webhook_url = ?")
		args = append(args, strings.TrimSpace(*patch.WebhookURL))
	}
	if patch.DebounceSeconds != nil {
		assignments = append(assignments, "debounce_seconds = ?")
		args = append(args, *patch.DebounceSeconds)
	}
	if patch.Enabled != nil {
		assignments = append(assignments, "enabled = ?")
		args = append(args, boolToInt(*patch.Enabled))
	}
	if len(assignments) == 0 {
		return AlertRule{}, errors.New("alert rule patch is empty")
	}
	args = append(args, id)
	result, err := s.db.ExecContext(ctx, `UPDATE alert_rules SET `+strings.Join(assignments, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return AlertRule{}, fmt.Errorf("update alert rule %d: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return AlertRule{}, fmt.Errorf("read alert rule update count: %w", err)
	}
	if rows == 0 {
		return AlertRule{}, ErrAlertRuleNotFound
	}
	return s.GetAlertRule(ctx, id)
}

func (s *Store) DeleteAlertRule(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete alert rule %d: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read alert rule delete count: %w", err)
	}
	if rows == 0 {
		return ErrAlertRuleNotFound
	}
	return nil
}

func (s *Store) InsertAlertEvent(ctx context.Context, event AlertEvent) (AlertEvent, error) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO alert_events (rule_id, node_id, ups_id, subject_key, kind, message, created_at, delivered, delivery_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.RuleID, strings.TrimSpace(event.NodeID), strings.TrimSpace(event.UPSID), strings.TrimSpace(event.SubjectKey), strings.TrimSpace(event.Kind), strings.TrimSpace(event.Message), event.CreatedAt.UTC().Format(time.RFC3339), boolToInt(event.Delivered), strings.TrimSpace(event.DeliveryError))
	if err != nil {
		return AlertEvent{}, fmt.Errorf("insert alert event: %w", err)
	}
	eventID, err := result.LastInsertId()
	if err != nil {
		return AlertEvent{}, fmt.Errorf("read alert event id: %w", err)
	}
	event.ID = eventID
	return event, nil
}

func (s *Store) ListAlertEvents(ctx context.Context, limit int) ([]AlertEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, rule_id, node_id, ups_id, subject_key, kind, message, created_at, delivered, delivery_error
		FROM alert_events
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list alert events: %w", err)
	}
	defer rows.Close()
	var events []AlertEvent
	for rows.Next() {
		event, err := scanAlertEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alert events: %w", err)
	}
	return events, nil
}

func (s *Store) LastAlertEvent(ctx context.Context, ruleID int64, subjectKey string) (AlertEvent, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, rule_id, node_id, ups_id, subject_key, kind, message, created_at, delivered, delivery_error
		FROM alert_events
		WHERE rule_id = ? AND subject_key = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, ruleID, strings.TrimSpace(subjectKey))
	event, err := scanAlertEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AlertEvent{}, false, nil
		}
		return AlertEvent{}, false, err
	}
	return event, true, nil
}

func (s *Store) ListNodeUPSSummaries(ctx context.Context, nodeID string) ([]UPSLatestSample, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.name, u.driver, latest.variable, latest.value, latest.captured_at
		FROM ups u
		LEFT JOIN (
			SELECT s1.ups_id, s1.variable, s1.value, s1.captured_at
			FROM samples s1
			JOIN (
				SELECT ups_id, variable, MAX(captured_at) AS captured_at
				FROM samples
				GROUP BY ups_id, variable
			) newest
			ON newest.ups_id = s1.ups_id
			AND newest.variable = s1.variable
			AND newest.captured_at = s1.captured_at
		) latest ON latest.ups_id = u.id
		WHERE u.node_id = ?
		ORDER BY u.name ASC, latest.variable ASC
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list UPS summaries for node %s: %w", nodeID, err)
	}
	defer rows.Close()

	type aggregate struct {
		summary UPSLatestSample
		seen    bool
	}
	ordered := make([]string, 0)
	byID := make(map[string]*aggregate)
	for rows.Next() {
		var upsID string
		var name string
		var driver string
		var variable sql.NullString
		var value sql.NullString
		var capturedAt sql.NullString
		if err := rows.Scan(&upsID, &name, &driver, &variable, &value, &capturedAt); err != nil {
			return nil, fmt.Errorf("scan UPS summary row: %w", err)
		}
		agg, ok := byID[upsID]
		if !ok {
			agg = &aggregate{summary: UPSLatestSample{Name: name, Driver: driver}}
			byID[upsID] = agg
			ordered = append(ordered, upsID)
		}
		if variable.Valid && value.Valid {
			agg.seen = true
			applyLatestVariable(&agg.summary, variable.String, value.String)
		}
		if capturedAt.Valid {
			parsed, parseErr := time.Parse(time.RFC3339, capturedAt.String)
			if parseErr != nil {
				return nil, fmt.Errorf("parse UPS sample captured_at: %w", parseErr)
			}
			if agg.summary.CapturedAt.IsZero() || parsed.After(agg.summary.CapturedAt) {
				agg.summary.CapturedAt = parsed
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate UPS summaries: %w", err)
	}

	summaries := make([]UPSLatestSample, 0, len(ordered))
	for _, upsID := range ordered {
		summaries = append(summaries, byID[upsID].summary)
	}
	return summaries, nil
}

func (s *Store) GetUPSDetail(ctx context.Context, nodeID, upsName string) (UPSDetail, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.name, u.driver
		FROM ups u
		WHERE u.node_id = ? AND u.name = ?
	`, nodeID, upsName)
	var upsID string
	var detail UPSDetail
	if err := row.Scan(&upsID, &detail.Name, &detail.Driver); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UPSDetail{}, ErrUPSNotFound
		}
		return UPSDetail{}, fmt.Errorf("load UPS detail %s/%s: %w", nodeID, upsName, err)
	}
	detail.Variables = make(map[string]string)

	rows, err := s.db.QueryContext(ctx, `
		SELECT s1.variable, s1.value, s1.captured_at
		FROM samples s1
		JOIN (
			SELECT variable, MAX(captured_at) AS captured_at
			FROM samples
			WHERE ups_id = ?
			GROUP BY variable
		) newest
		ON newest.variable = s1.variable AND newest.captured_at = s1.captured_at
		WHERE s1.ups_id = ?
		ORDER BY s1.variable ASC
	`, upsID, upsID)
	if err != nil {
		return UPSDetail{}, fmt.Errorf("list UPS detail samples %s/%s: %w", nodeID, upsName, err)
	}
	defer rows.Close()
	for rows.Next() {
		var variable string
		var value string
		var capturedAt string
		if err := rows.Scan(&variable, &value, &capturedAt); err != nil {
			return UPSDetail{}, fmt.Errorf("scan UPS detail sample: %w", err)
		}
		detail.Variables[variable] = value
		parsed, err := time.Parse(time.RFC3339, capturedAt)
		if err != nil {
			return UPSDetail{}, fmt.Errorf("parse UPS detail captured_at: %w", err)
		}
		if detail.CapturedAt.IsZero() || parsed.After(detail.CapturedAt) {
			detail.CapturedAt = parsed
		}
	}
	if err := rows.Err(); err != nil {
		return UPSDetail{}, fmt.Errorf("iterate UPS detail samples: %w", err)
	}
	return detail, nil
}

func (s *Store) ListUPSHistory(ctx context.Context, nodeID, upsName string, limit int) ([]UPSHistorySample, error) {
	return s.ListUPSHistoryFiltered(ctx, nodeID, upsName, "", time.Time{}, limit)
}

func (s *Store) ListUPSHistoryFiltered(ctx context.Context, nodeID, upsName, variable string, since time.Time, limit int) ([]UPSHistorySample, error) {
	if limit <= 0 {
		limit = 200
	}
	row := s.db.QueryRowContext(ctx, `SELECT id FROM ups WHERE node_id = ? AND name = ?`, nodeID, upsName)
	var upsID string
	if err := row.Scan(&upsID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUPSNotFound
		}
		return nil, fmt.Errorf("load UPS history id %s/%s: %w", nodeID, upsName, err)
	}
	query := `
		SELECT variable, value, captured_at
		FROM samples
		WHERE ups_id = ?
	`
	args := []any{upsID}
	if trimmedVariable := strings.TrimSpace(variable); trimmedVariable != "" {
		query += ` AND variable = ?`
		args = append(args, trimmedVariable)
	}
	if !since.IsZero() {
		query += ` AND captured_at >= ?`
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	query += ` ORDER BY captured_at DESC, variable ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list UPS history %s/%s: %w", nodeID, upsName, err)
	}
	defer rows.Close()
	history := make([]UPSHistorySample, 0, limit)
	for rows.Next() {
		var sample UPSHistorySample
		var capturedAt string
		if err := rows.Scan(&sample.Variable, &sample.Value, &capturedAt); err != nil {
			return nil, fmt.Errorf("scan UPS history sample: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339, capturedAt)
		if err != nil {
			return nil, fmt.Errorf("parse UPS history captured_at: %w", err)
		}
		sample.CapturedAt = parsed
		history = append(history, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate UPS history: %w", err)
	}
	return history, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanNode(row scanner) (Node, error) {
	var node Node
	var adopted int
	var adoptedAt string
	var lastPolledAt string
	var lastSeen string
	if err := row.Scan(&node.ID, &node.Instance, &node.Hostname, &node.Address, &node.Port, &node.Version, &node.UPSCount, &node.DisplayName, &node.LocationLabel, &node.SiteLabel, &adopted, &adoptedAt, &node.CommsState, &node.PollFailures, &lastPolledAt, &node.LastPollError, &lastSeen); err != nil {
		return Node{}, err
	}
	if adoptedAt != "" {
		parsedAdoptedAt, err := time.Parse(time.RFC3339, adoptedAt)
		if err != nil {
			return Node{}, fmt.Errorf("parse node adopted_at: %w", err)
		}
		node.AdoptedAt = parsedAdoptedAt
	}
	if lastPolledAt != "" {
		parsedLastPolledAt, err := time.Parse(time.RFC3339, lastPolledAt)
		if err != nil {
			return Node{}, fmt.Errorf("parse node last_polled_at: %w", err)
		}
		node.LastPolledAt = parsedLastPolledAt
	}
	parsed, err := time.Parse(time.RFC3339, lastSeen)
	if err != nil {
		return Node{}, fmt.Errorf("parse node last_seen: %w", err)
	}
	node.Adopted = adopted == 1
	if strings.TrimSpace(node.CommsState) == "" {
		node.CommsState = CommsStateUnknown
	}
	node.LastSeen = parsed
	return node, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func applyLatestVariable(summary *UPSLatestSample, variable, raw string) {
	if summary == nil {
		return
	}
	switch strings.TrimSpace(variable) {
	case "ups.status":
		summary.Status = strings.TrimSpace(raw)
	case "battery.charge":
		summary.BatteryChargePercent = parseOptionalFloat(raw)
	case "ups.load":
		summary.LoadPercent = parseOptionalFloat(raw)
	case "battery.runtime":
		summary.RuntimeSeconds = parseOptionalInt64(raw)
	}
}

func parseOptionalFloat(raw string) *float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil
	}
	return &parsed
}

func parseOptionalInt64(raw string) *int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func scanAlertRule(row scanner) (AlertRule, error) {
	var rule AlertRule
	var threshold sql.NullFloat64
	var enabled int
	var createdAt string
	if err := row.Scan(&rule.ID, &rule.Kind, &rule.UPSVar, &threshold, &rule.WebhookURL, &rule.DebounceSeconds, &enabled, &createdAt); err != nil {
		return AlertRule{}, err
	}
	if threshold.Valid {
		value := threshold.Float64
		rule.Threshold = &value
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return AlertRule{}, fmt.Errorf("parse alert rule created_at: %w", err)
	}
	rule.Enabled = enabled == 1
	rule.CreatedAt = parsedCreatedAt
	return rule, nil
}

func scanAlertEvent(row scanner) (AlertEvent, error) {
	var event AlertEvent
	var delivered int
	var createdAt string
	if err := row.Scan(&event.ID, &event.RuleID, &event.NodeID, &event.UPSID, &event.SubjectKey, &event.Kind, &event.Message, &createdAt, &delivered, &event.DeliveryError); err != nil {
		return AlertEvent{}, err
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return AlertEvent{}, fmt.Errorf("parse alert event created_at: %w", err)
	}
	event.Delivered = delivered == 1
	event.CreatedAt = parsedCreatedAt
	return event, nil
}
