import { clearSelection, getSelectedRects, isPassiveRect, reduceDeletionTargets, selectionIds } from "./selection.js";
import { CancelScan, GetFullTree, GetScanProgress, GetScanPreview, OpenPath, ValidateScanPath, RefreshFolders } from "./wailsjs/go/main/App.js";
import { byId, query, queryAll } from "./dom.js";
import { formatCount, formatDuration } from "./format.js";
import { remapNavigation, pushBrowserHistoryEntry, rollbackBrowserHistory, updateNavButtons } from "./navigation.js";
import { addControlEventListeners, eventMatchesShortcut, shortcutCanRun } from "./controls.js";
import { hideRectToast, showErrorToast } from "./notifications.js";
import { logDebug, logError } from "./logging.js";
import { hideLocationSelector, showLocationSelector } from "./locations.js";
import { AppState } from "./state.js";

let redraw = async () => {};
let hideContextMenu = () => {};
let scanProgressTimer = null;
let scanProgressToken = 0;
let scanCancelledByUser = false;
let scanDotsTimer = null;
let analyzeInFlight = false;
let scanReportPath = "";
let displayedScanProgress = 0;

const SCAN_PROGRESS_CAP = 0.96;
const SCAN_PROGRESS_SMOOTHING = 0.08;
const SCAN_PROGRESS_STEP_COUNT = 50;
const SCAN_COMPLETION_DELAY_MS = 180;

function renderScanProgress(fraction) {
  const clamped = Math.max(0, Math.min(1, fraction));
  const stepped = clamped === 1
    ? 1
    : Math.floor(clamped * SCAN_PROGRESS_STEP_COUNT) / SCAN_PROGRESS_STEP_COUNT;
  const percentage = stepped * 100;
  query("#scanDialog .scan-progress-bar").style.setProperty("--scan-progress", `${percentage}%`);
  query(".scan-progress").setAttribute("aria-valuenow", String(Math.round(percentage)));
  byId("compactScanBar").style.setProperty("--scan-progress", `${percentage}%`);
  byId("compactScanProgress").setAttribute("aria-valuenow", String(Math.round(percentage)));
  byId("compactScanPercent").textContent = `${Math.round(percentage)}%`;
}

function setUIBusy(state) {
  AppState.scanInProgress = state;
  queryAll("#analyzeButton, #triggerFolderSelectButton, #settingsButton").forEach(button => { button.disabled = state; });
  byId("pathInput").disabled = state;
}

function clearTreemapForScan() {
  hideLocationSelector();
  hideRectToast();
  for (const context of [AppState.colorCtx, AppState.hoverCtx, AppState.idCtx, AppState.tmpCtx, AppState.maskCtx, AppState.flashCtx]) {
    if (context) context.clearRect(0, 0, context.canvas.width, context.canvas.height);
  }
  AppState.rects = [];
  AppState.node_id = null;
  AppState.scanRootPath = null;
  AppState.fileCount = 0;
  AppState.dirCount = 0;
  AppState.navHistory = [];
  AppState.navIndex = -1;
  AppState.navSession++;
  clearSelection();
  hideContextMenu();
  updateNavButtons();
}

function clearScanWarning() {
  scanReportPath = "";
  byId("scanWarningIndicator").hidden = true;
}

function showScanWarning(report) {
  const errorCount = Number(report?.errorCount || 0);
  if (errorCount <= 0) {
    clearScanWarning();
    return;
  }

  scanReportPath = String(report?.reportPath || "");
  const details = String(report?.details || "").replace(/=(\d+)/g, ": $1");
  byId("scanWarningSummary").textContent = `${errorCount} scan ${errorCount === 1 ? "issue was" : "issues were"} recorded${details ? `: ${details}` : ""}.`;
  const saveError = byId("scanReportSaveError");
  saveError.hidden = !report?.saveError;
  saveError.textContent = report?.saveError
    ? scanReportPath
      ? "The report was saved, but old-report cleanup was incomplete."
      : "The scan report could not be saved."
    : "";
  const reportButton = byId("viewScanReportButton");
  reportButton.hidden = !scanReportPath;
  reportButton.title = scanReportPath;
  byId("scanWarningIndicator").hidden = false;
}

async function openScanReport() {
  if (!scanReportPath) return;
  try {
    await OpenPath(scanReportPath);
  } catch (error) {
    logError("opening scan report failed:", error);
    showErrorToast(error);
  }
}

