import assert from "node:assert/strict";
import test from "node:test";
import { deferred, loadUI } from "./helpers/ui.mjs";

async function harness(snapshot) {
  const elements = new Map();
  const element = id => {
    if (!elements.has(id)) elements.set(id, {
      hidden: true, textContent: "", value: "test-folder", handlers: {},
      style: { setProperty() {} }, setAttribute() {},
      addEventListener(name, fn) { this.handlers[name] = fn; },
      showModal() { this.open = true; }, close() { this.open = false; },
    });
    return elements.get(id);
  };
  const state = { navSession: 0 };
  let resolveScan, rejectScan;
  const scan = new Promise((resolve, reject) => { resolveScan = resolve; rejectScan = reject; });
  let started;
  const scanning = new Promise(resolve => { started = resolve; });
  const errors = [];
  let resizes = 0;
  let scans = 0, cancellations = 0, nextTimer = 0;
  const timers = new Map(), intervals = new Set();
  const controls = [{ disabled: false }, { disabled: false }];
  const backend = {
    CancelScan: async () => { cancellations++; rejectScan(new Error("scan cancelled")); },
    GetFullTree: () => { scans++; started(); return scan; },
    GetScanProgress: async () => ({}), LoadScanSnapshot: async () => snapshot,
    OpenPath() {}, ValidateScanPath: async path => path,
  };
  const modules = {
    "./wailsjs/go/main/App.js": Object.fromEntries(Object.keys(backend).map(key => [key, (...args) => backend[key](...args)])),
    "./dom.js": { byId: element, query: element, queryAll: () => controls },
    "./format.js": { formatCount: String, formatDuration: String },
    "./navigation.js": { replaceBrowserHistoryEntry() {}, updateNavButtons() {} },
    "./notifications.js": { hideRectToast() {}, showErrorToast: error => errors.push(error) },
    "./logging.js": { logDebug() {}, logError() {} },
    "./locations.js": { hideLocationSelector() {}, showLocationSelector() {} },
    "./state.js": { AppState: state },
  };
  const ui = await loadUI("scan.js", modules, {
    // Tests explicitly trigger progress polls; the completion paint delay
    // resolves on a microtask, avoiding wall-clock sleeps.
    setTimeout(fn, delay) {
      const id = ++nextTimer;
      if (delay === 180) queueMicrotask(fn);
      else timers.set(id, fn);
      return id;
    },
    clearTimeout: id => timers.delete(id),
    setInterval() { const id = ++nextTimer; intervals.add(id); return id; },
    clearInterval: id => intervals.delete(id),
  });
  ui.initScan({ redraw: async () => {}, hideContextMenu() {}, resizeCanvas() { resizes++; } });
  return { element, state, errors, scanning, resolveScan, rejectScan,
    analyze: ui.analyze, resizes: () => resizes, backend, controls, timers, intervals,
    scans: () => scans, cancellations: () => cancellations,
    poll() { const [id, callback] = timers.entries().next().value; timers.delete(id); return callback(); } };
}

const snapshot = { rootId: 7, fileCount: 2, dirCount: 1, snapshotAgeMilliseconds: 86400000 };

test("cancelled snapshot verification remains visibly cached", async () => {
  const h = await harness(snapshot);
  const run = h.analyze();
  await h.scanning;
  const banner = h.element("cachedScanStatus");
  assert.equal(banner.hidden, false);
  assert.match(banner.textContent, /Cached results.*Verifying.*Snapshot from/);
  const timestamp = banner.textContent.split("Snapshot from")[1];
  await h.element("cancelScanButton").handlers.click();
  await run;
  assert.equal(h.state.node_id, snapshot.rootId);
  assert.equal(banner.hidden, false);
  assert.match(banner.textContent, /Verification cancelled/);
  assert.equal(banner.textContent.split("Snapshot from")[1], timestamp);
  assert.equal(h.errors.length, 0);
});

