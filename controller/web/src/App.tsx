import { useEffect, useState } from "react";
import { Link, Route, Routes, useNavigate, useParams } from "react-router-dom";
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import {
  adoptNode,
  createAlertRule,
  deleteAlertRule,
  fetchAlertEvents,
  fetchAlertRules,
  fetchNode,
  fetchNodeHealth,
  fetchNodes,
  fetchUPSDetail,
  fetchUPSHistory,
  forgetNode,
  runUPSCommand,
  testAlertRule,
  updateNode,
  type AlertEvent,
  type AlertRule,
  type NodeHealthResponse,
  type NodeRecord,
  type UPSDetailResponse,
  type UPSummary,
} from "./api";

const GROUPS: Array<[string, string, string]> = [
  ["pending", "Pending adoption", "Newly discovered nodes waiting for controller adoption."],
  ["adopted-online", "Adopted online", "Nodes marked adopted and currently visible on the network."],
  ["adopted-offline", "Adopted offline", "Known adopted nodes that are not currently broadcasting."],
];

function App() {
  const [theme, setTheme] = useState<"light" | "dark">("light");
  const [toast, setToast] = useState<string | null>(null);

  useEffect(() => {
    const saved = window.localStorage.getItem("wattkeeper-theme");
    const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    setTheme(saved === "dark" || saved === "light" ? saved : prefersDark ? "dark" : "light");
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    window.localStorage.setItem("wattkeeper-theme", theme);
  }, [theme]);

  useEffect(() => {
    if (!toast) {
      return;
    }
    const timer = window.setTimeout(() => setToast(null), 3200);
    return () => window.clearTimeout(timer);
  }, [toast]);

  return (
    <main className="shell">
      <header className="topbar">
        <div className="brand">
          <div className="brand-mark-wrap">
            <img className="brand-mark" src="/logo.svg" alt="Wattkeeper logo" />
          </div>
          <div className="brand-copy">
            <p className="eyebrow">Controller</p>
            <h1>Wattkeeper Fleet</h1>
            <p className="lede">GUI-first controller built on the live Phase 3 APIs, preserving the existing Wattkeeper visual language.</p>
          </div>
        </div>
        <nav className="toolbar nav-links">
          <Link className="button button--ghost" to="/">Fleet</Link>
          <Link className="button button--ghost" to="/alerts">Alerts</Link>
          <button className="button button--ghost" type="button" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>{theme === "dark" ? "Light mode" : "Dark mode"}</button>
        </nav>
      </header>

      <Routes>
        <Route path="/" element={<FleetPage onToast={setToast} />} />
        <Route path="/nodes/:nodeID" element={<NodeDetailPage />} />
        <Route path="/nodes/:nodeID/ups/:upsName" element={<UPSDetailPage onToast={setToast} />} />
        <Route path="/alerts" element={<AlertsPage onToast={setToast} />} />
      </Routes>

      <div className={`toast${toast ? " is-visible" : ""}`} role="status" aria-live="polite">{toast}</div>
    </main>
  );
}

