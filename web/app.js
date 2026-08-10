/* Frontend when the Go backend owns tree + layout.

Backend contract:
  GET  /api/get_full_tree?path=...             -> { ok:true, root_id:<number> }
  GET  /api/layout?node_id=...&w=...&h=...     -> { rects:[ { x,y,w,h, node_id, parent_id, name, size,
                                                         is_folder, is_free_space, depth, full_path,
                                                         children:[rectIndex,...] } ] }
  POST /api/open_in_file_browser               body: { path }

Notes:
- We use rect ARRAY INDEX for picking (id buffer), and rect.node_id for app logic.
- children are rect indices into the SAME rects array.
*/

function getScale() {
  return Math.max(1, window.devicePixelRatio || 1); 
}
const AppState = {
  // focus & history (use node_id everywhere)
  node_id: null,
  navHistory: [],
  navIndex: -1,
  navSession: 0,
  browserHistoryPosition: 0,
  scanRootPath: "",

  // canvases
  colorCanvas: null, colorCtx: null,
  idCanvas: null,    idCtx: null,
  tmpCanvas: null,   tmpCtx: null,
  maskCanvas: null,  maskCtx: null,

  // current layout
  rects: [],
  selectedRectIndex: null,
  selectedNodeId:null,
  profile: null,

  //Scale
  zoomFactor: 1,
  scale: getScale(),

  pickingFolderDialogIsOpen: false
};

const FONT_SIZE = 10;
const PALETTES = Object.freeze({
  default: ["#ff9b85", "#ffbe76", "#ffe066", "#7bed9f", "#70d6ff", "#a29bfe", "#dfe4ea"],
  legacy: ["#ff7f7f", "#ffbf7f", "#ffff00", "#7fff7f", "#7fffff", "#bfbfff", "#bfbfbf", "#ff7fff"],
  single: ["#9fc5d8"],
  duotone: ["#f2c078", "#78a6c8"],
  tricolor: ["#e8846b", "#e3bf62", "#78a88b"],
  playful: ["#ff6b6b", "#ffd93d", "#6bcb77", "#4d96ff", "#c77dff", "#ff8fab", "#72efdd"],
  monochrome: ["#f0f0f0", "#dedede", "#cccccc", "#bababa", "#a8a8a8", "#969696", "#848484"],
  earth: ["#d9c7a6", "#c3a982", "#ad8b63", "#96a17b", "#7f9168", "#b98268", "#8f7559"],
  ocean: ["#c6e8e5", "#a8dadc", "#8ecfd1", "#73c0c5", "#78b7d0", "#91a8d0", "#a7c4bc"],
  retro: ["#e49a78", "#e5bd63", "#a8b47d", "#78a0a8", "#a58aa8", "#c9ae88", "#87949a"],
});
const DEFAULT_APPEARANCE = Object.freeze({
  palette: "default",
  zoomFactor: 1,
  cornerRadius: 0,
  reliefStrength: 0.30,
});
const DEFAULT_PROFILE_SETTINGS = Object.freeze({
  excludedPaths: Object.freeze([]),
  skipHidden: false,
  minFileSize: 1024,
  followSymlinks: false,
  skipNetworkFS: true,
  allowDelete: false,
  rescanOnDelete: true,
});
const AppearanceState = { ...DEFAULT_APPEARANCE };

const SCALE_MIN = 0.5;
const SCALE_MAX = 5.0;
const SCALE_STEP_KEYS = 1.1;         
const SCALE_SMOOTH_BASE = 1.0015;    

// ---------- API ----------


// web/app.js
import { GetFullTree, Layout, OpenInFileBrowser, OpenPath, GetAssociatedIcon, DefaultPath, SetShowFreeSpace, PickFolder, GetProfile, SetProfile, ValidateScanPath, GetScanProgress, CancelScan, DeleteNode } from "./wailsjs/go/main/App.js";

async function apiScan(path) {
  console.time("scan");
  const res = await GetFullTree(path);
  console.timeEnd("scan");
  return res
}

async function apiLayoutById(nodeId, w, h, scale) {
  const rects = await Layout(nodeId, w, h, scale);
  return { rects };
}

async function apiOpenInFileBrowser(path) {
  await OpenInFileBrowser(path);
}

document.addEventListener("DOMContentLoaded", async () => {
  replaceBrowserHistoryEntry(null, -1);

  document.getElementById("pathGroup")?.removeAttribute("title");
  const analyzeButton = document.getElementById("analyzeButton");
  if (analyzeButton) {
    analyzeButton.removeAttribute("title");
    analyzeButton.dataset.tooltip = "Scan folder";
  }

  document.getElementById("analyzeButton")?.addEventListener("click", analyze);
  document.getElementById("triggerFolderSelectButton")?.addEventListener("click", triggerFolderSelect);
  document.getElementById("rootButton")?.addEventListener("click", goToRoot);
  document.getElementById("parentButton")?.addEventListener("click", goToParent);
  document.getElementById("backwardButton")?.addEventListener("click", goBackward);
  document.getElementById("forwardButton")?.addEventListener("click", goForward);
  document.getElementById("toggleFreeSpaceButton")?.addEventListener("click", toggleFreeSpace);
  document.getElementById("settingsButton")?.addEventListener("click", openSettings);
  document.getElementById("settingsForm")?.addEventListener("submit", saveSettings);
  document.getElementById("closeSettingsButton")?.addEventListener("click", closeSettings);
  document.getElementById("cancelSettingsButton")?.addEventListener("click", closeSettings);
  document.getElementById("restoreDefaultsButton")?.addEventListener("click", openRestoreDefaultsConfirmation);
  document.getElementById("cancelRestoreDefaultsButton")?.addEventListener("click", closeRestoreDefaultsConfirmation);
  document.getElementById("confirmRestoreDefaultsButton")?.addEventListener("click", restoreDefaultSettings);
  document.getElementById("restoreDefaultsDialog")?.addEventListener("cancel", (e) => {
    e.preventDefault();
    closeRestoreDefaultsConfirmation();
  });
  document.getElementById("cancelDeleteButton")?.addEventListener("click", closeDeleteConfirmation);
  document.getElementById("confirmDeleteButton")?.addEventListener("click", confirmSelectedDeletion);
  document.getElementById("deleteConfirmDialog")?.addEventListener("cancel", (e) => {
    e.preventDefault();
    closeDeleteConfirmation();
  });
  document.getElementById("settingsMinFileSizeUnit")?.addEventListener("change", convertSettingsSizeUnit);
  document.querySelectorAll("[data-settings-tab]").forEach(tab => {
    tab.addEventListener("click", () => showSettingsTab(tab.dataset.settingsTab));
  });
  document.getElementById("settingsPalette")?.addEventListener("change", updateAppearanceFormOutputs);
  document.getElementById("settingsZoomFactor")?.addEventListener("input", updateAppearanceFormOutputs);
  document.getElementById("settingsCornerRadius")?.addEventListener("input", updateAppearanceFormOutputs);
  document.getElementById("settingsReliefStrength")?.addEventListener("input", updateAppearanceFormOutputs);
  document.getElementById("cancelScanButton")?.addEventListener("click", cancelActiveScan);
  document.getElementById("scanDialog")?.addEventListener("cancel", (e) => {
    e.preventDefault();
    cancelActiveScan();
  });

  try {
    const profile = await GetProfile();
    AppState.profile = profile;
    applyAppearance(profile.appearance, false);
  } catch (e) {
    console.error("loading appearance failed:", e);
  }

  try {
    const p = await DefaultPath();
    const el = document.getElementById("pathInput");
    if (el && p) el.value = p;
  } catch (e) {
    console.error(e);
  }
});

