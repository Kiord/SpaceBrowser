import assert from "node:assert/strict";
import test from "node:test";
import { loadUI, eventTarget } from "./helpers/ui.mjs";

async function renderLocation(capacity) {
  const element = () => ({
    ...eventTarget(), children: [], dataset: {}, attributes: {}, hidden: true,
    style: { setProperty(name, value) { this[name] = value; } },
    append(...children) { this.children.push(...children); },
    replaceChildren() { this.children = []; },
    setAttribute(name, value) { this.attributes[name] = value; },
  });
  const elements = new Map();
  const byId = id => {
    if (!elements.has(id)) elements.set(id, element());
    return elements.get(id);
  };
  const ui = await loadUI("locations.js", {
    "./wailsjs/go/main/App.js": { GetScanLocations: async () => [{ name: "Disk", path: "/volume", ...capacity }] },
    "./dom.js": { byId }, "./folder-picker.js": { chooseFolder() {} },
    "./logging.js": { logError() {} }, "./format.js": { formatSize: size => `${size} B` },
  }, { document: { createElement: element } });
  let scans = 0;
  ui.initLocationSelector({ analyze: async () => { scans++; } });
  await new Promise(resolve => setImmediate(resolve));
  const tile = byId("locationList").children[0].children[0];
  return { ui, element, tile, usage: tile.children[1].children[2], byId, scans: () => scans };
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
    assert.equal(h.byId('pathInput').value, '/volume');
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
    assert.equal(h.byId('pathInput').value, '/volume');
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
