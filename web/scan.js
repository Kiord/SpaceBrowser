import { CancelScan, GetFullTree, GetScanProgress, ValidateScanPath } from "./wailsjs/go/main/App.js";
import { byId, query, queryAll } from "./dom.js";
import { formatDuration } from "./format.js";
import { replaceBrowserHistoryEntry, updateNavButtons } from "./navigation.js";
import { hideRectToast, showErrorToast } from "./notifications.js";
import { logDebug, logError } from "./logging.js";
import { AppState } from "./state.js";

let redraw = async () => {};
let hideContextMenu = () => {};
let scanProgressTimer = null;
let scanProgressToken = 0;
let scanCancelledByUser = false;
let scanDotsTimer = null;
let analyzeInFlight = false;

function setUIBusy(state) {
  queryAll(".controls button").forEach(button => { button.disabled = state; });
}

function clearTreemapForScan() {
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

function startScanProgress(path) {
  const dialog = byId("scanDialog");
  const cancelButton = byId("cancelScanButton");
  const progressElement = query(".scan-progress");
  const progressBar = query(".scan-progress-bar");
  const dotsElement = byId("scanningDots");
  byId("scanQueryPath").textContent = path;
  byId("scanQueryPath").title = path;
  byId("scanCurrentPath").textContent = path;
  progressBar.style.width = "0%";
  progressElement.classList.add("is-indeterminate");
  progressElement.removeAttribute("aria-valuenow");
  byId("scanElapsedTime").textContent = "0:00";
  byId("scanEstimate").textContent = "Estimating remaining time…";
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
      if (progress?.determinate) {
        const fraction = Math.max(0, Math.min(1, Number(progress.fraction || 0)));
        progressElement.classList.remove("is-indeterminate");
        progressBar.style.width = `${fraction * 100}%`;
        progressElement.setAttribute("aria-valuemin", "0");
        progressElement.setAttribute("aria-valuemax", "100");
        progressElement.setAttribute("aria-valuenow", String(Math.round(fraction * 100)));
      } else {
        progressElement.classList.add("is-indeterminate");
        progressElement.removeAttribute("aria-valuenow");
      }
      byId("scanElapsedTime").textContent = formatDuration(progress?.elapsedMilliseconds || 0);
      byId("scanEstimate").textContent = !progress?.determinate
        ? "Remaining time unavailable for folders"
        : progress.remainingMilliseconds >= 0
          ? `Remaining ~${formatDuration(progress.remainingMilliseconds, true)}`
          : "";
    } catch (error) {
      logDebug("scan progress unavailable:", error);
    } finally {
      if (token === scanProgressToken) scanProgressTimer = setTimeout(poll, 120);
    }
  };
  scanProgressTimer = setTimeout(poll, 120);
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
  button.textContent = "Cancelling…";
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
    clearTreemapForScan();
    startScanProgress(canonicalPath);
    scanStarted = true;

    const { rootId, fileCount, dirCount } = await GetFullTree(canonicalPath);
    stopScanProgress();
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
  }
}

export function initScan(options) {
  redraw = options.redraw;
  hideContextMenu = options.hideContextMenu;
  byId("analyzeButton").addEventListener("click", analyze);
  byId("cancelScanButton").addEventListener("click", cancelActiveScan);
  byId("scanDialog").addEventListener("cancel", event => {
    event.preventDefault();
    cancelActiveScan();
  });
}
