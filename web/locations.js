import { GetScanLocations } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { chooseFolder } from "./folder-picker.js";
import { logError } from "./logging.js";
import { formatSize } from "./format.js";

let analyzeLocation = async () => {};
let loadGeneration = 0;
let visibilityChanged = () => {};
const tiles = new Map();
const knownLocations = new Map();
let scanJobs = [];
let createStatus;
const pathKey = path => /^[a-z]:|^\\\\/i.test(path) ? path.replaceAll("\\", "/").toLowerCase().replace(/\/+$/, "") : path.replace(/\/+$/, "") || "/";

function addLocation(location) {
  const key = pathKey(location.path);
  if (tiles.has(key)) return;
  const tile = document.createElement("div");
  tile.className = "location-tile";
  tile.append(locationButton(location));
  tile.addEventListener("click", event => {
    // Buttons handle their own clicks, including the scan controls.
    if (event.target.closest("button")) return;
    return openLocation(location);
  });
  byId("locationList").append(tile);
  tiles.set(key, { element: tile });
}

export function setLocationScanJobs(jobs, statusFactory) {
  scanJobs = jobs;
  createStatus = statusFactory;
  renderScanJobs();
}

function renderScanJobs() {
  for (const job of scanJobs) {
    const key = pathKey(job.path);
    if (!tiles.has(key)) {
      const location = knownLocations.get(key) || { path: job.path, name: job.path.split(/[\\/]/).filter(Boolean).at(-1) || job.path, kind: "folder" };
      addLocation(location);
    }
    const tile = tiles.get(key);
    if (!tile.status && createStatus) {
      tile.status = createStatus();
      tile.element.append(tile.status.element);
    }
    tile.status?.update(job);
  }
  if (tiles.size) byId("locationStatus").hidden = true;
}

const fallbackIcon = `
  <svg viewBox="0 0 24 24" aria-hidden="true">
    <path d="M4 5h16v14H4z"></path>
    <path d="M4 14h16M8 17h.01M11 17h.01"></path>
  </svg>`;

async function openLocation(location) {
  byId("pathInput").value = location.path;
  await analyzeLocation();
}

function locationButton(location) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "location-card";
  button.dataset.path = location.path;
  button.dataset.tooltip = `Open ${location.path}`;

  const icon = document.createElement("span");
  icon.className = "location-card-icon";
  if (location.iconUrl) {
    const image = document.createElement("img");
    image.src = location.iconUrl;
    image.alt = "";
    icon.append(image);
  } else {
    icon.innerHTML = location.kind === "folder" ? '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 7V5h7l2 3h9v12H3z"></path><path d="M3 9h18"></path></svg>' : fallbackIcon;
  }

  const text = document.createElement("span");
  text.className = "location-card-text";
  const name = document.createElement("strong");
  name.textContent = location.name || location.path;
  const path = document.createElement("span");
  path.textContent = location.filesystem ? `${location.path} · ${location.filesystem}` : location.path;
  text.append(name, path);
  const total = location.diskTotal;
  const free = location.diskFree;
  if (Number.isFinite(total) && total > 0 && Number.isFinite(free) && free >= 0 && free <= total) {
    const used = total - free;
    const percent = used / total * 100;
    const usage = document.createElement("span");
    usage.className = "location-disk-usage";
    const label = document.createElement("span");
    label.textContent = `${formatSize(used, 1)} / ${formatSize(total, 1)} (${percent.toFixed(1)}%)`;
    const bar = document.createElement("span");
    bar.className = "compact-scan-progress location-disk-bar";
    bar.dataset.level = percent >= 95 ? "critical" : percent >= 80 ? "warning" : "normal";
    bar.setAttribute("role", "meter");
    bar.setAttribute("aria-label", "Disk space used");
    bar.setAttribute("aria-valuemin", "0");
    bar.setAttribute("aria-valuemax", "100");
    bar.setAttribute("aria-valuenow", String(percent));
    bar.setAttribute("aria-valuetext", label.textContent);
    const fill = document.createElement("span");
    fill.className = "scan-progress-bar";
    fill.style.setProperty("--scan-progress", `${percent}%`);
    bar.append(fill);
    usage.append(label, bar);
    text.append(usage);
  }
  button.append(icon, text);

  button.addEventListener("click", () => openLocation(location));
  return button;
}

async function loadLocations() {
  const generation = ++loadGeneration;
  const list = byId("locationList");
  const status = byId("locationStatus");
  list.replaceChildren();
  tiles.clear();
  status.hidden = false;
  status.textContent = "Finding available locations...";
  byId("refreshLocationsButton").disabled = true;
  try {
    const locations = await GetScanLocations();
    if (generation !== loadGeneration) return;
    const usable = Array.isArray(locations)
      ? locations.filter(location => location?.path)
      : [];
    knownLocations.clear();
    for (const location of usable) { knownLocations.set(pathKey(location.path), location); addLocation(location); }
    renderScanJobs();
    status.hidden = tiles.size > 0;
    status.textContent = tiles.size > 0
      ? ""
      : "No available locations were found. You can still choose a folder above.";
  } catch (error) {
    if (generation !== loadGeneration) return;
    logError("loading scan locations failed:", error);
    renderScanJobs();
    status.hidden = tiles.size > 0;
    status.textContent = "Locations could not be loaded. You can still choose a folder above.";
  } finally {
    if (generation === loadGeneration) byId("refreshLocationsButton").disabled = false;
  }
}

export function hideLocationSelector() {
  byId("locationSelector").hidden = true;
  visibilityChanged(false);
}

export function showLocationSelector({ refresh = false } = {}) {
  const selector = byId("locationSelector");
  const wasHidden = selector.hidden;
  selector.hidden = false;
  visibilityChanged(true);
  if (refresh || (wasHidden && byId("locationList").childElementCount === 0)) loadLocations();
}

export function initLocationSelector(options) {
  analyzeLocation = options.analyze;
  visibilityChanged = options.visibilityChanged || (() => {});
  byId("refreshLocationsButton").addEventListener("click", loadLocations);
  byId("chooseLocationFolderButton").addEventListener("click", async () => {
    const path = await chooseFolder({ focusInput: false });
    if (!path) return;
    hideLocationSelector();
    await analyzeLocation();
  });
  showLocationSelector({ refresh: true });
}
