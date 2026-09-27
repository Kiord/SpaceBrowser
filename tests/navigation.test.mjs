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
  let selected = null;
  let setFreeSpace = async () => {};
  const ui = await loadUI("navigation.js", {
    "./wailsjs/go/main/App.js": { SetShowFreeSpace: value => setFreeSpace(value) },
    "./dom.js": { byId },
    "./controls.js": { addControlEventListeners: noop, eventMatchesShortcut: noop, shortcutCanRun: noop },
    "./logging.js": { logError: noop }, "./state.js": { AppState: state },
  }, { window });
  ui.initNavigation({ redraw: () => { redraws++; }, getSelectedRect: () => selected, isPassiveRect: rect => rect.is_small_files });
  return { ui, byId, window, state, calls, redraws: () => redraws,
    select: rect => { selected = rect; }, failToggle: () => { setFreeSpace = async () => { throw new Error("failed"); }; } };
}

test("back and forward restore matching history entries", async () => {
  const h = await harness();
  h.ui.visit(1);
  const first = h.calls.at(-1).args[0];
  h.ui.visit(2);
  const second = h.calls.at(-1).args[0];
  h.ui.goBackward();
  assert.equal(h.calls.at(-1).name, "back");
  await h.window.emit("popstate", { state: first });
  assert.equal(h.state.node_id, 1);
  assert.equal(h.state.navIndex, 1);
  h.ui.updateNavButtons();
  assert.equal(h.byId("forwardButton").disabled, false);
  h.ui.goForward();
  assert.equal(h.calls.at(-1).name, "forward");
  await h.window.emit("popstate", { state: second });
  assert.equal(h.state.node_id, 2);
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