function FleetPage({ onToast }: { onToast: (message: string) => void }) {
  const [nodes, setNodes] = useState<NodeRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  async function loadNodes(silent = false) {
    try {
      if (!silent) {
        setLoading(true);
      }
      const payload = await fetchNodes();
      setNodes(payload.nodes ?? []);
      setError(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Unknown error");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void loadNodes();
  }, []);

  return (
    <>
      <section className="surface hero">
        <div className="hero-grid">
          <div>
            <div className="section-head">
              <h2>Fleet overview</h2>
              <span className="helper">Discovery, adoption, comms state, and recent UPS telemetry in one place.</span>
            </div>
            <div className="cards cards--summary">
              <article className="card"><span className="eyebrow">Nodes</span><strong>{nodes.length}</strong><p>Total discovered and persisted nodes.</p></article>
              <article className="card"><span className="eyebrow">Online</span><strong>{nodes.filter((node) => node.status === "adopted-online").length}</strong><p>Adopted nodes currently visible on the network.</p></article>
              <article className="card"><span className="eyebrow">Pending</span><strong>{nodes.filter((node) => node.status === "pending").length}</strong><p>Nodes waiting for adoption.</p></article>
            </div>
          </div>
          <aside className="card refresh-panel">
            <span className="eyebrow">Inventory state</span>
            <strong>{loading ? "Loading" : error ? "Attention needed" : "Ready"}</strong>
            <p>{error ?? "The fleet page is now driven by the live controller APIs and is ready for richer charts and command workflows."}</p>
            <button className="button button--ghost" type="button" onClick={() => void loadNodes()}>Refresh now</button>
          </aside>
        </div>
      </section>

      <section className="surface fleet-shell">
        <div className="section-head">
          <h2>Fleet inventory</h2>
          <span className="helper">One card per node, with recent UPS telemetry summaries and drill-in links.</span>
        </div>
        {error ? <div className="empty-state"><p>{error}</p></div> : null}
        {!error && loading ? <div className="empty-state"><p>Loading controller inventory...</p></div> : null}
        {!error && !loading ? (
          <div className="fleet-groups">
            {GROUPS.map(([status, title, subtitle]) => {
              const groupNodes = nodes.filter((node) => node.status === status);
              const chipClass = status === "pending" ? "chip--pending" : status === "adopted-online" ? "chip--online" : "chip--offline";
              return (
                <section className="group-section" key={status}>
                  <div className="group-head">
                    <div>
                      <h3>{title}</h3>
                      <p>{subtitle}</p>
                    </div>
                    <span className={`chip ${chipClass}`}>{groupNodes.length} node{groupNodes.length === 1 ? "" : "s"}</span>
                  </div>
                  {groupNodes.length === 0 ? (
                    <div className="empty-state"><p>No nodes in this group right now.</p></div>
                  ) : (
                    <div className="node-grid">
                      {groupNodes.map((node) => (
                        <NodeCard key={node.id} node={node} onChanged={() => void loadNodes(true)} onToast={onToast} />
                      ))}
                    </div>
                  )}
                </section>
              );
            })}
          </div>
        ) : null}
      </section>
    </>
  );
}

function NodeCard({ node, onChanged, onToast }: { node: NodeRecord; onChanged: () => void; onToast: (message: string) => void }) {
  const navigate = useNavigate();
  const chipClass = node.status === "pending" ? "chip--pending" : node.status === "adopted-online" ? "chip--online" : "chip--offline";
  const title = node.display_name || node.instance || node.hostname || node.id;
  const locationBits = [node.location_label, node.site_label].filter(Boolean);

  async function handleAdopt() {
    try {
      await adoptNode(node.id);
      onToast(`Adopted ${node.id}.`);
      onChanged();
    } catch (error) {
      onToast(error instanceof Error ? error.message : "Adoption failed.");
    }
  }

  async function handleForget() {
    if (!window.confirm(`Forget ${node.id}? This removes the controller record and stored trust material.`)) {
      return;
    }
    try {
      await forgetNode(node.id);
      onToast(`Forgot ${node.id}.`);
      onChanged();
    } catch (error) {
      onToast(error instanceof Error ? error.message : "Forget failed.");
    }
  }

  async function handleEdit() {
    const displayName = window.prompt("Display name", node.display_name || "");
    if (displayName === null) {
      return;
    }
    const locationLabel = window.prompt("Location label", node.location_label || "");
    if (locationLabel === null) {
      return;
    }
    const siteLabel = window.prompt("Site label", node.site_label || "");
    if (siteLabel === null) {
      return;
    }
    try {
      await updateNode(node.id, { display_name: displayName, location_label: locationLabel, site_label: siteLabel });
      onToast(`Updated details for ${node.id}.`);
      onChanged();
    } catch (error) {
      onToast(error instanceof Error ? error.message : "Update failed.");
    }
  }

  return (
    <article className="node-card">
      <header>
        <div>
          <span className="eyebrow">{node.id}</span>
          <h4>{title}</h4>
          <p>{node.address || "address unavailable"}{node.port ? ` • port ${node.port}` : ""}</p>
        </div>
        <span className={`chip ${chipClass}`}>{node.status}</span>
      </header>
      <div className="node-meta">
        <div className="card"><span className="eyebrow">Version</span><strong>{node.version || "dev"}</strong></div>
        <div className="card"><span className="eyebrow">UPS count</span><strong>{node.ups_count}</strong></div>
        <div className="card"><span className="eyebrow">Comms</span><strong>{formatCommsState(node.comms_state, node.poll_failures, node.last_poll_error)}</strong></div>
        <div className="card"><span className="eyebrow">Location</span><strong>{locationBits.join(" / ") || "unassigned"}</strong></div>
      </div>
      {Array.isArray(node.ups_summaries) && node.ups_summaries.length > 0 ? (
        <div className="node-meta">
          {node.ups_summaries.map((summary) => (
            <button className="card button button--ghost" type="button" key={summary.name} onClick={() => navigate(`/nodes/${encodeURIComponent(node.id)}/ups/${encodeURIComponent(summary.name)}`)}>
              <span className="eyebrow">{summary.name}</span>
              <strong>{formatUPSSummary(summary)}</strong>
            </button>
          ))}
        </div>
      ) : null}
      <div className="node-actions">
        <button className="button button--ghost" type="button" onClick={() => navigate(`/nodes/${encodeURIComponent(node.id)}`)}>Node detail</button>
        {node.status === "pending" && node.live ? <button className="button button--primary" type="button" onClick={() => void handleAdopt()}>Adopt node</button> : null}
        <button className="button button--ghost" type="button" onClick={() => void handleEdit()}>Edit details</button>
        <button className="button button--ghost" type="button" onClick={() => void handleForget()}>Forget node</button>
      </div>
    </article>
  );
}

function NodeDetailPage() {
  const { nodeID = "" } = useParams();
  const [node, setNode] = useState<NodeRecord | null>(null);
  const [health, setHealth] = useState<NodeHealthResponse["health"] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const [nodePayload, healthPayload] = await Promise.all([fetchNode(nodeID), fetchNodeHealth(nodeID)]);
        if (!cancelled) {
          setNode(nodePayload);
          setHealth(healthPayload.health);
          setError(null);
        }
      } catch (loadError) {
        if (!cancelled) {
          setError(loadError instanceof Error ? loadError.message : "Unknown error");
        }
      }
    }
    if (nodeID) {
      void load();
    }
    return () => {
      cancelled = true;
    };
  }, [nodeID]);

  if (error) {
    return <section className="surface fleet-shell"><div className="empty-state"><p>{error}</p></div></section>;
  }
  if (!node || !health) {
    return <section className="surface fleet-shell"><div className="empty-state"><p>Loading node detail...</p></div></section>;
  }

  const locationBits = [node.location_label, node.site_label].filter(Boolean);
  return (
    <section className="surface fleet-shell">
      <div className="section-head">
        <div>
          <h2>{node.display_name || node.instance || node.id}</h2>
          <span className="helper">Node detail powered by trusted controller-to-node reads.</span>
        </div>
        <Link className="button button--ghost" to="/">Back to fleet</Link>
      </div>
      <div className="cards cards--summary">
        <article className="card"><span className="eyebrow">Agent version</span><strong>{health.version || node.version || "dev"}</strong></article>
        <article className="card"><span className="eyebrow">CPU temp</span><strong>{health.cpu_temperature_celsius != null ? `${health.cpu_temperature_celsius.toFixed(1)} C` : "unknown"}</strong></article>
        <article className="card"><span className="eyebrow">Disk free</span><strong>{formatBytes(health.disk_free_bytes ?? 0)}</strong></article>
        <article className="card"><span className="eyebrow">Location</span><strong>{locationBits.join(" / ") || "unassigned"}</strong></article>
      </div>
      <div className="fleet-groups">
        <section className="group-section">
          <div className="group-head">
            <div>
              <h3>UPS inventory</h3>
              <p>Drill into any UPS for live detail, history charts, and instant commands.</p>
            </div>
          </div>
          {!node.ups_summaries?.length ? <div className="empty-state"><p>No UPS telemetry has been stored for this node yet.</p></div> : (
            <div className="node-grid">
              {node.ups_summaries.map((summary) => (
                <Link className="card button button--ghost detail-link" key={summary.name} to={`/nodes/${encodeURIComponent(node.id)}/ups/${encodeURIComponent(summary.name)}`}>
                  <span className="eyebrow">{summary.name}</span>
                  <strong>{formatUPSSummary(summary)}</strong>
                </Link>
              ))}
            </div>
          )}
        </section>
      </div>
    </section>
  );
}

