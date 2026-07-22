const POLL_INTERVAL_MS = 15000;
const THEME_PREF_STORAGE_KEY = "strom-theme-preference";
const LEGACY_THEME_STORAGE_KEY = "strom-theme";
const prefersDarkMedia = window.matchMedia("(prefers-color-scheme: dark)");

const state = {
  health: null,
  upses: [],
  selectedUPS: null,
  detail: null,
  nextRefreshAt: Date.now() + POLL_INTERVAL_MS,
  refreshTimer: null,
  toastTimer: null,
  pendingCommand: null,
  themePreference: "system",
  profileMenuOpen: false,
  lastUpdatedAt: null,
  lastRefreshError: null,
  dirtyVariables: new Set(),
};

const els = {
  topbar: document.querySelector(".topbar"),
  profileMenu: document.getElementById("profile-menu"),
  topbarToolbar: document.getElementById("topbar-toolbar"),
  profileMenuToggles: Array.from(document.querySelectorAll("[data-menu-toggle]")),
  profileMenuPanel: document.getElementById("profile-menu-panel"),
  themeOptions: Array.from(document.querySelectorAll("[data-theme-option]")),
  metrics: document.getElementById("metrics-grid"),
  refreshIndicator: document.getElementById("refresh-indicator"),
  refreshCountdown: document.getElementById("refresh-countdown"),
  refreshRingProgress: document.getElementById("refresh-ring-progress"),
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

function scheduleRefresh() {
  if (state.refreshTimer) {
    window.clearTimeout(state.refreshTimer);
  }
  const delay = Math.max(0, state.nextRefreshAt - Date.now());
  state.refreshTimer = window.setTimeout(async () => {
    await refreshAll({ preserveSelection: true, silent: true });
  }, delay);
}

const REFRESH_RING_CIRCUMFERENCE = 2 * Math.PI * 15.5;

function startRefreshRing(durationMs) {
  const ring = els.refreshRingProgress;
  if (!ring) {
    return;
  }
  ring.style.transition = "none";
  ring.style.strokeDasharray = `${REFRESH_RING_CIRCUMFERENCE}`;
  ring.style.strokeDashoffset = `${REFRESH_RING_CIRCUMFERENCE}`;
  // Force a reflow so the transition below restarts from the reset offset above.
  void ring.getBoundingClientRect();
  ring.style.transition = `stroke-dashoffset ${durationMs}ms linear`;
  ring.style.strokeDashoffset = "0";
}

function updateRefreshCountdown() {
  if (!els.refreshCountdown) {
    return;
  }
  if (state.lastRefreshError) {
    els.refreshCountdown.textContent = `Refresh failed: ${state.lastRefreshError}`;
    els.refreshCountdown.classList.add("helper--error");
    return;
  }
  els.refreshCountdown.classList.remove("helper--error");
  if (!state.lastUpdatedAt) {
    els.refreshCountdown.textContent = "Loading live metrics\u2026";
    return;
  }
  const remainingSeconds = Math.ceil(Math.max(0, state.nextRefreshAt - Date.now()) / 1000);
  els.refreshCountdown.textContent = remainingSeconds <= 0 ? "Refreshing\u2026" : `Refreshing in ${remainingSeconds}s`;
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
  if (els.refreshIndicator) {
    els.refreshIndicator.classList.add("is-refreshing");
  }
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
      if (state.dirtyVariables.size === 0) {
        await loadUPSDetail(state.selectedUPS, { silent: true });
      }
    } else {
      renderEmptyDetail();
    }
    state.lastUpdatedAt = Date.now();
    state.lastRefreshError = null;
    state.nextRefreshAt = Date.now() + POLL_INTERVAL_MS;
    scheduleRefresh();
    startRefreshRing(POLL_INTERVAL_MS);
    if (!options.silent) {
      showToast("Dashboard refreshed.");
    }
  } catch (error) {
    state.lastRefreshError = error.message;
    showToast(error.message, true);
    state.nextRefreshAt = Date.now() + POLL_INTERVAL_MS;
    scheduleRefresh();
    startRefreshRing(POLL_INTERVAL_MS);
  } finally {
    if (els.refreshIndicator) {
      els.refreshIndicator.classList.remove("is-refreshing");
    }
    updateRefreshCountdown();
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
  await refreshAll({ preserveSelection: true, silent: true });
}

function renderHealth() {
  if (!state.health) {
    return;
  }
  const cards = [
    ["Version", state.health.version],
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
      ${detailMetric("Battery charge", formatPercent(metrics.battery_charge_percent))}
      ${detailMetric("Load", formatPercent(metrics.load_percent))}
      ${detailMetric("Runtime", formatDuration(metrics.runtime_seconds))}
      ${detailMetric("Input voltage", formatVoltage(metrics.input_voltage))}
      ${detailMetric("Output voltage", formatVoltage(metrics.output_voltage))}
      ${detailMetric("Battery voltage", formatVoltage(metrics.battery_voltage))}
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
    await refreshAll({ preserveSelection: true, silent: true });
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

function detailMetric(label, value) {
  return `<article class="metric-card metric-card--compact"><span class="eyebrow">${escapeHTML(label)}</span><div class="metric-value metric-value--compact">${escapeHTML(value)}</div></article>`;
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

initTheme();
renderEmptyDetail();
if (els.refreshIndicator) {
  els.refreshIndicator.addEventListener("click", () => {
    refreshAll({ preserveSelection: true });
  });
}
startRefreshRing(POLL_INTERVAL_MS);
window.setInterval(updateRefreshCountdown, 1000);
updateRefreshCountdown();
refreshAll({ preserveSelection: true, silent: true });
window.addEventListener("beforeunload", (event) => {
  if (state.dirtyVariables.size === 0) {
    return;
  }
  event.preventDefault();
  event.returnValue = "";
});
