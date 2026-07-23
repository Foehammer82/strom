const THEME_PREF_STORAGE_KEY = "strom-theme-preference";
const LEGACY_THEME_STORAGE_KEY = "strom-theme";
const prefersDarkMedia = window.matchMedia("(prefers-color-scheme: dark)");

const state = {
  health: null,
  metricHistory: [],
  metricHistoryLong: [],
  metricHistoryLongFetchedAt: 0,
  healthStreamConnected: false,
  upses: [],
  selectedUPS: null,
  detail: null,
  toastTimer: null,
  pendingCommand: null,
  themePreference: "system",
  profileMenuOpen: false,
  dirtyVariables: new Set(),
  trendCache: {},
  metricDetailLabel: null,
  metricDetailWindow: "10m",
  metricDetailRefreshTimer: null,
};

// Metrics that get a background trend sparkline on their metric card, keyed
// by the card label used in renderHealth(). Each extractor pulls the
// relevant numeric field out of a metricSample (see agent/internal/api/history.go).
const TREND_EXTRACTORS = {
  "CPU usage": (sample) => sample.cpu_usage_percent,
  "CPU temp": (sample) => sample.cpu_temperature_celsius,
  "Memory used": (sample) => sample.memory_used_bytes,
  "Disk free": (sample) => sample.disk_free_bytes,
};

// TREND_FORMATTERS renders a single sample value for the hover tooltip and
// the metric detail dialog, keyed the same way as TREND_EXTRACTORS.
const TREND_FORMATTERS = {
  "CPU usage": (value) => `${value.toFixed(1)}%`,
  "CPU temp": (value) => `${value.toFixed(1)} C`,
  "Memory used": (value) => formatBytes(value),
  "Disk free": (value) => formatBytes(value),
};

// METRIC_DETAIL_WINDOWS maps the dialog's window buttons to a duration in
// milliseconds used to slice state.metricHistoryLong; "10m" is handled
// separately since it's served live from state.metricHistory instead.
const METRIC_DETAIL_WINDOWS = {
  "10m": 10 * 60 * 1000,
  "1h": 60 * 60 * 1000,
  "6h": 6 * 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
};

// METRIC_DETAIL_TICK_INTERVALS_MS is the spacing between the detail
// dialog's x-axis gridlines for each window, chosen to give a denser set
// of reference marks (10-12) across that window's duration than a typical
// axis label count so the snapped hover point tracks the plotted line more
// closely. Gridlines land on absolute clock boundaries (see
// computeTimeTicks()), and hovering the chart snaps to whichever of these
// gridlines is nearest the cursor, then further snaps to the actual sample
// closest to that instant so the point marker always lands exactly on the
// rendered polyline (see handleMetricDetailHover).
const METRIC_DETAIL_TICK_INTERVALS_MS = {
  "10m": 60 * 1000,
  "1h": 5 * 60 * 1000,
  "6h": 30 * 60 * 1000,
  "24h": 2 * 60 * 60 * 1000,
};

// How often to refetch /api/health/history/long while the metric detail
// dialog is open on a window wider than 10 minutes (the long buffer only
// gains a new sample once a minute, see agent/internal/api/history.go).
const METRIC_DETAIL_LONG_REFRESH_MS = 60 * 1000;

// METRIC_DETAIL_CHART is the detail dialog's SVG viewBox size, and
// METRIC_DETAIL_MARGIN reserves space around the plotted data for the
// axis value/time labels (rendered as HTML overlays, not SVG <text>, so
// they aren't skewed by the viewBox's non-uniform preserveAspectRatio="none"
// scaling). All chart math below works in "plot space" (0,0 at the top-left
// of the plotted area, sized METRIC_DETAIL_PLOT_WIDTH x METRIC_DETAIL_PLOT_HEIGHT)
// and only adds the margin offset when writing final SVG coordinates.
const METRIC_DETAIL_CHART = { width: 600, height: 220 };
const METRIC_DETAIL_MARGIN = { top: 10, right: 16, bottom: 28, left: 60 };
const METRIC_DETAIL_PLOT_WIDTH = METRIC_DETAIL_CHART.width - METRIC_DETAIL_MARGIN.left - METRIC_DETAIL_MARGIN.right;
const METRIC_DETAIL_PLOT_HEIGHT = METRIC_DETAIL_CHART.height - METRIC_DETAIL_MARGIN.top - METRIC_DETAIL_MARGIN.bottom;

const els = {
  topbar: document.querySelector(".topbar"),
  profileMenu: document.getElementById("profile-menu"),
  topbarToolbar: document.getElementById("topbar-toolbar"),
  profileMenuToggles: Array.from(document.querySelectorAll("[data-menu-toggle]")),
  profileMenuPanel: document.getElementById("profile-menu-panel"),
  themeOptions: Array.from(document.querySelectorAll("[data-theme-option]")),
  metrics: document.getElementById("metrics-grid"),
  healthStreamStatus: document.getElementById("health-stream-status"),
  versionBadge: document.getElementById("version-badge"),
  upsCountBadge: document.getElementById("ups-count-badge"),
  upsGrid: document.getElementById("ups-grid"),
  detail: document.getElementById("ups-detail"),
  toast: document.getElementById("toast"),
  confirmModal: document.getElementById("confirm-modal"),
  confirmText: document.getElementById("confirm-text"),
  confirmSubmit: document.getElementById("confirm-submit"),
  confirmCancel: document.getElementById("confirm-cancel"),
  rawJsonModal: document.getElementById("raw-json-modal"),
  rawJsonNameBadge: document.getElementById("raw-json-name-badge"),
  rawJsonContent: document.getElementById("raw-json-content"),
  rawJsonCode: document.getElementById("raw-json-code"),
	  rawJsonCopy: document.getElementById("raw-json-copy"),
  rawJsonClose: document.getElementById("raw-json-close"),
  upsMetadataModal: document.getElementById("ups-metadata-modal"),
  upsMetadataSubtitle: document.getElementById("ups-metadata-subtitle"),
  upsMetadataForm: document.getElementById("ups-metadata-form"),
  upsMetadataDisplayName: document.getElementById("ups-metadata-display-name"),
  upsMetadataTags: document.getElementById("ups-metadata-tags"),
  upsMetadataError: document.getElementById("ups-metadata-error"),
  upsMetadataCancel: document.getElementById("ups-metadata-cancel"),
  upsMetadataSave: document.getElementById("ups-metadata-save"),
  chartTooltip: document.getElementById("chart-tooltip"),
  metricDetailModal: document.getElementById("metric-detail-modal"),
  metricDetailTitle: document.getElementById("metric-detail-title"),
  metricDetailClose: document.getElementById("metric-detail-close"),
  metricDetailWindowButtons: Array.from(document.querySelectorAll("[data-metric-window]")),
  metricDetailSvg: document.getElementById("metric-detail-svg"),
  metricDetailPolyline: document.getElementById("metric-detail-polyline"),
  metricDetailGuide: document.getElementById("metric-detail-guide"),
  metricDetailGuideY: document.getElementById("metric-detail-guide-y"),
  metricDetailGridX: document.getElementById("metric-detail-grid-x"),
  metricDetailGridY: document.getElementById("metric-detail-grid-y"),
  metricDetailAxisX: document.getElementById("metric-detail-axis-x"),
  metricDetailAxisY: document.getElementById("metric-detail-axis-y"),
  metricDetailPoint: document.getElementById("metric-detail-point"),
  metricDetailEmpty: document.getElementById("metric-detail-empty"),
  metricDetailSummary: document.getElementById("metric-detail-summary"),
  metricDetailRange: document.getElementById("metric-detail-range"),
};

