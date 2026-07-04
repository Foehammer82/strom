const POLL_INTERVAL_MS = 15000;
const RING_CIRCUMFERENCE = 2 * Math.PI * 34;

const state = {
  health: null,
  upses: [],
  selectedUPS: null,
  detail: null,
  paused: false,
  nextRefreshAt: Date.now() + POLL_INTERVAL_MS,
  refreshTimer: null,
  tickTimer: null,
  toastTimer: null,
  pendingCommand: null,
};

const els = {
  themeToggle: document.getElementById("theme-toggle"),
  refreshNow: document.getElementById("refresh-now"),
  refreshToggle: document.getElementById("refresh-toggle"),
  refreshSeconds: document.getElementById("refresh-seconds"),
  refreshStatus: document.getElementById("refresh-status"),
  refreshRing: document.getElementById("refresh-ring"),
  metrics: document.getElementById("metrics-grid"),
  upsGrid: document.getElementById("ups-grid"),
  detail: document.getElementById("ups-detail"),
  toast: document.getElementById("toast"),
  confirmModal: document.getElementById("confirm-modal"),
  confirmText: document.getElementById("confirm-text"),
  confirmInput: document.getElementById("confirm-input"),
  confirmSubmit: document.getElementById("confirm-submit"),
  confirmCancel: document.getElementById("confirm-cancel"),
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
  if (state.tickTimer) {
    window.clearInterval(state.tickTimer);
  }
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
  const delay = Math.max(0, state.nextRefreshAt - Date.now());
  state.refreshTimer = window.setTimeout(async () => {
    await refreshAll({ preserveSelection: true, silent: true });
  }, delay);
}

function updateRefreshCountdown() {
  if (state.paused) {
    els.refreshSeconds.textContent = "paused";
    els.refreshStatus.textContent = "Auto refresh is paused.";
    els.refreshRing.style.strokeDasharray = `${RING_CIRCUMFERENCE} ${RING_CIRCUMFERENCE}`;
    els.refreshRing.style.strokeDashoffset = `${RING_CIRCUMFERENCE}`;
    return;
  }
  const remaining = Math.max(0, state.nextRefreshAt - Date.now());
  const seconds = Math.ceil(remaining / 1000);
  els.refreshSeconds.textContent = `${seconds}s`;
  els.refreshStatus.textContent = `Polling node health and UPS telemetry every 15 seconds.`;
  const progress = 1 - remaining / POLL_INTERVAL_MS;
  els.refreshRing.style.strokeDasharray = `${RING_CIRCUMFERENCE} ${RING_CIRCUMFERENCE}`;
  els.refreshRing.style.strokeDashoffset = `${RING_CIRCUMFERENCE * progress}`;
}

async function fetchJSON(url, options) {
  const response = await window.fetch(url, {
    credentials: "same-origin",
    headers: { "Accept": "application/json", ...(options && options.headers ? options.headers : {}) },
    ...options,
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw new Error(payload && payload.error ? payload.error : `${response.status} ${response.statusText}`);
  }
  return payload;
}

async function refreshAll(options = {}) {
  try {
    const [health, upses] = await Promise.all([
      fetchJSON("/api/health"),
      fetchJSON("/api/ups"),
    ]);
    state.health = health;
    state.upses = upses;

    if (!state.selectedUPS && upses.length > 0) {
      state.selectedUPS = upses[0].name;
    }
    if (options.preserveSelection !== false && state.selectedUPS) {
      const exists = upses.some((ups) => ups.name === state.selectedUPS);
      state.selectedUPS = exists ? state.selectedUPS : (upses[0] ? upses[0].name : null);
    }
    renderHealth();
    renderUPSGrid();
    if (state.selectedUPS) {
      await loadUPSDetail(state.selectedUPS, { silent: true });
    } else {
      renderEmptyDetail();
    }
    state.nextRefreshAt = Date.now() + POLL_INTERVAL_MS;
    scheduleRefresh();
    if (!options.silent) {
      showToast("Dashboard refreshed.");
    }
  } catch (error) {
    els.refreshStatus.textContent = error.message;
    showToast(error.message, true);
    state.nextRefreshAt = Date.now() + POLL_INTERVAL_MS;
    scheduleRefresh();
  }
}

async function loadUPSDetail(name, options = {}) {
  try {
    state.selectedUPS = name;
    renderUPSGrid();
    const detail = await fetchJSON(`/api/ups/${encodeURIComponent(name)}`);
    state.detail = detail;
    renderDetail();
    if (!options.silent) {
      showToast(`Loaded ${name}.`);
    }
  } catch (error) {
    renderEmptyDetail(error.message);
    showToast(error.message, true);
  }
}

async function runCommand(command) {
  if (!state.selectedUPS) {
    return;
  }
  try {
    const response = await fetchJSON(`/api/ups/${encodeURIComponent(state.selectedUPS)}/command`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ cmd: command.name }),
    });
    showToast(`${response.command}: ${response.output || "OK"}`);
    await refreshAll({ preserveSelection: true, silent: true });
  } catch (error) {
    showToast(error.message, true);
  }
}

