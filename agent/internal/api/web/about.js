(() => {
  const aboutDialog = document.getElementById("about-dialog");
  const acknowledgementsDialog = document.getElementById("acknowledgements-dialog");
  if (!aboutDialog || !acknowledgementsDialog) return;

  const aboutContent = document.getElementById("about-content");
  const acknowledgementContent = document.getElementById("acknowledgements-content");
  const acknowledgementFilter = document.getElementById("acknowledgements-filter");
  const acknowledgementSearch = document.getElementById("acknowledgements-search");
  let data = null;

  const clear = (element) => { while (element.firstChild) element.removeChild(element.firstChild); };
  const text = (tag, value, className) => {
    const element = document.createElement(tag);
    element.textContent = value;
    if (className) element.className = className;
    return element;
  };
  const link = (label, href) => {
    const element = document.createElement("a");
    element.textContent = label;
    element.href = href;
    element.target = "_blank";
    element.rel = "noreferrer";
    return element;
  };
  const closeMenus = () => {
    document.querySelectorAll(".menu-panel").forEach((panel) => { panel.hidden = true; });
    document.querySelectorAll(".toolbar").forEach((toolbar) => toolbar.classList.remove("is-open"));
    document.querySelectorAll(".menu-toggle").forEach((toggle) => toggle.setAttribute("aria-expanded", "false"));
  };
  const formatDuration = (seconds) => {
    const total = Math.max(0, Number(seconds) || 0);
    const days = Math.floor(total / 86400);
    const hours = Math.floor((total % 86400) / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    if (days) return `${days}d ${hours}h`;
    if (hours) return `${hours}h ${minutes}m`;
    return `${minutes}m`;
  };
  const detailList = (items) => {
    const list = document.createElement("dl");
    list.className = "about-details";
    items.forEach(([label, value]) => {
      const term = text("dt", label);
      const description = text("dd", value || "Unavailable");
      list.append(term, description);
    });
    return list;
  };
  const heading = (title, description) => {
    const section = document.createElement("section");
    section.className = "about-section";
    section.append(text("h3", title));
    if (description) section.append(text("p", description, "helper"));
    return section;
  };
  const renderError = (container, message) => {
    clear(container);
    container.append(text("p", message, "about-error"));
  };
  const renderAbout = () => {
    clear(aboutContent);
    const strom = heading("Strom Node", "A local UPS node that configures Network UPS Tools, exposes live telemetry, and can be managed through a Strom controller.");
    const links = document.createElement("p");
    links.className = "about-links";
    links.append(link("Documentation", "https://foehammer82.github.io/strom/getting-started/"), text("span", " · "), link("Source", "https://github.com/Foehammer82/strom"), text("span", " · "), link("MIT License", "https://github.com/Foehammer82/strom/blob/main/LICENSE"));
    strom.append(links);

    const node = heading("This node");
    node.append(detailList([
      ["Strom version", data.version],
      ["Node serial", data.serial],
      ["Hostname", data.hostname],
      ["Deployment", data.adopted ? "Managed by a Strom controller" : "Standalone node"],
      ["Operating system", data.operating_system?.name],
      ["Kernel", data.kernel],
      ["Architecture", data.architecture],
      ["Go runtime", data.go_version],
      ["Agent uptime", formatDuration(data.uptime_seconds)],
    ]));

    const acknowledgements = heading("Open-source acknowledgments", "Strom is grateful to every project and maintainer whose work makes this node possible.");
    const featured = document.createElement("ul");
    featured.className = "about-featured-list";
    (data.featured_dependencies || []).forEach((dependency) => {
      const item = document.createElement("li");
      const title = dependency.source ? link(dependency.name, dependency.source) : text("span", dependency.name);
      item.append(title);
      if (dependency.version) item.append(text("span", ` ${dependency.version}`, "helper"));
      featured.append(item);
    });
    if (!featured.childElementCount) featured.append(text("li", "Acknowledgement metadata is unavailable for this build.", "helper"));
    acknowledgements.append(featured);
    const allButton = text("button", "View all acknowledgments", "button button--ghost");
    allButton.type = "button";
    allButton.addEventListener("click", () => { aboutDialog.close(); acknowledgementsDialog.showModal(); renderAcknowledgements(); acknowledgementSearch.focus(); });
    acknowledgements.append(allButton);

    aboutContent.append(strom, node, acknowledgements);
    if (data.warnings?.length) aboutContent.append(text("p", data.warnings.join(" "), "helper"));
  };
  const renderAcknowledgements = () => {
    if (!data) return;
    clear(acknowledgementContent);
    const query = acknowledgementSearch.value.trim().toLowerCase();
    const category = acknowledgementFilter.value;
    const matches = (value) => !query || value.name.toLowerCase().includes(query) || value.version.toLowerCase().includes(query);
    const appendGroup = (title, records, source) => {
      const visible = records.filter(matches);
      if (!visible.length) return;
      const section = heading(title, `${visible.length} shown`);
      const list = document.createElement("ul");
      list.className = "acknowledgement-list";
      visible.forEach((record) => {
        const item = document.createElement("li");
        item.append(record.source ? link(record.name, record.source) : text("span", record.name));
        if (record.version) item.append(text("span", ` ${record.version}`, "helper"));
        if (source === "debian" && record.architecture) item.append(text("span", ` (${record.architecture})`, "helper"));
        list.append(item);
      });
      section.append(list);
      acknowledgementContent.append(section);
    };
    if (category !== "debian") appendGroup("Go modules", data.go_modules || [], "go");
    if (category !== "go") appendGroup("Installed Debian packages", data.debian_packages || [], "debian");
    if (!acknowledgementContent.childElementCount) acknowledgementContent.append(text("p", "No acknowledgments match this filter.", "helper"));
  };
  const loadAbout = async () => {
    if (data) return data;
    renderError(aboutContent, "Loading node information…");
    const response = await fetch("/api/about", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error("Unable to load node information.");
    data = await response.json();
    return data;
  };
  document.querySelectorAll("[data-about-open]").forEach((button) => button.addEventListener("click", async () => {
    closeMenus();
    aboutDialog.showModal();
    try { await loadAbout(); renderAbout(); } catch (error) { renderError(aboutContent, error.message); }
  }));
  document.querySelectorAll("[data-about-close]").forEach((button) => button.addEventListener("click", () => aboutDialog.close()));
  document.querySelectorAll("[data-acknowledgements-close]").forEach((button) => button.addEventListener("click", () => acknowledgementsDialog.close()));
  document.querySelectorAll("[data-acknowledgements-back]").forEach((button) => button.addEventListener("click", () => { acknowledgementsDialog.close(); aboutDialog.showModal(); }));
  [aboutDialog, acknowledgementsDialog].forEach((dialog) => dialog.addEventListener("click", (event) => { if (event.target === dialog) dialog.close(); }));
  acknowledgementFilter.addEventListener("change", renderAcknowledgements);
  acknowledgementSearch.addEventListener("input", renderAcknowledgements);
})();
