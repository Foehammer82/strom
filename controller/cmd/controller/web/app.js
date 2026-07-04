const POLL_INTERVAL_MS = 5000;
const RING_CIRCUMFERENCE = 2 * Math.PI * 34;

const state = {
  nodes: [],
  paused: false,
  nextRefreshAt: Date.now() + POLL_INTERVAL_MS,
  refreshTimer: null,
  tickTimer: null,
  toastTimer: null,
};

const els = {
  themeToggle: document.getElementById("theme-toggle"),
  refreshNow: document.getElementById("refresh-now"),
  refreshToggle: document.getElementById("refresh-toggle"),
  refreshSeconds: document.getElementById("refresh-seconds"),
  refreshStatus: document.getElementById("refresh-status"),
  refreshRing: document.getElementById("refresh-ring"),
  fleetGroups: document.getElementById("fleet-groups"),
  toast: document.getElementById("toast"),
};

function initTheme() {
  const saved = window.localStorage.getItem("wattkeeper-theme");
  const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  applyTheme(saved || (prefersDark ? "dark" : "light"));
  els.themeToggle.addEventListener("click", () => {
    const next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    applyTheme(next);
    window.localStorage.setItem("wattkeeper-theme", next);
  });
}

function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  els.themeToggle.textContent = theme === "dark" ? "Light mode" : "Dark mode";
}

function startTimers() {
  state.tickTimer = window.setInterval(updateRefreshCountdown, 250);
  updateRefreshCountdown();
}

function scheduleRefresh() {
  if (state.refreshTimer) {
    window.clearTimeout(state.refreshTimer);
  }
  if (state.paused) {
    return;
  }
  state.refreshTimer = window.setTimeout(() => refreshNodes(true), Math.max(0, state.nextRefreshAt - Date.now()));
}

function updateRefreshCountdown() {
  if (state.paused) {
    els.refreshSeconds.textContent = "paused";
    els.refreshStatus.textContent = "Fleet auto refresh is paused.";
    els.refreshRing.style.strokeDasharray = `${RING_CIRCUMFERENCE} ${RING_CIRCUMFERENCE}`;
    els.refreshRing.style.strokeDashoffset = `${RING_CIRCUMFERENCE}`;
    return;
  }
  const remaining = Math.max(0, state.nextRefreshAt - Date.now());
  const seconds = Math.ceil(remaining / 1000);
  els.refreshSeconds.textContent = `${seconds}s`;
  els.refreshStatus.textContent = "Polling the fleet inventory every 5 seconds.";
  const progress = 1 - remaining / POLL_INTERVAL_MS;
  els.refreshRing.style.strokeDasharray = `${RING_CIRCUMFERENCE} ${RING_CIRCUMFERENCE}`;
  els.refreshRing.style.strokeDashoffset = `${RING_CIRCUMFERENCE * progress}`;
}

async function refreshNodes(silent = false) {
  try {
    const response = await fetch("/api/nodes", { headers: { Accept: "application/json" } });
    const payload = await response.json();
    if (!response.ok) {
      throw new Error(payload.error || `${response.status} ${response.statusText}`);
    }
    state.nodes = payload.nodes || [];
    renderFleet();
    state.nextRefreshAt = Date.now() + POLL_INTERVAL_MS;
    scheduleRefresh();
    if (!silent) {
      showToast("Fleet inventory refreshed.");
    }
  } catch (error) {
    els.refreshStatus.textContent = error.message;
    showToast(error.message, true);
    state.nextRefreshAt = Date.now() + POLL_INTERVAL_MS;
    scheduleRefresh();
  }
}

function renderFleet() {
  const groups = [
    ["pending", "Pending adoption", "Newly discovered nodes waiting for controller adoption."],
    ["adopted-online", "Adopted online", "Nodes marked adopted and currently visible on the network."],
    ["adopted-offline", "Adopted offline", "Known adopted nodes that are not currently broadcasting."],
  ];
  els.fleetGroups.innerHTML = groups.map(([status, title, subtitle]) => renderGroup(status, title, subtitle)).join("");
}

