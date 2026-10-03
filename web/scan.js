import { QueueScan, QueueFolderRefresh, GetScanJobs, SelectScanJob, GetScanJobView, SetScanJobPaused, CancelScanJob, OpenPath, ValidateScanPath } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { AppState } from "./state.js";
import { clearSelection, getSelectedRects, isPassiveRect, reduceDeletionTargets, selectionIds } from "./selection.js";
import { pushBrowserHistoryEntry, remapNavigation, updateNavButtons } from "./navigation.js";
import { addControlEventListeners, eventMatchesShortcut, shortcutCanRun } from "./controls.js";
import { hideRectToast, showErrorToast } from "./notifications.js";
import { logError } from "./logging.js";
import { hideLocationSelector, showLocationSelector, setLocationScanJobs } from "./locations.js";
import { pendingScan, updateScanStatus, createTileScanStatus } from "./scan-status.js";

let redraw = async () => {};
let repaintScanLabels = () => {};
let hideContextMenu = () => {};
let scanReportPath = "";
let jobs = [];
let selectedID = null;
let selectedKey = "";
let selectionToken = 0;
let nextSession = 0;
let pollTimer = null;
let polling = false;
let queueRequest = false;
let toolbar;
const views = new Map();
const previousViews = new Map();
const reportedFailures = new Set();
const pathKey = path => /^[a-z]:|^\\\\/i.test(path) ? path.replaceAll("\\", "/").toLowerCase().replace(/\/+$/, "") : path.replace(/\/+$/, "") || "/";

const captureView = () => ({ node_id: AppState.node_id, rects: AppState.rects, scanRootPath: AppState.scanRootPath,
  navHistory: [...AppState.navHistory], navIndex: AppState.navIndex, selectedNodeIds: new Set(selectionIds()),
  fileCount: AppState.fileCount, dirCount: AppState.dirCount, jobID: selectedID, homeVisible: AppState.homeVisible });

function clearView() {
  AppState.node_id = null;
  AppState.rects = [];
  AppState.navHistory = [];
  AppState.navIndex = -1;
  clearSelection();
  for (const name of ["color", "hover", "id", "tmp", "mask", "flash"]) {
    const ctx = AppState[`${name}Ctx`];
    if (ctx) ctx.clearRect(0, 0, ctx.canvas.width, ctx.canvas.height);
  }
}

function applyJobState(job) {
  AppState.liveScanPreview = !!job && pendingScan(job) && !job.partial;
  AppState.scanPaused = job?.state === "paused" || job?.state === "queued";
  if (AppState.liveScanPreview) {
    AppState.fileCount = job.progress?.fileCount || 0;
    AppState.dirCount = job.progress?.dirCount || 0;
    if (!AppState.scanPaused) AppState.scanDots = ".".repeat(Math.floor(performance.now() / 350) % 3 + 1);
    repaintScanLabels();
  }
}

export function updateScanVisibility() {
  if (!toolbar) return;
  byId("topbar").setAttribute("data-home", String(!!AppState.homeVisible));
  const job = jobs.find(item => item.id === selectedID);
  if (job) updateScanStatus(toolbar, job);
  toolbar.container.hidden = !!AppState.homeVisible || !job || job.state === "cancelled";
}

async function changePause(job) {
  try { await SetScanJobPaused(job.id, job.state !== "paused"); await pollJobs(); }
  catch (error) { showErrorToast(error); }
}

async function cancelJob(job) {
  try { await CancelScanJob(job.id); await pollJobs(); }
  catch (error) { showErrorToast(error); }
}

function presentJobs() {
  AppState.scanInProgress = jobs.some(pendingScan);
  setLocationScanJobs(jobs, () => createTileScanStatus(changePause, cancelJob));
  applyJobState(jobs.find(job => job.id === selectedID));
  updateScanVisibility();
  updateNavButtons();
}

async function applyView(view, token) {
  if (!view || token !== selectionToken || view.job.id !== selectedID) return;
  if (view.key === selectedKey) return;
  selectedKey = view.key;
  if (view.restored) {
    const previous = previousViews.get(selectedID);
    if (previous?.jobID != null && previous.jobID !== selectedID) {
      await selectJob(previous.jobID, !AppState.homeVisible);
      return;
    }
    clearView();
    if (previous && view.tree) Object.assign(AppState, previous);
    AppState.liveScanPreview = false;
    if (AppState.node_id == null) showLocationSelector();
    else await redraw();
    updateNavButtons();
    return;
  }
  if (!view.tree) return;
  if (AppState.node_id == null) {
    AppState.node_id = view.tree.rootId;
    AppState.navHistory = [view.tree.rootId];
    AppState.navIndex = 0;
    pushBrowserHistoryEntry(view.tree.rootId, 0);
  }
  if (view.job.state === "completed") {
    if (view.nodeIds) {
      remapNavigation(view.nodeIds, view.tree.rootId);
      AppState.selectedNodeIds = new Set([...selectionIds()].map(id => view.nodeIds[id] ?? id).filter(id => id >= 0));
    }
    AppState.fileCount = view.tree.fileCount;
    AppState.dirCount = view.tree.dirCount;
    showScanWarning(view.job.report);
  }
  applyJobState(view.job);
  await redraw();
}

