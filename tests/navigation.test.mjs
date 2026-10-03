import assert from "node:assert/strict";
import test from "node:test";
import { dom, eventTarget, loadUI, noop } from "./helpers/ui.mjs";

async function harness() {
  const { byId } = dom();
  const window = eventTarget();
  const calls = [];
  // History methods record browser requests; popstate is delivered explicitly
  // by tests, just as the browser delivers it after a traversal.
  window.history = Object.fromEntries(["replaceState", "pushState", "back", "forward", "go"].map(name =>
    [name, (...args) => calls.push({ name, args })]));
  const state = { node_id: 0, navHistory: [0], navIndex: 0, navSession: 1, browserHistoryPosition: 0, rects: [{}] };
  let redraws = 0;
  let setFreeSpace = async () => {};
  const ui = await loadUI("navigation.js", {
    "./wailsjs/go/main/App.js": { SetShowFreeSpace: value => setFreeSpace(value) },
    "./dom.js": { byId },
    "./controls.js": { addControlEventListeners: noop, eventMatchesShortcut: noop, shortcutCanRun: noop },
    "./logging.js": { logError: noop }, "./state.js": { AppState: state },
  }, { window });
  ui.initNavigation({ redraw: () => { redraws++; }, hideHome: () => { state.homeVisible = false; } });
  return { ui, byId, window, state, calls, redraws: () => redraws,
    select: rect => { state.rects = rect == null ? [] : Array.isArray(rect) ? rect : [rect]; state.selectedNodeIds = new Set(state.rects.map(item => item.node_id)); }, failToggle: () => { setFreeSpace = async () => { throw new Error("failed"); }; } };
}

test("home ignores tree navigation but Back can reopen the current tree", async () => {
  const h = await harness();
  h.ui.visit(1);
  h.state.homeVisible = true;
  h.state.rects = [{ parent_id: 0 }];
  const before = h.calls.length;
  h.ui.goToRoot();
  h.ui.goToParent();
  h.ui.goForward();
  assert.equal(h.calls.length, before);
  assert.equal(h.state.node_id, 1);
  h.ui.goBackward();
  assert.equal(h.state.homeVisible, false);
  h.state.homeVisible = true;
  h.state.node_id = null;
  h.ui.goBackward();
  assert.equal(h.calls.length, before);
  assert.equal(h.state.homeVisible, true);
});

test("back and forward restore matching history entries", async () => {
  const h = await harness();
  h.ui.visit(1);
  const first = h.calls.at(-1).args[0];
  h.ui.visit(2);
  const second = h.calls.at(-1).args[0];
  h.state.selectedNodeIds = new Set([2, 3]);
  h.ui.goBackward();
  assert.equal(h.calls.at(-1).name, "back");
  await h.window.emit("popstate", { state: first });
  assert.equal(h.state.node_id, 1);
  assert.equal(h.state.navIndex, 1);
  assert.deepEqual([...h.state.selectedNodeIds], [2, 3]);
  h.ui.updateNavButtons();
  assert.equal(h.byId("forwardButton").disabled, false);
  h.ui.goForward();
  assert.equal(h.calls.at(-1).name, "forward");
  await h.window.emit("popstate", { state: second });
  assert.equal(h.state.node_id, 2);
  assert.deepEqual([...h.state.selectedNodeIds], [2, 3]);
  assert.equal(h.redraws(), 4);
});

test("visiting after going back replaces the forward branch", async () => {
  const h = await harness();
  h.ui.visit(1);
  const first = h.calls.at(-1).args[0];
  h.ui.visit(2);
  await h.window.emit("popstate", { state: first });
  h.ui.visit(3);
  assert.deepEqual(Array.from(h.state.navHistory), [0, 1, 3]);
  h.ui.updateNavButtons();
  assert.equal(h.byId("forwardButton").disabled, true);
});

test("old scan sessions and removed forward entries cannot navigate", async () => {
  const h = await harness();
  h.ui.visit(1);
  const entry = h.calls.at(-1).args[0];
  h.ui.visit(2);
  const removed = h.calls.at(-1).args[0];
  await h.window.emit("popstate", { state: entry });
  h.ui.trimInvalidForwardNavigation();
  const redraws = h.redraws();
  await h.window.emit("popstate", { state: removed });
  await h.window.emit("popstate", { state: { ...entry, session: 0 } });
  assert.equal(h.state.node_id, 1);
  assert.equal(h.redraws(), redraws);
  assert.equal(h.calls.at(-1).name, "go");
});

test("invalid visits, files and virtual selections do not navigate", async () => {
  const h = await harness();
  for (const id of [null, -1, 0]) h.ui.visit(id);
  for (const rect of [null, { node_id: 2, is_folder: false }, { node_id: 3, is_folder: true, is_small_files: true }]) {
    h.select(rect); h.ui.navigateToSelected();
  }
  assert.equal(h.redraws(), 0);
  assert.equal(h.calls.length, 1);
});

test("free-space toggle rolls back when the backend rejects it", async () => {
  const h = await harness();
  h.byId("toggleFreeSpaceButton").setAttribute("aria-pressed", "true");
  h.failToggle();
  await h.ui.toggleFreeSpace();
  assert.equal(h.byId("toggleFreeSpaceButton").getAttribute("aria-pressed"), "true");
  assert.equal(h.redraws(), 0);
});

