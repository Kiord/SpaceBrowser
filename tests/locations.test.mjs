import assert from "node:assert/strict";
import test from "node:test";
import { loadUI, eventTarget, deferred } from "./helpers/ui.mjs";

async function renderLocation(capacity) {
  const element = () => ({
    scrollIntoView() {}, ...eventTarget(), children: [], dataset: {}, attributes: {}, hidden: true,
    classList: { add() {} },
    style: { setProperty(name, value) { this[name] = value; } },
    append(...children) { for (const child of children) { child.remove(); child.parent = this; this.children.push(child); } },
    remove() { if (this.parent) this.parent.children = this.parent.children.filter(child => child !== this); },
    replaceChildren() { this.children = []; },
    setAttribute(name, value) { this.attributes[name] = value; },
  });
  const elements = new Map();
  const byId = id => {
    if (!elements.has(id)) elements.set(id, element());
    return elements.get(id);
  };
  const ui = await loadUI("locations.js", {
    "./wailsjs/go/app/App.js": { GetScanLocations: async () => [{ name: "Disk", path: "/volume", ...capacity }] },
    "./dom.js": { byId }, "./folder-picker.js": { chooseFolder() {} },
    "./logging.js": { logError() {} }, "./format.js": { formatSize: size => `${size} B` },
  }, { document: { createElement: element } });
  let scans = 0, selectedPath;
  ui.initLocationSelector({ analyze: async path => { scans++; selectedPath = path; } });
  await new Promise(resolve => setImmediate(resolve));
  const tile = byId("locationList").children[0].children[0];
  return { ui, element, tile, usage: tile.children[1].children[2], byId, scans: () => scans, selectedPath: () => selectedPath };
}

for (const [used, level] of [[0, "normal"], [79.9, "normal"], [80, "warning"], [94.9, "warning"], [95, "critical"], [100, "critical"]]) {
  test(`volume tile displays ${used}% fullness as ${level}`, async () => {
    const h = await renderLocation({ diskTotal: 1000, diskFree: 1000 - used * 10 });
    const [label, bar] = h.usage.children;
    assert.equal(label.textContent, `${used * 10} B / 1000 B (${used.toFixed(1)}%)`);
    assert.equal(bar.dataset.level, level);
    assert.ok(Math.abs(Number(bar.attributes['aria-valuenow']) - used) < 0.0001);
    assert.ok(Math.abs(parseFloat(bar.children[0].style['--scan-progress']) - used) < 0.0001);
    await h.tile.emit('click');
    assert.equal(h.selectedPath(), '/volume');
    assert.equal(h.scans(), 1);
  });
}

for (const capacity of [{}, { diskTotal: 0, diskFree: 0 }, { diskTotal: 10 }, { diskTotal: 10, diskFree: -1 }, { diskTotal: 10, diskFree: 11 }]) {
  test(`volume tile omits unavailable or invalid capacity: ${JSON.stringify(capacity)}`, async () => {
    const h = await renderLocation(capacity);
    assert.equal(h.usage, undefined);
    assert.equal(h.tile.children[1].children[0].textContent, 'Disk');
  });
}


test('filesystem type is shown on a volume tile', async () => {
  const h = await renderLocation({ filesystem: 'NTFS' });
  assert.equal(h.tile.children[1].children[1].textContent, '/volume · NTFS');
});

test('tile background and scan progress area open the location', async () => {
  const h = await renderLocation({});
  const progress = h.element();
  h.ui.setLocationScanJobs([{ id: 1, path: '/volume', state: 'paused' }], () => ({ element: progress, update() {} }));
  const tile = h.byId('locationList').children[0];
  for (const target of [tile, progress]) {
    target.closest = () => null;
    await tile.emit('click', { target });
    assert.equal(h.selectedPath(), '/volume');
  }
  assert.equal(h.scans(), 2);
});

test('bubbling button clicks do not open a tile again or activate it from scan controls', async () => {
  const h = await renderLocation({});
  const tile = h.byId('locationList').children[0];
  await h.tile.emit('click');
  await tile.emit('click', { target: { closest: () => h.tile } });
  assert.equal(h.scans(), 1);
  for (const control of ['pause', 'cancel']) {
    await tile.emit('click', { target: { closest: () => ({ control }) } });
  }
  assert.equal(h.scans(), 1);
});

test('folder jobs get a separate tile with progress but no disk usage bar', async () => {
  const h = await renderLocation({ diskTotal: 100, diskFree: 50 });
  const updates = [];
  const factory = () => ({ element: h.element(), update: job => updates.push(job.state) });
  h.ui.setLocationScanJobs([{ id: 1, path: '/volume/folder', state: 'running' }], factory);
  const tiles = h.byId('locationList').children;
  assert.equal(tiles.length, 2);
  const folder = tiles[1];
  assert.equal(folder.children[0].children[1].children.length, 2);
  assert.equal(folder.children[0].dataset.path, '/volume/folder');
  h.ui.setLocationScanJobs([{ id: 1, path: '/volume/folder', state: 'paused' }], factory);
  assert.equal(tiles.length, 2);
  assert.deepEqual(updates, ['running', 'paused']);
});