// ---------- Settings ----------
function normalizedAppearance(appearance) {
  const source = appearance || {};
  const palette = PALETTES[source.palette] ? source.palette : DEFAULT_APPEARANCE.palette;
  const relief = source.reliefStrength == null
    ? DEFAULT_APPEARANCE.reliefStrength
    : Number(source.reliefStrength);
  return {
    palette,
    zoomFactor: Math.max(SCALE_MIN, Math.min(SCALE_MAX, Number(source.zoomFactor) || DEFAULT_APPEARANCE.zoomFactor)),
    cornerRadius: Math.max(0, Math.min(10, Math.round(Number(source.cornerRadius) || 0))),
    reliefStrength: Math.max(0, Math.min(0.5, Number.isFinite(relief) ? relief : DEFAULT_APPEARANCE.reliefStrength)),
  };
}

async function applyAppearance(appearance, redrawNow = true) {
  Object.assign(AppearanceState, normalizedAppearance(appearance));
  AppState.zoomFactor = AppearanceState.zoomFactor;
  AppState.scale = getScale() * AppState.zoomFactor;
  if (redrawNow && AppState.node_id != null) await redraw();
}

function showSettingsTab(name) {
  document.querySelectorAll("[data-settings-tab]").forEach(tab => {
    const active = tab.dataset.settingsTab === name;
    tab.classList.toggle("is-active", active);
    tab.setAttribute("aria-selected", String(active));
  });
  document.querySelectorAll("[data-settings-panel]").forEach(panel => {
    panel.hidden = panel.dataset.settingsPanel !== name;
  });
}

function updatePalettePreview(paletteName) {
  const preview = document.getElementById("settingsPalettePreview");
  if (!preview) return;
  preview.replaceChildren(...(PALETTES[paletteName] || PALETTES.default).map(color => {
    const swatch = document.createElement("span");
    swatch.style.backgroundColor = color;
    return swatch;
  }));
}

function activePalette() {
  const palette = PALETTES[AppearanceState.palette];
  return Array.isArray(palette) && palette.length > 0 ? palette : PALETTES.default;
}

function updateAppearanceFormOutputs() {
  const palette = document.getElementById("settingsPalette").value;
  const zoom = Number(document.getElementById("settingsZoomFactor").value);
  const radius = Number(document.getElementById("settingsCornerRadius").value);
  const relief = Number(document.getElementById("settingsReliefStrength").value);
  updatePalettePreview(palette);
  document.getElementById("settingsZoomFactorValue").textContent = `${zoom.toFixed(1)}×`;
  document.getElementById("settingsCornerRadiusValue").textContent = `${radius.toFixed(0)} px`;
  document.getElementById("settingsReliefStrengthValue").textContent = `${(1 + relief).toFixed(2)}×`;
}

function populateAppearanceForm(appearance, useCurrentZoom = true) {
  const values = normalizedAppearance(appearance);
  document.getElementById("settingsPalette").value = values.palette;
  document.getElementById("settingsZoomFactor").value = String(useCurrentZoom ? (AppState.zoomFactor || values.zoomFactor) : values.zoomFactor);
  document.getElementById("settingsCornerRadius").value = String(values.cornerRadius);
  document.getElementById("settingsReliefStrength").value = String(values.reliefStrength);
  updateAppearanceFormOutputs();
}

async function openSettings() {
  const dialog = document.getElementById("settingsDialog");
  const error = document.getElementById("settingsError");
  if (!dialog || dialog.open) return;

  error.textContent = "";
  try {
    const profile = await GetProfile();
    AppState.profile = profile;
    document.getElementById("settingsPlatform").textContent = profile.platformSystem || "";
    document.getElementById("settingsExcludedPaths").value = (profile.excludedPaths || []).join("\n");
    const threshold = splitSizeIntoUnit(profile.minFileSize ?? 0);
    const sizeInput = document.getElementById("settingsMinFileSize");
    const unitSelect = document.getElementById("settingsMinFileSizeUnit");
    sizeInput.value = String(threshold.value);
    unitSelect.value = threshold.unit;
    unitSelect.dataset.previousUnit = threshold.unit;
    document.getElementById("settingsSkipHidden").checked = !!profile.skipHidden;
    document.getElementById("settingsFollowSymlinks").checked = !!profile.followSymlinks;
    document.getElementById("settingsSkipNetworkFS").checked = !!profile.skipNetworkFS;
    document.getElementById("settingsAllowDelete").checked = !!profile.allowDelete;
    document.getElementById("settingsRescanOnDelete").checked = !!profile.rescanOnDelete;
    populateAppearanceForm(profile.appearance);
    showSettingsTab("general");
    dialog.showModal();
  } catch (err) {
    console.error("loading settings failed:", err);
  }
}

function closeSettings() {
  closeRestoreDefaultsConfirmation();
  const dialog = document.getElementById("settingsDialog");
  if (dialog?.open) dialog.close();
}

function openRestoreDefaultsConfirmation() {
  const dialog = document.getElementById("restoreDefaultsDialog");
  if (dialog && !dialog.open) dialog.showModal();
}

function closeRestoreDefaultsConfirmation() {
  const dialog = document.getElementById("restoreDefaultsDialog");
  if (dialog?.open) dialog.close();
}

function restoreDefaultSettings() {
  document.getElementById("settingsExcludedPaths").value = DEFAULT_PROFILE_SETTINGS.excludedPaths.join("\n");
  const threshold = splitSizeIntoUnit(DEFAULT_PROFILE_SETTINGS.minFileSize);
  const sizeInput = document.getElementById("settingsMinFileSize");
  const unitSelect = document.getElementById("settingsMinFileSizeUnit");
  sizeInput.value = String(threshold.value);
  unitSelect.value = threshold.unit;
  unitSelect.dataset.previousUnit = threshold.unit;
  document.getElementById("settingsSkipHidden").checked = DEFAULT_PROFILE_SETTINGS.skipHidden;
  document.getElementById("settingsFollowSymlinks").checked = DEFAULT_PROFILE_SETTINGS.followSymlinks;
  document.getElementById("settingsSkipNetworkFS").checked = DEFAULT_PROFILE_SETTINGS.skipNetworkFS;
  document.getElementById("settingsAllowDelete").checked = DEFAULT_PROFILE_SETTINGS.allowDelete;
  document.getElementById("settingsRescanOnDelete").checked = DEFAULT_PROFILE_SETTINGS.rescanOnDelete;
  populateAppearanceForm(DEFAULT_APPEARANCE, false);
  document.getElementById("settingsError").textContent = "";
  closeRestoreDefaultsConfirmation();
}

async function saveSettings(e) {
  e.preventDefault();
  const dialog = document.getElementById("settingsDialog");
  const error = document.getElementById("settingsError");
  const sizeValue = document.getElementById("settingsMinFileSize").valueAsNumber;
  const sizeUnit = document.getElementById("settingsMinFileSizeUnit").value;
  const minFileSize = sizeValue * SIZE_UNITS[sizeUnit];

  if (!Number.isFinite(sizeValue) || sizeValue < 0 || !Number.isSafeInteger(minFileSize)) {
    error.textContent = "Small-file threshold must resolve to a non-negative whole number of bytes.";
    return;
  }

  const excludedPaths = document.getElementById("settingsExcludedPaths").value
    .split(/\r?\n/)
    .map(path => path.trim())
    .filter(Boolean);

  const profile = {
    platformSystem: document.getElementById("settingsPlatform").textContent,
    excludedPaths,
    skipHidden: document.getElementById("settingsSkipHidden").checked,
    minFileSize,
    followSymlinks: document.getElementById("settingsFollowSymlinks").checked,
    skipNetworkFS: document.getElementById("settingsSkipNetworkFS").checked,
    allowDelete: document.getElementById("settingsAllowDelete").checked,
    rescanOnDelete: document.getElementById("settingsRescanOnDelete").checked,
    appearance: {
      palette: document.getElementById("settingsPalette").value,
      zoomFactor: Number(document.getElementById("settingsZoomFactor").value),
      cornerRadius: Number(document.getElementById("settingsCornerRadius").value),
      reliefStrength: Number(document.getElementById("settingsReliefStrength").value),
    },
  };

  const saveButton = e.submitter;
  error.textContent = "";
  if (saveButton) saveButton.disabled = true;
  try {
    await SetProfile(profile);
    AppState.profile = profile;
    dialog.close();
    await applyAppearance(profile.appearance);
  } catch (err) {
    error.textContent = String(err || "Unable to save settings.");
  } finally {
    if (saveButton) saveButton.disabled = false;
  }
}