for (const selectedIds of [[2], [3], [2, 3]]) {
  test(`Parent button preserves selected nodes ${selectedIds.join(", ")}`, async () => {
    const h = await harness();
    Object.assign(h.state, {
      node_id: 2, navHistory: [0, 1, 2], navIndex: 2,
      rects: [{ node_id: 2, parent_id: 1, is_folder: true }, { node_id: 3, parent_id: 2 }],
      selectedNodeIds: new Set(selectedIds),
    });
    await h.byId("parentButton").emit("click");
    assert.equal(h.state.node_id, 1);
    assert.deepEqual([...h.state.selectedNodeIds], selectedIds);
    assert.equal(h.redraws(), 1);
    assert.equal(h.calls.at(-1).name, "pushState");
  });
}

test("Parent at the root leaves selection and navigation unchanged", async () => {
  const h = await harness();
  h.state.rects = [{ node_id: 0, parent_id: null }, { node_id: 1, parent_id: 0 }];
  h.state.selectedNodeIds = new Set([1]);
  await h.byId("parentButton").emit("click");
  assert.equal(h.state.node_id, 0);
  assert.deepEqual([...h.state.selectedNodeIds], [1]);
  assert.equal(h.redraws(), 0);
});

test("visiting multiple selections does nothing and visiting one folder preserves selection", async () => {
  const h = await harness();
  h.select([{ node_id: 2, is_folder: true }, { node_id: 3, is_folder: true }]);
  h.ui.navigateToSelected();
  assert.equal(h.state.node_id, 0);
  assert.equal(h.redraws(), 0);
  h.select({ node_id: 2, is_folder: true });
  h.ui.navigateToSelected();
  assert.equal(h.state.node_id, 2);
  assert.deepEqual([...h.state.selectedNodeIds], [2]);
});

for (const action of ["root", "visit"]) {
  test(`${action} preserves multiple selected nodes`, async () => {
    const h = await harness();
    h.ui.visit(2);
    h.state.selectedNodeIds = new Set([2, 3]);
    if (action === "root") await h.byId("rootButton").emit("click");
    else h.ui.visit(4);
    assert.equal(h.state.node_id, action === "root" ? 0 : 4);
    assert.deepEqual([...h.state.selectedNodeIds], [2, 3]);
  });
}

test('refresh preserves browser history across repeated node ID remapping', async () => {
  const h = await harness();
  h.ui.visit(1);
  const first = h.calls.at(-1).args[0];
  h.ui.visit(2);
  const second = h.calls.at(-1).args[0];
  h.ui.remapNavigation({ 0: 0, 1: 11, 2: 12 }, 0);
  h.ui.remapNavigation({ 0: 0, 11: 21, 12: 22 }, 0);
  await h.window.emit('popstate', { state: first });
  assert.equal(h.state.node_id, 21);
  await h.window.emit('popstate', { state: second });
  assert.equal(h.state.node_id, 22);
});

test('refresh skips removed history entries and uses a surviving folder for the current view', async () => {
  const h = await harness();
  h.ui.visit(1);
  const removed = h.calls.at(-1).args[0];
  h.ui.visit(2);
  h.ui.remapNavigation({ 0: 0, 1: -1, 2: -1 }, 0);
  assert.equal(h.state.node_id, 0);
  await h.window.emit('popstate', { state: removed });
  assert.equal(h.state.node_id, 0);
  assert.equal(h.calls.at(-1).name, 'go');
  assert.deepEqual(h.calls.at(-1).args, [-1]);
});

test('cancellation rolls browser history back to the pre-scan entry', async () => {
  const h = await harness();
  h.ui.visit(8);
  const previous = h.calls.at(-1).args[0];
  const previousPosition = h.state.browserHistoryPosition;
  h.state.navSession = 2;
  h.state.navHistory = [0]; h.state.navIndex = 0;
  h.ui.pushBrowserHistoryEntry(0, 0);
  h.ui.visit(3);
  Object.assign(h.state, { node_id: 8, navSession: 1, navHistory: [0, 8], navIndex: 1 });
  h.ui.rollbackBrowserHistory(previousPosition);
  assert.equal(h.calls.at(-1).name, 'go');
  assert.deepEqual(h.calls.at(-1).args, [-2]);
  await h.window.emit('popstate', { state: previous });
  assert.equal(h.state.node_id, 8);
  assert.equal(h.state.navIndex, 1);
});

test('home has no enabled navigation after cancelling an initial scan', async () => {
  const h = await harness();
  Object.assign(h.state, { node_id: null, navHistory: [], navIndex: -1, rects: [] });
  h.ui.updateNavButtons();
  for (const id of ['rootButton', 'parentButton', 'backwardButton', 'forwardButton']) assert.equal(h.byId(id).disabled, true);
});


test('Back from Home restores the current scan view without changing selection or history', async () => {
  const h = await harness();
  Object.assign(h.state, { homeVisible: true, node_id: 7, navHistory: [0, 7], navIndex: 1, selectedNodeIds: new Set([9]) });
  h.ui.updateNavButtons();
  assert.equal(h.byId('homeButton').disabled, true);
  assert.equal(h.byId('backwardButton').disabled, false);
  assert.equal(h.byId('rootButton').disabled, true);
  h.ui.goBackward();
  assert.equal(h.state.homeVisible, false);
  assert.equal(h.state.node_id, 7);
  assert.deepEqual([...h.state.selectedNodeIds], [9]);
  assert.equal(h.redraws(), 1);
});