function UPSDetailPage({ onToast }: { onToast: (message: string) => void }) {
  const { nodeID = "", upsName = "" } = useParams();
  const [detail, setDetail] = useState<UPSDetailResponse | null>(null);
  const [chargeHistory, setChargeHistory] = useState<Array<{ timestamp: string; value: number }>>([]);
  const [loadHistory, setLoadHistory] = useState<Array<{ timestamp: string; value: number }>>([]);
  const [runtimeHistory, setRuntimeHistory] = useState<Array<{ timestamp: string; value: number }>>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const [detailPayload, charge, load, runtime] = await Promise.all([
          fetchUPSDetail(nodeID, upsName),
          fetchUPSHistory(nodeID, upsName, "battery.charge"),
          fetchUPSHistory(nodeID, upsName, "ups.load"),
          fetchUPSHistory(nodeID, upsName, "battery.runtime"),
        ]);
        if (!cancelled) {
          setDetail(detailPayload);
          setChargeHistory(toChartSeries(charge.samples));
          setLoadHistory(toChartSeries(load.samples));
          setRuntimeHistory(toChartSeries(runtime.samples));
          setError(null);
        }
      } catch (loadError) {
        if (!cancelled) {
          setError(loadError instanceof Error ? loadError.message : "Unknown error");
        }
      }
    }
    if (nodeID && upsName) {
      void load();
    }
    return () => {
      cancelled = true;
    };
  }, [nodeID, upsName]);

  async function handleCommand(commandName: string, destructive: boolean) {
    if (destructive && !window.confirm(`Run destructive command ${commandName}?`)) {
      return;
    }
    try {
      const payload = await runUPSCommand(nodeID, upsName, commandName);
      onToast(`${payload.command}: ${payload.output || "OK"}`);
      const updated = await fetchUPSDetail(nodeID, upsName);
      setDetail(updated);
    } catch (runError) {
      onToast(runError instanceof Error ? runError.message : "Command failed.");
    }
  }

  if (error) {
    return <section className="surface fleet-shell"><div className="empty-state"><p>{error}</p></div></section>;
  }
  if (!detail) {
    return <section className="surface fleet-shell"><div className="empty-state"><p>Loading UPS detail...</p></div></section>;
  }

  return (
    <section className="surface fleet-shell">
      <div className="section-head">
        <div>
          <h2>{detail.name}</h2>
          <span className="helper">Live controller-side UPS detail with 24h charts and instant commands.</span>
        </div>
        <Link className="button button--ghost" to={`/nodes/${encodeURIComponent(nodeID)}`}>Back to node</Link>
      </div>
      <div className="cards cards--summary">
        <article className="card"><span className="eyebrow">Status</span><strong>{detail.status || detail.metrics?.status || "unknown"}</strong></article>
        <article className="card"><span className="eyebrow">Battery</span><strong>{detail.metrics?.battery_charge_percent != null ? `${detail.metrics.battery_charge_percent}%` : "unknown"}</strong></article>
        <article className="card"><span className="eyebrow">Load</span><strong>{detail.metrics?.load_percent != null ? `${detail.metrics.load_percent}%` : "unknown"}</strong></article>
        <article className="card"><span className="eyebrow">Runtime</span><strong>{detail.metrics?.runtime_seconds != null ? formatDuration(detail.metrics.runtime_seconds) : "unknown"}</strong></article>
      </div>
      <div className="charts-grid">
        <ChartCard title="Battery charge" data={chargeHistory} stroke="#14b8a6" />
        <ChartCard title="UPS load" data={loadHistory} stroke="#65a30d" />
        <ChartCard title="Runtime" data={runtimeHistory} stroke="#0f766e" />
      </div>
      <div className="fleet-groups">
        <section className="group-section">
          <div className="group-head"><div><h3>Instant commands</h3><p>Trusted controller-side passthrough over the pinned HTTPS channel.</p></div></div>
          {!detail.commands?.length ? <div className="empty-state"><p>This UPS does not report any instant commands.</p></div> : (
            <div className="node-grid">
              {detail.commands.map((command) => (
                <article className="card" key={command.name}>
                  <span className="eyebrow">{command.destructive ? "Destructive" : "Command"}</span>
                  <strong>{command.name}</strong>
                  <p>{command.description || "No description reported by NUT."}</p>
                  <button className={`button ${command.destructive ? "button--ghost" : "button--primary"}`} type="button" onClick={() => void handleCommand(command.name, command.destructive)}>
                    {command.destructive ? "Confirm and run" : "Run command"}
                  </button>
                </article>
              ))}
            </div>
          )}
        </section>
        <section className="group-section">
          <div className="group-head"><div><h3>Latest variables</h3><p>Raw NUT variables from the most recent trusted/live or stored UPS detail.</p></div></div>
          <div className="variable-grid">
            {Object.entries(detail.variables).sort(([left], [right]) => left.localeCompare(right)).map(([key, value]) => (
              <article className="card" key={key}>
                <span className="eyebrow">{key}</span>
                <strong>{value}</strong>
              </article>
            ))}
          </div>
        </section>
      </div>
    </section>
  );
}

