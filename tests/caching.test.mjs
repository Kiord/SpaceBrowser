import assert from "node:assert/strict";
import test from "node:test";
import { eventTarget, loadUI } from "./helpers/ui.mjs";

async function harness() {
  const elements = new Map();
  const make = () => ({ ...eventTarget(), children: [], textContent: "", value: "", hidden: false,
    append(...children) { this.children.push(...children); }, replaceChildren(...children) { this.children = children; },
    setAttribute() {}, classList: { add() {}, remove() {}, toggle() {} }, style: {} });
  const byId = id => { if (!elements.has(id)) elements.set(id, make()); return elements.get(id); };
  let scans = [
    { path: '/one/<file>', scannedAt: '2026-10-18T10:00:00Z', diskBytes: 100, memoryBytes: 200, loaded: true },
    { path: '/two', scannedAt: '2026-10-17T10:00:00Z', diskBytes: 300, memoryBytes: 0, loaded: false },
  ];
  const calls = []; let refreshed = 0, fail = false, enabled = true;
  const ui = await loadUI('settings.js', {
    './wailsjs/go/app/App.js': {
      GetCachingEnabled: async () => enabled, SetCachingEnabled(value) { enabled = value; }, GetCachedScans: async () => scans,
      ClearCachedScan: async path => { if (fail) throw Error('disk error'); calls.push(path); scans = scans.filter(scan => scan.path !== path); },
      ClearAllCachedScans: async () => { calls.push('all'); scans = []; },
      GetDefaultProfile() {}, GetDefaultSettingsPath() {}, GetProfile() {}, GetSettingsPath() {}, PickSettingsPath() {}, SaveSettings() {},
    },
    './dom.js': { byId, queryAll: () => [] },
    './format.js': { SIZE_UNITS: {}, splitSizeIntoUnit() {}, formatSize: value => `${value} B` },
    './controls.js': { addControlEventListeners() {}, shortcutFromEvent() {} },
    './logging.js': { logError() {} },
    './state.js': { AppState: {}, AppearanceState: {}, PALETTES: {}, SCALE_MAX: 5, SCALE_MIN: .5, getScale: () => 1, setProfiles() {} },
  }, { document: { addEventListener() {}, createElement: make } });
  ui.initSettings({ redraw: async () => {}, cachedScansChanged: async () => { refreshed++; } });
  return { ui, byId, calls, get refreshed() { return refreshed; }, fail() { fail = true; } };
}

test('caching tab lists dates and separates disk usage from loaded RAM, clears one or all', async () => {
  const h = await harness(); await h.ui.renderCachedScans();
  const rows = h.byId('cachedScansList').children;
  assert.equal(rows.length, 2);
  assert.equal(rows[0].children[0].children[0].textContent, '/one/<file>');
  assert.match(rows[0].children[0].children[1].textContent, /Scanned/);
  assert.match(rows[0].children[0].children[2].textContent, /100 B on disk.*200 B RAM/);
  assert.match(rows[1].children[0].children[2].textContent, /Not loaded into RAM/);
  await rows[0].children[1].emit('click');
  assert.deepEqual(h.calls, ['/one/<file>']);
  assert.equal(h.byId('cachedScansList').children.length, 1);
  await h.byId('clearAllCachedScans').emit('click');
  assert.deepEqual(h.calls, ['/one/<file>', 'all']);
  assert.equal(h.refreshed, 2);
  assert.equal(h.byId('cachedScansList').children.length, 0);
  assert.equal(h.byId('clearAllCachedScans').disabled, true);
});

test('failed cache clear keeps the list and reports the error', async () => {
  const h = await harness(); await h.ui.renderCachedScans(); h.fail();
  await h.byId('cachedScansList').children[0].children[1].emit('click');
  assert.equal(h.byId('cachedScansList').children.length, 2);
  assert.equal(h.refreshed, 0);
  assert.match(h.byId('cachedScansStatus').textContent, /disk error/);
});


test('session caching toggle applies immediately without clearing scans', async () => {
 const h=await harness();await h.ui.renderCachedScans();
 const toggle=h.byId('settingsCachingEnabled');toggle.checked=false;
 await toggle.emit('change');
 assert.equal(toggle.checked,false);assert.equal(toggle.disabled,false);
 assert.equal(h.refreshed,1);assert.deepEqual(h.calls,[]);
 assert.equal(h.byId('cachedScansList').children.length,2);
});
