import assert from "node:assert/strict";
import test from "node:test";
import { loadUI, deferred } from "./helpers/ui.mjs";

async function harness() {
  const elements = new Map();
  const element = id => {
    if (!elements.has(id)) elements.set(id, { hidden: true, value: '/one', textContent: '', handlers: {}, attributes: {},
      style: { setProperty() {} }, setAttribute(key, value) { this.attributes[key] = value; },
      addEventListener(name, fn) { this.handlers[name] = fn; } });
    return elements.get(id);
  };
  const state = { node_id: null, rects: [], navHistory: [], navIndex: -1, navSession: 0, homeVisible: false, selectedNodeIds: new Set() };
  const jobs = [], calls = [], errors = [], timers = new Map();
  let selected, nextTimer = 0, redraws = 0, labels = 0, tileJobs = [], refreshTile;
  const view = id => {
    const job = jobs.find(job => job.id === id);
    return { job: { ...job }, key: `${id}:${job.revision}:${job.state}`, tree: job.tree, restored: job.state === 'cancelled', nodeIds: job.nodeIds };
  };
  const backend = {
    QueueScan: async path => {
      calls.push(['queue', path]);
      for (const job of jobs) if (job.state === 'running') job.state = 'paused';
      const job = { id: jobs.length + 1, path, state: 'running', progress: {}, revision: 0 };
      jobs.push(job); return job;
    },
    QueueFolderRefresh: async (targets, tracked) => { calls.push(['refresh', targets, tracked]); const job = await backend.QueueScan(state.scanRootPath); job.partial = true; return job; },
    GetScanJobs: async () => jobs.map(job => ({ ...job })),
    SelectScanJob: async id => { selected = id; calls.push(['select', id]); return view(id); },
    GetScanJobView: async id => selected === id ? view(id) : null,
    SetScanJobPaused: async (id, paused) => { calls.push(['pause', id, paused]); jobs[id-1].state = paused ? 'paused' : 'queued'; },
    CancelScanJob: async id => { calls.push(['cancel', id]); jobs[id-1].state = 'cancelled'; },
    ValidateScanPath: async path => path,
    OpenPath: async path => calls.push(['open', path]),
  };
  const modules = {
    './wailsjs/go/main/App.js': Object.fromEntries(Object.keys(backend).map(key => [key, (...args) => backend[key](...args)])),
    './dom.js': { byId: element }, './state.js': { AppState: state },
    './navigation.js': { pushBrowserHistoryEntry() {}, updateNavButtons() {}, remapNavigation(mapping, fallback) { state.node_id = mapping[state.node_id] ?? fallback; } },
    './controls.js': { addControlEventListeners() {}, eventMatchesShortcut() {}, shortcutCanRun() {} },
    './notifications.js': { hideRectToast() {}, showErrorToast: error => errors.push(error) },
    './logging.js': { logError: (...args) => errors.push(args) },
    './locations.js': { hideLocationSelector() { state.homeVisible = false; }, showLocationSelector() { state.homeVisible = true; }, setLocationScanJobs(value, factory) { tileJobs = value; factory(); } },
    './scan-status.js': { pendingScan: job => ['running', 'queued', 'paused'].includes(job?.state), updateScanStatus(refs, job) { refs.container.hidden = job.state === 'cancelled'; }, createTileScanStatus(pause, cancel, refresh) { refreshTile = refresh; } },
  };
  const ui = await loadUI('scan.js', modules, { performance: { now: () => 1000 },
    setTimeout: fn => { timers.set(++nextTimer, fn); return nextTimer; }, clearTimeout: id => timers.delete(id) });
  ui.initScan({ redraw: async () => { redraws++; }, repaintScanLabels: () => { labels++; }, hideContextMenu() {} });
  return { ui, jobs, state, element, calls, errors, backend, tileJobs: () => tileJobs, redraws: () => redraws, labels: () => labels,
    refreshTile: job => refreshTile(job),
    async poll() { const [id, fn] = timers.entries().next().value; timers.delete(id); await fn(); },
    preview(index, count = 3) { Object.assign(jobs[index], { tree: { rootId: 0, fileCount: count, dirCount: 1 }, revision: jobs[index].revision + 1, progress: { fileCount: count, dirCount: 1 } }); },
    complete(index) { this.preview(index); jobs[index].state = 'completed'; },
  };
}

test('a second scan takes priority without cancelling the first', async () => {
  const h = await harness();
  await h.ui.analyze();
  h.element('pathInput').value = '/two';
  await h.ui.analyze();
  assert.deepEqual(h.jobs.map(job => job.state), ['paused', 'running']);
  assert.equal(h.calls.some(call => call[0] === 'cancel'), false);
  assert.equal(h.state.scanRootPath, '/two');
});

test('Home receives per-tile progress while the toolbar stays hidden', async () => {
  const h = await harness(); h.state.homeVisible = true;
  await h.ui.analyze();
  assert.equal(h.state.homeVisible, true);
  assert.equal(h.element('compactScanStatus').hidden, true);
  assert.equal(h.tileJobs().length, 1);
  h.preview(0); await h.poll();
  assert.equal(h.state.fileCount, 3);
  assert.equal(h.state.homeVisible, true);
});

