import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const source = await readFile(new URL("../web/scan.js", import.meta.url), "utf8");

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
  const modules = {
    "./wailsjs/go/main/App.js": {
      CancelScan: async () => rejectScan(new Error("scan cancelled")),
      GetFullTree: () => { started(); return scan; },
      GetScanProgress: async () => ({}), LoadScanSnapshot: async () => snapshot,
      OpenPath() {}, ValidateScanPath: async path => path,
    },
    "./dom.js": { byId: element, query: element, queryAll: () => [] },
    "./format.js": { formatCount: String, formatDuration: String },
    "./navigation.js": { replaceBrowserHistoryEntry() {}, updateNavButtons() {} },
    "./notifications.js": { hideRectToast() {}, showErrorToast: error => errors.push(error) },
    "./logging.js": { logDebug() {}, logError() {} },
    "./locations.js": { hideLocationSelector() {}, showLocationSelector() {} },
    "./state.js": { AppState: state },
  };
  const context = vm.createContext({
    // Progress polling is irrelevant here; completeScanProgress still awaits
    // its paint delay, so resolve that callback without waiting in real time.
    setTimeout(fn, delay) { if (delay === 180) queueMicrotask(fn); return 1; },
    clearTimeout() {}, setInterval() { return 1; }, clearInterval() {},
  });
  const module = new vm.SourceTextModule(source, { context });
  await module.link(name => new vm.SyntheticModule(Object.keys(modules[name]), function () {
    for (const [key, value] of Object.entries(modules[name])) this.setExport(key, value);
  }, { context }));
  await module.evaluate();
  module.namespace.initScan({ redraw: async () => {}, hideContextMenu() {}, resizeCanvas() { resizes++; } });
  return { element, state, errors, scanning, resolveScan, rejectScan,
    analyze: module.namespace.analyze, resizes: () => resizes };
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