function initTheme() {
  const savedPref = normalizeThemePreference(window.localStorage.getItem(THEME_PREF_STORAGE_KEY));
  const legacyTheme = normalizeThemePreference(window.localStorage.getItem(LEGACY_THEME_STORAGE_KEY));
  const initialPreference = savedPref || legacyTheme || "system";
  applyThemePreference(initialPreference, { persist: false });

  els.themeOptions.forEach((option) => {
    option.addEventListener("click", () => {
      const nextPreference = normalizeThemePreference(option.dataset.themeOption);
      if (!nextPreference) {
        return;
      }
      applyThemePreference(nextPreference);
      closeProfileMenu();
    });
  });

  if (typeof prefersDarkMedia.addEventListener === "function") {
    prefersDarkMedia.addEventListener("change", handleSystemThemeChange);
  } else {
    prefersDarkMedia.addListener(handleSystemThemeChange);
  }
}

function normalizeThemePreference(value) {
  if (value === "system" || value === "light" || value === "dark") {
    return value;
  }
  return null;
}

function resolveTheme(preference) {
  if (preference === "light" || preference === "dark") {
    return preference;
  }
  return prefersDarkMedia.matches ? "dark" : "light";
}

function handleSystemThemeChange() {
  if (state.themePreference !== "system") {
    return;
  }
  applyThemePreference("system", { persist: false });
}

function applyThemePreference(preference, options = { persist: true }) {
  state.themePreference = preference;
  const resolvedTheme = resolveTheme(preference);
  document.documentElement.dataset.theme = resolvedTheme;

  els.themeOptions.forEach((option) => {
    option.setAttribute("aria-checked", option.dataset.themeOption === preference ? "true" : "false");
  });

  if (options.persist) {
    window.localStorage.setItem(THEME_PREF_STORAGE_KEY, preference);
    window.localStorage.setItem(LEGACY_THEME_STORAGE_KEY, resolvedTheme);
  }
}

function toggleProfileMenu() {
  if (!els.profileMenuPanel || els.profileMenuToggles.length === 0) {
    return;
  }
  if (state.profileMenuOpen) {
    closeProfileMenu({ focusTrigger: false });
    return;
  }
  state.profileMenuOpen = true;
  if (els.topbarToolbar) {
    els.topbarToolbar.classList.add("is-open");
  }
  if (els.topbar) {
    els.topbar.classList.add("is-menu-open");
  }
  els.profileMenuPanel.hidden = false;
  els.profileMenuToggles.forEach((toggle) => toggle.setAttribute("aria-expanded", "true"));
  focusSelectedThemeOption();
}

function closeProfileMenu(options = { focusTrigger: false }) {
  if (!els.profileMenuPanel || els.profileMenuToggles.length === 0) {
    return;
  }
  state.profileMenuOpen = false;
  if (els.topbarToolbar) {
    els.topbarToolbar.classList.remove("is-open");
  }
  if (els.topbar) {
    els.topbar.classList.remove("is-menu-open");
  }
  els.profileMenuPanel.hidden = true;
  els.profileMenuToggles.forEach((toggle) => toggle.setAttribute("aria-expanded", "false"));
  if (options.focusTrigger) {
    els.profileMenuToggles[0].focus();
  }
}

function focusSelectedThemeOption() {
  const index = els.themeOptions.findIndex((option) => option.getAttribute("aria-checked") === "true");
  const next = els.themeOptions[Math.max(0, index)] || els.themeOptions[0];
  if (next) {
    next.focus();
  }
}

function handleMenuOptionNavigation(event) {
  if (!state.profileMenuOpen) {
    return;
  }
  const focusedIndex = els.themeOptions.findIndex((option) => option === document.activeElement);
  if (event.key === "ArrowDown") {
    event.preventDefault();
    const nextIndex = focusedIndex < 0 ? 0 : (focusedIndex + 1) % els.themeOptions.length;
    els.themeOptions[nextIndex]?.focus();
    return;
  }
  if (event.key === "ArrowUp") {
    event.preventDefault();
    const nextIndex = focusedIndex < 0 ? els.themeOptions.length - 1 : (focusedIndex - 1 + els.themeOptions.length) % els.themeOptions.length;
    els.themeOptions[nextIndex]?.focus();
    return;
  }
  if (event.key === "ArrowRight") {
    event.preventDefault();
    const nextIndex = focusedIndex < 0 ? 0 : (focusedIndex + 1) % els.themeOptions.length;
    els.themeOptions[nextIndex]?.focus();
    return;
  }
  if (event.key === "ArrowLeft") {
    event.preventDefault();
    const nextIndex = focusedIndex < 0 ? els.themeOptions.length - 1 : (focusedIndex - 1 + els.themeOptions.length) % els.themeOptions.length;
    els.themeOptions[nextIndex]?.focus();
    return;
  }
  if (event.key === "Home") {
    event.preventDefault();
    els.themeOptions[0]?.focus();
    return;
  }
  if (event.key === "End") {
    event.preventDefault();
    els.themeOptions[els.themeOptions.length - 1]?.focus();
    return;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    closeProfileMenu({ focusTrigger: true });
    return;
  }
  if (event.key === "Tab") {
    closeProfileMenu({ focusTrigger: false });
  }
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

// refreshAll does a one-shot full reload of the UPS inventory + selected
// detail via REST. Health/metrics and the live UPS list are otherwise pushed
// continuously over /api/health/stream (see connectHealthStream() /
// updateLiveUPSData()); this is only used for the initial page load on
// browsers without EventSource support, since that fallback path never
// receives the SSE-driven updates.
async function refreshAll(options = {}) {
  try {
    const upses = await fetchJSON("/api/ups");
    state.upses = upses;

    if (!state.selectedUPS && upses.length > 0) {
      state.selectedUPS = upses[0].name;
    }
    if (options.preserveSelection !== false && state.selectedUPS) {
      const exists = upses.some((ups) => ups.name === state.selectedUPS);
      state.selectedUPS = exists ? state.selectedUPS : (upses[0] ? upses[0].name : null);
    }
    renderUPSGrid();
    if (state.selectedUPS) {
      await loadUPSDetail(state.selectedUPS, { silent: true });
    } else {
      renderEmptyDetail();
    }
    if (!options.silent) {
      showToast("Dashboard refreshed.");
    }
  } catch (error) {
    showToast(error.message, true);
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
    await loadUPSDetail(state.selectedUPS, { silent: true });
  } catch (error) {
    showToast(error.message, true);
  }
}

async function setWritableVariable(variableName, value) {
  const response = await fetchJSON(`/api/ups/${encodeURIComponent(state.selectedUPS)}/setvar`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ var: variableName, value }),
  });
  return response;
}

