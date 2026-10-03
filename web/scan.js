import { clearSelection, getSelectedRects, isPassiveRect, reduceDeletionTargets, selectionIds } from "./selection.js";
import { CancelScan, SetScanPaused, OpenScanSnapshot, GetFullTree, GetScanProgress, GetScanPreview, OpenPath, ValidateScanPath, RefreshFolders } from "./wailsjs/go/main/App.js";
import { byId, query, queryAll } from "./dom.js";
import { formatCount, formatDuration } from "./format.js";
import { remapNavigation, pushBrowserHistoryEntry, rollbackBrowserHistory, updateNavButtons } from "./navigation.js";
import { addControlEventListeners, eventMatchesShortcut, shortcutCanRun } from "./controls.js";
import { hideRectToast, showErrorToast } from "./notifications.js";
import { logDebug, logError } from "./logging.js";
import { hideLocationSelector, showLocationSelector } from "./locations.js";
import { AppState } from "./state.js";

let redraw = async () => {};
let repaintScanLabels = () => {};
let scanStartedAt = 0;
let scanCompleted = false;
let activeScanGeneration = null;
let pauseRequestPending = false;
let pauseRevision = 0;
let paused = false;
let pauseStartedAt = 0;
let pausedMilliseconds = 0;
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

// Legacy scanDialog readouts remain synchronized for reference only; the dialog has no opener.
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
  queryAll("#analyzeButton, #triggerFolderSelectButton").forEach(button => { button.disabled = state; });
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
  if (dialog.open) dialog.close();
  scanStartedAt = performance.now();
  pausedMilliseconds = 0;
  pauseStartedAt = 0;
  scanCompleted = false;
  activeScanGeneration = null;
  paused = false;
  pauseRequestPending = false;
  AppState.scanPaused = false;
  byId("compactScanActions").hidden = false;
  byId("compactCancelScanButton").disabled = false;
  byId("compactCancelScanButton").setAttribute("aria-label", "Cancel scan");
  scanCancelledByUser = false;
  renderPauseButton();
  byId("compactScanStatus").setAttribute("data-complete", "false");
  byId("compactScanPercent").hidden = false;
  byId("scanPhase").textContent = "Scanning ";
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
  cancelButton.hidden = false;
  cancelButton.disabled = false;
  cancelButton.textContent = "Cancel Scan";
  scanCancelledByUser = false;
  let dotCount = 1;
  dotsElement.textContent = ".";
  clearInterval(scanDotsTimer);
  scanDotsTimer = setInterval(() => {
    if (paused) return;
    dotCount = dotCount % 3 + 1;
    dotsElement.textContent = ".".repeat(dotCount);
    AppState.scanDots = dotsElement.textContent;
    repaintScanLabels();
  }, 350);
  AppState.scanDots = ".";
  byId("compactScanStatus").hidden = false;
  byId("compactScanTime").textContent = "0:00";
  byId("closeScanDetailsButton").hidden = false;
  AppState.liveScanPreview = live;

  const token = ++scanProgressToken;
  let previewRevision = 0;
  const poll = async () => {
    if (token !== scanProgressToken) return;
    try {
      const requestedPauseRevision = pauseRevision;
      const progress = await GetScanProgress();
      // Completion, cancellation, or a new scan may have invalidated this
      // request while the backend response was in flight.
      if (token !== scanProgressToken) return;
      if (progress?.active && Number.isFinite(progress.generation)) activeScanGeneration = progress.generation;
      if (!pauseRequestPending && requestedPauseRevision === pauseRevision) setPausedState(!!progress?.paused);
      AppState.scanPaused = paused;
      renderPauseButton();
      if (live) {
        AppState.fileCount = progress?.fileCount ?? AppState.fileCount;
        AppState.dirCount = progress?.dirCount ?? AppState.dirCount;
        repaintScanLabels();
      }
      if (progress?.path) byId("scanCurrentPath").textContent = progress.path;
      const workFraction = Math.max(0, Math.min(1, Number(progress?.fraction || 0)));
      const target = workFraction * SCAN_PROGRESS_CAP;
      if (!paused && target > displayedScanProgress) {
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
  scanCompleted = true;
  byId("compactScanActions").hidden = true;
  AppState.scanPaused = false;
  AppState.liveScanPreview = false;
  setPausedState(false);
  const elapsed = performance.now() - scanStartedAt - pausedMilliseconds;
  byId("scanElapsedTime").textContent = formatDuration(elapsed);
  byId("compactScanTime").textContent = `Done in ${(elapsed / 1000).toFixed(1)}s`;
  byId("compactScanPercent").hidden = true;
  byId("compactScanStatus").setAttribute("data-complete", "true");
  byId("scanPhase").textContent = "Scanned ";
  byId("scanningDots").textContent = "";
  byId("cancelScanButton").hidden = true;
  await new Promise(resolve => setTimeout(resolve, SCAN_COMPLETION_DELAY_MS));
  const dialog = byId("scanDialog");
  if (dialog.open) dialog.close();
  byId("compactScanStatus").hidden = !scanCompleted;
}

function stopScanProgress() {
  AppState.scanPaused = false;
  byId("compactScanActions").hidden = true;
  scanProgressToken++;
  clearTimeout(scanProgressTimer);
  scanProgressTimer = null;
  clearInterval(scanDotsTimer);
  scanDotsTimer = null;
  const dialog = byId("scanDialog");
  if (dialog.open) dialog.close();
  byId("compactScanStatus").hidden = !scanCompleted;
}

async function cancelActiveScan() {
  if (scanCancelledByUser || scanCompleted) return;
  scanCancelledByUser = true;
  const button = byId("compactCancelScanButton");
  button.disabled = true;
  button.setAttribute("aria-label", "Cancelling scan");
  byId("pauseScanButton").disabled = true;
  try {
    await CancelScan();
  } catch (error) {
    logError("cancelling scan failed:", error);
  }
}

function setPausedState(nextPaused) {
  if (paused === nextPaused) return;
  const now = performance.now();
  if (nextPaused) pauseStartedAt = now;
  else pausedMilliseconds += now - pauseStartedAt;
  paused = nextPaused;
}

function renderPauseButton() {
  const button = byId("pauseScanButton");
  button.disabled = activeScanGeneration == null || pauseRequestPending || scanCancelledByUser;
  button.setAttribute("aria-label", paused ? "Continue scan" : "Pause scan");
  button.setAttribute("data-tooltip", paused ? "Continue scan" : "Pause scan");
  button.setAttribute("aria-pressed", String(paused));
  byId("pauseScanIcon").setAttribute("d", paused ? "M8 4l12 8-12 8Z" : "M8 5v14M16 5v14");
}

async function toggleScanPause() {
  if (activeScanGeneration == null || pauseRequestPending || scanCompleted || scanCancelledByUser) return;
  const token = scanProgressToken;
  pauseRequestPending = true;
  pauseRevision++;
  renderPauseButton();
  try {
    const result = await SetScanPaused(activeScanGeneration, !paused);
    if (token !== scanProgressToken) return;
    setPausedState(!!result);
    AppState.scanPaused = paused;
    repaintScanLabels();
  } catch (error) {
    if (token === scanProgressToken) showErrorToast(error);
  } finally {
    if (token === scanProgressToken) { pauseRequestPending = false; renderPauseButton(); }
  }
}

function initializeScanView(rootId) {
  if (AppState.node_id != null) return;
  AppState.node_id = rootId;
  AppState.navHistory = [rootId];
  AppState.navIndex = 0;
  pushBrowserHistoryEntry(rootId, 0);
}

export async function openLocation() {
  const path = byId("pathInput").value?.trim();
  if (!path) return;
  if (analyzeInFlight) {
    try {
      const canonicalPath = await ValidateScanPath(path);
      if (canonicalPath === AppState.scanRootPath) {
        hideLocationSelector();
        await redraw();
        updateNavButtons();
      } else {
        byId("pathInput").value = AppState.scanRootPath;
        showErrorToast("A scan is already running. Reopen its tile, or cancel it before scanning another location.");
      }
    } catch (error) { showErrorToast(error); }
    return;
  }
  analyzeInFlight = true;
  try {
    const canonicalPath = await ValidateScanPath(path);
    byId("pathInput").value = canonicalPath;
    if (canonicalPath === AppState.scanRootPath && AppState.node_id != null) {
      hideLocationSelector();
      await redraw();
      return;
    }
    const snapshot = await OpenScanSnapshot(canonicalPath);
    if (snapshot) {
      clearTreemapForScan();
      AppState.scanRootPath = canonicalPath;
      initializeScanView(snapshot.rootId);
      AppState.fileCount = snapshot.fileCount;
      AppState.dirCount = snapshot.dirCount;
      clearScanWarning();
      byId("compactScanStatus").hidden = true;
      await redraw();
      return;
    }
  } catch (error) {
    showErrorToast(error);
    return;
  } finally {
    analyzeInFlight = false;
    updateNavButtons();
  }
  await analyze();
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
  const previous = Object.fromEntries(["homeVisible", "node_id", "rects", "scanRootPath", "fileCount", "dirCount", "navHistory", "navIndex", "navSession"].map(key => [key, AppState[key]]));
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
      if (previous.homeVisible) showLocationSelector();
      rollbackBrowserHistory(previousPosition);
      byId("scanWarningIndicator").hidden = previousWarningHidden;
      if (AppState.node_id != null) await redraw();
    }
    const wasCancelled = scanCancelledByUser || /scan cancelled/i.test(String(error));
    if (!wasCancelled) showErrorToast(error);
  } finally {
    if (scanStarted) stopScanProgress();
    analyzeInFlight = false;
    AppState.liveScanPreview = false;
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
  repaintScanLabels = options.repaintScanLabels || (() => {});
  hideContextMenu = options.hideContextMenu;
  addControlEventListeners(event => {
    if (!shortcutCanRun(event) || !eventMatchesShortcut(event, AppState.profile?.controls?.refresh)) return;
    event.preventDefault();
    refreshSelectedFolders();
  });
  byId("analyzeButton").addEventListener("click", analyze);
  byId("viewScanReportButton").addEventListener("click", openScanReport);
  byId("compactCancelScanButton").addEventListener("click", cancelActiveScan);
  byId("pauseScanButton").addEventListener("click", toggleScanPause);
}