test('opening a paused tile shows its snapshot without resuming or enqueueing', async () => {
  const h = await harness(); await h.ui.analyze(); h.preview(0); await h.poll();
  await h.element('pauseScanButton').handlers.click();
  h.state.homeVisible = true;
  const queues = h.calls.filter(call => call[0] === 'queue').length;
  await h.ui.openLocation();
  assert.equal(h.jobs[0].state, 'paused');
  assert.equal(h.state.homeVisible, false);
  assert.equal(h.state.node_id, 0);
  assert.equal(h.calls.filter(call => call[0] === 'queue').length, queues);
  assert.deepEqual(h.calls.filter(call => call[0] === 'pause'), [['pause', 1, true]]);
});

test('cancelling a live rescan restores the previous completed view and selection', async () => {
  const h = await harness(); await h.ui.analyze(); h.complete(0); await h.poll();
  Object.assign(h.state, { node_id: 7, navHistory: [0, 7], navIndex: 1, selectedNodeIds: new Set([9]) });
  h.element('pathInput').value = '/two'; await h.ui.analyze(); h.preview(1); await h.poll();
  await h.element('compactCancelScanButton').handlers.click();
  assert.equal(h.state.scanRootPath, '/one');
  assert.equal(h.state.node_id, 7);
  assert.deepEqual([...h.state.selectedNodeIds], [9]);
  assert.equal(h.state.homeVisible, false);
});

test('cancelling from Home hides progress and clicking the tile starts a fresh scan', async () => {
  const h = await harness(); await h.ui.analyze(); h.preview(0); await h.poll();
  h.state.homeVisible = true;
  await h.element('compactCancelScanButton').handlers.click();
  assert.equal(h.state.homeVisible, true);
  assert.equal(h.element('compactScanStatus').hidden, true);
  assert.equal(h.jobs.length, 1);
  await h.ui.openLocation();
  assert.equal(h.jobs.length, 2);
  assert.equal(h.jobs[0].state, 'cancelled');
  assert.equal(h.jobs[1].state, 'running');
  assert.equal(h.state.homeVisible, false);
  assert.deepEqual(h.errors, []);
});

test('Home refresh rescans the completed tile path and pauses another active scan', async () => {
  const h = await harness(); await h.ui.analyze(); h.complete(0); await h.poll();
  h.element('pathInput').value = '/two'; await h.ui.analyze();
  h.state.homeVisible = true;
  await h.refreshTile(h.jobs[0]);
  assert.deepEqual(h.jobs.map(job => [job.path, job.state]),
    [['/one', 'completed'], ['/two', 'paused'], ['/one', 'running']]);
  assert.equal(h.state.homeVisible, true);
  assert.deepEqual(h.errors, []);
});

test('opening a cancelled rescan tile reuses its earlier completed scan', async () => {
  const h = await harness(); await h.ui.analyze(); h.complete(0); await h.poll();
  await h.ui.analyze(); h.preview(1); await h.poll();
  h.state.homeVisible = true;
  await h.element('compactCancelScanButton').handlers.click();
  await h.ui.openLocation();
  assert.equal(h.jobs.length, 2);
  assert.deepEqual(h.calls.filter(call => call[0] === 'select').at(-1), ['select', 1]);
  assert.equal(h.state.homeVisible, false);
  assert.deepEqual(h.errors, []);
});

test('polling a background completion does not select it or replace the current view', async () => {
  const h = await harness(); await h.ui.analyze();
  h.element('pathInput').value = '/two'; await h.ui.analyze(); h.preview(1); await h.poll();
  h.state.node_id = 8;
  h.complete(0); await h.poll();
  assert.equal(h.state.scanRootPath, '/two');
  assert.equal(h.state.node_id, 8);
});

test('late preview replies cannot overwrite a newly selected job', async () => {
  const h = await harness(); await h.ui.analyze(); h.preview(0);
  const stale = deferred();
  h.backend.GetScanJobView = () => stale.promise;
  const poll = h.poll(); await Promise.resolve(); await Promise.resolve();
  h.element('pathInput').value = '/two'; await h.ui.analyze();
  stale.resolve({ job: h.jobs[0], tree: h.jobs[0].tree, key: 'stale' }); await poll;
  assert.equal(h.state.scanRootPath, '/two');
  assert.equal(h.state.node_id, null);
});

test('counts update independently of preview revision', async () => {
  const h = await harness(); await h.ui.analyze(); h.preview(0); await h.poll();
  const draws = h.redraws(); h.jobs[0].progress.fileCount = 500; await h.poll();
  assert.equal(h.state.fileCount, 500); assert.equal(h.redraws(), draws);
});

test('partial refresh requests collapse children and carry navigation and selection IDs', async () => {
  const h = await harness();
  Object.assign(h.state, { scanRootPath: '/one', node_id: 0, navHistory: [0], selectedNodeIds: new Set([1,2]), rects: [
    { node_id:1, is_folder:true, full_path:'/one/folder' }, { node_id:2, is_folder:true, full_path:'/one/folder/child' }] });
  await h.ui.refreshSelectedFolders();
  const request = h.calls.find(call => call[0] === 'refresh');
  assert.deepEqual(Array.from(request[1], item => ({...item})), [{nodeId:1,path:'/one/folder'}]);
  assert.deepEqual(Array.from(request[2]), [1,2,0]);
});

test('opening a never-scanned tile queues its first scan', async () => {
  const h = await harness(); h.state.homeVisible = true;
  await h.ui.openLocation();
  assert.equal(h.jobs.length, 1); assert.equal(h.state.homeVisible, false);
});
