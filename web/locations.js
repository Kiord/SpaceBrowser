import { GetScanLocations } from "./wailsjs/go/app/App.js";
import { byId } from "./dom.js";
import { logError } from "./logging.js";
import { formatSize } from "./format.js";

let analyzeLocation = async () => {};
let loadGeneration = 0;
let visibilityChanged = () => {};
const tiles = new Map();
const knownLocations = new Map();
let scanJobs = [];
let createStatus;
let removeLocation;
let selectedPath = "";
let cyclingPath = "";
const pathKey = path => /^[a-z]:|^\\\\/i.test(path) ? path.replaceAll("\\", "/").toLowerCase().replace(/\/+$/, "") : path.replace(/\/+$/, "") || "/";

function addLocation(location) {
  const key = pathKey(location.path);
  const existing = tiles.get(key);
  if (existing) {
    existing.location = location;
    existing.element.replaceChildren(locationButton(location));
    if (existing.status) existing.element.append(existing.status.element);
    return;
  }
  const tile = document.createElement("div");
  tile.className = "location-tile";
  tile.append(locationButton(location));
  tile.addEventListener("click", event => {
    // Buttons handle their own clicks, including the scan controls.
    if (event.target.closest("button")) return;
    return openLocation(tiles.get(key).location);
  });
  byId("locationList").append(tile);
  tiles.set(key, { element: tile, location });
  tile.setAttribute("data-selected", String(key === pathKey(selectedPath)));
}

// Map insertion order matches the visible top-to-bottom tile order.
export function cycleLocation(direction, continuing = false) {
  const entries = [...tiles.entries()];
  if (!entries.length) return false;
  const current = entries.findIndex(([key]) => key === pathKey(continuing ? cyclingPath : selectedPath));
  const index = current < 0 ? (direction > 0 ? 0 : entries.length - 1)
    : (current + direction + entries.length) % entries.length;
  const [key, tile] = entries[index];
  selectedPath = tile.location.path;
  cyclingPath = selectedPath;
  for (const [otherKey, other] of entries) other.element.setAttribute("data-selected", String(otherKey === key));
  tile.element.scrollIntoView({ block: "nearest" });
  void openLocation(tile.location);
  return true;
}

export function setLocationScanJobs(jobs, statusFactory, path = selectedPath, onRemove) {
  selectedPath = path;
  for (const [key, tile] of tiles) tile.element.setAttribute("data-selected", String(key === pathKey(path)));
  scanJobs = jobs;
  createStatus = statusFactory;
  removeLocation = onRemove;
  renderScanJobs();
}

function renderScanJobs() {
  const currentPaths = new Set(scanJobs.map(job => pathKey(job.path)));
  for (const [key, tile] of tiles) {
    if (tile.status && !currentPaths.has(key)) { tile.status.element.remove(); tile.status = null; }
    if (tile.location.kind === "folder" && !knownLocations.has(key) && !currentPaths.has(key)) {
      tile.element.remove();
      tiles.delete(key);
    }
  }
  // A cancelled rescan must not hide the completed snapshot it restored.
  // Keep cancelled-only locations so their tile remains available to scan again.
  const displayedJobs = new Map();
  for (const job of scanJobs) {
    const key = pathKey(job.path);
    const previous = displayedJobs.get(key);
    if (job.state !== "cancelled" || !previous || previous.state === "cancelled") {
      displayedJobs.set(key, job);
    }
  }
  for (const [key, job] of displayedJobs) {
    if (!tiles.has(key)) {
      const location = knownLocations.get(key) || { path: job.path, name: job.path.split(/[\\/]/).filter(Boolean).at(-1) || job.path, kind: "folder" };
      addLocation(location);
    }
    const tile = tiles.get(key);
    if (tile.location.kind === "folder" && removeLocation && !tile.remove) {
      tile.remove = document.createElement("button");
      tile.remove.type = "button";
      tile.remove.className = "location-remove-button";
      tile.remove.textContent = "×";
      tile.remove.setAttribute("aria-label", `Remove ${job.path} and clear its cache`);
      tile.remove.setAttribute("data-tooltip", "Remove location and clear cache");
      tile.remove.addEventListener("click", async () => {
        tile.remove.disabled = true;
        try { await removeLocation({ path: tile.location.path }); }
        finally { tile.remove.disabled = false; }
      });
      tile.element.append(tile.remove);
      tile.element.classList.add("location-tile-removable");
    }
    if (!tile.status && createStatus) {
      tile.status = createStatus();
      tile.element.append(tile.status.element);
    }
    tile.status?.update(job);
  }
  const ordered = [...tiles.entries()].sort(([, left], [, right]) =>
    Number(left.location.kind === "folder") - Number(right.location.kind === "folder"));
  const current = [...tiles.keys()];
  if (ordered.some(([key], index) => key !== current[index])) {
    tiles.clear();
    for (const [key, tile] of ordered) {
      tiles.set(key, tile);
      byId("locationList").append(tile.element);
    }
  }
  if (tiles.size) byId("locationStatus").hidden = true;
}

const fallbackIcon = `
  <svg viewBox="0 0 24 24" aria-hidden="true">
    <path d="M4 5h16v14H4z"></path>
    <path d="M4 14h16M8 17h.01M11 17h.01"></path>
  </svg>`;

async function openLocation(location) {
  await analyzeLocation(location.path);
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
  path.textContent = [location.path, location.mediaType, location.busType, location.filesystem].filter(Boolean).join(" · ");
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
      : "No available locations were found. You can still enter a folder path above.";
  } catch (error) {
    if (generation !== loadGeneration) return;
    logError("loading scan locations failed:", error);
    renderScanJobs();
    status.hidden = tiles.size > 0;
    status.textContent = "Locations could not be loaded. You can still enter a folder path above.";
  } finally {
    if (generation === loadGeneration) byId("refreshLocationsButton").disabled = false;
  }
}

export function hideLocationSelector() {
  byId("emptyLocation").hidden = true;
  visibilityChanged(false);
}

export function showLocationSelector({ refresh = false } = {}) {
  byId("emptyLocation").hidden = false;
  visibilityChanged(true);
  if (refresh) loadLocations();
}

export function initLocationSelector(options) {
  analyzeLocation = options.analyze;
  visibilityChanged = options.visibilityChanged || (() => {});
  byId("refreshLocationsButton").addEventListener("click", loadLocations);
  showLocationSelector({ refresh: true });
}
