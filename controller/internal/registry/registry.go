package registry

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

var ErrNodeNotFound = errors.New("node not found")

type Node struct {
	ID        string    `json:"id"`
	Instance  string    `json:"instance"`
	Hostname  string    `json:"hostname"`
	Address   string    `json:"address"`
	Port      int       `json:"port"`
	Version   string    `json:"version"`
	UPSCount  int       `json:"ups_count"`
	Adopted   bool      `json:"adopted"`
	AdoptedAt time.Time `json:"adopted_at"`
	LastSeen  time.Time `json:"last_seen"`
}

type Trust struct {
	ControllerURL  string
	TLSPort        int
	TLSFingerprint string
	NUTUser        string
	APITokenEnc    string
	NUTPasswordEnc string
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
		`ALTER TABLE nodes ADD COLUMN controller_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN tls_port INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN tls_fingerprint TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN nut_user TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN api_token_enc TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN nut_password_enc TEXT NOT NULL DEFAULT ''`,
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
		SELECT id, instance, hostname, address, port, version, ups_count, adopted, adopted_at, last_seen
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

func (s *Store) GetNode(ctx context.Context, id string) (Node, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, instance, hostname, address, port, version, ups_count, adopted, adopted_at, last_seen
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

type scanner interface {
	Scan(dest ...any) error
}

func scanNode(row scanner) (Node, error) {
	var node Node
	var adopted int
	var adoptedAt string
	var lastSeen string
	if err := row.Scan(&node.ID, &node.Instance, &node.Hostname, &node.Address, &node.Port, &node.Version, &node.UPSCount, &adopted, &adoptedAt, &lastSeen); err != nil {
		return Node{}, err
	}
	if adoptedAt != "" {
		parsedAdoptedAt, err := time.Parse(time.RFC3339, adoptedAt)
		if err != nil {
			return Node{}, fmt.Errorf("parse node adopted_at: %w", err)
		}
		node.AdoptedAt = parsedAdoptedAt
	}
	parsed, err := time.Parse(time.RFC3339, lastSeen)
	if err != nil {
		return Node{}, fmt.Errorf("parse node last_seen: %w", err)
	}
	node.Adopted = adopted == 1
	node.LastSeen = parsed
	return node, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
