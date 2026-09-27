import assert from "node:assert/strict";
import test from "node:test";
import { deferred, loadUI, noop } from "./helpers/ui.mjs";

async function harness() {
  const requests = [], painted = [];
  const state = { node_id: 1, scale: 1, rects: [], selectedRectIndex: null, selectedNodeId: null };
  for (const name of ["color", "id", "hover"]) {
    state[`${name}Canvas`] = { width: 800, height: 600 };
    state[`${name}Ctx`] = { clearRect: noop, strokeRect: noop, fillRect: (...args) => painted.push({ name, args }) };
  }
  const ui = await loadUI("treemap-view.js", {
    "./wailsjs/go/main/App.js": { Layout: (...args) => { const request = deferred(); requests.push({ ...request, args }); return request.promise; } },
    "./file-actions.js": { hideContextMenu: noop, openRectWithDefault: noop, showContextMenu: noop },
    "./format.js": { debounce: fn => fn, formatCompactSize: String, formatCount: String, formatModTime: String, formatSize: String },
    "./navigation.js": { navigateToSelected: noop, updateNavButtons: noop },
    "./notifications.js": { hideRectToast: noop, initNotifications: noop },
    "./logging.js": { logDebug: noop, logWarning: noop },
    "./state.js": { AppState: state, AppearanceState: { reliefStrength: 0, cornerRadius: 0 }, FONT_SIZE: 10, activePalette: () => ["#ffffff"], getScale: () => 1 },
  }, { performance, cancelAnimationFrame: noop });
  return { ui, state, requests, painted };
}

const rect = id => ({ node_id: id, parent_id: null, x: id, y: 0, w: 8, h: 8, children: [] });

test("an older layout response cannot overwrite or paint over the latest redraw", async () => {
  const h = await harness();
  const first = h.ui.redraw(), second = h.ui.redraw();
  const latest = [rect(2)];
  h.requests[1].resolve(latest);
  await second;
  const paints = h.painted.length;
  h.requests[0].resolve([rect(1)]);
  await first;
  assert.equal(h.state.rects, latest);
  assert.equal(h.painted.length, paints);
  assert.equal(paints, 2);
});

for (const change of ["navigation", "width", "height", "scale", "cleared scan"]) {
  test(`layout response is discarded after ${change}`, async () => {
    const h = await harness();
    const pending = h.ui.redraw();
    if (change === "navigation") h.state.node_id = 9;
    if (change === "width") h.state.colorCanvas.width++;
    if (change === "height") h.state.colorCanvas.height++;
    if (change === "scale") h.state.scale = 2;
    if (change === "cleared scan") h.state.node_id = null;
    h.requests[0].resolve([rect(1)]);
    await pending;
    assert.equal(h.state.rects.length, 0);
    assert.equal(h.painted.length, 0);
  });
}

test("failed and malformed layouts preserve the last displayed rectangles", async () => {
  const h = await harness();
  const previous = [rect(7)];
  h.state.rects = previous;
  const failed = h.ui.redraw();
  h.requests[0].reject(new Error("layout unavailable"));
  await assert.rejects(failed, /layout unavailable/);
  const malformed = h.ui.redraw();
  h.requests[1].resolve(null);
  await malformed;
  assert.equal(h.state.rects, previous);
  assert.equal(h.painted.length, 0);
});

test("redraw without a scan never calls the backend", async () => {
  const h = await harness();
  h.state.node_id = null;
  await h.ui.redraw();
  assert.equal(h.requests.length, 0);
});