function handleVariableInputChange(variable, control) {
  const original = control.dataset.variableOriginal || "";
  const current = String(control.value == null ? "" : control.value).trim();
  const isDirty = current !== original;
  if (isDirty) {
    state.dirtyVariables.add(variable.name);
  } else {
    state.dirtyVariables.delete(variable.name);
  }
  const row = control.closest("[data-variable-row]");
  if (row) {
    row.classList.toggle("is-dirty", isDirty);
    const tag = row.querySelector(".action-row-dirty-tag");
    if (tag) {
      tag.hidden = !isDirty;
    }
  }
  updateSettingsApplyButton();
}

function updateSettingsApplyButton() {
  const button = document.getElementById("settings-apply");
  const hint = document.getElementById("settings-apply-hint");
  if (!button) {
    return;
  }
  const count = state.dirtyVariables.size;
  button.disabled = count === 0;
  if (hint) {
    hint.textContent = count === 0 ? "No changes to apply." : `${count} setting${count === 1 ? "" : "s"} changed.`;
  }
}

async function applyDirtyVariables() {
  if (!state.selectedUPS || !state.detail || state.dirtyVariables.size === 0) {
    return;
  }
  const applyButton = document.getElementById("settings-apply");
  if (applyButton) {
    applyButton.disabled = true;
  }
  const names = Array.from(state.dirtyVariables);
  let succeeded = 0;
  let failed = 0;
  for (const name of names) {
    const variable = state.detail.writable.find((item) => item.name === name);
    const control = document.querySelector(`[data-variable-input="${cssEscape(name)}"]`);
    if (!variable || !control) {
      state.dirtyVariables.delete(name);
      continue;
    }
    const value = String(control.value == null ? "" : control.value).trim();
    try {
      await setWritableVariable(variable.name, value);
      state.dirtyVariables.delete(name);
      succeeded += 1;
    } catch (error) {
      failed += 1;
      showToast(`${variable.name}: ${error.message}`, true);
    }
  }
  if (succeeded > 0) {
    showToast(succeeded === 1 ? "Setting applied." : `${succeeded} settings applied.`);
  }
  await loadUPSDetail(state.selectedUPS, { silent: true });
}

function renderHealth() {
  if (!state.health) {
    return;
  }
  if (els.versionBadge) {
    els.versionBadge.textContent = state.health.version;
  }
  const cards = [
    ["Uptime", formatDuration(state.health.uptime_seconds)],
    ["Disk free", formatBytes(state.health.disk_free_bytes)],
    ["CPU temp", state.health.cpu_temperature_celsius == null ? "unavailable" : `${state.health.cpu_temperature_celsius.toFixed(1)} C`],
    ["CPU usage", state.health.cpu_usage_percent == null ? "unavailable" : `${state.health.cpu_usage_percent.toFixed(1)}%`],
    ["Memory used", `${formatBytes(state.health.memory_used_bytes)} / ${formatBytes(state.health.memory_total_bytes)}`],
  ];
  els.metrics.innerHTML = cards.map(([label, value]) => {
    const interactive = Boolean(TREND_EXTRACTORS[label]);
    return `
    <article class="metric-card ${interactive ? "metric-card--interactive" : ""}" data-metric-label="${escapeAttribute(label)}" ${interactive ? 'tabindex="0" role="button" aria-haspopup="dialog"' : ""}>
      <span class="eyebrow">${escapeHTML(label)}</span>
      <div class="metric-value">${escapeHTML(value)}</div>
      ${renderMetricTrend(label)}
    </article>
  `;
  }).join("");
  wireMetricCards();

  // Keep an open metric detail dialog in sync with the live 10 minute
  // window without requiring the user to reopen it.
  if (state.metricDetailLabel && state.metricDetailWindow === "10m") {
    renderMetricDetailChart();
  }
}

// wireMetricCards attaches click-to-open-detail listeners to the freshly
// rendered .metric-card elements. Cards are re-created via innerHTML on
// every render, so listeners are (re)attached each time, mirroring the same
// pattern used for .ups-card in renderUPSGrid(). Hover tooltips only exist
// in the metric detail dialog (see handleMetricDetailHover()), not on these
// small card sparklines.
function wireMetricCards() {
  els.metrics.querySelectorAll(".metric-card[data-metric-label]").forEach((card) => {
    const label = card.dataset.metricLabel;
    if (!TREND_EXTRACTORS[label]) {
      return;
    }
    const open = () => openMetricDetailModal(label);
    card.addEventListener("click", open);
    card.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        open();
      }
    });
  });
}

// renderMetricTrend builds a small background sparkline (an inline SVG
// polyline) for the given card label from state.metricHistory, if that
// label has a TREND_EXTRACTORS entry and enough non-null samples. This is a
// purely decorative preview; hovering it does nothing, see
// handleMetricDetailHover() for the interactive chart in the detail dialog.
// Returns an empty string when there's nothing meaningful to draw yet.
function renderMetricTrend(label) {
  const extractor = TREND_EXTRACTORS[label];
  if (!extractor) {
    return "";
  }
  const sparkline = buildSparkline(state.metricHistory, extractor, 100, 32);
  if (!sparkline) {
    return "";
  }
  return `<svg class="metric-trend" viewBox="0 0 100 32" preserveAspectRatio="none" aria-hidden="true">
    <polyline points="${sparkline.points}"></polyline>
  </svg>`;
}

// buildSparkline converts an array of metricSample values into an SVG
// <polyline> "points" attribute (autoscaled to [0, height] per metric so
// low-magnitude series like CPU usage still show visible movement), along
// with the plotted {x, timestamp, value} for each retained sample so callers
// can support hover tooltips. Returns null when there are fewer than 2
// usable samples.
function buildSparkline(samples, extractor, width, height) {
  const plotted = [];
  for (const sample of samples) {
    const value = extractor(sample);
    if (value == null || !Number.isFinite(value)) {
      continue;
    }
    plotted.push({ timestamp: sample.timestamp, value });
  }
  if (plotted.length < 2) {
    return null;
  }
  const values = plotted.map((point) => point.value);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const step = width / (plotted.length - 1);
  plotted.forEach((point, index) => {
    point.x = index * step;
    point.y = height - ((point.value - min) / range) * height;
  });
  return {
    points: plotted.map((point) => `${point.x.toFixed(2)},${point.y.toFixed(2)}`).join(" "),
    plotted,
    min,
    max,
    width,
    height,
  };
}

// buildTimeSeriesChart converts samples into an SVG-ready time series where
// the x position is proportional to elapsed time within [startTime,
// endTime] (unlike buildSparkline's uniform index spacing). The metric
// detail dialog needs this for its axis gridlines and snap-to-gridline
// hover to line up with real clock time. Returns null when there are fewer
// than 2 usable samples.
function buildTimeSeriesChart(samples, extractor, startTime, endTime, width, height) {
  const plotted = [];
  for (const sample of samples) {
    const value = extractor(sample);
    if (value == null || !Number.isFinite(value)) {
      continue;
    }
    const timestamp = new Date(sample.timestamp).getTime();
    if (Number.isNaN(timestamp)) {
      continue;
    }
    plotted.push({ timestamp, value });
  }
  if (plotted.length < 2) {
    return null;
  }
  plotted.sort((a, b) => a.timestamp - b.timestamp);
  const values = plotted.map((point) => point.value);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const timeRange = endTime - startTime || 1;
  plotted.forEach((point) => {
    point.x = ((point.timestamp - startTime) / timeRange) * width;
    point.y = height - ((point.value - min) / range) * height;
  });
  return {
    points: plotted.map((point) => `${point.x.toFixed(2)},${point.y.toFixed(2)}`).join(" "),
    plotted,
    min,
    max,
    width,
    height,
    startTime,
    endTime,
  };
}