test('volume tile displays available hardware and filesystem details together', async () => {
  const h = await renderLocation({ mediaType: 'SSD', busType: 'NVMe', filesystem: 'NTFS' });
  assert.equal(h.tile.children[1].children[1].textContent, '/volume · SSD · NVMe · NTFS');
});


test('location cycling wraps in tile order and advances without waiting for opens', async () => {
  const h = await renderLocation({});
  h.ui.setLocationScanJobs([{ path: '/folder-a' }, { path: '/folder-b' }], null, '/volume');
  for (const [direction, path] of [[1, '/folder-a'], [1, '/folder-b'], [1, '/volume'], [-1, '/folder-b'], [-1, '/folder-a']]) {
    assert.equal(h.ui.cycleLocation(direction), true);
    assert.equal(h.selectedPath(), path);
  }
  h.ui.setLocationScanJobs([], null, '/missing');
  h.ui.cycleLocation(-1);
  assert.equal(h.selectedPath(), '/volume');
});

test('folder tile cross removes its location and cache without opening it', async () => {
  const h = await renderLocation({ kind: 'volume' });
  let jobs = [{ id: 1, path: '/volume/folder', state: 'running' }];
  const removed = [];
  const factory = () => ({ element: h.element(), update() {} });
  const remove = async job => {
    removed.push(job.path);
    jobs = [];
    h.ui.setLocationScanJobs(jobs, factory, '', remove);
  };
  h.ui.setLocationScanJobs(jobs, factory, '', remove);
  const [volume, folder] = h.byId('locationList').children;
  assert.equal(volume.children.some(child => child.className === 'location-remove-button'), false);
  const cross = folder.children.find(child => child.className === 'location-remove-button');
  assert.ok(cross);
  assert.match(cross.attributes['aria-label'], /clear its cache/);
  await cross.emit('click');
  await folder.emit('click', { target: { closest: () => cross } });
  assert.deepEqual(removed, ['/volume/folder']);
  assert.deepEqual(h.byId('locationList').children, [volume]);
  assert.equal(h.scans(), 0);
});

test('failed removal keeps the folder tile and re-enables its cross', async () => {
  const h = await renderLocation({ kind: 'volume' });
  h.ui.setLocationScanJobs([{ id: 1, path: '/folder', state: 'cancelled' }], null, '', async () => { throw new Error('clear failed'); });
  const folder = h.byId('locationList').children[1];
  const cross = folder.children.find(child => child.className === 'location-remove-button');
  await assert.rejects(cross.emit('click'), /clear failed/);
  assert.equal(cross.disabled, false);
  assert.equal(h.byId('locationList').children[1], folder);
});


test('scan updates do not reset the cursor during a held-Control cycle', async () => {
  const h = await renderLocation({});
  const jobs = [{ path: '/folder-a' }, { path: '/folder-b' }];
  h.ui.setLocationScanJobs(jobs, null, '/volume');
  h.ui.cycleLocation(1);
  h.ui.setLocationScanJobs(jobs, null, '/volume');
  h.ui.cycleLocation(1, true);
  assert.equal(h.selectedPath(), '/folder-b');
  h.ui.setLocationScanJobs(jobs, null, '/volume');
  h.ui.cycleLocation(1, false);
  assert.equal(h.selectedPath(), '/folder-a');
});