async function selectJob(id, reveal = true) {
  if (selectedID != null && selectedID !== id) views.set(selectedID, captureView());
  const changing = selectedID !== id;
  selectedID = id;
  selectedKey = "";
  const token = ++selectionToken;
  hideRectToast(); hideContextMenu();
  const view = await SelectScanJob(id);
  if (token !== selectionToken) return;
  if (changing) {
    clearView();
    const saved = views.get(id) || (view?.job.partial ? previousViews.get(id) : null);
    if (saved) {
      const { jobID, homeVisible, ...state } = saved;
      Object.assign(AppState, state);
    }
    nextSession = Math.max(nextSession, AppState.navSession || 0) + 1;
    AppState.navSession = nextSession;
    if (AppState.node_id != null) pushBrowserHistoryEntry(AppState.node_id, AppState.navIndex);
  }
  if (view) {
    AppState.scanRootPath = view.job.path;
    byId("pathInput").value = view.job.path;
    clearScanWarning();
  }
  if (reveal) hideLocationSelector();
  presentJobs();
  await applyView(view, token);
  updateScanVisibility();
}

async function pollJobs() {
  if (polling) return;
  polling = true;
  clearTimeout(pollTimer);
  try {
    jobs = await GetScanJobs();
    presentJobs();
    const token = selectionToken;
    if (selectedID != null) await applyView(await GetScanJobView(selectedID), token);
    for (const job of jobs) {
      if (job.state === "failed" && !reportedFailures.has(job.id)) {
        reportedFailures.add(job.id); showErrorToast(job.error);
      }
    }
  } catch (error) { logError("scan queue update failed:", error); }
  finally {
    polling = false;
    if (jobs.some(pendingScan)) pollTimer = setTimeout(pollJobs, 250);
  }
}

async function enqueue(request) {
  if (queueRequest) return;
  queueRequest = true;
  const previous = captureView();
  try {
    const job = await request();
    if (!previousViews.has(job.id)) previousViews.set(job.id, previous);
    jobs = await GetScanJobs();
    presentJobs();
    // Keep Home visible when adding another scan there. Its tile owns progress.
    await selectJob(job.id, !previous.homeVisible);
    await pollJobs();
  } catch (error) { showErrorToast(error); }
  finally { queueRequest = false; }
}

export async function analyze() {
  const path = byId("pathInput").value?.trim();
  if (path) await enqueue(() => QueueScan(path));
}

export async function openLocation() {
  try {
    const path = await ValidateScanPath(byId("pathInput").value?.trim());
    jobs = await GetScanJobs();
    const related = jobs.filter(job => pathKey(job.path) === pathKey(path));
    const match = [...related].reverse().find(job => job.state !== "cancelled" && job.state !== "failed");
    if (match) { await selectJob(match.id); await pollJobs(); return; }
    hideLocationSelector();
    byId("pathInput").value = path;
    await analyze();
  } catch (error) { showErrorToast(error); }
}

export async function refreshSelectedFolders() {
  const selected = getSelectedRects();
  if (!selected.length || selected.some(rect => !rect.is_folder || !rect.full_path || isPassiveRect(rect))) return;
  const targets = reduceDeletionTargets(selected).map(rect => ({ nodeId: rect.node_id, path: rect.full_path }));
  const tracked = [...new Set([...selectionIds(), ...AppState.navHistory, AppState.node_id])];
  await enqueue(() => QueueFolderRefresh(targets, tracked));
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

export function initScan(options) {
  redraw = options.redraw;
  repaintScanLabels = options.repaintScanLabels || (() => {});
  hideContextMenu = options.hideContextMenu;
  toolbar = { container: byId("compactScanStatus"), time: byId("compactScanTime"), percent: byId("compactScanPercent"),
    bar: byId("compactScanBar"), progress: byId("compactScanProgress"), actions: byId("compactScanActions"),
    pause: byId("pauseScanButton"), icon: byId("pauseScanIcon") };
  byId("analyzeButton").addEventListener("click", analyze);
  byId("pauseScanButton").addEventListener("click", () => {
    const job = jobs.find(item => item.id === selectedID); if (job) return changePause(job);
  });
  byId("compactCancelScanButton").addEventListener("click", () => {
    const job = jobs.find(item => item.id === selectedID); if (job) return cancelJob(job);
  });
  byId("viewScanReportButton").addEventListener("click", openScanReport);
  addControlEventListeners(event => {
    if (!shortcutCanRun(event) || !eventMatchesShortcut(event, AppState.profile?.controls?.refresh)) return;
    event.preventDefault(); refreshSelectedFolders();
  });
  updateScanVisibility();
}