function startScanProgress(path, live = false) {
  const dialog = byId("scanDialog");
  const cancelButton = byId("cancelScanButton");
  const progressElement = query(".scan-progress");
  const dotsElement = byId("scanningDots");
  byId("scanQueryPath").textContent = path;
  byId("scanQueryPath").title = path;
  byId("scanCurrentPath").textContent = path;
  progressElement.setAttribute("aria-valuemin", "0");
  progressElement.setAttribute("aria-valuemax", "100");
  displayedScanProgress = 0;
  renderScanProgress(0);
  byId("scanElapsedTime").textContent = "0:00";
  byId("scanFileCount").textContent = "0";
  byId("scanFolderCount").textContent = "0";
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
  byId("compactScanStatus").hidden = !live;
  byId("compactScanTime").textContent = "0:00";
  byId("closeScanDetailsButton").hidden = !live;
  AppState.liveScanPreview = live;
  if (!live && !dialog.open) dialog.showModal();

  const token = ++scanProgressToken;
  let previewRevision = 0;
  const poll = async () => {
    if (token !== scanProgressToken) return;
    try {
      const progress = await GetScanProgress();
      // Completion, cancellation, or a new scan may have invalidated this
      // request while the backend response was in flight.
      if (token !== scanProgressToken) return;
      if (progress?.path) byId("scanCurrentPath").textContent = progress.path;
      const workFraction = Math.max(0, Math.min(1, Number(progress?.fraction || 0)));
      const target = workFraction * SCAN_PROGRESS_CAP;
      if (target > displayedScanProgress) {
        displayedScanProgress += (target - displayedScanProgress) * SCAN_PROGRESS_SMOOTHING;
      }
      renderScanProgress(displayedScanProgress);
      byId("scanElapsedTime").textContent = formatDuration(progress?.elapsedMilliseconds || 0);
      byId("scanFileCount").textContent = formatCount(progress?.fileCount);
      byId("scanFolderCount").textContent = formatCount(progress?.dirCount);
      byId("compactScanTime").textContent = formatDuration(progress?.elapsedMilliseconds || 0);
      if (live && !scanCancelledByUser && progress?.livePreview && progress.rootPath === path) {
        const preview = await GetScanPreview(progress.generation);
        if (token !== scanProgressToken || scanCancelledByUser) return;
        if (preview && preview.revision !== previewRevision) {
          previewRevision = preview.revision;
          initializeScanView(preview.rootId);
          AppState.fileCount = preview.fileCount;
          AppState.dirCount = preview.dirCount;
          await redraw();
        }
      }
    } catch (error) {
      logDebug("scan progress unavailable:", error);
    } finally {
      if (token === scanProgressToken) scanProgressTimer = setTimeout(poll, 120);
    }
  };
  scanProgressTimer = setTimeout(poll, 120);
}

async function completeScanProgress(fileCount, dirCount) {
  scanProgressToken++;
  clearTimeout(scanProgressTimer);
  scanProgressTimer = null;
  clearInterval(scanDotsTimer);
  scanDotsTimer = null;
  byId("scanFileCount").textContent = formatCount(fileCount);
  byId("scanFolderCount").textContent = formatCount(dirCount);
  renderScanProgress(1);
  await new Promise(resolve => setTimeout(resolve, SCAN_COMPLETION_DELAY_MS));
  const dialog = byId("scanDialog");
  if (dialog.open) dialog.close();
  byId("compactScanStatus").hidden = true;
}

function stopScanProgress() {
  scanProgressToken++;
  clearTimeout(scanProgressTimer);
  scanProgressTimer = null;
  clearInterval(scanDotsTimer);
  scanDotsTimer = null;
  const dialog = byId("scanDialog");
  if (dialog.open) dialog.close();
  byId("compactScanStatus").hidden = true;
}

async function cancelActiveScan() {
  if (scanCancelledByUser) return;
  scanCancelledByUser = true;
  const button = byId("cancelScanButton");
  button.disabled = true;
  button.textContent = "Cancelling...";
  try {
    await CancelScan();
  } catch (error) {
    logError("cancelling scan failed:", error);
  }
}

function initializeScanView(rootId) {
  if (AppState.node_id != null) return;
  AppState.node_id = rootId;
  AppState.navHistory = [rootId];
  AppState.navIndex = 0;
  pushBrowserHistoryEntry(rootId, 0);
}