async function setWritableVariable(variable) {
  if (!state.selectedUPS) {
    return;
  }
  const control = document.querySelector(`[data-variable-input="${cssEscape(variable.name)}"]`);
  if (!control) {
    return;
  }
  const value = String(control.value == null ? "" : control.value).trim();
  try {
    const response = await fetchJSON(`/api/ups/${encodeURIComponent(state.selectedUPS)}/setvar`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ var: variable.name, value }),
    });
    showToast(`${response.variable} updated to ${response.value || "(empty)"}.`);
    await loadUPSDetail(state.selectedUPS, { silent: true });
    await refreshAll({ preserveSelection: true, silent: true });
  } catch (error) {
    showToast(error.message, true);
  }
}

function renderHealth() {
  if (!state.health) {
    return;
  }
  const cards = [
    ["Version", state.health.version],
    ["Serial", state.health.serial || "unknown"],
    ["Uptime", formatDuration(state.health.uptime_seconds)],
    ["Disk free", formatBytes(state.health.disk_free_bytes)],
    ["CPU temp", state.health.cpu_temperature_celsius == null ? "unavailable" : `${state.health.cpu_temperature_celsius.toFixed(1)} C`],
    ["UPS count", String(state.health.upses.length)],
  ];
  els.metrics.innerHTML = cards.map(([label, value]) => `
    <article class="metric-card">
      <span class="eyebrow">${escapeHTML(label)}</span>
      <div class="metric-value">${escapeHTML(value)}</div>
    </article>
  `).join("");
}

function renderUPSGrid() {
  if (state.upses.length === 0) {
    els.upsGrid.innerHTML = `
      <div class="empty-state">
        <h3>No UPS devices discovered</h3>
        <p>Plug in a supported UPS and the agent will populate telemetry and available controls here.</p>
      </div>
    `;
    return;
  }

  els.upsGrid.innerHTML = state.upses.map((ups) => {
    const chipClass = statusClass(ups.status);
    return `
      <article class="ups-card ${ups.name === state.selectedUPS ? "is-selected" : ""}" data-ups-name="${escapeAttribute(ups.name)}" tabindex="0">
        <header>
          <div>
            <h3>${escapeHTML(ups.name)}</h3>
            <p>${escapeHTML(ups.driver)}</p>
          </div>
          <span class="chip ${chipClass}">${escapeHTML(ups.status)}</span>
        </header>
        <div class="stat-grid">
          ${miniMetric("Charge", formatPercent(ups.battery_charge_percent))}
          ${miniMetric("Load", formatPercent(ups.load_percent))}
          ${miniMetric("Runtime", formatDuration(ups.runtime_seconds))}
          ${miniMetric("Output", formatVoltage(ups.output_voltage))}
        </div>
      </article>
    `;
  }).join("");

  els.upsGrid.querySelectorAll(".ups-card").forEach((card) => {
    const select = () => loadUPSDetail(card.dataset.upsName);
    card.addEventListener("click", select);
    card.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        select();
      }
    });
  });
}

