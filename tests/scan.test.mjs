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
  let selected, nextTimer = 0, redraws = 0, labels = 0, tileJobs = [], refreshTile, pauseTile, cancelTile, removeTile;
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
    ClearCachedScan: async path => { for (let i=jobs.length-1;i>=0;i--) if (jobs[i].path === path) jobs.splice(i,1); },
    RemoveScanLocation: async path => { calls.push(['remove', path]); await backend.ClearCachedScan(path); },
    OpenPath: async path => calls.push(['open', path]),
  };
  const modules = {
    './wailsjs/go/app/App.js': Object.fromEntries(Object.keys(backend).map(key => [key, (...args) => backend[key](...args)])),
    './dom.js': { byId: element }, './state.js': { AppState: state },
    './navigation.js': { pushBrowserHistoryEntry() {}, updateNavButtons() {}, remapNavigation(mapping, fallback) { state.node_id = mapping[state.node_id] ?? fallback; } },
    './controls.js': { addControlEventListeners() {}, eventMatchesShortcut() {}, shortcutCanRun() {} },
    './notifications.js': { hideRectToast() {}, showErrorToast: error => errors.push(error) },
    './logging.js': { logError: (...args) => errors.push(args) },
    './locations.js': { rememberLocation() {}, hideLocationSelector() { state.homeVisible = false; }, showLocationSelector() { state.homeVisible = true; }, setLocationScanJobs(value, factory, path, remove) { tileJobs = value; removeTile = remove; factory(); } },
    './scan-status.js': { refreshScanIcon: '<svg></svg>', pendingScan: job => ['running', 'queued', 'paused'].includes(job?.state), updateScanStatus(refs, job) { refs.container.hidden = job.state === 'cancelled'; }, createTileScanStatus(pause, cancel, refresh) { refreshTile = refresh; pauseTile = pause; cancelTile = cancel; } },
  };
  const ui = await loadUI('scan.js', modules, { performance: { now: () => 1000 },
    setTimeout: fn => { timers.set(++nextTimer, fn); return nextTimer; }, clearTimeout: id => timers.delete(id) });
  ui.initScan({ redraw: async () => { redraws++; }, repaintScanLabels: () => { labels++; }, hideContextMenu() {} });
  return { ui, jobs, state, element, calls, errors, backend, tileJobs: () => tileJobs, redraws: () => redraws, labels: () => labels,
    refreshTile: job => refreshTile(job),
    scanTile: () => refreshTile({ path: state.scanRootPath, state: 'idle' }),
    removeTile: job => removeTile(job),
    pauseTile: () => pauseTile(jobs.find(job => job.id === selected)),
    cancelTile: () => cancelTile(jobs.find(job => job.id === selected)),
    async poll() { const [id, fn] = timers.entries().next().value; timers.delete(id); await fn(); },
    preview(index, count = 3) { Object.assign(jobs[index], { tree: { rootId: 0, fileCount: count, dirCount: 1 }, revision: jobs[index].revision + 1, progress: { fileCount: count, dirCount: 1 } }); },
    complete(index) { this.preview(index); jobs[index].state = 'completed'; },
  };
}

test('removing the selected folder clears its view and cached location', async () => {
  const h = await harness();
  await h.ui.analyze();
  h.preview(0);
  await h.poll();
  await h.removeTile(h.jobs[0]);
  assert.equal(h.jobs.length, 0);
  assert.equal(h.state.node_id, null);
  assert.equal(h.state.scanRootPath, '');
  assert.equal(h.state.liveScanPreview, false);
  assert.equal(h.state.homeVisible, true);
  assert.equal(h.element('emptyLocationTitle').textContent, 'Choose a location');
  assert.deepEqual(h.calls.at(-1), ['remove', '/one']);
});

test('selecting an unscanned location preserves the running scan and query input', async () => {
  const h = await harness();
  await h.ui.analyze(); h.preview(0); await h.poll();
  h.state.node_id = 7;
  h.element('pathInput').value = '/draft';
  await h.ui.openLocation('/unscanned');
  assert.equal(h.jobs.length, 1);
  assert.equal(h.jobs[0].state, 'running');
  assert.equal(h.state.node_id, null);
  assert.equal(h.element('pathInput').value, '/draft');
  await h.ui.openLocation('/one');
  assert.equal(h.state.node_id, 7);
  assert.equal(h.element('pathInput').value, '/draft');
});

test('removing a selected unscanned folder clears its empty view', async () => {
  const h = await harness();
  await h.ui.openLocation('/new-folder');
  await h.removeTile({ path: '/new-folder' });
  assert.equal(h.state.scanRootPath, '');
  assert.equal(h.element('emptyLocationTitle').textContent, 'Choose a location');
  assert.equal(h.jobs.length, 0);
});