function renderGroup(status, title, subtitle) {
  const nodes = state.nodes.filter((node) => node.status === status);
  const chipClass = status === "pending" ? "chip--pending" : (status === "adopted-online" ? "chip--online" : "chip--offline");
  return `
    <section class="group-section">
      <div class="group-head">
        <div>
          <h3>${escapeHTML(title)}</h3>
          <p>${escapeHTML(subtitle)}</p>
        </div>
        <span class="chip ${chipClass}">${nodes.length} node${nodes.length === 1 ? "" : "s"}</span>
      </div>
      ${nodes.length === 0 ? `<div class="empty-state"><p>No nodes in this group right now.</p></div>` : `<div class="node-grid">${nodes.map(renderNodeCard).join("")}</div>`}
    </section>
  `;
}

function renderNodeCard(node) {
  const chipClass = node.status === "pending" ? "chip--pending" : (node.status === "adopted-online" ? "chip--online" : "chip--offline");
  let actions = "";
  if (node.status === "pending" && node.live) {
    actions = `<div class="node-actions"><button class="button button--primary" data-adopt-node="${escapeHTML(node.id)}">Adopt node</button></div>`;
  } else if (node.status === "adopted-online") {
    actions = `<div class="node-actions"><button class="button button--ghost" data-node-health="${escapeHTML(node.id)}">Node health</button></div>`;
  }
  return `
    <article class="node-card">
      <header>
        <div>
          <span class="eyebrow">${escapeHTML(node.id)}</span>
          <h4>${escapeHTML(node.instance || node.hostname || node.id)}</h4>
          <p>${escapeHTML(node.address || "address unavailable")}${node.port ? ` • port ${node.port}` : ""}</p>
        </div>
        <span class="chip ${chipClass}">${escapeHTML(node.status)}</span>
      </header>
      <div class="node-meta">
        ${metaCard("Version", node.version || "dev")}
        ${metaCard("UPS count", String(node.ups_count ?? 0))}
        ${metaCard("Last seen", formatRelativeTime(node.last_seen))}
        ${metaCard("Hostname", node.hostname || "unknown")}
      </div>
      ${actions}
    </article>
  `;
}

async function adoptNode(nodeID) {
  try {
    const response = await fetch(`/api/nodes/${encodeURIComponent(nodeID)}/adopt`, {
      method: "POST",
      headers: { Accept: "application/json" },
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(payload.error || `${response.status} ${response.statusText}`);
    }
    showToast(`Adopted ${nodeID}.`);
    await refreshNodes(true);
  } catch (error) {
    showToast(error.message, true);
  }
}

async function loadTrustedNodeHealth(nodeID) {
  try {
    const response = await fetch(`/api/nodes/${encodeURIComponent(nodeID)}/health`, {
      headers: { Accept: "application/json" },
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(payload.error || `${response.status} ${response.statusText}`);
    }
    const health = payload.health || {};
    showToast(`${nodeID}: uptime ${formatDuration(health.uptime_seconds)}, UPSes ${Array.isArray(health.upses) ? health.upses.length : 0}.`);
  } catch (error) {
    showToast(error.message, true);
  }
}

function metaCard(label, value) {
  return `<div class="card"><span class="eyebrow">${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong></div>`;
}

function formatRelativeTime(raw) {
  if (!raw) {
    return "never";
  }
  const value = new Date(raw);
  const seconds = Math.max(0, Math.round((Date.now() - value.getTime()) / 1000));
  if (seconds < 60) {
    return `${seconds}s ago`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours}h ago`;
  }
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function formatDuration(value) {
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

function showToast(message, isError = false) {
  els.toast.textContent = message;
  els.toast.classList.add("is-visible");
  els.toast.style.borderColor = isError ? "rgba(185, 28, 28, 0.35)" : "";
  if (state.toastTimer) {
    window.clearTimeout(state.toastTimer);
  }
  state.toastTimer = window.setTimeout(() => {
    els.toast.classList.remove("is-visible");
    els.toast.style.borderColor = "";
  }, 3200);
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

els.refreshNow.addEventListener("click", () => refreshNodes(false));
els.refreshToggle.addEventListener("click", () => {
  state.paused = !state.paused;
  els.refreshToggle.textContent = state.paused ? "Resume auto refresh" : "Pause auto refresh";
  if (!state.paused) {
    state.nextRefreshAt = Date.now() + POLL_INTERVAL_MS;
  }
  scheduleRefresh();
  updateRefreshCountdown();
});

initTheme();
startTimers();
refreshNodes(true);

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-adopt-node]");
  if (button) {
    adoptNode(button.dataset.adoptNode);
    return;
  }
  const healthButton = event.target.closest("[data-node-health]");
  if (healthButton) {
    loadTrustedNodeHealth(healthButton.dataset.nodeHealth);
  }
});