test("failed verification retains the cached warning", async () => {
  const h = await harness(snapshot);
  const run = h.analyze();
  await h.scanning;
  h.rejectScan(new Error("filesystem unavailable"));
  await run;
  assert.equal(h.element("cachedScanStatus").hidden, false);
  assert.match(h.element("cachedScanStatus").textContent, /Verification failed/);
  assert.equal(h.errors.length, 1);
});

test("successful verification removes the warning and resizes the treemap", async () => {
  const h = await harness(snapshot);
  const run = h.analyze();
  await h.scanning;
  h.resolveScan({ rootId: 9, fileCount: 3, dirCount: 1 });
  await run;
  assert.equal(h.element("cachedScanStatus").hidden, true);
  assert.equal(h.element("cachedScanStatus").textContent, "");
  assert.equal(h.state.node_id, 9);
  assert.equal(h.resizes(), 2);
});

test("cancellation without a snapshot does not claim cached results exist", async () => {
  const h = await harness({ rootId: -1 });
  const run = h.analyze();
  await h.scanning;
  await h.element("cancelScanButton").handlers.click();
  await run;
  assert.equal(h.element("cachedScanStatus").hidden, true);
  assert.equal(h.state.node_id, null);
});

test("duplicate scan requests are ignored and cancellation cleans up timers and controls", async () => {
  const h = await harness({ rootId: -1 });
  const run = h.analyze();
  await h.scanning;
  assert.ok(h.controls.every(button => button.disabled));
  await h.analyze();
  assert.equal(h.scans(), 1);
  const cancel = h.element("cancelScanButton").handlers.click;
  await Promise.all([cancel(), cancel()]);
  await run;
  assert.equal(h.cancellations(), 1);
  assert.equal(h.element("scanDialog").open, false);
  assert.ok(h.controls.every(button => !button.disabled));
  assert.equal(h.timers.size, 0);
  assert.equal(h.intervals.size, 0);
});

test("validation failure preserves the displayed tree and never starts scanning", async () => {
  const h = await harness(snapshot);
  h.state.node_id = 22;
  h.backend.ValidateScanPath = async () => { throw new Error("invalid folder"); };
  await h.analyze();
  assert.equal(h.state.node_id, 22);
  assert.equal(h.scans(), 0);
  assert.match(String(h.errors[0]), /invalid folder/);
  assert.ok(h.controls.every(button => !button.disabled));
});

test("late progress responses cannot overwrite completed scan counts", async () => {
  const h = await harness({ rootId: -1 });
  const progress = deferred();
  h.backend.GetScanProgress = () => progress.promise;
  const run = h.analyze();
  await h.scanning;
  const poll = h.poll();
  h.resolveScan({ rootId: 3, fileCount: 100, dirCount: 20 });
  await run;
  progress.resolve({ fileCount: 4, dirCount: 2, path: "old-path", fraction: 0.1 });
  await poll;
  assert.equal(h.element("scanFileCount").textContent, "100");
  assert.equal(h.element("scanFolderCount").textContent, "20");
  assert.equal(h.timers.size, 0);
});

test("a cancelled scan's pending progress cannot update the next scan", async () => {
  const h = await harness({ rootId: -1 });
  const progress = deferred();
  h.backend.GetScanProgress = () => progress.promise;
  const first = h.analyze();
  await h.scanning;
  const poll = h.poll();
  await h.element("cancelScanButton").handlers.click();
  await first;

  const nextScan = deferred(), nextStarted = deferred();
  h.backend.GetFullTree = () => { nextStarted.resolve(); return nextScan.promise; };
  h.element("pathInput").value = "new-folder";
  const second = h.analyze();
  await nextStarted.promise;
  progress.resolve({ path: "old-folder", fileCount: 999, dirCount: 99, fraction: 0.9 });
  await poll;
  const displayedPath = h.element("scanCurrentPath").textContent;
  const displayedFiles = h.element("scanFileCount").textContent;
  nextScan.resolve({ rootId: 4, fileCount: 50, dirCount: 5 });
  await second;
  assert.equal(displayedPath, "new-folder");
  assert.equal(displayedFiles, "0");
  assert.equal(h.timers.size, 0);
});