// computeTimeTicks returns clock-aligned gridline timestamps between
// startTime and endTime spaced intervalMs apart (e.g. every 10 minutes on
// the clock, not just every 10 minutes counting back from "now"), so the
// x-axis gridlines land on clean, stable marks like :00/:10/:20 instead of
// drifting as the live window advances.
function computeTimeTicks(startTime, endTime, intervalMs) {
  const ticks = [];
  const first = Math.ceil(startTime / intervalMs) * intervalMs;
  for (let tick = first; tick <= endTime; tick += intervalMs) {
    ticks.push(tick);
  }
  return ticks;
}

// computeValueTicks returns `count` evenly spaced values between min and
// max (inclusive), used for the Y-axis gridlines.
function computeValueTicks(min, max, count) {
  const ticks = [];
  const range = max - min;
  for (let i = 0; i < count; i += 1) {
    ticks.push(min + (range * i) / (count - 1));
  }
  return ticks;
}

function hideChartTooltip() {
  if (els.chartTooltip) {
    els.chartTooltip.hidden = true;
  }
}

function showChartTooltip(clientX, clientY, label, point) {
  if (!els.chartTooltip) {
    return;
  }
  const formatter = TREND_FORMATTERS[label];
  const value = formatter ? formatter(point.value) : String(point.value);
  els.chartTooltip.innerHTML = `<strong>${escapeHTML(value)}</strong><span>${escapeHTML(formatTooltipTimestamp(point.timestamp))}</span>`;
  els.chartTooltip.hidden = false;
  const offset = 14;
  els.chartTooltip.style.left = `${clientX + offset}px`;
  els.chartTooltip.style.top = `${clientY + offset}px`;
}

