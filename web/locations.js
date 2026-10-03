import { GetScanLocations } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { chooseFolder } from "./folder-picker.js";
import { logError } from "./logging.js";
import { formatSize } from "./format.js";

let analyzeLocation = async () => {};
let loadGeneration = 0;
let visibilityChanged = () => {};

const fallbackIcon = `
  <svg viewBox="0 0 24 24" aria-hidden="true">
    <path d="M4 5h16v14H4z"></path>
    <path d="M4 14h16M8 17h.01M11 17h.01"></path>
  </svg>`;

function locationButton(location) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "location-card";
  button.dataset.path = location.path;
  button.dataset.tooltip = `Scan ${location.path}`;

  const icon = document.createElement("span");
  icon.className = "location-card-icon";
  if (location.iconUrl) {
    const image = document.createElement("img");
    image.src = location.iconUrl;
    image.alt = "";
    icon.append(image);
  } else {
    icon.innerHTML = fallbackIcon;
  }

  const text = document.createElement("span");
  text.className = "location-card-text";
  const name = document.createElement("strong");
  name.textContent = location.name || location.path;
  const path = document.createElement("span");
  path.textContent = location.path;
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

  button.addEventListener("click", async () => {
    byId("pathInput").value = location.path;
    await analyzeLocation();
  });
  return button;
}

async function loadLocations() {
  const generation = ++loadGeneration;
  const list = byId("locationList");
  const status = byId("locationStatus");
  list.replaceChildren();
  status.hidden = false;
  status.textContent = "Finding available locations...";
  byId("refreshLocationsButton").disabled = true;
  try {
    const locations = await GetScanLocations();
    if (generation !== loadGeneration) return;
    const usable = Array.isArray(locations)
      ? locations.filter(location => location?.path)
      : [];
    for (const location of usable) list.append(locationButton(location));
    status.hidden = usable.length > 0;
    status.textContent = usable.length > 0
      ? ""
      : "No available locations were found. You can still choose a folder above.";
  } catch (error) {
    if (generation !== loadGeneration) return;
    logError("loading scan locations failed:", error);
    status.hidden = false;
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