const SIZE_UNITS = {
  B: 1,
  KB: 1024,
  MB: 1024 ** 2,
  GB: 1024 ** 3,
};

function splitSizeIntoUnit(bytes) {
  for (const unit of ["GB", "MB", "KB"]) {
    const multiplier = SIZE_UNITS[unit];
    if (bytes >= multiplier && bytes % multiplier === 0) {
      return { value: bytes / multiplier, unit };
    }
  }
  return { value: bytes, unit: "B" };
}

function convertSettingsSizeUnit(e) {
  const select = e.currentTarget;
  const input = document.getElementById("settingsMinFileSize");
  const oldUnit = select.dataset.previousUnit || "B";
  const bytes = input.valueAsNumber * SIZE_UNITS[oldUnit];
  if (Number.isFinite(bytes)) {
    input.value = String(bytes / SIZE_UNITS[select.value]);
  }
  select.dataset.previousUnit = select.value;
}


// ---------- Boot ----------
window.addEventListener("load", () => {
  AppState.colorCanvas = document.getElementById("colorCanvas");
  AppState.idCanvas    = document.getElementById("idCanvas");
  AppState.tmpCanvas   = document.getElementById("tmpCanvas");
  AppState.maskCanvas  = document.getElementById("maskCanvas");

  AppState.colorCtx = AppState.colorCanvas.getContext("2d");
  AppState.idCtx    = AppState.idCanvas.getContext("2d", { willReadFrequently: true });
  AppState.idCtx.imageSmoothingEnabled = false;
  AppState.tmpCtx  = AppState.tmpCanvas.getContext("2d", { alpha: true });
  AppState.maskCtx = AppState.maskCanvas.getContext("2d", { alpha: true });

  resizeCanvas();
  
  installZoomInterception();

  AppState.colorCanvas.addEventListener("click", (e) => {
    const { x, y } = getCanvasCoords(e);
    const rectIndex = rectIndexAtPoint(x, y);
    selectRectByIndex(rectIndex);
    hideContextMenu();
  });

  AppState.colorCanvas.addEventListener("contextmenu", (e) => {
    e.preventDefault();
    const { x, y } = getCanvasCoords(e);
    const rectIndex = rectIndexAtPoint(x, y);
    selectRectByIndex(rectIndex, /*dontDeselect=*/true);
    const r = getSelectedRect();
    if (r && !isPassiveRect(r)) {
      showContextMenu(e.pageX, e.pageY);
    } else {
      hideContextMenu();
    }
  });
  
  AppState.colorCanvas.addEventListener("dblclick", (e) => {
    const { x, y } = getCanvasCoords(e);
    const rectIndex = rectIndexAtPoint(x, y);
    const rect = AppState.rects[rectIndex];
    if (rect && !isPassiveRect(rect)){
      selectRectByIndex(rectIndex);
      navigateToSelected();
      hideContextMenu();
    }
  });

  AppState.colorCanvas.addEventListener("pointermove", updateRectToast);
  AppState.colorCanvas.addEventListener("pointerleave", (e) => {
    if (!e.relatedTarget?.closest?.("#rectToast")) hideRectToast();
  });

  document.getElementById("rectToastOpen")?.addEventListener("click", async (e) => {
    e.stopPropagation();
    if (hoveredRect?.full_path) await OpenPath(hoveredRect.full_path);
  });
  document.getElementById("rectToast")?.addEventListener("pointerleave", (e) => {
    if (e.relatedTarget !== AppState.colorCanvas) hideRectToast();
  });
});


window.addEventListener("resize", debounce(async () => {
  resizeCanvas();
  await redraw(); // re-request layout for current focus
}, 150));

// ---------- Analyze (scan then layout immediately) ----------
let scanProgressTimer = null;
let scanProgressToken = 0;
let scanCancelledByUser = false;
let scanDotsTimer = null;

function clearTreemapForScan() {
  hideRectToast();
  for (const ctx of [AppState.colorCtx, AppState.idCtx, AppState.tmpCtx, AppState.maskCtx]) {
    if (ctx) ctx.clearRect(0, 0, ctx.canvas.width, ctx.canvas.height);
  }
  AppState.rects = [];
  AppState.node_id = null;
  AppState.navHistory = [];
  AppState.navIndex = -1;
  AppState.navSession++;
  replaceBrowserHistoryEntry(null, -1);
  AppState.selectedRectIndex = null;
  AppState.selectedNodeId = null;
  hideContextMenu();
  updateNavButtons();
}

function startScanProgress(path) {
  const dialog = document.getElementById("scanDialog");
  const cancelButton = document.getElementById("cancelScanButton");
  const progressElement = document.querySelector(".scan-progress");
  const dotsElement = document.getElementById("scanningDots");
  document.getElementById("scanQueryPath").textContent = path;
  document.getElementById("scanQueryPath").title = path;
  document.getElementById("scanCurrentPath").textContent = path;
  document.querySelector(".scan-progress-bar").style.width = "0%";
  progressElement.classList.add("is-indeterminate");
  progressElement.removeAttribute("aria-valuenow");
  document.getElementById("scanElapsedTime").textContent = "0:00";
  document.getElementById("scanEstimate").textContent = "Estimating remaining time…";
  cancelButton.disabled = false;
  cancelButton.textContent = "Cancel";
  scanCancelledByUser = false;
  let dotCount = 1;
  dotsElement.textContent = ".";
  clearInterval(scanDotsTimer);
  scanDotsTimer = setInterval(() => {
    dotCount = dotCount % 3 + 1;
    dotsElement.textContent = ".".repeat(dotCount);
  }, 350);
  if (!dialog.open) dialog.showModal();

  const token = ++scanProgressToken;
  const poll = async () => {
    if (token !== scanProgressToken) return;
    try {
      const progress = await GetScanProgress();
      if (progress?.path) {
        document.getElementById("scanCurrentPath").textContent = progress.path;
      }
      const progressElement = document.querySelector(".scan-progress");
      if (progress?.determinate) {
        const fraction = Math.max(0, Math.min(1, Number(progress.fraction || 0)));
        const percent = Math.round(fraction * 100);
        progressElement.classList.remove("is-indeterminate");
        document.querySelector(".scan-progress-bar").style.width = `${fraction * 100}%`;
        progressElement.setAttribute("aria-valuemin", "0");
        progressElement.setAttribute("aria-valuemax", "100");
        progressElement.setAttribute("aria-valuenow", String(percent));
      } else {
        progressElement.classList.add("is-indeterminate");
        progressElement.removeAttribute("aria-valuenow");
      }
      document.getElementById("scanElapsedTime").textContent = formatDuration(progress?.elapsedMilliseconds || 0);
      document.getElementById("scanEstimate").textContent = !progress?.determinate
        ? "Remaining time unavailable for folders"
        : progress.remainingMilliseconds >= 0
          ? `Remaining ~${formatDuration(progress.remainingMilliseconds, true)}`
          : "";
    } catch (err) {
      console.debug("scan progress unavailable:", err);
    } finally {
      if (token === scanProgressToken) {
        scanProgressTimer = setTimeout(poll, 120);
      }
    }
  };
  scanProgressTimer = setTimeout(poll, 120);
}

