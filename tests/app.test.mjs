import assert from "node:assert/strict";
import test from "node:test";
import { dom, eventTarget, loadUI, noop } from "./helpers/ui.mjs";

async function harness(rects, initialPath = "") {
  const document = eventTarget(), window = eventTarget();
  const { byId } = dom();
  for (const id of ["pathGroup", "analyzeButton"]) {
    byId(id).removeAttribute = noop;
    byId(id).dataset = {};
  }
  const state = { rects, selectedNodeIds: new Set(rects.map(rect => rect.node_id)), profile: { controls: { visitSelected: "Enter" } } };
  const handlers = [], calls = [];
  await loadUI("app.js", {
    "./wailsjs/go/main/App.js": { DefaultPath: async () => "", GetInitialScanPath: async () => initialPath },
    "./dom.js": { byId },
    "./file-actions.js": { hideContextMenu: noop, initFileActions: noop,
      openRectWithDefault: (...args) => calls.push(["open", ...args]) },
    "./folder-picker.js": { initFolderPicker: noop },
    "./controls.js": { addControlEventListeners: handler => handlers.push(handler),
      eventMatchesShortcut: (event, binding) => event.key === binding, shortcutCanRun: event => !event.blocked },
    "./logging.js": { logError: noop },
    "./locations.js": { initLocationSelector: () => { state.homeVisible = true; }, showLocationSelector: noop, hideLocationSelector: () => { state.homeVisible = false; } },
    "./navigation.js": { initNavigation: noop, updateNavButtons: noop, navigateToSelected: () => calls.push(["visit"]) },
    "./scan.js": { analyze: () => calls.push(['scan', byId('pathInput').value, state.homeVisible]), openLocation: noop, initScan: noop, updateScanVisibility: noop },
    "./settings.js": { initSettings: noop, loadSettingsState: async () => {} },
    "./treemap-view.js": { initTreemapView: noop, redraw: noop, resizeCanvas: noop, repaintScanLabels: noop },
    "./zoom.js": { initZoom: noop },
    "./state.js": { AppState: state },
  }, { document, window });
  await document.emit("DOMContentLoaded");
  let prevented = false;
  return { calls, state, prevented: () => prevented,
    press: (extra = {}) => handlers[0]({ key: "Enter", preventDefault() { prevented = true; }, ...extra }) };
}

test('CLI path opens the treemap before starting its scan', async () => {
  const h = await harness([], 'D:\\folder');
  assert.deepEqual(h.calls, [['scan', 'D:\\folder', false]]);
  assert.equal(h.state.homeVisible, false);
});

test('launching without a CLI path keeps Home visible without scanning', async () => {
  const h = await harness([]);
  assert.equal(h.state.homeVisible, true);
  assert.deepEqual(h.calls, []);
});

for (const scenario of [
  { name: "multiple files", rects: [{ node_id: 1 }, { node_id: 2 }], action: "open" },
  { name: "one file", rects: [{ node_id: 1 }], action: "open" },
  { name: "one folder", rects: [{ node_id: 1, is_folder: true }], action: "visit" },
  { name: "files and folders", rects: [{ node_id: 1 }, { node_id: 2, is_folder: true }] },
  { name: "multiple folders", rects: [{ node_id: 1, is_folder: true }, { node_id: 2, is_folder: true }] },
  { name: "no selection", rects: [] },
]) {
  test(`Visit shortcut handles ${scenario.name}`, async () => {
    const h = await harness(scenario.rects);
    h.state.homeVisible = false;
    h.press();
    // Opening without an explicit rectangle uses the complete selection,
    // exactly as the Open shortcut does.
    assert.deepEqual(h.calls, scenario.action ? [[scenario.action]] : []);
    assert.equal(h.prevented(), !!scenario.action);
  });
}

test("Visit shortcut respects blocked and nonmatching keyboard events", async () => {
  const h = await harness([{ node_id: 1 }, { node_id: 2 }]);
  h.state.homeVisible = false;
  h.press({ blocked: true });
  h.press({ key: "Escape" });
  assert.deepEqual(h.calls, []);
});