function renderDetail() {
  if (!state.detail) {
    renderEmptyDetail();
    return;
  }
  const detail = state.detail;
  const metrics = detail.metrics;
  els.detail.innerHTML = `
    <div class="detail-heading">
      <div>
        <span class="eyebrow">UPS detail</span>
        <h2>${escapeHTML(detail.name)}</h2>
        <p class="muted">${escapeHTML(detail.driver)} • Updated ${new Date(detail.updated_at).toLocaleTimeString()}</p>
      </div>
      <span class="chip ${statusClass(detail.status)}">${escapeHTML(detail.status)}</span>
    </div>

    <section>
      <div class="section-head">
        <h3>Live metrics</h3>
      </div>
      <div class="detail-grid">
        ${detailMetric("Battery charge", formatPercent(metrics.battery_charge_percent))}
        ${detailMetric("Load", formatPercent(metrics.load_percent))}
        ${detailMetric("Runtime", formatDuration(metrics.runtime_seconds))}
        ${detailMetric("Input voltage", formatVoltage(metrics.input_voltage))}
        ${detailMetric("Output voltage", formatVoltage(metrics.output_voltage))}
        ${detailMetric("Battery voltage", formatVoltage(metrics.battery_voltage))}
      </div>
    </section>

    <section>
      <div class="section-head">
        <h3>Commands</h3>
        <span class="helper">All NUT instant commands the node can execute are exposed here.</span>
      </div>
      ${renderCommands(detail.commands)}
    </section>

    <section>
      <div class="section-head">
        <h3>Writable settings</h3>
        <span class="helper">Any writable NUT variables detected on this UPS are editable here.</span>
      </div>
      ${renderWritable(detail.writable)}
    </section>

    <section>
      <div class="section-head">
        <h3>Raw variables</h3>
      </div>
      <div class="json-card"><pre>${escapeHTML(JSON.stringify(detail.variables, null, 2))}</pre></div>
    </section>

    <div class="footer-links">
      <a href="/status">Public status</a>
      <a href="/status/details">Detailed JSON</a>
      <a href="/healthz">Health payload</a>
      <a href="/settings">Settings</a>
      <a href="/auth/logout">Sign out</a>
    </div>
  `;

  els.detail.querySelectorAll("[data-command]").forEach((button) => {
    button.addEventListener("click", () => {
      const command = detail.commands.find((item) => item.name === button.dataset.command);
      if (!command) {
        return;
      }
      if (command.destructive) {
        openConfirmModal(command);
        return;
      }
      runCommand(command);
    });
  });

  els.detail.querySelectorAll("[data-variable-submit]").forEach((button) => {
  button.addEventListener("click", () => {
    const variable = detail.writable.find((item) => item.name === button.dataset.variableSubmit);
    if (!variable) {
    return;
    }
    setWritableVariable(variable);
  });
  });
}

function renderCommands(commands) {
  if (!commands || commands.length === 0) {
    return `
      <div class="empty-state">
        <p>This UPS does not report any instant commands through NUT.</p>
      </div>
    `;
  }
  return `<div class="command-grid">${commands.map((command) => `
    <article class="command-card">
      <div>
        <span class="eyebrow">${command.destructive ? "Destructive" : "Command"}</span>
        <h3>${escapeHTML(command.name)}</h3>
        <p>${escapeHTML(command.description || "No description reported by NUT.")}</p>
      </div>
      <button class="button ${command.destructive ? "button--ghost" : "button--primary"}" data-command="${escapeAttribute(command.name)}">
        ${command.destructive ? "Confirm and run" : "Run command"}
      </button>
    </article>
  `).join("")}</div>`;
}

function renderEmptyDetail(message) {
  els.detail.innerHTML = `
    <div class="empty-state">
      <h3>Select a UPS</h3>
      <p>${escapeHTML(message || "Pick a UPS card to inspect full telemetry, raw variables, and supported commands.")}</p>
    </div>
  `;
}

function renderWritable(writable) {
  if (!writable || writable.length === 0) {
    return `
      <div class="empty-state">
        <p>This UPS does not report any writable NUT variables.</p>
      </div>
    `;
  }
  return `<div class="command-grid">${writable.map((variable) => `
    <article class="variable-card">
      <div>
        <span class="eyebrow">${escapeHTML(variable.editor)} editor</span>
        <h3>${escapeHTML(variable.name)}</h3>
        <p>${escapeHTML(variable.description || "No description reported by NUT.")}</p>
      </div>
      ${renderVariableInput(variable)}
      <button class="button button--primary" data-variable-submit="${escapeAttribute(variable.name)}">Apply setting</button>
    </article>
  `).join("")}</div>`;
}

