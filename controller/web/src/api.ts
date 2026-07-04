export type UPSummary = {
  name: string;
  driver: string;
  status?: string;
  battery_charge_percent?: number;
  load_percent?: number;
  runtime_seconds?: number;
  captured_at?: string;
};

export type NodeRecord = {
  id: string;
  instance: string;
  hostname: string;
  address: string;
  port: number;
  version: string;
  ups_count: number;
  display_name: string;
  location_label: string;
  site_label: string;
  adopted: boolean;
  live: boolean;
  status: string;
  comms_state: string;
  poll_failures: number;
  last_polled_at?: string;
  last_poll_error?: string;
  last_seen: string;
  ups_summaries?: UPSummary[];
};

export type NodeHealthResponse = {
  node_id: string;
  health: {
    version?: string;
    uptime_seconds?: number;
    serial?: string;
    cpu_temperature_celsius?: number;
    disk_free_bytes?: number;
    upses?: Array<{ name: string; driver: string; status: string }>;
  };
};

export type UPSDetailResponse = {
  node_id: string;
  name: string;
  driver: string;
  status?: string;
  metrics?: UPSummary;
  variables: Record<string, string>;
  commands?: Array<{ name: string; description?: string; destructive: boolean }>;
  writable?: Array<{ name: string; description?: string; editor: string; current_value?: string }>;
  live: boolean;
  captured_at?: string;
};

export type UPSHistoryResponse = {
  node_id: string;
  name: string;
  samples: Array<{ variable: string; value: string; captured_at: string }>;
};

export type AlertRule = {
  id: number;
  kind: string;
  ups_var?: string;
  threshold?: number;
  webhook_url: string;
  debounce_seconds: number;
  enabled: boolean;
  created_at: string;
};

export type AlertEvent = {
  id: number;
  rule_id: number;
  node_id: string;
  ups_id?: string;
  subject_key: string;
  kind: string;
  message: string;
  created_at: string;
  delivered: boolean;
  delivery_error?: string;
};

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...(init?.headers ?? {}),
    },
    ...init,
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error((payload as { error?: string }).error || `${response.status} ${response.statusText}`);
  }
  return payload as T;
}

export function fetchNodes() {
  return requestJSON<{ nodes: NodeRecord[] }>("/api/nodes");
}

export function fetchNode(nodeID: string) {
  return requestJSON<NodeRecord>(`/api/nodes/${encodeURIComponent(nodeID)}`);
}

export function fetchNodeHealth(nodeID: string) {
  return requestJSON<NodeHealthResponse>(`/api/nodes/${encodeURIComponent(nodeID)}/health`);
}

export function fetchUPSDetail(nodeID: string, upsName: string) {
  return requestJSON<UPSDetailResponse>(`/api/nodes/${encodeURIComponent(nodeID)}/ups/${encodeURIComponent(upsName)}`);
}

export function fetchUPSHistory(nodeID: string, upsName: string, variable: string, hours = 24, limit = 400) {
  const params = new URLSearchParams({ var: variable, hours: String(hours), limit: String(limit) });
  return requestJSON<UPSHistoryResponse>(`/api/nodes/${encodeURIComponent(nodeID)}/ups/${encodeURIComponent(upsName)}/history?${params.toString()}`);
}

export function runUPSCommand(nodeID: string, upsName: string, command: string) {
  return requestJSON<{ ups: string; command: string; output: string }>(`/api/nodes/${encodeURIComponent(nodeID)}/ups/${encodeURIComponent(upsName)}/command`, {
    method: "POST",
    body: JSON.stringify({ cmd: command }),
  });
}

export function adoptNode(nodeID: string) {
  return requestJSON(`/api/nodes/${encodeURIComponent(nodeID)}/adopt`, { method: "POST" });
}

export function forgetNode(nodeID: string) {
  return requestJSON(`/api/nodes/${encodeURIComponent(nodeID)}`, { method: "DELETE" });
}

export function updateNode(nodeID: string, payload: { display_name: string; location_label: string; site_label: string }) {
  return requestJSON<NodeRecord>(`/api/nodes/${encodeURIComponent(nodeID)}`, {
    method: "PATCH",
    body: JSON.stringify(payload),
  });
}

export function fetchAlertRules() {
  return requestJSON<{ rules: AlertRule[] }>("/api/alerts/rules");
}

export function fetchAlertEvents(limit = 50) {
  return requestJSON<{ events: AlertEvent[] }>(`/api/alerts/events?limit=${limit}`);
}

export function createAlertRule(payload: { kind: string; webhook_url: string; debounce_seconds: number; ups_var?: string; threshold?: number; enabled?: boolean }) {
  return requestJSON<AlertRule>("/api/alerts/rules", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function deleteAlertRule(id: number) {
  return requestJSON(`/api/alerts/rules/${id}`, { method: "DELETE" });
}

export function testAlertRule(id: number) {
  return requestJSON<AlertEvent>(`/api/alerts/rules/${id}/test`, { method: "POST" });
}