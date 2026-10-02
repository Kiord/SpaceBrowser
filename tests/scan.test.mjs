import assert from "node:assert/strict";
import test from "node:test";
import { deferred, loadUI } from "./helpers/ui.mjs";

async function harness() {
  const elements = new Map();
  const element = id => {
    if (!elements.has(id)) elements.set(id, {
      hidden: true, open: false, textContent: "", value: "test-folder", handlers: {},
      attributes: {}, style: { setProperty() {} }, setAttribute(name, value) { this.attributes[name] = value; },
      addEventListener(name, fn) { this.handlers[name] = fn; },
      showModal() { this.open = true; }, close() { this.open = false; },
    });
    return elements.get(id);
  };
  const state = { node_id: null, rects: [], navSession: 0, browserHistoryPosition: 0, navHistory: [], navIndex: -1 };
  let resolveScan, rejectScan;
  const scan = new Promise((resolve, reject) => { resolveScan = resolve; rejectScan = reject; });
  let started;
  const scanning = new Promise(resolve => { started = resolve; });
  const errors = [];
  const shortcuts = [];
  let redraws = 0;
  let locationPrompts = 0;
  let scans = 0, cancellations = 0, nextTimer = 0;
  const timers = new Map(), intervals = new Set();
  const controls = [{ disabled: false }, { disabled: false }];
  const backend = {
    CancelScan: async () => { cancellations++; rejectScan(new Error("scan cancelled")); },
    GetFullTree: () => { scans++; started(); return scan; },
    RefreshFolders: () => { scans++; started(); return scan; },
    GetScanProgress: async () => ({}),
    GetScanPreview: async () => null,
    OpenPath() {}, ValidateScanPath: async path => path,
  };
  const modules = {
    "./wailsjs/go/main/App.js": Object.fromEntries(Object.keys(backend).map(key => [key, (...args) => backend[key](...args)])),
    "./dom.js": { byId: element, query: element, queryAll: () => controls },
    "./format.js": { formatCount: String, formatDuration: String },
    "./navigation.js": { pushBrowserHistoryEntry() { state.browserHistoryPosition++; }, rollbackBrowserHistory(position) { state.browserHistoryPosition = position; }, updateNavButtons() {}, remapNavigation(mapping) { state.node_id = mapping[state.node_id] ?? state.node_id; } },
    "./controls.js": { addControlEventListeners: fn => shortcuts.push(fn), eventMatchesShortcut: (event, binding) => event.binding === binding, shortcutCanRun: event => !event.blocked },
    "./notifications.js": { hideRectToast() {}, showErrorToast: error => errors.push(error) },
    "./logging.js": { logDebug() {}, logError() {} },
    "./locations.js": { hideLocationSelector() {}, showLocationSelector() { locationPrompts++; } },
    "./state.js": { AppState: state },
  };
  const ui = await loadUI("scan.js", modules, {
    performance: { now: () => 1500 },
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
  ui.initScan({ redraw: async () => { redraws++; }, hideContextMenu() {} });
  return { element, state, errors, scanning, resolveScan, rejectScan, shortcuts,
    analyze: ui.analyze, refresh: ui.refreshSelectedFolders, redraws: () => redraws, locationPrompts: () => locationPrompts, backend, controls, timers, intervals,
    scans: () => scans, cancellations: () => cancellations,
    poll() { const [id, callback] = timers.entries().next().value; timers.delete(id); return callback(); } };
}

test("starting a scan clears previous results and publishes only the completed tree", async () => {
  const h = await harness();
  Object.assign(h.state, { node_id: 7, scanRootPath: "previous-folder", fileCount: 99, dirCount: 8 });
  const run = h.analyze();
  await h.scanning;
  assert.equal(h.state.node_id, null);
  assert.equal(h.state.scanRootPath, "test-folder");
  assert.equal(h.state.fileCount, 0);
  assert.equal(h.state.dirCount, 0);
  assert.equal(h.redraws(), 0);
  h.resolveScan({ rootId: 9, fileCount: 3, dirCount: 1 });
  await run;
  assert.equal(h.state.node_id, 9);
  assert.equal(h.state.fileCount, 3);
  assert.equal(h.state.scanRootPath, "test-folder");
  assert.equal(h.redraws(), 1);
});

test("cancelled scans restore the previous view", async () => {
  const h = await harness();
  h.state.node_id = 7;
  const run = h.analyze();
  await h.scanning;
  await h.element("cancelScanButton").handlers.click();
  await run;
  assert.equal(h.state.node_id, 7);
  assert.equal(h.redraws(), 1);
  assert.equal(h.locationPrompts(), 0);
  assert.equal(h.errors.length, 0);
});

test("failed scans leave no stale tree and display the error", async () => {
  const h = await harness();
  const run = h.analyze();
  await h.scanning;
  h.rejectScan(new Error("filesystem unavailable"));
  await run;
  assert.equal(h.state.node_id, null);
  assert.equal(h.redraws(), 0);
  assert.equal(h.locationPrompts(), 1);
  assert.match(String(h.errors[0]), /filesystem unavailable/);
});

test("duplicate scan requests are ignored and cancellation cleans up timers and controls", async () => {
  const h = await harness();
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
  const h = await harness();
  h.state.node_id = 22;
  h.backend.ValidateScanPath = async () => { throw new Error("invalid folder"); };
  await h.analyze();
  assert.equal(h.state.node_id, 22);
  assert.equal(h.scans(), 0);
  assert.match(String(h.errors[0]), /invalid folder/);
  assert.ok(h.controls.every(button => !button.disabled));
});

test("late progress responses cannot overwrite completed scan counts", async () => {
  const h = await harness();
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
  const h = await harness();
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

function selectRefreshFolders(h) {
  Object.assign(h.state, {
    node_id: 0, scanRootPath: '/root', navHistory: [0, 2], navIndex: 0,
    fileCount: 10, dirCount: 4, selectedNodeIds: new Set([1, 2, 3]),
    rects: [
      { node_id: 1, full_path: '/root/a', is_folder: true },
      { node_id: 2, full_path: '/root/a/child', is_folder: true },
      { node_id: 3, full_path: '/root/b', is_folder: true },
    ],
  });
}

test('refresh batches folders, collapses descendants and remaps selection', async () => {
  const h = await harness();
  selectRefreshFolders(h);
  const previous = h.state.rects;
  const original = h.backend.RefreshFolders;
  let request;
  h.backend.RefreshFolders = (...args) => { request = args; return original(...args); };
  const run = h.refresh();
  await h.scanning;
  assert.equal(h.state.rects, previous);
  assert.equal(h.state.node_id, 0);
  assert.deepEqual(Array.from(request[0], target => ({ ...target })), [{ nodeId: 1, path: '/root/a' }, { nodeId: 3, path: '/root/b' }]);
  await h.refresh();
  await h.analyze();
  assert.equal(h.scans(), 1);
  h.resolveScan({ fileCount: 12, dirCount: 5, nodeIds: { 0: 0, 1: 1, 2: 7, 3: 3 } });
  await run;
  assert.deepEqual([...h.state.selectedNodeIds], [1, 7, 3]);
  assert.equal(h.state.scanRootPath, '/root');
  assert.equal(h.state.fileCount, 12);
  assert.equal(h.state.dirCount, 5);
  assert.equal(h.redraws(), 1);
  assert.equal(h.element('scanDialog').open, false);
});

for (const cancel of [true, false]) {
  test(`${cancel ? 'cancelled' : 'failed'} refresh keeps the existing tree and selection`, async () => {
    const h = await harness();
    selectRefreshFolders(h);
    const previous = h.state.rects;
    const run = h.refresh();
    await h.scanning;
    if (cancel) await h.element('cancelScanButton').handlers.click();
    else h.rejectScan(new Error('unavailable'));
    await run;
    assert.equal(h.state.rects, previous);
    assert.equal(h.state.node_id, 0);
    assert.equal(h.state.fileCount, 10);
    assert.deepEqual([...h.state.selectedNodeIds], [1, 2, 3]);
    assert.equal(h.redraws(), 0);
    assert.equal(h.errors.length, cancel ? 0 : 1);
    assert.equal(h.element('scanDialog').open, false);
    assert.equal(h.timers.size, 0);
    assert.equal(h.intervals.size, 0);
  });
}

test('refresh ignores files, mixed selections, and empty selection', async () => {
  const h = await harness();
  selectRefreshFolders(h);
  h.state.rects[1].is_folder = false;
  await h.refresh();
  h.state.selectedNodeIds = new Set([2]);
  await h.refresh();
  h.state.selectedNodeIds.clear();
  await h.refresh();
  assert.equal(h.scans(), 0);
});

test('refresh shortcut respects binding and editable/modal guards', async () => {
  const h = await harness();
  selectRefreshFolders(h);
  h.state.profile = { controls: { refresh: 'Ctrl+R' } };
  let prevented = 0;
  const event = { binding: 'Ctrl+R', preventDefault() { prevented++; } };
  h.shortcuts[0]({ ...event, blocked: true });
  h.shortcuts[0]({ ...event, binding: 'Alt+R' });
  assert.equal(h.scans(), 0);
  h.shortcuts[0](event);
  await h.scanning;
  assert.equal(prevented, 1);
  h.resolveScan({ fileCount: 10, dirCount: 4, nodeIds: {} });
  // Wait for the async shortcut action to finish.
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.redraws(), 1);
});

test('live previews redraw at new revisions and preserve browsing through completion', async () => {
  const h = await harness();
  h.backend.GetScanProgress = async () => ({ livePreview: true, rootPath: 'test-folder', generation: 1, fileCount: 3, dirCount: 2 });
  let revision = 1;
  h.backend.GetScanPreview = async () => ({ rootId: 0, revision, fileCount: 3, dirCount: 2 });
  const run = h.analyze();
  await h.scanning;
  assert.equal(h.element('scanDialog').open, false);
  assert.equal(h.element('compactScanStatus').hidden, false);
  await h.poll();
  assert.equal(h.state.node_id, 0);
  assert.equal(h.redraws(), 1);
  h.state.node_id = 2;
  h.state.navHistory = [0, 2];
  h.state.navIndex = 1;
  h.state.selectedNodeIds = new Set([3]);
  await h.poll();
  assert.equal(h.redraws(), 1);
  revision++;
  await h.poll();
  assert.equal(h.state.node_id, 2);
  assert.deepEqual([...h.state.selectedNodeIds], [3]);
  assert.equal(h.redraws(), 2);
  h.resolveScan({ rootId: 0, fileCount: 10, dirCount: 4 });
  await run;
  assert.equal(h.state.node_id, 2);
  assert.deepEqual([...h.state.selectedNodeIds], [3]);
  assert.deepEqual(h.state.navHistory, [0, 2]);
  assert.equal(h.state.fileCount, 10);
  assert.equal(h.element('compactScanStatus').hidden, false);
  assert.equal(h.state.liveScanPreview, false);
  assert.equal(h.state.scanInProgress, false);
});

test('cancelled live preview restores pre-scan navigation, selection and counts', async () => {
  const h = await harness();
  Object.assign(h.state, { node_id: 8, navHistory: [5, 8, 9], navIndex: 1, navSession: 4,
    browserHistoryPosition: 7, selectedNodeIds: new Set([11]), scanRootPath: 'old', fileCount: 100, dirCount: 8 });
  h.backend.GetScanProgress = async () => ({ livePreview: true, rootPath: 'test-folder', generation: 1 });
  h.backend.GetScanPreview = async () => ({ rootId: 0, revision: 1, fileCount: 1, dirCount: 1 });
  const run = h.analyze();
  await h.scanning;
  await h.poll();
  assert.equal(h.state.node_id, 0);
  h.state.navHistory = [0, 1]; h.state.node_id = 1; h.state.navIndex = 1;
  h.state.browserHistoryPosition++;
  await h.element('cancelScanButton').handlers.click();
  await run;
  assert.equal(h.state.node_id, 8);
  assert.equal(h.state.scanRootPath, 'old');
  assert.equal(h.state.navSession, 4);
  assert.equal(h.state.browserHistoryPosition, 7);
  assert.deepEqual(h.state.navHistory, [5, 8, 9]);
  assert.equal(h.state.navIndex, 1);
  assert.deepEqual([...h.state.selectedNodeIds], [11]);
  assert.equal(h.state.fileCount, 100);
  assert.equal(h.locationPrompts(), 0);
});

test('cancelling the first live scan returns home', async () => {
  const h = await harness();
  h.backend.GetScanProgress = async () => ({ livePreview: true, rootPath: 'test-folder', generation: 1 });
  h.backend.GetScanPreview = async () => ({ rootId: 0, revision: 1, fileCount: 1, dirCount: 1 });
  const run = h.analyze();
  await h.scanning;
  await h.poll();
  await h.element('cancelScanButton').handlers.click();
  await run;
  assert.equal(h.state.node_id, null);
  assert.equal(h.state.rects.length, 0);
  assert.equal(h.locationPrompts(), 1);
});

for (const cancel of [false, true]) {
  test(`late preview cannot overwrite ${cancel ? 'restored' : 'completed'} tree`, async () => {
    const h = await harness();
    const preview = deferred();
    h.backend.GetScanProgress = async () => ({ livePreview: true, rootPath: 'test-folder', generation: 1 });
    h.backend.GetScanPreview = () => preview.promise;
    const run = h.analyze();
    await h.scanning;
    const poll = h.poll();
    await Promise.resolve();
    if (cancel) await h.element('cancelScanButton').handlers.click();
    else h.resolveScan({ rootId: 4, fileCount: 80, dirCount: 10 });
    await run;
    const redraws = h.redraws();
    preview.resolve({ rootId: 0, revision: 1, fileCount: 1, dirCount: 1 });
    await poll;
    assert.equal(h.state.node_id, cancel ? null : 4);
    assert.equal(h.redraws(), redraws);
    assert.equal(h.timers.size, 0);
  });
}

test('details can expand and minimize without cancelling the scan', async () => {
  const h = await harness();
  const run = h.analyze();
  await h.scanning;
  h.element('scanDetailsButton').handlers.click();
  assert.equal(h.element('scanDialog').open, true);
  h.element('closeScanDetailsButton').handlers.click();
  assert.equal(h.element('scanDialog').open, false);
  h.element('scanDetailsButton').handlers.click();
  h.element('scanDialog').handlers.cancel({ preventDefault() {} });
  assert.equal(h.element('scanDialog').open, false);
  assert.equal(h.cancellations(), 0);
  h.resolveScan({ rootId: 0, fileCount: 1, dirCount: 1 });
  await run;
});

for (const partial of [false, true]) {
  test(`${partial ? 'partial' : 'full'} scan starts compact and retains completed status and details`, async () => {
    const h = await harness();
    if (partial) selectRefreshFolders(h);
    const run = partial ? h.refresh() : h.analyze();
    await h.scanning;
    assert.equal(h.element('scanDialog').open, false);
    assert.equal(h.element('compactScanStatus').hidden, false);
    assert.equal(h.element('compactScanStatus').handlers.click, undefined);
    assert.equal(h.element('compactScanStatus').attributes['data-complete'], 'false');
    h.element('scanDetailsButton').handlers.click();
    assert.equal(h.element('scanDialog').open, true);
    h.element('closeScanDetailsButton').handlers.click();
    assert.equal(h.cancellations(), 0);
    h.resolveScan({ rootId: 0, fileCount: 12, dirCount: 5, nodeIds: {} });
    await run;
    assert.equal(h.element('compactScanStatus').hidden, false);
    assert.equal(h.element('compactScanStatus').attributes['data-complete'], 'true');
    assert.match(h.element('compactScanTime').textContent, /^Done in [0-9.]+s$/);
    assert.equal(h.element('compactScanPercent').hidden, true);
    assert.equal(h.element('cancelScanButton').hidden, true);
    assert.equal(h.element('scanningDots').textContent, '');
    h.element('scanDetailsButton').handlers.click();
    assert.equal(h.element('scanDialog').open, true);
    assert.equal(h.element('scanPhase').textContent, 'Scanned ');
    assert.equal(h.element('scanFileCount').textContent, '12');
    assert.equal(h.timers.size, 0);
    assert.equal(h.intervals.size, 0);
  });
}