test('late volume discovery upgrades a saved-scan placeholder without losing controls', async () => {
  const pending = deferred();
  const make = () => ({ ...eventTarget(), children: [], dataset: {}, attributes: {}, style: { setProperty() {} },
    classList: { values: new Set(), add(value) { this.values.add(value); }, remove(value) { this.values.delete(value); }, contains(value) { return this.values.has(value); } },
    append(...children) { for (const child of children) { child.remove(); child.parent = this; this.children.push(child); } },
    remove() { if (this.parent) this.parent.children = this.parent.children.filter(child => child !== this); },
    replaceChildren(...children) { this.children = children; },
    setAttribute(key, value) { this.attributes[key] = value; } });
  const elements = new Map();
  const byId = id => { if (!elements.has(id)) elements.set(id, make()); return elements.get(id); };
  const ui = await loadUI('locations.js', {
    './wailsjs/go/app/App.js': { GetScanLocations: () => pending.promise },
    './dom.js': { byId }, './logging.js': { logError() {} }, './format.js': { formatSize: String },
  }, { document: { createElement: make } });
  ui.initLocationSelector({ analyze: async () => {} });
  const progress = make();
  ui.setLocationScanJobs([{ id: 1, path: 'D:\\', state: 'completed', cached: true }], () => ({ element: progress, update() {} }), '', async () => {});
  const tile = byId('locationList').children[0];
  assert.equal(tile.classList.contains('location-tile-removable'), true);
  pending.resolve([{ name: 'Data', path: 'D:\\', kind: 'volume', iconUrl: 'data:image/png;base64,icon', filesystem: 'NTFS', diskTotal: 100, diskFree: 20 }]);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(byId('locationList').children.length, 1);
  assert.equal(tile.children[0].children[0].children[0].src, 'data:image/png;base64,icon');
  const details = tile.children[0].children[1];
  assert.equal(details.children[0].textContent, 'Data');
  assert.match(details.children[1].textContent, /NTFS/);
  assert.ok(details.children[2]);
  assert.equal(tile.children[1], progress);
  assert.equal(tile.classList.contains('location-tile-removable'), false);
  assert.equal(tile.children.some(child => child.className === 'location-remove-button'), false);
});

test('late volume discovery keeps folders below all volumes and cycling follows that order', async () => {
  const pending = deferred();
  const make = () => ({ ...eventTarget(), children: [], dataset: {}, attributes: {}, scrollIntoView() {},
    append(...children) { for (const child of children) { child.remove(); child.parent = this; this.children.push(child); } },
    remove() { if (this.parent) this.parent.children = this.parent.children.filter(child => child !== this); },
    replaceChildren(...children) { this.children = []; this.append(...children); },
    setAttribute(key, value) { this.attributes[key] = value; } });
  const elements = new Map();
  const byId = id => { if (!elements.has(id)) elements.set(id, make()); return elements.get(id); };
  const opened = [];
  const ui = await loadUI('locations.js', {
    './wailsjs/go/app/App.js': { GetScanLocations: () => pending.promise },
    './dom.js': { byId }, './logging.js': { logError() {} }, './format.js': { formatSize: String },
  }, { document: { createElement: make } });
  ui.initLocationSelector({ analyze: async path => opened.push(path) });
  const jobs = [{ path: '/folder', state: 'completed' }, { path: '/disk', state: 'completed' }];
  ui.setLocationScanJobs(jobs, null, '/disk');
  pending.resolve([{ name: 'Disk', path: '/disk', kind: 'volume' }, { name: 'Other', path: '/other', kind: 'removable' }]);
  await new Promise(resolve => setImmediate(resolve));
  const paths = () => byId('locationList').children.map(tile => tile.children[0].dataset.path);
  assert.deepEqual(paths(), ['/disk', '/other', '/folder']);
  ui.cycleLocation(1);
  ui.cycleLocation(1);
  assert.deepEqual(opened, ['/other', '/folder']);
  ui.setLocationScanJobs([...jobs, { path: '/new-folder', state: 'running' }], null);
  assert.deepEqual(paths(), ['/disk', '/other', '/folder', '/new-folder']);
});


test('cancelling a rescan restores the saved snapshot tile status and timestamp', async () => {
  const h = await renderLocation({});
  const updates = [];
  const factory = () => ({ element: h.element(), update: job => updates.push(job) });
  const saved = { id: 1, path: 'C:\\', state: 'completed', cached: true,
    scannedAt: '2026-10-18T10:00:00Z', progress: { fraction: 1, elapsedMilliseconds: 4500 } };
  const rescan = { id: 2, path: 'c:/', state: 'running', progress: { fraction: 0.3 } };
  h.ui.setLocationScanJobs([saved], factory);
  assert.equal(updates.at(-1), saved);
  h.ui.setLocationScanJobs([saved, rescan], factory);
  assert.equal(updates.at(-1), rescan);
  h.ui.setLocationScanJobs([saved, { ...rescan, state: 'cancelled' }], factory);
  assert.equal(updates.at(-1), saved);
  assert.equal(updates.at(-1).scannedAt, saved.scannedAt);
  assert.equal(updates.at(-1).progress.fraction, 1);
  const restarted = { ...rescan, id: 3 };
  h.ui.setLocationScanJobs([saved, { ...rescan, state: 'cancelled' }, restarted], factory);
  assert.equal(updates.at(-1), restarted);
});

test('a cancelled first scan still supplies the hidden status for its location', async () => {
  const h = await renderLocation({});
  const updates = [];
  const cancelled = { id: 1, path: '/volume', state: 'cancelled' };
  h.ui.setLocationScanJobs([cancelled], () => ({ element: h.element(), update: job => updates.push(job) }));
  assert.deepEqual(updates, [cancelled]);
});
