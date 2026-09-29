import assert from "node:assert/strict";
import test from "node:test";
import { loadUI } from "./helpers/ui.mjs";

async function harness() {
  const state = { selectedNodeIds: new Set(), rects: [
    { node_id: 1, full_path: "/a", is_folder: true },
    { node_id: 2, full_path: "/a/b" },
    { node_id: 3, full_path: "/ab" },
    { node_id: -1, is_small_files: true },
  ] };
  return { state, ui: await loadUI("selection.js", { "./state.js": { AppState: state } }) };
}

test("normal clicks replace selection; modifier clicks add and toggle", async () => {
  const { state, ui } = await harness();
  ui.selectRect(0);
  ui.selectRect(1, { additive: true });
  assert.deepEqual([...state.selectedNodeIds], [1, 2]);
  assert.equal(ui.getSelectedRect(), null);
  ui.selectRect(0, { additive: true });
  assert.equal(ui.getSelectedRect().node_id, 2);
  ui.selectRect(0);
  ui.selectRect(0);
  assert.deepEqual([...state.selectedNodeIds], [1]);
});

test("context selection preserves a selected group and replaces it on another item", async () => {
  const { state, ui } = await harness();
  ui.selectRect(0); ui.selectRect(1, { additive: true });
  ui.selectRect(0, { preserve: true });
  assert.deepEqual([...state.selectedNodeIds], [1, 2]);
  ui.selectRect(2, { preserve: true });
  assert.deepEqual([...state.selectedNodeIds], [3]);
});

test("blank and virtual items cannot join a selection; blank clicks clear it", async () => {
  const { state, ui } = await harness();
  ui.selectRect(0);
  ui.selectRect(3, { additive: true }); ui.selectRect(-1, { additive: true });
  assert.deepEqual([...state.selectedNodeIds], [1]);
  ui.selectRect(-1);
  assert.equal(state.selectedNodeIds.size, 0);
});

test("selection survives layout changes and reappears when returning to selected items", async () => {
  const { state, ui } = await harness();
  ui.selectRect(0); ui.selectRect(1, { additive: true });
  state.rects.reverse();
  assert.equal(ui.getSelectedRects().length, 2);
  const originalRects = state.rects;
  state.rects = state.rects.filter(rect => rect.node_id !== 1);
  assert.deepEqual([...state.selectedNodeIds], [1, 2]);
  assert.deepEqual(Array.from(ui.getSelectedRects(), rect => rect.node_id), [2]);
  state.rects = [];
  assert.equal(ui.getSelectedRects().length, 0);
  state.rects = originalRects;
  assert.equal(ui.getSelectedRects().length, 2);
});

test("deletion collapses directories and descendants without confusing sibling prefixes", async () => {
  const { state, ui } = await harness();
  assert.deepEqual(Array.from(ui.reduceDeletionTargets(state.rects.slice(0, 3), "linux"), rect => rect.full_path), ["/a", "/ab"]);
  const windows = [
    { full_path: "D:\\Data\\file", node_id: 2 },
    { full_path: "d:\\data", is_folder: true, node_id: 1 },
    { full_path: "D:\\Data2", node_id: 3 },
  ];
  assert.deepEqual(Array.from(ui.reduceDeletionTargets(windows, "windows"), rect => rect.node_id), [1, 3]);
});