function AlertsPage({ onToast }: { onToast: (message: string) => void }) {
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [events, setEvents] = useState<AlertEvent[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState({ kind: "node_offline", webhook_url: "", debounce_seconds: 300, threshold: "20" });

  async function load() {
    try {
      const [rulesPayload, eventsPayload] = await Promise.all([fetchAlertRules(), fetchAlertEvents()]);
      setRules(rulesPayload.rules);
      setEvents(eventsPayload.events);
      setError(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Unknown error");
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function handleCreate(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    try {
      await createAlertRule({
        kind: form.kind,
        webhook_url: form.webhook_url,
        debounce_seconds: form.debounce_seconds,
        threshold: form.kind === "low_battery" ? Number(form.threshold) : undefined,
        ups_var: form.kind === "low_battery" ? "battery.charge" : undefined,
      });
      onToast(`Created ${form.kind} alert rule.`);
      await load();
    } catch (createError) {
      onToast(createError instanceof Error ? createError.message : "Alert rule creation failed.");
    }
  }

  async function handleDelete(id: number) {
    if (!window.confirm(`Delete alert rule ${id}?`)) {
      return;
    }
    try {
      await deleteAlertRule(id);
      onToast(`Deleted alert rule ${id}.`);
      await load();
    } catch (deleteError) {
      onToast(deleteError instanceof Error ? deleteError.message : "Delete failed.");
    }
  }

  async function handleTest(id: number) {
    try {
      await testAlertRule(id);
      onToast(`Test-fired alert rule ${id}.`);
      await load();
    } catch (testError) {
      onToast(testError instanceof Error ? testError.message : "Test-fire failed.");
    }
  }

  return (
    <section className="surface fleet-shell">
      <div className="section-head">
        <div>
          <h2>Alerts</h2>
          <span className="helper">Webhook-only Phase 3 alerting with debounce and recent event history.</span>
        </div>
      </div>
      {error ? <div className="empty-state"><p>{error}</p></div> : null}
      <div className="fleet-groups alerts-layout">
        <section className="group-section">
          <div className="group-head"><div><h3>Create rule</h3><p>Global rules only, keeping Phase 3 simple.</p></div></div>
          <form className="form-grid" onSubmit={handleCreate}>
            <label className="field">
              <span className="eyebrow">Kind</span>
              <select value={form.kind} onChange={(event) => setForm((current) => ({ ...current, kind: event.target.value }))}>
                <option value="node_offline">node_offline</option>
                <option value="comms_lost">comms_lost</option>
                <option value="on_battery">on_battery</option>
                <option value="low_battery">low_battery</option>
              </select>
            </label>
            <label className="field">
              <span className="eyebrow">Webhook URL</span>
              <input value={form.webhook_url} onChange={(event) => setForm((current) => ({ ...current, webhook_url: event.target.value }))} placeholder="https://example.invalid/webhook" />
            </label>
            <label className="field">
              <span className="eyebrow">Debounce seconds</span>
              <input type="number" min={1} value={form.debounce_seconds} onChange={(event) => setForm((current) => ({ ...current, debounce_seconds: Number(event.target.value) || 300 }))} />
            </label>
            {form.kind === "low_battery" ? (
              <label className="field">
                <span className="eyebrow">Threshold (%)</span>
                <input type="number" min={1} max={100} value={form.threshold} onChange={(event) => setForm((current) => ({ ...current, threshold: event.target.value }))} />
              </label>
            ) : null}
            <button className="button button--primary" type="submit">Create rule</button>
          </form>
        </section>
        <section className="group-section">
          <div className="group-head"><div><h3>Rules</h3><p>Existing webhook alert rules.</p></div></div>
          {!rules.length ? <div className="empty-state"><p>No alert rules yet.</p></div> : (
            <div className="node-grid">
              {rules.map((rule) => (
                <article className="card" key={rule.id}>
                  <span className="eyebrow">{rule.kind}</span>
                  <strong>Rule #{rule.id}</strong>
                  <p>{rule.webhook_url}</p>
                  <p>Debounce {rule.debounce_seconds}s{rule.threshold != null ? ` • threshold ${rule.threshold}%` : ""}</p>
                  <div className="node-actions">
                    <button className="button button--ghost" type="button" onClick={() => void handleTest(rule.id)}>Test fire</button>
                    <button className="button button--ghost" type="button" onClick={() => void handleDelete(rule.id)}>Delete</button>
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>
        <section className="group-section">
          <div className="group-head"><div><h3>Recent events</h3><p>Newest alert events first.</p></div></div>
          {!events.length ? <div className="empty-state"><p>No alert events yet.</p></div> : (
            <div className="variable-grid">
              {events.map((event) => (
                <article className="card" key={event.id}>
                  <span className="eyebrow">{event.kind}</span>
                  <strong>{event.message}</strong>
                  <p>{new Date(event.created_at).toLocaleString()}</p>
                  <p>{event.delivered ? "Delivered" : `Delivery error: ${event.delivery_error || "unknown"}`}</p>
                </article>
              ))}
            </div>
          )}
        </section>
      </div>
    </section>
  );
}

function ChartCard({ title, data, stroke }: { title: string; data: Array<{ timestamp: string; value: number }>; stroke: string }) {
  return (
    <section className="card chart-card">
      <span className="eyebrow">{title}</span>
      <div className="chart-shell">
        {data.length === 0 ? (
          <div className="empty-state"><p>No stored samples yet.</p></div>
        ) : (
          <ResponsiveContainer width="100%" height={220}>
            <LineChart data={data}>
              <XAxis dataKey="timestamp" tick={{ fill: "currentColor", fontSize: 12 }} minTickGap={24} />
              <YAxis tick={{ fill: "currentColor", fontSize: 12 }} width={48} />
              <Tooltip />
              <Line type="monotone" dataKey="value" stroke={stroke} strokeWidth={2} dot={false} />
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>
    </section>
  );
}

function toChartSeries(samples: Array<{ value: string; captured_at: string }>) {
  return [...samples]
    .reverse()
    .map((sample) => ({
      timestamp: new Date(sample.captured_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
      value: Number(sample.value),
    }))
    .filter((sample) => Number.isFinite(sample.value));
}

function formatCommsState(state: string | undefined, failures: number, error?: string) {
  const normalized = String(state || "unknown");
  if (normalized === "healthy") {
    return "healthy";
  }
  if (normalized === "offline") {
    return failures > 0 ? `offline (${failures} failed polls)` : "offline";
  }
  if (normalized === "degraded") {
    return error ? `degraded: ${error}` : "degraded";
  }
  return "unknown";
}

function formatUPSSummary(summary: UPSummary) {
  const parts: string[] = [];
  if (summary.status) {
    parts.push(summary.status);
  }
  if (summary.battery_charge_percent != null) {
    parts.push(`${summary.battery_charge_percent}% battery`);
  }
  if (summary.load_percent != null) {
    parts.push(`${summary.load_percent}% load`);
  }
  if (summary.runtime_seconds != null) {
    parts.push(formatDuration(summary.runtime_seconds));
  }
  return parts.join(" • ") || summary.driver || "no recent telemetry";
}

function formatDuration(value: number | undefined) {
  if (value == null) {
    return "unknown";
  }
  const totalSeconds = Number(value);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) {
    return `${hours}h ${minutes}m`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "unknown";
  }
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(value >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

export default App;