export async function analyze() {
  const path = byId("pathInput").value?.trim();
  if (!path) return;
  if (analyzeInFlight) {
    logDebug("analyze ignored: a scan request is already active");
    return;
  }

  analyzeInFlight = true;
  scanCancelledByUser = false;
  let scanStarted = false;
  let committed = false;
  const previous = Object.fromEntries(["node_id", "rects", "scanRootPath", "fileCount", "dirCount", "navHistory", "navIndex", "navSession"].map(key => [key, AppState[key]]));
  previous.selectedNodeIds = new Set(selectionIds());
  const previousPosition = AppState.browserHistoryPosition;
  const previousWarningHidden = byId("scanWarningIndicator").hidden;
  setUIBusy(true);
  try {
    const canonicalPath = await ValidateScanPath(path);
    byId("pathInput").value = canonicalPath;
    byId("scanWarningIndicator").hidden = true;
    clearTreemapForScan();
    AppState.scanRootPath = canonicalPath;
    startScanProgress(canonicalPath, true);
    scanStarted = true;

    const { rootId, fileCount, dirCount, scanReport } = await GetFullTree(canonicalPath);
    committed = true;
    await completeScanProgress(fileCount, dirCount);
    scanStarted = false;

    initializeScanView(rootId);
    AppState.fileCount = fileCount;
    AppState.dirCount = dirCount;
    showScanWarning(scanReport);
    await redraw();
  } catch (error) {
    logError("analyze failed:", error);
    if (scanStarted) stopScanProgress();
    if (scanStarted && !committed) {
      AppState.liveScanPreview = false;
      clearTreemapForScan();
      Object.assign(AppState, previous);
      rollbackBrowserHistory(previousPosition);
      byId("scanWarningIndicator").hidden = previousWarningHidden;
      if (AppState.node_id != null) await redraw();
    }
    const wasCancelled = scanCancelledByUser || /scan cancelled/i.test(String(error));
    if (!wasCancelled) showErrorToast(error);
  } finally {
    if (scanStarted) stopScanProgress();
    analyzeInFlight = false;
    setUIBusy(false);
    updateNavButtons();
    if (AppState.node_id == null) showLocationSelector({ refresh: true });
  }
}

export async function refreshSelectedFolders() {
  const selected = getSelectedRects();
  if (analyzeInFlight || !selected.length || selected.some(rect => !rect.is_folder || !rect.full_path || isPassiveRect(rect))) return;
  const targets = reduceDeletionTargets(selected).map(rect => ({ nodeId: rect.node_id, path: rect.full_path }));
  analyzeInFlight = true;
  const session = AppState.navSession;
  setUIBusy(true);
  hideContextMenu();
  hideRectToast();
  startScanProgress(targets.length === 1 ? targets[0].path : `${targets.length} selected folders`);
  try {
    const tracked = [...new Set([...selectionIds(), ...AppState.navHistory, AppState.node_id])];
    const result = await RefreshFolders(targets, tracked);
    if (session !== AppState.navSession) return;
    const mapping = result.nodeIds;
    remapNavigation(mapping, mapping[targets[0].nodeId] ?? targets[0].nodeId);
    AppState.selectedNodeIds = new Set([...selectionIds()].map(id => mapping[id] ?? id).filter(id => id >= 0));
    AppState.fileCount = result.fileCount;
    AppState.dirCount = result.dirCount;
    showScanWarning(result.scanReport);
    await redraw();
    await completeScanProgress(result.fileCount, result.dirCount);
  } catch (error) {
    logError("folder refresh failed:", error);
    if (!scanCancelledByUser && !/scan cancelled/i.test(String(error))) showErrorToast(error);
  } finally {
    stopScanProgress();
    analyzeInFlight = false;
    AppState.liveScanPreview = false;
    setUIBusy(false);
    updateNavButtons();
  }
}

export function initScan(options) {
  redraw = options.redraw;
  hideContextMenu = options.hideContextMenu;
  addControlEventListeners(event => {
    if (!shortcutCanRun(event) || !eventMatchesShortcut(event, AppState.profile?.controls?.refresh)) return;
    event.preventDefault();
    refreshSelectedFolders();
  });
  byId("analyzeButton").addEventListener("click", analyze);
  byId("viewScanReportButton").addEventListener("click", openScanReport);
  byId("cancelScanButton").addEventListener("click", cancelActiveScan);
  byId("compactScanStatus").addEventListener("click", () => {
    const dialog = byId("scanDialog");
    if (!dialog.open) dialog.showModal();
  });
  byId("closeScanDetailsButton").addEventListener("click", () => byId("scanDialog").close());
  byId("scanDialog").addEventListener("cancel", event => {
    event.preventDefault();
    if (!byId("compactScanStatus").hidden) byId("scanDialog").close();
    else cancelActiveScan();
  });
}