function formatTooltipTimestamp(timestamp) {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

// formatAxisTimestamp renders a compact x-axis gridline label: just the
// time for short windows, plus the date for "24h" where marks can span more
// than one calendar day.
function formatAxisTimestamp(timestamp, windowKey) {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  if (windowKey === "24h") {
    return date.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric" });
  }
  return date.toLocaleString(undefined, { hour: "numeric", minute: "2-digit" });
}

function setHealthStreamStatus(connected) {
  state.healthStreamConnected = connected;
  if (!els.healthStreamStatus) {
    return;
  }
  els.healthStreamStatus.textContent = connected ? "Live" : "Reconnecting\u2026";
  els.healthStreamStatus.classList.toggle("is-live", connected);
  els.healthStreamStatus.classList.toggle("is-reconnecting", !connected);
}

// connectHealthStream opens the Server-Sent Events connection that drives
// the entire live dashboard: node health metrics + trend history, and the
// UPS list/detail metrics (see /api/health/stream in
// agent/internal/api/api.go). EventSource reconnects automatically on its
// own, so there's no manual retry loop here.
function connectHealthStream() {
  if (typeof window.EventSource !== "function") {
    return;
  }
  const source = new EventSource("/api/health/stream");
  source.addEventListener("open", () => setHealthStreamStatus(true));
  source.addEventListener("history", (event) => {
    try {
      state.metricHistory = JSON.parse(event.data) || [];
    } catch (error) {
      return;
    }
    renderHealth();
  });
  source.addEventListener("health", (event) => {
    let health;
    try {
      health = JSON.parse(event.data);
    } catch (error) {
      return;
    }
    state.health = health;
    setHealthStreamStatus(true);
    renderHealth();
    updateLiveUPSData(health.upses || []);
  });
  source.onerror = () => {
    setHealthStreamStatus(false);
  };
}

// updateLiveUPSData applies a fresh UPS list snapshot (pushed every
// liveStreamInterval via the health SSE event) to the grid and, for the
// currently selected UPS, patches just its live metrics/status in place
// (see updateDetailMetrics) rather than re-fetching + re-rendering the full
// detail (variables/commands/writable), which only change on explicit
// user actions and are lazily loaded once per selection via loadUPSDetail().
function updateLiveUPSData(upses) {
  state.upses = upses;
  renderUPSGrid();

  const stillSelected = state.selectedUPS && upses.some((ups) => ups.name === state.selectedUPS);
  if (!stillSelected) {
    if (upses.length === 0) {
      state.selectedUPS = null;
      state.detail = null;
      renderEmptyDetail();
      return;
    }
    loadUPSDetail(upses[0].name, { silent: true });
    return;
  }

  const match = upses.find((ups) => ups.name === state.selectedUPS);
  if (match) {
    updateDetailMetrics(match);
  }
}

// updateDetailMetrics patches the live-changing parts of the currently
// rendered UPS detail view (status chip/accent + the metrics grid values)
// in place, without touching the variables/commands/settings sections. This
// keeps in-progress settings edits (state.dirtyVariables) untouched by live
// ticks, since only renderDetail() (a full re-render on explicit selection
// or reload) ever clears them.
function updateDetailMetrics(metrics) {
  if (!state.detail || state.detail.name !== metrics.name) {
    return;
  }
  state.detail.metrics = metrics;
  state.detail.status = metrics.status;

  const chip = els.detail.querySelector(".detail-heading-actions .chip");
  if (chip) {
    chip.textContent = metrics.status;
    chip.className = `chip ${statusClass(metrics.status)}`;
  }
  els.detail.classList.remove("detail-shell--good", "detail-shell--warn", "detail-shell--danger");
  els.detail.classList.add(accentClassForStatus(metrics.status));

  const values = {
    "battery-charge": formatPercent(metrics.battery_charge_percent),
    "load": formatPercent(metrics.load_percent),
    "runtime": formatDuration(metrics.runtime_seconds),
    "input-voltage": formatVoltage(metrics.input_voltage),
    "output-voltage": formatVoltage(metrics.output_voltage),
    "battery-voltage": formatVoltage(metrics.battery_voltage),
  };
  Object.entries(values).forEach(([key, value]) => {
    const node = els.detail.querySelector(`[data-metric="${key}"] .metric-value--compact`);
    if (node) {
      node.textContent = value;
    }
  });
}

function renderUPSGrid() {
  if (els.upsCountBadge) {
    els.upsCountBadge.textContent = String(state.upses.length);
  }
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
    const accentClass = chipClass ? `ups-card--${chipClass.replace("chip--", "")}` : "ups-card--good";
	const metadata = ups.metadata || {};
	const title = metadata.display_name || ups.name;
    return `
      <article class="ups-card ${accentClass} ${ups.name === state.selectedUPS ? "is-selected" : ""}" data-ups-name="${escapeAttribute(ups.name)}" tabindex="0">
        <header>
          <div>
            <h3>${escapeHTML(title)}</h3>
            <p>${escapeHTML(ups.driver)}</p>
          </div>
          <span class="chip ${chipClass}">${escapeHTML(ups.status)}</span>
        </header>
        <div class="stat-grid">
          ${statItem("Charge", formatPercent(ups.battery_charge_percent))}
          ${statItem("Load", formatPercent(ups.load_percent))}
          ${statItem("Runtime", formatDuration(ups.runtime_seconds))}
          ${statItem("Output", formatVoltage(ups.output_voltage))}
        </div>
      </article>
    `;
  }).join("");

  els.upsGrid.querySelectorAll(".ups-card").forEach((card) => {
    const select = () => {
      if (state.dirtyVariables.size > 0 && card.dataset.upsName !== state.selectedUPS) {
        const proceed = window.confirm("You have unapplied setting changes on this UPS. Switch UPS and discard them?");
        if (!proceed) {
          return;
        }
      }
      loadUPSDetail(card.dataset.upsName);
    };
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
  state.dirtyVariables.clear();
  const detail = state.detail;
  const metrics = detail.metrics;
  const metadata = detail.metadata || {};
  const title = metadata.display_name || detail.name;
  els.detail.classList.remove("detail-shell--good", "detail-shell--warn", "detail-shell--danger");
  els.detail.classList.add(accentClassForStatus(detail.status));
  els.detail.innerHTML = `
    <div class="detail-heading">
      <div class="detail-title-row">
        <h2 class="detail-title">${escapeHTML(title)}</h2>
    		<span class="detail-operational-name">${escapeHTML(detail.name)}</span>
        <span class="detail-meta-driver">${escapeHTML(detail.driver)}</span>
      </div>
      <div class="detail-heading-actions">
        <span class="chip ${statusClass(detail.status)}">${escapeHTML(detail.status)}</span>
		<button type="button" class="button button--ghost button--compact" id="edit-ups-metadata">Edit details</button>
        <button type="button" class="button button--ghost button--compact" id="view-raw-json">Raw JSON</button>
      </div>
    </div>
  	${metadata.tags?.length ? `<div class="ups-detail-context">${metadata.tags.map((tag) => `<span class="tag">${escapeHTML(tag)}</span>`).join("")}</div>` : ""}

    <div class="detail-metrics-grid">
      ${detailMetric("Battery charge", formatPercent(metrics.battery_charge_percent), "battery-charge")}
      ${detailMetric("Load", formatPercent(metrics.load_percent), "load")}
      ${detailMetric("Runtime", formatDuration(metrics.runtime_seconds), "runtime")}
      ${detailMetric("Input voltage", formatVoltage(metrics.input_voltage), "input-voltage")}
      ${detailMetric("Output voltage", formatVoltage(metrics.output_voltage), "output-voltage")}
      ${detailMetric("Battery voltage", formatVoltage(metrics.battery_voltage), "battery-voltage")}
    </div>

    <section>
      <div class="section-head">
        <h3>UPS details</h3>
        <span class="helper">All reported NUT variables, grouped for scanning.</span>
      </div>
      ${renderVariableGroups(detail.variables)}
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
        <h3>Settings</h3>
        <span class="helper">Any writable NUT variables detected on this UPS are editable here.</span>
      </div>
      ${renderWritable(detail.writable)}
    </section>
  `;

  document.getElementById("view-raw-json")?.addEventListener("click", () => {
    openRawJsonModal(detail);
  });

	document.getElementById("edit-ups-metadata")?.addEventListener("click", () => {
		openUPSMetadataModal(detail);
	});


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

  els.detail.querySelectorAll("[data-variable-input]").forEach((control) => {
    const variable = detail.writable.find((item) => item.name === control.dataset.variableInput);
    if (!variable) {
      return;
    }
    const handleChange = () => handleVariableInputChange(variable, control);
    control.addEventListener("input", handleChange);
    control.addEventListener("change", handleChange);
  });

  const settingsApplyButton = els.detail.querySelector("#settings-apply");
  if (settingsApplyButton) {
    settingsApplyButton.addEventListener("click", applyDirtyVariables);
  }
  updateSettingsApplyButton();
}

function renderCommands(commands) {
  if (!commands || commands.length === 0) {
    return `
      <div class="empty-state">
        <p>This UPS does not report any instant commands through NUT.</p>
      </div>
    `;
  }
  return `<ul class="action-list">${commands.map((command) => {
    const presentation = controlPresentation(command);
    return `
    <li class="action-row ${command.destructive ? "action-row--destructive" : ""}">
      <div class="action-row-text">
        <div class="action-row-title-line">
          <span class="action-row-title">${escapeHTML(presentation.label)}</span>
		  <span class="action-row-identifier">${escapeHTML(command.name)}</span>
          ${command.destructive ? '<span class="tag tag--danger">Destructive</span>' : ""}
        </div>
        <p class="action-row-desc">${escapeHTML(presentation.description || command.description || "No description reported by NUT.")}</p>
      </div>
      <div class="action-row-controls">
        <button class="button button--compact ${command.destructive ? "button--danger" : "button--primary"}" data-command="${escapeAttribute(command.name)}">
          ${command.destructive ? "Confirm & run" : "Run"}
        </button>
      </div>
    </li>
  `;
  }).join("")}</ul>`;
}

function renderEmptyDetail(message) {
  els.detail.classList.remove("detail-shell--good", "detail-shell--warn", "detail-shell--danger");
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
  return `
    <ul class="action-list">${writable.map((variable) => {
      const presentation = controlPresentation(variable);
      return `
      <li class="action-row" data-variable-row="${escapeAttribute(variable.name)}">
        <div class="action-row-text">
          <div class="action-row-title-line">
            <span class="action-row-title">${escapeHTML(presentation.label)}</span>
			<span class="action-row-identifier">${escapeHTML(variable.name)}</span>
            <span class="tag">${escapeHTML(variable.editor)}</span>
            <span class="tag tag--warn action-row-dirty-tag" hidden>Modified</span>
          </div>
          <p class="action-row-desc">${escapeHTML(presentation.description || variable.description || "No description reported by NUT.")}</p>
        </div>
        <div class="action-row-controls">
          ${renderVariableInput(variable)}
        </div>
      </li>
    `;
    }).join("")}</ul>
    <div class="settings-apply-bar">
      <span id="settings-apply-hint" class="helper">No changes to apply.</span>
      <button id="settings-apply" class="button button--primary" type="button" disabled>Apply changes</button>
    </div>
  `;
}

const commonControlPresentation = {
  "beeper.disable": { label: "Disable audible alarm", description: "Turn off the UPS alarm." },
  "beeper.enable": { label: "Enable audible alarm", description: "Turn on the UPS alarm." },
  "beeper.mute": { label: "Mute audible alarm", description: "Silence the current UPS alarm." },
  "beeper.toggle": { label: "Toggle audible alarm", description: "Turn the UPS alarm on or off." },
  "load.off": { label: "Turn load off", description: "Immediately turn off power to connected equipment." },
  "load.on": { label: "Turn load on", description: "Turn on power to connected equipment." },
  "shutdown.return": { label: "Shut down until utility returns", description: "Turn off the load and restore it when utility power returns." },
  "shutdown.stayoff": { label: "Shut down and stay off", description: "Turn off the load until manually restarted." },
  "test.battery.start.deep": { label: "Run deep battery test", description: "Start an extended battery self-test." },
  "test.battery.start.quick": { label: "Run quick battery test", description: "Start a short battery self-test." },
  "ups.delay.reboot": { label: "Restart delay", description: "Seconds the UPS waits before restarting the load." },
  "ups.delay.shutdown": { label: "Shutdown delay", description: "Seconds the UPS waits before shutting down the load." },
  "ups.delay.start": { label: "Startup delay", description: "Seconds the UPS waits before restoring the load." },
};

function controlPresentation(control) {
  return commonControlPresentation[control.name] || { label: humanizeNUTIdentifier(control.name), description: "" };
}

function humanizeNUTIdentifier(name) {
  return String(name)
    .split(/[._-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function variableGroupFor(name) {
  if (name.startsWith("battery.")) return "Battery";
  if (name.startsWith("input.")) return "Input";
  if (name.startsWith("output.")) return "Output";
  if (name.startsWith("ups.")) return "UPS";
  if (name.startsWith("device.")) return "Device";
  if (name.startsWith("driver.")) return "Driver";
  if (name.startsWith("ambient.")) return "Environment";
  return "Other";
}

function renderVariableGroups(variables) {
  const groups = new Map();
  Object.entries(variables || {}).sort(([left], [right]) => left.localeCompare(right)).forEach(([name, value]) => {
    const group = variableGroupFor(name);
    if (!groups.has(group)) groups.set(group, []);
    groups.get(group).push([name, value]);
  });
  if (groups.size === 0) {
    return '<div class="empty-state"><p>No NUT variables are currently available.</p></div>';
  }
  const order = ["Battery", "Input", "Output", "UPS", "Device", "Driver", "Environment", "Other"];
  return `<div class="variable-groups">${order.filter((group) => groups.has(group)).map((group) => `
    <div class="variable-group">
      <h4>${escapeHTML(group)}</h4>
      <dl>${groups.get(group).map(([name, value]) => `<div><dt>${escapeHTML(name)}</dt><dd>${escapeHTML(value)}</dd></div>`).join("")}</dl>
    </div>
  `).join("")}</div>`;
}

function renderVariableInput(variable) {
  const value = variable.current_value || "";
  if (variable.editor === "select") {
    return `
      <select data-variable-input="${escapeAttribute(variable.name)}" data-variable-original="${escapeAttribute(value)}" aria-label="${escapeAttribute(variable.name)} value">
        ${variable.options.map((option) => `<option value="${escapeAttribute(option)}" ${option === value ? "selected" : ""}>${escapeHTML(option)}</option>`).join("")}
      </select>
    `;
  }

  const min = variable.min == null ? "" : ` min="${escapeAttribute(variable.min)}"`;
  const max = variable.max == null ? "" : ` max="${escapeAttribute(variable.max)}"`;
  const type = variable.editor === "number" ? "number" : "text";
  return `
    <input data-variable-input="${escapeAttribute(variable.name)}" data-variable-original="${escapeAttribute(value)}" aria-label="${escapeAttribute(variable.name)} value" type="${type}" value="${escapeAttribute(value)}"${min}${max}>
  `;
}

function openConfirmModal(command) {
  state.pendingCommand = command;
  els.confirmText.textContent = `Run ${command.name} on ${state.selectedUPS}? This action cannot be undone.`;
  els.confirmModal.classList.add("is-open");
  els.confirmCancel.focus();
}

function closeConfirmModal() {
  state.pendingCommand = null;
  els.confirmModal.classList.remove("is-open");
}

function openRawJsonModal(detail) {
  els.rawJsonNameBadge.textContent = detail.name;
  els.rawJsonCode.innerHTML = highlightJSON(detail.variables);
  els.rawJsonModal.classList.add("is-open");
  els.rawJsonClose.focus();
}

// highlightJSON renders a pretty-printed, colorized version of `value` as an
// HTML string for display inside a <code> element. It escapes only the HTML
// entities that matter (&, <, >) so the JSON string quote characters remain
// intact for the token regex below; the result is never used as anything but
// element content (never re-parsed as HTML from a different trust boundary).
function highlightJSON(value) {
  const json = JSON.stringify(value, null, 2)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
  const tokenPattern = /("(?:\\u[a-fA-F0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(?:true|false)\b|\bnull\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g;
  return json.replace(tokenPattern, (match, _string, isKey) => {
    let className = "json-token-number";
    if (/^"/.test(match)) {
      className = isKey ? "json-token-key" : "json-token-string";
    } else if (match === "true" || match === "false") {
      className = "json-token-boolean";
    } else if (match === "null") {
      className = "json-token-null";
    }
    return `<span class="${className}">${match}</span>`;
  });
}

async function copyRawJSON() {
  const value = els.rawJsonCode.textContent;
  try {
    await navigator.clipboard.writeText(value);
  } catch (_) {
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents(els.rawJsonCode);
    selection.removeAllRanges();
    selection.addRange(range);
    document.execCommand("copy");
    selection.removeAllRanges();
  }
  showToast("Raw JSON copied.");
}

function closeRawJsonModal() {
  els.rawJsonModal.classList.remove("is-open");
}

function openUPSMetadataModal(detail) {
  const metadata = detail.metadata || {};
  els.upsMetadataSubtitle.textContent = detail.name;
  els.upsMetadataDisplayName.value = metadata.display_name || "";
  els.upsMetadataDisplayName.placeholder = detail.name;
  els.upsMetadataTags.value = (metadata.tags || []).join(", ");
  els.upsMetadataError.hidden = true;
  els.upsMetadataError.textContent = "";
  els.upsMetadataModal.classList.add("is-open");
  els.upsMetadataDisplayName.focus();
}

function closeUPSMetadataModal() {
  els.upsMetadataModal.classList.remove("is-open");
}

// openMetricDetailModal opens the larger chart dialog for a single trend
// metric (label must have a TREND_EXTRACTORS entry). Defaults to the live
// 10 minute window, which is served entirely from state.metricHistory
// (already streaming); wider windows are fetched on demand from
// /api/health/history/long, see selectMetricDetailWindow().
function openMetricDetailModal(label) {
  if (!TREND_EXTRACTORS[label] || !els.metricDetailModal) {
    return;
  }
  state.metricDetailLabel = label;
  if (els.metricDetailTitle) {
    els.metricDetailTitle.textContent = label;
  }
  els.metricDetailModal.classList.add("is-open");
  if (els.metricDetailClose) {
    els.metricDetailClose.focus();
  }
  selectMetricDetailWindow(state.metricDetailWindow || "10m");
}

function closeMetricDetailModal() {
  if (els.metricDetailModal) {
    els.metricDetailModal.classList.remove("is-open");
  }
  state.metricDetailLabel = null;
  if (state.metricDetailRefreshTimer) {
    window.clearInterval(state.metricDetailRefreshTimer);
    state.metricDetailRefreshTimer = null;
  }
}

// selectMetricDetailWindow switches the dialog's active time window,
// fetching the long-retention buffer on demand for anything wider than the
// live 10 minute view, then (re)renders the chart.
async function selectMetricDetailWindow(windowKey) {
  state.metricDetailWindow = windowKey;
  els.metricDetailWindowButtons.forEach((button) => {
    button.classList.toggle("is-active", button.dataset.metricWindow === windowKey);
  });
  if (state.metricDetailRefreshTimer) {
    window.clearInterval(state.metricDetailRefreshTimer);
    state.metricDetailRefreshTimer = null;
  }
  if (windowKey === "10m") {
    renderMetricDetailChart();
    return;
  }
  await refreshMetricHistoryLong();
  renderMetricDetailChart();
  state.metricDetailRefreshTimer = window.setInterval(() => {
    refreshMetricHistoryLong().then(renderMetricDetailChart);
  }, METRIC_DETAIL_LONG_REFRESH_MS);
}

async function refreshMetricHistoryLong() {
  try {
    state.metricHistoryLong = await fetchJSON("/api/health/history/long");
    state.metricHistoryLongFetchedAt = Date.now();
  } catch (error) {
    showToast(error.message, true);
  }
}

// renderMetricDetailChart draws the active metric/window combination into
// the dialog's larger SVG: axis gridlines (patched into the
// #metric-detail-grid-x/-y <g> containers), the data <polyline>, and HTML
// axis value/time labels (patched into #metric-detail-axis-x/-y, overlaid
// on top of the SVG rather than drawn as SVG <text> so they aren't skewed
// by the viewBox's non-uniform preserveAspectRatio="none" scaling).
// Everything is computed in "plot space" (METRIC_DETAIL_PLOT_WIDTH x
// METRIC_DETAIL_PLOT_HEIGHT, origin at the plot's top-left) and only
// offset by METRIC_DETAIL_MARGIN when writing final coordinates, so the
// axis labels have dedicated space and gridlines/data never reach the
// container's rounded corners. Gridline/tick pixel-and-instant pairs plus
// the margin are cached on state.trendCache so handleMetricDetailHover()
// can snap to them without recomputing.
function renderMetricDetailChart() {
  const label = state.metricDetailLabel;
  if (!label || !els.metricDetailModal || !els.metricDetailModal.classList.contains("is-open")) {
    return;
  }
  const extractor = TREND_EXTRACTORS[label];
  const windowKey = state.metricDetailWindow || "10m";
  const windowMs = METRIC_DETAIL_WINDOWS[windowKey] || METRIC_DETAIL_WINDOWS["10m"];
  const source = windowKey === "10m" ? state.metricHistory : state.metricHistoryLong;
  const { width: chartWidth, height: chartHeight } = METRIC_DETAIL_CHART;
  const margin = METRIC_DETAIL_MARGIN;
  const plotWidth = METRIC_DETAIL_PLOT_WIDTH;
  const plotHeight = METRIC_DETAIL_PLOT_HEIGHT;
  const endTime = Date.now();
  const startTime = endTime - windowMs;
  const samples = source.filter((sample) => new Date(sample.timestamp).getTime() >= startTime);
  const series = buildTimeSeriesChart(samples, extractor, startTime, endTime, plotWidth, plotHeight);

  const tickIntervalMs = METRIC_DETAIL_TICK_INTERVALS_MS[windowKey] || METRIC_DETAIL_TICK_INTERVALS_MS["10m"];
  const xTicks = computeTimeTicks(startTime, endTime, tickIntervalMs).map((timestamp) => ({
    timestamp,
    x: ((timestamp - startTime) / windowMs) * plotWidth,
  }));
  if (els.metricDetailGridX) {
    els.metricDetailGridX.innerHTML = xTicks
      .map((tick) => {
        const x = (tick.x + margin.left).toFixed(2);
        return `<line class="metric-detail-grid-line" x1="${x}" y1="${margin.top}" x2="${x}" y2="${margin.top + plotHeight}"></line>`;
      })
      .join("");
  }
  if (els.metricDetailAxisX) {
    // Gridlines are intentionally dense (for closer hover-snap granularity),
    // but labeling every one of them would overlap; only label a subset so
    // the axis stays readable (roughly 5-6 labels regardless of tick count).
    const labelStride = Math.max(1, Math.ceil(xTicks.length / 6));
    els.metricDetailAxisX.innerHTML = xTicks
      .filter((_, index) => index % labelStride === 0)
      .map((tick) => {
        const leftPercent = (((tick.x + margin.left) / chartWidth) * 100).toFixed(2);
        return `<span style="left:${leftPercent}%">${escapeHTML(formatAxisTimestamp(tick.timestamp, windowKey))}</span>`;
      })
      .join("");
  }

  state.trendCache[`detail:${label}`] = series ? { ...series, xTicks, margin, plotWidth, plotHeight } : { xTicks, margin, plotWidth, plotHeight, plotted: [] };

  if (!series) {
    if (els.metricDetailGridY) {
      els.metricDetailGridY.innerHTML = "";
    }
    if (els.metricDetailAxisY) {
      els.metricDetailAxisY.innerHTML = "";
    }
    els.metricDetailPolyline.setAttribute("points", "");
    if (els.metricDetailEmpty) {
      els.metricDetailEmpty.hidden = false;
    }
    if (els.metricDetailSummary) {
      els.metricDetailSummary.textContent = "";
    }
    if (els.metricDetailRange) {
      els.metricDetailRange.textContent = "";
    }
    return;
  }

  const formatter = TREND_FORMATTERS[label];
  const valueRange = series.max - series.min || 1;
  const yTicks = computeValueTicks(series.min, series.max, 5).map((value) => ({
    value,
    y: plotHeight - ((value - series.min) / valueRange) * plotHeight,
  }));
  if (els.metricDetailGridY) {
    els.metricDetailGridY.innerHTML = yTicks
      .map((tick) => {
        const y = (tick.y + margin.top).toFixed(2);
        return `<line class="metric-detail-grid-line" x1="${margin.left}" y1="${y}" x2="${chartWidth - margin.right}" y2="${y}"></line>`;
      })
      .join("");
  }
  if (els.metricDetailAxisY) {
    els.metricDetailAxisY.innerHTML = yTicks
      .map((tick) => {
        const topPercent = (((tick.y + margin.top) / chartHeight) * 100).toFixed(2);
        const text = formatter ? formatter(tick.value) : String(Math.round(tick.value));
        return `<span style="top:${topPercent}%">${escapeHTML(text)}</span>`;
      })
      .join("");
  }

  if (els.metricDetailEmpty) {
    els.metricDetailEmpty.hidden = true;
  }
  els.metricDetailPolyline.setAttribute(
    "points",
    series.plotted.map((point) => `${(point.x + margin.left).toFixed(2)},${(point.y + margin.top).toFixed(2)}`).join(" "),
  );

  const latest = series.plotted[series.plotted.length - 1];
  if (els.metricDetailSummary && formatter) {
    els.metricDetailSummary.textContent = `Now: ${formatter(latest.value)} · Min: ${formatter(series.min)} · Max: ${formatter(series.max)}`;
  }
  if (els.metricDetailRange) {
    const first = series.plotted[0];
    els.metricDetailRange.textContent = `${formatTooltipTimestamp(first.timestamp)} \u2192 ${formatTooltipTimestamp(latest.timestamp)}`;
  }
}

// handleMetricDetailHover snaps the crosshair x position to whichever
// x-axis gridline tick is nearest the cursor, then snaps to the actual
// data sample closest to that tick's instant (rather than interpolating a
// value in between) so the crosshair and point marker always land exactly
// on a real vertex of the plotted polyline, and shows a tooltip for it.
// Hides everything (via handleMetricDetailLeave) once the cursor is
// outside the plotted chart area itself -- not just outside the <svg>,
// which now also covers the axis label margins.
function handleMetricDetailHover(event) {
  const label = state.metricDetailLabel;
  const series = state.trendCache[`detail:${label}`];
  if (!label || !series || !series.xTicks || series.xTicks.length === 0 || !series.plotted || series.plotted.length === 0) {
    handleMetricDetailLeave();
    return;
  }
  const rect = els.metricDetailSvg.getBoundingClientRect();
  if (rect.width === 0 || rect.height === 0) {
    return;
  }
  const { width: chartWidth, height: chartHeight } = METRIC_DETAIL_CHART;
  const margin = series.margin;
  const chartX = ((event.clientX - rect.left) / rect.width) * chartWidth;
  const chartY = ((event.clientY - rect.top) / rect.height) * chartHeight;
  const relX = chartX - margin.left;
  const relY = chartY - margin.top;
  if (relX < 0 || relX > series.plotWidth || relY < 0 || relY > series.plotHeight) {
    handleMetricDetailLeave();
    return;
  }

  let nearestTick = series.xTicks[0];
  let nearestTickDistance = Math.abs(nearestTick.x - relX);
  for (const tick of series.xTicks) {
    const distance = Math.abs(tick.x - relX);
    if (distance < nearestTickDistance) {
      nearestTick = tick;
      nearestTickDistance = distance;
    }
  }

  // Snap to the actual sample nearest that gridline instant (rather than
  // interpolating a value at the tick's exact timestamp) so the point
  // marker/crosshair always land exactly on a real vertex of the rendered
  // polyline instead of floating slightly off it between two samples.
  let nearestSample = series.plotted[0];
  let nearestSampleDistance = Math.abs(nearestSample.timestamp - nearestTick.timestamp);
  for (const sample of series.plotted) {
    const distance = Math.abs(sample.timestamp - nearestTick.timestamp);
    if (distance < nearestSampleDistance) {
      nearestSample = sample;
      nearestSampleDistance = distance;
    }
  }

  const guideX = nearestSample.x + margin.left;
  const guideY = nearestSample.y + margin.top;

  if (els.metricDetailGuide) {
    els.metricDetailGuide.setAttribute("x1", guideX.toFixed(2));
    els.metricDetailGuide.setAttribute("x2", guideX.toFixed(2));
    els.metricDetailGuide.hidden = false;
  }
  if (els.metricDetailGuideY) {
    els.metricDetailGuideY.setAttribute("y1", guideY.toFixed(2));
    els.metricDetailGuideY.setAttribute("y2", guideY.toFixed(2));
    els.metricDetailGuideY.hidden = false;
  }
  if (els.metricDetailPoint) {
    els.metricDetailPoint.style.left = `${((guideX / chartWidth) * 100).toFixed(2)}%`;
    els.metricDetailPoint.style.top = `${((guideY / chartHeight) * 100).toFixed(2)}%`;
    els.metricDetailPoint.hidden = false;
  }
  showChartTooltip(event.clientX, event.clientY, label, { timestamp: nearestSample.timestamp, value: nearestSample.value });
}

function handleMetricDetailLeave() {
  if (els.metricDetailGuide) {
    els.metricDetailGuide.hidden = true;
  }
  if (els.metricDetailGuideY) {
    els.metricDetailGuideY.hidden = true;
  }
  if (els.metricDetailPoint) {
    els.metricDetailPoint.hidden = true;
  }
  hideChartTooltip();
}

async function saveUPSMetadata() {
  if (!state.selectedUPS || !state.detail) {
    return;
  }
  const tags = els.upsMetadataTags.value.split(",").map((tag) => tag.trim()).filter(Boolean);
  const metadata = {
    display_name: els.upsMetadataDisplayName.value.trim(),
    tags,
  };
  els.upsMetadataSave.disabled = true;
  els.upsMetadataError.hidden = true;
  try {
    await fetchJSON(`/api/ups/${encodeURIComponent(state.selectedUPS)}/metadata`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(metadata),
    });
    closeUPSMetadataModal();
    await loadUPSDetail(state.selectedUPS, { silent: true });
    showToast("UPS details saved.");
  } catch (error) {
    els.upsMetadataError.textContent = error.message;
    els.upsMetadataError.hidden = false;
  } finally {
    els.upsMetadataSave.disabled = false;
  }
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

function statItem(label, value) {
  return `<div class="stat-item"><span class="stat-label">${escapeHTML(label)}</span><span class="stat-value">${escapeHTML(value)}</span></div>`;
}

function detailMetric(label, value, key) {
  return `<article class="metric-card metric-card--compact" data-metric="${key}"><span class="eyebrow">${escapeHTML(label)}</span><div class="metric-value metric-value--compact">${escapeHTML(value)}</div></article>`;
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

function accentClassForStatus(status) {
  const chipCls = statusClass(status);
  const suffix = chipCls ? chipCls.replace("chip--", "") : "good";
  return `detail-shell--${suffix}`;
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

els.profileMenuToggles.forEach((toggle) => {
  toggle.addEventListener("click", (event) => {
    event.stopPropagation();
    toggleProfileMenu();
  });
  toggle.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      if (!state.profileMenuOpen) {
        toggleProfileMenu();
      }
    }
  });
});
if (els.profileMenuPanel) {
  els.profileMenuPanel.addEventListener("keydown", handleMenuOptionNavigation);
}
document.addEventListener("click", (event) => {
  if (!state.profileMenuOpen) {
    return;
  }
  if (els.profileMenu.contains(event.target)) {
    return;
  }
  closeProfileMenu();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && state.profileMenuOpen) {
    closeProfileMenu({ focusTrigger: true });
  }
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

els.rawJsonClose.addEventListener("click", closeRawJsonModal);
els.rawJsonCopy.addEventListener("click", () => { void copyRawJSON(); });
els.rawJsonModal.addEventListener("click", (event) => {
  if (event.target === els.rawJsonModal) {
    closeRawJsonModal();
  }
});
els.upsMetadataCancel.addEventListener("click", closeUPSMetadataModal);
els.upsMetadataModal.addEventListener("click", (event) => {
  if (event.target === els.upsMetadataModal) {
    closeUPSMetadataModal();
  }
});
els.upsMetadataForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void saveUPSMetadata();
});

if (els.metricDetailClose) {
  els.metricDetailClose.addEventListener("click", closeMetricDetailModal);
}
if (els.metricDetailModal) {
  els.metricDetailModal.addEventListener("click", (event) => {
    if (event.target === els.metricDetailModal) {
      closeMetricDetailModal();
    }
  });
}
els.metricDetailWindowButtons.forEach((button) => {
  button.addEventListener("click", () => {
    void selectMetricDetailWindow(button.dataset.metricWindow);
  });
});
if (els.metricDetailSvg) {
  els.metricDetailSvg.addEventListener("mousemove", handleMetricDetailHover);
  els.metricDetailSvg.addEventListener("mouseleave", handleMetricDetailLeave);
}
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && els.metricDetailModal && els.metricDetailModal.classList.contains("is-open")) {
    closeMetricDetailModal();
  }
});

initTheme();
renderEmptyDetail();
connectHealthStream();
if (typeof window.EventSource !== "function") {
  // No SSE support: fall back to a one-shot REST load. Live updates for
  // health/UPS data simply won't happen on such a browser.
  refreshAll({ preserveSelection: true, silent: true });
}
window.addEventListener("beforeunload", (event) => {
  if (state.dirtyVariables.size === 0) {
    return;
  }
  event.preventDefault();
  event.returnValue = "";
});