test('late location validation cannot replace the latest sidebar selection', async () => {
  const h = await harness();
  const slow = deferred();
  h.backend.ValidateScanPath = path => path === '/slow' ? slow.promise : path;
  const first = h.ui.openLocation('/slow');
  await h.ui.openLocation('/latest');
  slow.resolve('/slow'); await first;
  assert.equal(h.state.scanRootPath, '/latest');
  assert.equal(h.jobs.length, 0);
});

test('a second scan takes priority without cancelling the first', async () => {
  const h = await harness();
  await h.ui.analyze();
  h.element('pathInput').value = '/two';
  await h.ui.analyze();
  assert.deepEqual(h.jobs.map(job => job.state), ['paused', 'running']);
  assert.equal(h.calls.some(call => call[0] === 'cancel'), false);
  assert.equal(h.state.scanRootPath, '/two');
});

test('explicit scan opens its view and leaves progress on the sidebar', async () => {
  const h = await harness(); h.state.homeVisible = true;
  await h.ui.analyze();
  assert.equal(h.state.homeVisible, false);
  assert.equal(h.element('compactScanStatus').hidden, true);
  assert.equal(h.tileJobs().length, 1);
  h.preview(0); await h.poll();
  assert.equal(h.state.fileCount, 3);
  assert.equal(h.state.homeVisible, false);
});

test('opening a paused tile shows its snapshot without resuming or enqueueing', async () => {
  const h = await harness(); await h.ui.analyze(); h.preview(0); await h.poll();
  await h.pauseTile();
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
  await h.cancelTile();
  assert.equal(h.state.scanRootPath, '/one');
  assert.equal(h.state.node_id, 7);
  assert.deepEqual([...h.state.selectedNodeIds], [9]);
  assert.equal(h.state.homeVisible, false);
});

test('cancelled location requires an explicit scan action', async () => {
  const h = await harness(); await h.ui.analyze(); h.preview(0); await h.poll();
  h.state.homeVisible = true;
  await h.cancelTile();
  assert.equal(h.state.homeVisible, true);
  assert.equal(h.element('compactScanStatus').hidden, true);
  assert.equal(h.jobs.length, 1);
  await h.ui.openLocation();
  assert.equal(h.jobs.length, 1);
  assert.equal(h.state.homeVisible, true);
  await h.scanTile();
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
  assert.equal(h.state.homeVisible, false);
  assert.deepEqual(h.errors, []);
});

test('opening a cancelled rescan tile reuses its earlier completed scan', async () => {
  const h = await harness(); await h.ui.analyze(); h.complete(0); await h.poll();
  await h.ui.analyze(); h.preview(1); await h.poll();
  h.state.homeVisible = true;
  await h.cancelTile();
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

test('opening a never-scanned tile does not scan until explicitly requested', async () => {
  const h = await harness(); h.state.homeVisible = true;
  await h.ui.openLocation();
  assert.equal(h.jobs.length, 0); assert.equal(h.state.homeVisible, true);
  await h.scanTile();
  assert.equal(h.jobs.length, 1); assert.equal(h.state.homeVisible, false);
});

test('central Scan starts the selected empty location and hides while scanning', async () => {
  const h = await harness();
  const button = h.element('scanLocationButton');
  assert.equal(button.hidden, true);
  h.element('pathInput').value = '/draft-path';
  await h.ui.openLocation('/selected-folder');
  assert.equal(button.hidden, false);
  await button.handlers.click();
  assert.deepEqual(h.calls.find(call => call[0] === 'queue'), ['queue', '/selected-folder']);
  assert.equal(h.element('pathInput').value, '/draft-path');
  assert.equal(button.hidden, true);
  await button.handlers.click();
  assert.equal(h.jobs.length, 1);
});


test('switching completed locations preserves navigation without rescanning', async () => {
  const h = await harness();
  await h.ui.analyze(); h.complete(0); await h.poll();
  h.state.node_id = 42;
  h.element('pathInput').value = '/two';
  await h.ui.analyze(); h.complete(1); await h.poll();
  h.state.node_id = 24;
  const queues = h.calls.filter(call => call[0] === 'queue').length;
  await h.ui.openLocation('/one');
  assert.equal(h.state.node_id, 42);
  await h.ui.openLocation('/two');
  assert.equal(h.state.node_id, 24);
  await h.ui.openLocation('/one');
  assert.equal(h.state.node_id, 42);
  assert.equal(h.calls.filter(call => call[0] === 'queue').length, queues);
});


test('saved snapshots display their scan date and open without starting a scan', async () => {
  const h = await harness();
  await h.ui.analyze(); h.complete(0);
  Object.assign(h.jobs[0], { cached: true, scannedAt: '2026-10-18T10:00:00Z' });
  await h.poll();
  const count = h.calls.filter(call => call[0] === 'queue').length;
  await h.ui.openLocation('/one');
  assert.equal(h.calls.filter(call => call[0] === 'queue').length, count);
});