function formatDuration(milliseconds, roundUp = false) {
  const rawSeconds = milliseconds / 1000;
  const totalSeconds = Math.max(0, roundUp ? Math.ceil(rawSeconds) : Math.round(rawSeconds));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) {
    return `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
  }
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function stopScanProgress() {
  scanProgressToken++;
  clearTimeout(scanProgressTimer);
  scanProgressTimer = null;
  clearInterval(scanDotsTimer);
  scanDotsTimer = null;
  const dialog = document.getElementById("scanDialog");
  if (dialog?.open) dialog.close();
}

async function cancelActiveScan() {
  if (scanCancelledByUser) return;
  scanCancelledByUser = true;
  const button = document.getElementById("cancelScanButton");
  button.disabled = true;
  button.textContent = "Cancelling…";
  try {
    await CancelScan();
  } catch (err) {
    console.error("cancelling scan failed:", err);
  }
}

async function analyze() {
  const path = document.getElementById("pathInput").value?.trim();
  if (!path) return;

  let scanStarted = false;
  setUIBusy(true);
  try {
    const canonicalPath = await ValidateScanPath(path);
    document.getElementById("pathInput").value = canonicalPath;
    clearTreemapForScan();
    startScanProgress(canonicalPath);
    scanStarted = true;

    const {rootId, fileCount, dirCount} = await apiScan(canonicalPath);
    stopScanProgress();
    scanStarted = false;
    

    // set focus & history
    AppState.node_id = rootId;
    AppState.scanRootPath = canonicalPath;
    AppState.navHistory = [rootId];
    AppState.fileCount = fileCount;
    AppState.dirCount = dirCount;
    AppState.navIndex = 0;
    replaceBrowserHistoryEntry(rootId, 0);
    AppState.selectedRectIndex = null;
    AppState.selectedNodeId = null;

    // immediately layout + draw
    await redraw();
  } catch (e) {
    console.error("analyze failed:", e);
    if (scanStarted) stopScanProgress();
    const wasCancelled = scanCancelledByUser || /scan cancelled/i.test(String(e));
    if (!wasCancelled) showErrorToast(e);
  } finally {
    if (scanStarted) stopScanProgress();
    setUIBusy(false);
    updateNavButtons();
  }
}

// ---------- Layout + Draw ----------
async function redraw() {
  hideRectToast();
  const nodeId = AppState.node_id;
  if (nodeId == null) return;

  const w = AppState.colorCanvas.width;
  const h = AppState.colorCanvas.height;

  console.time("layout(fetch)");
  const payload = await apiLayoutById(nodeId, w, h, AppState.scale);
  console.timeEnd("layout(fetch)");

  const rects = payload?.rects;
  if (!Array.isArray(rects)) {
    console.warn("no rects from backend");
    return;
  }

  AppState.rects = rects;
  AppState.selectedRectIndex = null;

  console.time("draw");
  drawTreemap(rects);
  console.timeEnd("draw");

  updateNavButtons();
}

function formatModTime(sec) {
  if (!sec) return "";
  const d = new Date(sec * 1000);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function drawTreemap(rects) {
  const ctx = AppState.colorCtx;
  const idc = AppState.idCtx;
  ctx.clearRect(0, 0, AppState.colorCanvas.width, AppState.colorCanvas.height);
  idc.clearRect(0, 0, AppState.idCanvas.width, AppState.idCanvas.height);

  for (let i = 0; i < rects.length; i++) {
    drawRect(rects[i], /*writeId*/true, ctx, i);
  }
}

function textFits(ctx, text, maxw){
  return ctx.measureText(text).width <= maxw;
}

function ellipsize(ctx, text, maxw){
  if (!text) return '';
  if (textFits(ctx, text, maxw)) return text;

  const ell = '…';
  if (!textFits(ctx, ell, maxw)) return ''; 

  let lo = 0, hi = text.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    const s = text.slice(0, mid) + ell;
    textFits(ctx, s, maxw) ? (lo = mid + 1) : (hi = mid);
  }
  const cut = Math.max(0, lo - 1);
  return cut <= 0 ? '' : (text.slice(0, cut) + ell)
}

function getCtxFontBounds(ctx, fontPx){
  const m = ctx.measureText("Mg"); // tall sample
  const ascent  = m.actualBoundingBoxAscent  ?? Math.round(fontPx * 0.8);
  const descent = m.actualBoundingBoxDescent ?? Math.max(1, Math.round(fontPx * 0.2));
  const textH   = ascent + descent;
  const lineH   = textH + pxI(2);          // base gap ~2px at 1x
  return { ascent, textH, lineH };
}

function filterLinesByAvailableSpace(ctx, lines, fontBounds, maxW, maxH){
  const { lineH, textH } = fontBounds;

  if (maxW <= 0 || maxH <= 0 || !lines || !lines.length) return [];

  const maxLines = Math.max(0, Math.floor((maxH + (lineH - textH)) / lineH));
  if (maxLines <= 0) return [];

  const out = [];

  for (let i = 0; i < lines.length; i++) {
    if (out.length >= maxLines) break;

    const spec = lines[i] || {};

    if (spec.ellipsize) {
      const text = ellipsize(ctx, spec.text, maxW);
      if (text.length > 0)
        out.push(text);
    }
    else if(textFits(ctx, spec.text, maxW)){
      out.push(spec.text);
    }
  }

  return out;
}

function writeCenteredLinesInRect(ctx, lines, fontBounds, rect){
  if (!lines || !lines.length) return;

  const text_lines = filterLinesByAvailableSpace(ctx, lines, fontBounds, rect.w - 2, rect.h - 2);

  const { ascent, lineH, textH } = fontBounds;

  const blockH = text_lines.length * lineH - (lineH - textH);

  const baseY = rect.y + (rect.h - blockH) / 2 + ascent;

  for (let i = 0; i < text_lines.length; i++) {
    const t = text_lines[i];
    if (!t) continue;
    const tw = ctx.measureText(t).width;
    const x  = Math.round(rect.x + (rect.w - tw) / 2);  
    const y  = Math.round(baseY + i * lineH);           
    ctx.fillText(t, x, y);
  }
}

function pxI(base) {        
  const s = AppState.scale || 1;
  return Math.max(1, Math.round(base * s));
}
function pxF(base) { 
  const s = AppState.scale || 1;
  return Math.max(0.5, base * s);
}

function blendHexColor(hex, target, amount) {
  let value = hex.replace("#", "");
  if (/^[0-9a-f]{3}$/i.test(value)) value = value.split("").map(channel => channel + channel).join("");
  if (!/^[0-9a-f]{6}$/i.test(value)) return hex;
  const channels = [0, 2, 4].map(offset => parseInt(value.slice(offset, offset + 2), 16));
  const blended = channels.map(channel => Math.round(channel + (target - channel) * amount));
  return `rgb(${blended[0]}, ${blended[1]}, ${blended[2]})`;
}

function drawRectRelief(ctx, rect, fillColor) {
  const brightness = 1 + AppearanceState.reliefStrength;
  if (brightness === 1.0 || rect.w < 4 || rect.h < 4) return;

  const amount = Math.min(1, Math.abs(brightness - 1));
  const lightColor = blendHexColor(fillColor, 255, amount);
  const darkColor = blendHexColor(fillColor, 0, amount);
  const left = rect.x + 1.5;
  const top = rect.y + 1.5;
  const right = rect.x + rect.w - 1.5;
  const bottom = rect.y + rect.h - 1.5;

  ctx.save();
  ctx.beginPath();
  addRectPath(ctx, rect.x, rect.y, rect.w, rect.h);
  ctx.clip();
  ctx.lineWidth = 1;
  ctx.lineCap = "butt";
  ctx.lineJoin = "miter";

  ctx.beginPath();
  ctx.moveTo(left, bottom);
  ctx.lineTo(left, top);
  ctx.lineTo(right, top);
  ctx.strokeStyle = lightColor;
  ctx.stroke();

  ctx.beginPath();
  ctx.moveTo(right, top);
  ctx.lineTo(right, bottom);
  ctx.lineTo(left, bottom);
  ctx.strokeStyle = darkColor;
  ctx.stroke();
  ctx.restore();
}

function drawRect(rect, writeId, ctx, rectIndex) {
  const isSelected = AppState.selectedNodeId == rect.node_id;
  const isRoot = rect.parent_id == null;
  const palette = activePalette();
  if (isSelected && rectIndex >= 0) AppState.selectedRectIndex = rectIndex;

  //  scaled UI constants  
  const PAD          = pxI(4);            
  const FONT_PX      = pxI(FONT_SIZE);    
  const STROKE_PX    = pxF(1);            
  const LABEL_MIN_W  = pxI(40);           
  const LABEL_MIN_H  = pxI(FONT_SIZE + 4);
  const FOLDER_W_MIN = pxI(60);           
  const FOLDER_H_MIN = pxI(15);

  // fill
  const fillColor = isSelected ? "#000000"
    : (rect.is_free_space || isRoot ? "#fff"
      : (rect.is_small_files ? "#e6dac5" : palette[(rect.depth || 0) % palette.length]));
  ctx.fillStyle = fillColor;
  fillRoundedRect(ctx, rect.x, rect.y, rect.w, rect.h);

  // stroke
  ctx.strokeStyle = "#222";
  ctx.lineWidth = STROKE_PX;
  strokeRoundedRect(ctx, rect.x + 0.5, rect.y + 0.5, rect.w - 1, rect.h - 1);
  drawRectRelief(ctx, rect, fillColor);

  //  ID buffer 
  if (writeId) {
    const rgb = idToColor(rectIndex);
    AppState.idCtx.fillStyle = `rgb(${rgb[0]},${rgb[1]},${rgb[2]})`;
    AppState.idCtx.fillRect(Math.round(rect.x), Math.round(rect.y), Math.round(rect.w), Math.round(rect.h));
  }

  //  too small for labels?
  if (rect.w < LABEL_MIN_W || rect.h < LABEL_MIN_H) return;

  //  text setup 
  ctx.font = `${FONT_PX}px sans-serif`;
  ctx.textBaseline = "alphabetic";
  ctx.fillStyle = isSelected ? "#fff" : "#000";

  const sizeStr = formatSize(rect.size || 0);
  const fontBounds = getCtxFontBounds(ctx, FONT_PX);

  const anonymize = false;

  if (rect.is_free_space) {
    const percent = 100 * rect.size / rect.disk_total;
    const fileCount = AppState.fileCount ?? '?';
    const dirCount = AppState.dirCount ?? '?';
    const lines = [
      {text:`Free Space: ${percent.toFixed(1)}%`, ellipsize:false},
      {text:`${sizeStr} Free`, ellipsize:false},
      {text:`Files: ${fileCount}`, ellipsize:false},
      {text:`Folders: ${dirCount}`, ellipsize:false}
    ];
    writeCenteredLinesInRect(ctx, lines, fontBounds, rect);
  }
  else if (rect.is_small_files) {
    const count = Number(rect.small_file_count || 0).toLocaleString();
    const limit = formatCompactSize(rect.small_file_limit || 0);
    writeCenteredLinesInRect(ctx, [
      { text: `${count} <${limit} files`, ellipsize: false },
      { text: sizeStr, ellipsize: false },
    ], fontBounds, rect);
  }
  else if (rect.is_folder) {
    if (rect.w > FOLDER_W_MIN && rect.h > FOLDER_H_MIN) {
      let display = `${anonymize ? "A folder" : rect.name} (${sizeStr})`;
      if (isRoot && rect.disk_total > 0) {
        const used = Math.max(0, rect.disk_total - (rect.disk_free || 0));
        display = `${rect.name} (${formatSize(used)} / ${formatSize(rect.disk_total)})`;
      }
      const label = ellipsize(ctx, display, rect.w - PAD*2);
      const y = Math.round(rect.y + PAD + fontBounds.ascent);
      const x = Math.round(rect.x + PAD);
      ctx.fillText(label, x, y);
    }
  }
  else { // file
    const dateStr = rect.mtime ? formatModTime(rect.mtime) : "";
    writeCenteredLinesInRect(ctx, [
      { text: anonymize ? "A file" : rect.name,  ellipsize: true  },
      { text: sizeStr,    ellipsize: false },
      { text: dateStr,    ellipsize: false },
    ], fontBounds, rect);
  }
}

//  Selection & partial redraw 
function getSelectedRect() {
  const i = AppState.selectedRectIndex;
  return (i == null) ? null : AppState.rects?.[i] || null;
}

function isPassiveRect(rect) {
  return !!(rect?.is_free_space || rect?.is_small_files);
}

function selectRectByIndex(rectIndex, dontDeselect=false) {
  const count = AppState.rects?.length || 0;

  if (rectIndex == null || rectIndex < 0 || rectIndex >= count) {
    if (!dontDeselect) {
      const prevIdx = AppState.selectedRectIndex;
      AppState.selectedRectIndex = null;
      AppState.selectedNodeId = null;
      if (prevIdx != null) reDrawRectByIndex(prevIdx);
    }
    return;
  }

  if (AppState.selectedRectIndex === rectIndex) {
    if (dontDeselect) return;
    const prevIdx = AppState.selectedRectIndex;
    AppState.selectedRectIndex = null;
    AppState.selectedNodeId = null;
    if (prevIdx != null) reDrawRectByIndex(prevIdx);
    return;
  }

  const rect = AppState.rects[rectIndex];
  if (isPassiveRect(rect)) {
    if (!dontDeselect) {
      const prevIdx = AppState.selectedRectIndex;
      AppState.selectedRectIndex = null;
      AppState.selectedNodeId = null;
      if (prevIdx != null) reDrawRectByIndex(prevIdx);
    }
    return;
  }

  const prevIdx = AppState.selectedRectIndex;
  AppState.selectedRectIndex = rectIndex;
  AppState.selectedNodeId = AppState.rects[rectIndex].node_id;

  if (prevIdx != null) reDrawRectByIndex(prevIdx);
  reDrawRectByIndex(rectIndex);
}

function reDrawRectByIndex(idx) {
  const r = AppState.rects?.[idx];
  if (!r || r.w <= 0 || r.h <= 0) return;

  // 1) draw rect into tmp
  drawRect(r, /*writeId*/false, AppState.tmpCtx, -1);

  // 2) mask: start with rect, punch out child rects (r.children are rect indices)
  AppState.maskCtx.clearRect(0, 0, AppState.maskCanvas.width, AppState.maskCanvas.height);
  AppState.maskCtx.fillStyle = 'rgba(0,0,0,1)';
  fillRoundedRect(AppState.maskCtx, r.x, r.y, r.w, r.h);

  AppState.maskCtx.save();
  AppState.maskCtx.globalCompositeOperation = 'destination-out';

  const childrenIdx = Array.isArray(r.children) ? r.children : [];
  for (let i = 0; i < childrenIdx.length; i++) {
    const cr = AppState.rects[childrenIdx[i]];
    fillRoundedRect(AppState.maskCtx, cr.x, cr.y, cr.w, cr.h);
  }
  AppState.maskCtx.restore();

  // 3) apply mask & blit
  AppState.tmpCtx.globalCompositeOperation = 'destination-in';
  AppState.tmpCtx.drawImage(AppState.maskCanvas, 0, 0);
  AppState.tmpCtx.globalCompositeOperation = 'source-over';

  AppState.colorCtx.drawImage(AppState.tmpCanvas, 0, 0);
}

// ---------- Navigation ----------
const HISTORY_STATE_KEY = "spacebrowserNavigation";

function browserHistoryState(nodeId, navIndex) {
  return {
    [HISTORY_STATE_KEY]: true,
    session: AppState.navSession,
    nodeId,
    navIndex,
    position: AppState.browserHistoryPosition
  };
}

function replaceBrowserHistoryEntry(nodeId, navIndex) {
  window.history.replaceState(browserHistoryState(nodeId, navIndex), "");
}

function pushBrowserHistoryEntry(nodeId, navIndex) {
  AppState.browserHistoryPosition++;
  window.history.pushState(browserHistoryState(nodeId, navIndex), "");
}

function navigateToSelected() {
  const r = getSelectedRect();
  if (!r || !r.is_folder || isPassiveRect(r)) return;
  visit(r.node_id);
}

function goToRoot() {
  if (!AppState.navHistory.length) return;
  visit(AppState.navHistory[0]);
}

function goToParent() {
  if (!AppState.rects?.length) return;
  const rootRect = AppState.rects[0];
  if (rootRect.parent_id == null) return;
  visit(rootRect.parent_id);
}

function visit(nodeId) {
  if (nodeId == null || nodeId < 0) return;
  if (nodeId === AppState.node_id) return;

  // trim forward history
  AppState.navHistory = AppState.navHistory.slice(0, AppState.navIndex + 1);

  AppState.navHistory.push(nodeId);
  AppState.navIndex = AppState.navHistory.length - 1;
  AppState.node_id = nodeId;
  AppState.selectedRectIndex = null;
  pushBrowserHistoryEntry(nodeId, AppState.navIndex);
  redraw();
}

function goBackward() {
  if (AppState.navIndex > 0) {
    window.history.back();
  }
}
function goForward() {
  if (AppState.navIndex < AppState.navHistory.length - 1) {
    window.history.forward();
  }
}

window.addEventListener("popstate", (e) => {
  const state = e.state;
  const isCurrentNavigation = state?.[HISTORY_STATE_KEY]
    && state.session === AppState.navSession
    && Number.isInteger(state.navIndex)
    && AppState.navHistory[state.navIndex] === state.nodeId;

  if (!isCurrentNavigation) {
    // Entries from an earlier scan may still surround the current entry. Return
    // to the current scan without ever applying their now-invalid node IDs.
    const stalePosition = Number(state?.position);
    window.history.go(Number.isFinite(stalePosition) && stalePosition > AppState.browserHistoryPosition ? -1 : 1);
    return;
  }

  AppState.browserHistoryPosition = state.position;
  AppState.navIndex = state.navIndex;
  AppState.node_id = state.nodeId;
  AppState.selectedRectIndex = null;
  redraw();
});

export async function toggleFreeSpace(e) {
  const button = e?.currentTarget ?? document.getElementById("toggleFreeSpaceButton");
  if (!button) return;

  const wasChecked = button.getAttribute("aria-pressed") === "true";
  const checked = !wasChecked;
  button.setAttribute("aria-pressed", String(checked));

  try {
    await SetShowFreeSpace(checked);
    await redraw();
  } catch (err) {
    button.setAttribute("aria-pressed", String(wasChecked));
    console.error("toggleFreeSpace failed:", err);
  }
}

function updateNavButtons() {
  const atRoot = AppState.navIndex === 0;
  document.getElementById("rootButton").disabled = atRoot;

  const hasParent = !!(AppState.rects && AppState.rects.length && AppState.rects[0].parent_id != null);
  document.getElementById("parentButton").disabled = !hasParent;

  document.getElementById("backwardButton").disabled = AppState.navIndex <= 0;
  document.getElementById("forwardButton").disabled = AppState.navIndex >= AppState.navHistory.length - 1;
}

let pendingDeletion = null;
let deletionInProgress = false;

function trashDestinationName() {
  return AppState.profile?.platformSystem === "windows" ? "Recycle Bin" : "Trash";
}

function requestSelectedDeletion() {
  hideContextMenu();
  hideRectToast();

  const rect = getSelectedRect();
  if (!rect) return;
  if (deletionInProgress) {
    showErrorToast("Another deletion is already in progress");
    return;
  }
  if (!AppState.profile?.allowDelete) {
    showErrorToast("Delete commands are disabled. Enable Allow delete command in Settings");
    return;
  }
  if (isPassiveRect(rect) || !rect.full_path) {
    showErrorToast("This item cannot be deleted");
    return;
  }
  if (rect.node_id === AppState.node_id) {
    showErrorToast("The current view cannot be deleted. Go to its parent first");
    return;
  }

  pendingDeletion = {
    nodeId: rect.node_id,
    path: rect.full_path,
    size: rect.size,
  };
  document.getElementById("deleteConfirmTitle").textContent = `Move this item to ${trashDestinationName()}?`;
  document.getElementById("deleteConfirmPath").textContent = rect.full_path;
  document.getElementById("deleteConfirmSize").textContent = detailedByteSize(rect.size);
  const dialog = document.getElementById("deleteConfirmDialog");
  if (dialog && !dialog.open) dialog.showModal();
}

function closeDeleteConfirmation() {
  if (deletionInProgress) return;
  const dialog = document.getElementById("deleteConfirmDialog");
  if (dialog?.open) dialog.close();
  pendingDeletion = null;
}

function waitForNextPaint() {
  return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
}

function trimInvalidForwardNavigation() {
  const hadForwardHistory = AppState.navIndex < AppState.navHistory.length - 1;
  AppState.navHistory = AppState.navHistory.slice(0, AppState.navIndex + 1);
  if (hadForwardHistory) pushBrowserHistoryEntry(AppState.node_id, AppState.navIndex);
  else replaceBrowserHistoryEntry(AppState.node_id, AppState.navIndex);
}

async function confirmSelectedDeletion() {
  if (!pendingDeletion || deletionInProgress) return;
  const target = pendingDeletion;
  const confirmButton = document.getElementById("confirmDeleteButton");
  const cancelButton = document.getElementById("cancelDeleteButton");
  deletionInProgress = true;
  confirmButton.disabled = true;
  cancelButton.disabled = true;
  document.getElementById("deleteConfirmDialog")?.close();
  pendingDeletion = null;
  showToastAt(lastMousePos.x, lastMousePos.y, `Moving to ${trashDestinationName()}…`, 30000);

  try {
    await waitForNextPaint();
    const result = await DeleteNode(target.nodeId);
    document.getElementById("deleteConfirmDialog")?.close();
    pendingDeletion = null;
    AppState.selectedRectIndex = null;
    AppState.selectedNodeId = null;

    if (AppState.profile?.rescanOnDelete) {
      if (AppState.scanRootPath) {
        document.getElementById("pathInput").value = AppState.scanRootPath;
      }
      await analyze();
    } else {
      AppState.fileCount = result.fileCount;
      AppState.dirCount = result.dirCount;
      trimInvalidForwardNavigation();
      await redraw();
      updateNavButtons();
      showToastAt(lastMousePos.x, lastMousePos.y, `Moved to ${trashDestinationName()}`, 1600);
    }
  } catch (err) {
    document.getElementById("deleteConfirmDialog")?.close();
    pendingDeletion = null;
    showErrorToast(err);
  } finally {
    deletionInProgress = false;
    confirmButton.disabled = false;
    cancelButton.disabled = false;
  }
}

// ---------- Context menu ----------
window.addEventListener("click", () => hideContextMenu());
function showContextMenu(x, y) {
  const m = document.getElementById("contextMenu");
  m.style.left = `${x}px`; m.style.top = `${y}px`; m.style.display = "block";

  const r = getSelectedRect?.();
  const liGo = m.querySelector('[data-action="goto"]');
  if (liGo) {
    if (!r?.is_folder) liGo.classList.add('disabled');
    else liGo.classList.remove('disabled');
  }

}
function hideContextMenu() {
  document.getElementById("contextMenu").style.display = "none";
}

let hoveredRect = null;
let hoveredRectIndex = -1;
const associatedIconCache = new Map();

function rectSupportsDetailsToast(rect) {
  return !!(rect?.full_path && rect.parent_id != null && !rect.is_free_space);
}

function detailedByteSize(bytes) {
  return `${Number(bytes || 0).toLocaleString()} bytes`;
}

function associatedIconKey(rect) {
  if (rect.is_folder) return "<folder>";
  const name = String(rect.name || "").toLocaleLowerCase();
  const dot = name.lastIndexOf(".");
  const extension = dot > 0 ? name.slice(dot) : "<file>";
  return extension === ".exe" || extension === ".lnk" || extension === ".ico"
    ? String(rect.full_path).toLocaleLowerCase()
    : extension;
}

function showFallbackIcon(isFolder) {
  const image = document.getElementById("rectToastAssociatedIcon");
  const fallback = document.getElementById("rectToastFallbackIcon");
  image.classList.add("is-hidden");
  fallback.classList.remove("is-hidden");
  document.getElementById("rectToastFallbackShape").setAttribute("d", isFolder
    ? "M3 8V5h7l2 3h9v11H3z"
    : "M6 3h8l4 4v14H6z");
  document.getElementById("rectToastFallbackDetail").setAttribute("d", isFolder
    ? "M3 9h18"
    : "M14 3v5h5");
}

function applyAssociatedIcon(rect, icon) {
  if (!icon || hoveredRect?.full_path !== rect.full_path) return;
  const image = document.getElementById("rectToastAssociatedIcon");
  image.src = icon;
  image.classList.remove("is-hidden");
  document.getElementById("rectToastFallbackIcon").classList.add("is-hidden");
}

function updateAssociatedIcon(rect) {
  const key = associatedIconKey(rect);
  const cached = associatedIconCache.get(key);
  if (typeof cached === "string") {
    if (cached) applyAssociatedIcon(rect, cached);
    else showFallbackIcon(rect.is_folder);
    return;
  }

  showFallbackIcon(rect.is_folder);
  if (cached) {
    cached.then(icon => applyAssociatedIcon(rect, icon));
    return;
  }

  const request = Promise.resolve(GetAssociatedIcon(rect.full_path, !!rect.is_folder))
    .then(icon => {
      const value = String(icon || "");
      associatedIconCache.set(key, value);
      return value;
    })
    .catch(() => {
      associatedIconCache.set(key, "");
      return "";
    });
  associatedIconCache.set(key, request);
  request.then(icon => applyAssociatedIcon(rect, icon));
}

function placeRectToast(x, y) {
  const toast = document.getElementById("rectToast");
  if (!toast || toast.hidden) return;

  const pad = 8;
  const offset = 14;
  const width = toast.offsetWidth;
  const height = toast.offsetHeight;
  const left = x + offset + width <= window.innerWidth - pad
    ? x + offset
    : x - width - offset;
  const top = y + offset + height <= window.innerHeight - pad
    ? y + offset
    : y - height - offset;

  toast.style.left = `${Math.max(pad, left)}px`;
  toast.style.top = `${Math.max(pad, top)}px`;
}

function hideRectToast() {
  const toast = document.getElementById("rectToast");
  if (toast) toast.hidden = true;
  hoveredRect = null;
  hoveredRectIndex = -1;
}

function updateRectToast(e) {
  const { x, y } = getCanvasCoords(e);
  const rectIndex = rectIndexAtPoint(x, y);
  const rect = AppState.rects?.[rectIndex];
  if (!rectSupportsDetailsToast(rect)) {
    hideRectToast();
    return;
  }
  if (rectIndex === hoveredRectIndex && !document.getElementById("rectToast")?.hidden) {
    placeRectToast(e.clientX, e.clientY);
    return;
  }

  hoveredRect = rect;
  hoveredRectIndex = rectIndex;
  const toast = document.getElementById("rectToast");
  const name = String(rect.name || "");
  const fullPath = String(rect.full_path);
  const suffix = fullPath.slice(-name.length);
  const nameIsPathSuffix = !!name && suffix.toLocaleLowerCase() === name.toLocaleLowerCase();
  document.getElementById("rectToastPathPrefix").textContent = nameIsPathSuffix
    ? fullPath.slice(0, -name.length)
    : fullPath;
  document.getElementById("rectToastName").textContent = nameIsPathSuffix ? name : "";
  document.getElementById("rectToastSize").textContent = detailedByteSize(rect.size);
  const date = rect.mtime ? formatModTime(rect.mtime) : "unavailable";
  document.getElementById("rectToastCreated").textContent = `Modification date : ${date}`;
  updateAssociatedIcon(rect);
  toast.hidden = false;
  placeRectToast(e.clientX, e.clientY);
}

async function openInSystemBrowser() {
  const r = getSelectedRect();
  if (r?.full_path) await apiOpenInFileBrowser(r.full_path);
  hideContextMenu();
}

let lastMousePos = { x: 0, y: 0 };
window.addEventListener("mousemove", e => { lastMousePos = { x: e.clientX, y: e.clientY }; });

function showToastAt(x, y, message="Copied path", duration=1000, variant="default") {
  const t = document.getElementById("toast");
  if (!t) return;

  t.textContent = message;
  t.classList.toggle("is-error", variant === "error");

  // place near cursor with small offset, clamp to viewport
  const pad = 8;
  t.style.left = `${Math.max(pad, Math.min(x + pad, window.innerWidth - t.offsetWidth - pad))}px`;
  t.style.top  = `${Math.max(pad, Math.min(y + pad, window.innerHeight - t.offsetHeight - pad))}px`;

  t.style.opacity = "1";
  t.style.transform = "translateY(0)";
  clearTimeout(t._hideTimer);
  t._hideTimer = setTimeout(() => {
    t.style.opacity = "0";
    t.style.transform = "translateY(6px)";
  }, duration);
}

function showErrorToast(error) {
  let message = String(error?.message || error || "Unable to analyze this path.")
    .replace(/^Error:\s*/i, "")
    .trim();
  if (message) {
    message = message[0].toUpperCase() + message.slice(1);
    if (!/[.!?]$/.test(message)) message += ".";
  }

  const toast = document.getElementById("toast");
  if (!toast) return;
  toast.textContent = message;
  const topbarBottom = document.getElementById("topbar")?.getBoundingClientRect().bottom || 38;
  const x = (window.innerWidth - toast.offsetWidth) / 2 - 8;
  showToastAt(x, topbarBottom, message, 2600, "error");
}

async function copySelectedPathAt(pos) {
  const r = getSelectedRect?.();
  if (!r?.full_path) return;

  try {
    await navigator.clipboard.writeText(r.full_path);
  } catch {
    const ta = document.createElement("textarea");
    ta.value = r.full_path;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.focus(); ta.select();
    document.execCommand("copy");
    ta.remove();
  }
  const p = pos || lastMousePos;
  showToastAt(p.x, p.y);
}

document.getElementById("contextMenu").addEventListener("click", async (e) => {
  const li = e.target.closest("li");
  if (!li) return;

  const r = getSelectedRect?.();
  if (!r) return;

  if (li.dataset.action === "copy") {
    await copySelectedPathAt({ x: e.clientX, y: e.clientY });
    return;
  }
  if (li.dataset.action === "delete") {
    requestSelectedDeletion();
    return;
  }
  if (li.dataset.action === "open" && r.full_path){
    await OpenInFileBrowser(r.full_path);
  } 
  else if (li.dataset.action === "goto") {
    visit(r.node_id);
  }
});

window.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "c") {
    const r = getSelectedRect?.();
    if (r?.full_path) {
      e.preventDefault();
      copySelectedPathAt();
    }
  }
});

window.addEventListener("keydown", (e) => {
  if (e.isComposing || e.key !== "Delete") return;
  const target = e.target;
  if (target instanceof HTMLElement && target.closest("input, textarea, select, [contenteditable='true'], dialog[open]")) return;
  if (!getSelectedRect()) return;
  e.preventDefault();
  requestSelectedDeletion();
});

window.addEventListener("keydown", (e) => {
  if (e.isComposing || e.key !== "Enter") return;

  const pathInput = document.getElementById("pathInput");
  if (document.activeElement === pathInput) {
    e.preventDefault();
    analyze();       
    return;
  }
  navigateToSelected();
});

// Match the navigation inputs desktop users expect from file browsers.
window.addEventListener("keydown", (e) => {
  if (e.isComposing || !e.altKey) return;
  if (e.key === "ArrowLeft") {
    e.preventDefault();
    goBackward();
  } else if (e.key === "ArrowRight") {
    e.preventDefault();
    goForward();
  }
});

// ---------- Utilities ----------
function getCanvasCoords(event) {
  const rect = AppState.colorCanvas.getBoundingClientRect();
  return { x: event.clientX - rect.left, y: event.clientY - rect.top };
}
function debounce(func, delay) {
  let t; return (...args) => { clearTimeout(t); t = setTimeout(() => func.apply(this, args), delay); };
}
function resizeCanvas() {
  const containerRect = AppState.colorCanvas.parentElement.getBoundingClientRect();
  const controlsRect  = document.querySelector(".controls").getBoundingClientRect();
  const width  = containerRect.width;
  const height = window.innerHeight - controlsRect.height;
  const dpr = window.devicePixelRatio || 1;

  for (const c of [AppState.colorCanvas, AppState.idCanvas, AppState.tmpCanvas, AppState.maskCanvas]) {
    c.width  = Math.max(1, Math.floor(width  * dpr));
    c.height = Math.max(1, Math.floor(height * dpr));
    c.style.width  = `${width}px`;
    c.style.height = `${height}px`;
  }
}
function setUIBusy(state) {
  document.querySelectorAll(".controls button").forEach(btn => btn.disabled = state);
}

// ID buffer helpers (rect-index <-> RGB)
function idToColor(id){ const code = id + 1; return [(code>>16)&255,(code>>8)&255,code&255]; }
function colorToId(color){ return ((color[0]<<16)|(color[1]<<8)|color[2]) - 1; }
function rectIndexAtPoint(x, y) {
  const dpr = window.devicePixelRatio || 1;
  const px = Math.round(x * dpr), py = Math.round(y * dpr);
  const pixel = AppState.idCtx.getImageData(px, py, 1, 1).data;
  return colorToId(pixel);
}

function currentCornerRadius() {
  return AppearanceState.cornerRadius * getScale();
}

function addRectPath(ctx, x, y, w, h) {
  const radius = currentCornerRadius();
  if (radius === 0) ctx.rect(x, y, w, h);
  else ctx.roundRect(x, y, w, h, radius);
}

function fillRoundedRect(ctx, x, y, w, h) {
  if (currentCornerRadius() === 0) {
    ctx.fillRect(x, y, w, h);
    return;
  }
  ctx.beginPath();
  addRectPath(ctx, x, y, w, h);
  ctx.fill();
}

function strokeRoundedRect(ctx, x, y, w, h) {
  if (currentCornerRadius() === 0) {
    ctx.strokeRect(x, y, w, h);
    return;
  }
  ctx.beginPath();
  addRectPath(ctx, x, y, w, h);
  ctx.stroke();
}

function formatSize(bytes) {
  if (!bytes) return '0 B';
  const u = ['B','KB','MB','GB','TB','PB']; const i = Math.floor(Math.log(bytes)/Math.log(1024));
  const n = bytes / Math.pow(1024, i);
  return `${n.toFixed(n < 10 ? 1 : 0)} ${u[i]}`;
}

function formatCompactSize(bytes) {
  return formatSize(bytes).replace(/\.0 (?=[A-Z])/, "").replace(" ", "");
}

// ---------- folder picker ----------
async function triggerFolderSelect() {
  if (AppState.pickingFolderDialogIsOpen){
    console.log('nope!');
    return;
  }
  const btn = document.getElementById('triggerFolderSelectButton');
  try {
    
    btn.disabled = true;
    AppState.pickingFolderDialogIsOpen = true;
    const path = await PickFolder();
    AppState.pickingFolderDialogIsOpen = false;
    btn.disabled = false;

    if (!path) return;
    const pathInput = document.getElementById("pathInput")
    pathInput.value = path;
    setTimeout(() => {
      pathInput.focus({ preventScroll: true });
      const n = pathInput.value.length;
      pathInput.setSelectionRange(n, n);
    }, 0);
    // analyze();
  } catch (e) {
    // user cancelled or error
    console.warn("folder pick cancelled or failed:", e);
    AppState.pickingFolderDialogIsOpen = false;
    btn.disabled = false;
  }
}


// ========= Manual Zoom Control =========


async function setScale(next, reason="") {
  const old = AppState.zoomFactor || 1;
  const clamped = Math.max(SCALE_MIN, Math.min(SCALE_MAX, next));
  if (clamped === old) return;
  AppState.zoomFactor = clamped;
  AppearanceState.zoomFactor = clamped;
  AppState.scale = getScale() * clamped;
  console.debug(`scale ${reason}:`, old, '→', clamped, `SCALE_MIN ${SCALE_MIN}`, `SCALE_MAX ${SCALE_MAX}`);
  
  await redraw();
}

// Smooth factor from wheel delta (handles big/small deltas consistently)
function factorFromWheelDelta(deltaY, deltaMode) {
  // deltaMode: 0=pixel, 1=line, 2=page. Normalize a bit:
  const k = (deltaMode === 1) ? 15 : (deltaMode === 2 ? 120 : 1);
  return Math.pow(SCALE_SMOOTH_BASE, -deltaY * k);
}

function installZoomInterception(root = window) {
  // 1) Ctrl/Cmd + wheel (desktop & trackpad pinch on Chromium)
  root.addEventListener('wheel', (e) => {
    if (e.ctrlKey || e.metaKey) {
      e.preventDefault();
      const f = factorFromWheelDelta(e.deltaY, e.deltaMode);
      setScale((AppState.zoomFactor || 1) * f, 'wheel');
    }
  }, { passive: false, capture: true });

  // 2) Ctrl/Cmd + +/-/0 (keyboard)
  const ZOOM_KEYS = new Set(['+', '=', '-', '_', '0', 'NumpadAdd', 'NumpadSubtract', 'Numpad0']);
  root.addEventListener('keydown', (e) => {
    if (!(e.ctrlKey || e.metaKey)) return;
    const keyOrCode = ZOOM_KEYS.has(e.key) || ZOOM_KEYS.has(e.code);
    if (!keyOrCode) return;

    e.preventDefault();

    // Reset
    if (e.key === '0' || e.code === 'Numpad0') {
      setScale(1, 'kbd-reset');
      return;
    }
    // Zoom in / out
    if (e.key === '+' || e.key === '=' || e.code === 'NumpadAdd') {
      setScale((AppState.zoomFactor || 1) * SCALE_STEP_KEYS, 'kbd-in');
    } else if (e.key === '-' || e.key === '_' || e.code === 'NumpadSubtract') {
      setScale((AppState.zoomFactor || 1) / SCALE_STEP_KEYS, 'kbd-out');
    }
  }, { capture: true });

}

