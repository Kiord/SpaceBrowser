import { CancelScan, GetFullTree, GetScanProgress, LoadScanSnapshot, OpenPath, ValidateScanPath } from "./wailsjs/go/main/App.js";
import { byId, query, queryAll } from "./dom.js";
import { formatCount, formatDuration } from "./format.js";
import { replaceBrowserHistoryEntry, updateNavButtons } from "./navigation.js";
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
  query(".scan-progress-bar").style.setProperty("--scan-progress", `${percentage}%`);
  query(".scan-progress").setAttribute("aria-valuenow", String(Math.round(percentage)));
}

function setUIBusy(state) {
  queryAll(".controls button").forEach(button => { button.disabled = state; });
}

function clearTreemapForScan() {
  hideLocationSelector();
  hideRectToast();
  for (const context of [AppState.colorCtx, AppState.idCtx, AppState.tmpCtx, AppState.maskCtx]) {
    if (context) context.clearRect(0, 0, context.canvas.width, context.canvas.height);
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

function startScanProgress(path) {
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
  if (!dialog.open) dialog.showModal();

  const token = ++scanProgressToken;
  const poll = async () => {
    if (token !== scanProgressToken) return;
    try {
      const progress = await GetScanProgress();
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
}

function stopScanProgress() {
  scanProgressToken++;
  clearTimeout(scanProgressTimer);
  scanProgressTimer = null;
  clearInterval(scanDotsTimer);
  scanDotsTimer = null;
  const dialog = byId("scanDialog");
  if (dialog.open) dialog.close();
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

export async function analyze() {
  const path = byId("pathInput").value?.trim();
  if (!path) return;
  if (analyzeInFlight) {
    logDebug("analyze ignored: a scan request is already active");
    return;
  }

  analyzeInFlight = true;
  let scanStarted = false;
  setUIBusy(true);
  try {
    const canonicalPath = await ValidateScanPath(path);
    byId("pathInput").value = canonicalPath;
    clearScanWarning();
    clearTreemapForScan();
		try {
      const snapshot = await LoadScanSnapshot(canonicalPath);
      if (Number(snapshot?.rootId) >= 0) {
        AppState.node_id = snapshot.rootId;
        AppState.scanRootPath = canonicalPath;
        AppState.navHistory = [snapshot.rootId];
        AppState.fileCount = snapshot.fileCount;
        AppState.dirCount = snapshot.dirCount;
        AppState.navIndex = 0;
        replaceBrowserHistoryEntry(snapshot.rootId, 0);
        await redraw();
				logDebug(`loaded cached scan snapshot (${snapshot.snapshotAgeMilliseconds || 0} ms old)`);
      }
    } catch (error) {
      logDebug("scan snapshot unavailable:", error);
    }
		startScanProgress(canonicalPath);
    scanStarted = true;

    const { rootId, fileCount, dirCount, scanReport } = await GetFullTree(canonicalPath);
    await completeScanProgress(fileCount, dirCount);
    scanStarted = false;

    AppState.node_id = rootId;
    AppState.scanRootPath = canonicalPath;
    AppState.navHistory = [rootId];
    AppState.fileCount = fileCount;
    AppState.dirCount = dirCount;
    AppState.navIndex = 0;
    replaceBrowserHistoryEntry(rootId, 0);
    AppState.selectedRectIndex = null;
    AppState.selectedNodeId = null;
    showScanWarning(scanReport);
    await redraw();
  } catch (error) {
    logError("analyze failed:", error);
    if (scanStarted) stopScanProgress();
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

export function initScan(options) {
  redraw = options.redraw;
  hideContextMenu = options.hideContextMenu;
  byId("analyzeButton").addEventListener("click", analyze);
  byId("viewScanReportButton").addEventListener("click", openScanReport);
  byId("cancelScanButton").addEventListener("click", cancelActiveScan);
  byId("scanDialog").addEventListener("cancel", event => {
    event.preventDefault();
    cancelActiveScan();
  });
}
