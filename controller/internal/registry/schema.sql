CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  instance TEXT NOT NULL,
  hostname TEXT NOT NULL,
  address TEXT NOT NULL,
  port INTEGER NOT NULL,
  version TEXT NOT NULL,
  ups_count INTEGER NOT NULL DEFAULT 0,
  display_name TEXT NOT NULL DEFAULT '',
  location_label TEXT NOT NULL DEFAULT '',
  site_label TEXT NOT NULL DEFAULT '',
  adopted INTEGER NOT NULL DEFAULT 0,
  adopted_at TEXT NOT NULL DEFAULT '',
  controller_url TEXT NOT NULL DEFAULT '',
  tls_port INTEGER NOT NULL DEFAULT 0,
  tls_fingerprint TEXT NOT NULL DEFAULT '',
  nut_user TEXT NOT NULL DEFAULT '',
  api_token_enc TEXT NOT NULL DEFAULT '',
  nut_password_enc TEXT NOT NULL DEFAULT '',
  comms_state TEXT NOT NULL DEFAULT 'unknown',
  poll_failures INTEGER NOT NULL DEFAULT 0,
  last_polled_at TEXT NOT NULL DEFAULT '',
  last_poll_error TEXT NOT NULL DEFAULT '',
  last_seen TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ups (
  id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL,
  name TEXT NOT NULL,
  driver TEXT NOT NULL,
  FOREIGN KEY(node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ups_id TEXT NOT NULL,
  variable TEXT NOT NULL,
  value TEXT NOT NULL,
  captured_at TEXT NOT NULL,
  FOREIGN KEY(ups_id) REFERENCES ups(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS alert_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  ups_var TEXT NOT NULL DEFAULT '',
  threshold REAL,
  webhook_url TEXT NOT NULL,
  debounce_seconds INTEGER NOT NULL DEFAULT 300,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id INTEGER NOT NULL,
  node_id TEXT NOT NULL,
  ups_id TEXT NOT NULL DEFAULT '',
  subject_key TEXT NOT NULL,
  kind TEXT NOT NULL,
  message TEXT NOT NULL,
  created_at TEXT NOT NULL,
  delivered INTEGER NOT NULL DEFAULT 0,
  delivery_error TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(rule_id) REFERENCES alert_rules(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_ups_node_id ON ups(node_id);
CREATE INDEX IF NOT EXISTS idx_samples_ups_var_time ON samples(ups_id, variable, captured_at);
CREATE INDEX IF NOT EXISTS idx_alert_events_rule_subject_time ON alert_events(rule_id, subject_key, created_at);