function renderVariableInput(variable) {
  const value = variable.current_value || "";
  if (variable.editor === "select") {
    return `
      <label class="field">
        <span>Value</span>
        <select data-variable-input="${escapeAttribute(variable.name)}">
          ${variable.options.map((option) => `<option value="${escapeAttribute(option)}" ${option === value ? "selected" : ""}>${escapeHTML(option)}</option>`).join("")}
        </select>
      </label>
    `;
  }

  const min = variable.min == null ? "" : ` min="${escapeAttribute(variable.min)}"`;
  const max = variable.max == null ? "" : ` max="${escapeAttribute(variable.max)}"`;
  const type = variable.editor === "number" ? "number" : "text";
  return `
    <label class="field">
      <span>Value</span>
      <input data-variable-input="${escapeAttribute(variable.name)}" type="${type}" value="${escapeAttribute(value)}"${min}${max}>
    </label>
  `;
}

function openConfirmModal(command) {
  state.pendingCommand = command;
  els.confirmText.textContent = `Type ${command.name} to confirm execution on ${state.selectedUPS}.`;
  els.confirmInput.value = "";
  els.confirmSubmit.disabled = true;
  els.confirmModal.classList.add("is-open");
  els.confirmInput.focus();
}

function closeConfirmModal() {
  state.pendingCommand = null;
  els.confirmModal.classList.remove("is-open");
}

function showToast(message, isError) {
  els.toast.textContent = message;
  els.toast.classList.add("is-visible");
  els.toast.style.borderColor = isError ? "rgba(185, 28, 28, 0.35)" : "";
  if (state.toastTimer) {
    window.clearTimeout(state.toastTimer);
  }
  state.toastTimer = window.setTimeout(() => {
    els.toast.classList.remove("is-visible");
    els.toast.style.borderColor = "";
  }, 3600);
}

function miniMetric(label, value) {
  return `<div class="mini-card"><span class="eyebrow">${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong></div>`;
}

function detailMetric(label, value) {
  return `<div class="detail-card"><span class="eyebrow">${escapeHTML(label)}</span><div class="metric-value">${escapeHTML(value)}</div></div>`;
}

function formatPercent(value) {
  return value == null ? "unavailable" : `${Number(value).toFixed(0)}%`;
}

function formatVoltage(value) {
  return value == null ? "unavailable" : `${Number(value).toFixed(1)} V`;
}

function formatDuration(value) {
  if (value == null) {
    return "unavailable";
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

function formatBytes(bytes) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = Number(bytes);
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function statusClass(status) {
  const normalized = String(status || "unknown").toLowerCase();
  if (normalized.includes("ob") || normalized.includes("dischrg") || normalized === "unknown") {
    return "chip--warn";
  }
  if (normalized.includes("replace") || normalized.includes("fault") || normalized.includes("shutdown")) {
    return "chip--danger";
  }
  return "";
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function escapeAttribute(value) {
  return escapeHTML(value);
}

function cssEscape(value) {
  if (window.CSS && typeof window.CSS.escape === "function") {
    return window.CSS.escape(value);
  }
  return String(value).replaceAll('"', '\\"');
}

els.refreshNow.addEventListener("click", () => refreshAll({ preserveSelection: true }));
els.refreshToggle.addEventListener("click", () => {
  state.paused = !state.paused;
  els.refreshToggle.textContent = state.paused ? "Resume auto refresh" : "Pause auto refresh";
  if (!state.paused) {
    state.nextRefreshAt = Date.now() + POLL_INTERVAL_MS;
  }
  scheduleRefresh();
  updateRefreshCountdown();
});

els.confirmInput.addEventListener("input", () => {
  els.confirmSubmit.disabled = !state.pendingCommand || els.confirmInput.value.trim() !== state.pendingCommand.name;
});
els.confirmCancel.addEventListener("click", closeConfirmModal);
els.confirmSubmit.addEventListener("click", async () => {
  if (!state.pendingCommand) {
    return;
  }
  const command = state.pendingCommand;
  closeConfirmModal();
  await runCommand(command);
});
els.confirmModal.addEventListener("click", (event) => {
  if (event.target === els.confirmModal) {
    closeConfirmModal();
  }
});

initTheme();
startTimers();
renderEmptyDetail();
refreshAll({ preserveSelection: true, silent: true });
