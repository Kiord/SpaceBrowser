import assert from "node:assert/strict";
import test from "node:test";
import { deferred, eventTarget, loadUI, noop } from "./helpers/ui.mjs";

async function harness() {
  const requests = [], painted = [], actions = [];
  let time = 1000, frameId = 0;
  const frames = new Map();
  const state = { node_id: 1, scale: 1, rects: [], selectedNodeIds: new Set() };
  for (const name of ["color", "id", "hover", "tmp", "mask", "flash"]) {
    state[`${name}Ctx`] = { clearRect: noop, strokeRect: noop, save: noop, restore: noop, drawImage: noop,
      beginPath: noop, rect: noop, clip: noop,
      measureText: text => ({ width: text.length * 6, actualBoundingBoxAscent: 8, actualBoundingBoxDescent: 2 }),
      fillText(text, x, y) { painted.push({ name, text, x, y }); },
      getImageData: x => ({ data: [0, 0, x + 1, 255] }),
      fillRect(...args) { painted.push({ name, args, fill: this.fillStyle }); } };
    state[`${name}Canvas`] = { ...eventTarget(), width: 800, height: 600, style: {},
      parentElement: { getBoundingClientRect: () => ({ width: 800, height: 600 }) },
      getBoundingClientRect: () => ({ left: 0, top: 0 }), getContext: () => state[`${name}Ctx`] };
  }
  const ui = await loadUI("treemap-view.js", {
    "./wailsjs/go/main/App.js": { LayoutWithBoxPadding: (...args) => { const request = deferred(); requests.push({ ...request, args }); return request.promise; } },
    "./file-actions.js": { hideContextMenu: noop, openRectWithDefault: rect => actions.push(["open", rect.node_id]), showContextMenu: () => actions.push(["menu"]) },
    "./format.js": { debounce: fn => fn, formatCompactSize: String, formatCount: String, formatModTime: String, formatSize: String },
    "./navigation.js": { navigateToSelected: () => actions.push(["visit"]), updateNavButtons: noop },
    "./notifications.js": { hideRectToast: noop, initNotifications: noop },
    "./logging.js": { logDebug: noop, logWarning: noop },
    "./state.js": { AppState: state, AppearanceState: { reliefStrength: 0, cornerRadius: 0, boxPadding: 5 }, FONT_SIZE: 10, activePalette: () => ["#ffffff"], getScale: () => 1 },
  }, { performance: { now: () => time }, cancelAnimationFrame: id => frames.delete(id), requestAnimationFrame: fn => { frames.set(++frameId, fn); return frameId; },
    window: { ...eventTarget(), devicePixelRatio: 1 }, document: { getElementById: id => state[id] } });
  return { ui, state, requests, painted, actions, frames, advanceFrame(ms) { time += ms; const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(fn => fn(time)); } };
}

const rect = id => ({ node_id: id, parent_id: null, x: id, y: 0, w: 8, h: 8, children: [] });

test("an older layout response cannot overwrite or paint over the latest redraw", async () => {
  const h = await harness();
  const first = h.ui.redraw(), second = h.ui.redraw();
  const latest = [rect(2)];
  h.requests[1].resolve(latest);
  await second;
  assert.equal(h.requests[1].args.at(-1), 5);
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

test("canvas clicks wire modifier selection and right-click preserves the group", async () => {
  const h = await harness();
  h.ui.initTreemapView();
  h.state.rects = [rect(1), rect(2), rect(3)];
  const canvas = h.state.colorCanvas;
  await canvas.emit("click", { clientX: 0, clientY: 0 });
  assert.deepEqual([...h.state.selectedNodeIds], [1]);
  await canvas.emit("click", { clientX: 1, clientY: 0, ctrlKey: true });
  assert.deepEqual([...h.state.selectedNodeIds], [1, 2]);
  assert.equal(h.ui.getSelectedRect(), null);
  await canvas.emit("contextmenu", { clientX: 0, clientY: 0 });
  assert.deepEqual([...h.state.selectedNodeIds], [1, 2]);
  assert.deepEqual(h.actions, [["menu"]]);
  await canvas.emit("click", { clientX: 2, clientY: 0, metaKey: true });
  assert.deepEqual([...h.state.selectedNodeIds], [1, 2, 3]);
  await canvas.emit("click", { clientX: 1, clientY: 0 });
  assert.deepEqual([...h.state.selectedNodeIds], [2]);
});

test("a redraw preserves and paints every selected node", async () => {
  const h = await harness();
  h.state.selectedNodeIds = new Set([2, 3]);
  const run = h.ui.redraw();
  h.requests[0].resolve([rect(3), rect(2), rect(4)]);
  await run;
  assert.deepEqual([...h.state.selectedNodeIds], [2, 3]);
  assert.deepEqual(h.painted.filter(paint => paint.name === "color").map(paint => paint.fill), ["#000000", "#000000", "#fff"]);
});

test("separate canvas clicks toggle a rectangle off immediately and repaint it", async () => {
  const h = await harness();
  h.ui.initTreemapView();
  h.state.rects = [rect(1)];
  const event = { clientX: 0, clientY: 0, detail: 1 };
  await h.state.colorCanvas.emit("click", event);
  assert.deepEqual([...h.state.selectedNodeIds], [1]);
  await h.state.colorCanvas.emit("click", event);
  assert.deepEqual([...h.state.selectedNodeIds], []);
  assert.equal(h.painted.filter(paint => paint.name === "tmp").at(-1).fill, "#fff");
  assert.deepEqual(h.actions, []);
});

for (const isFolder of [true, false]) {
  test(`double-click ${isFolder ? "visits a folder" : "opens a file"} and keeps it selected`, async () => {
    const h = await harness();
    h.ui.initTreemapView();
    h.state.rects = [{ ...rect(1), is_folder: isFolder }, rect(2)];
    h.state.selectedNodeIds = new Set([2]);
    const canvas = h.state.colorCanvas;
    // Browsers dispatch both clicks before the double-click event.
    await canvas.emit("click", { clientX: 0, clientY: 0, detail: 1 });
    assert.deepEqual([...h.state.selectedNodeIds], [1]);
    await canvas.emit("click", { clientX: 0, clientY: 0, detail: 2 });
    assert.deepEqual([...h.state.selectedNodeIds], [1]);
    await canvas.emit("dblclick", { clientX: 0, clientY: 0, detail: 2 });
    assert.deepEqual(h.actions, [isFolder ? ["visit"] : ["open", 1]]);
    assert.deepEqual([...h.state.selectedNodeIds], [1]);
    const redraw = h.ui.redraw();
    h.requests.at(-1).resolve(h.state.rects);
    await redraw;
    assert.deepEqual([...h.state.selectedNodeIds], [1]);
  });
}

test("modifier double-click does not activate or clear the selection", async () => {
  const h = await harness();
  h.ui.initTreemapView();
  h.state.rects = [rect(1), rect(2)];
  h.state.selectedNodeIds = new Set([1, 2]);
  for (const modifier of ["ctrlKey", "metaKey"]) {
    await h.state.colorCanvas.emit("dblclick", { clientX: 0, clientY: 0, [modifier]: true });
    assert.deepEqual([...h.state.selectedNodeIds], [1, 2]);
  }
  assert.deepEqual(h.actions, []);
});

test('click stays bound to the pressed rectangle when a live layout reorders it', async () => {
  const h = await harness();
  h.ui.initTreemapView();
  h.state.rects = [rect(1), rect(2)];
  await h.state.colorCanvas.emit('pointerdown', { clientX: 0, clientY: 0 });
  h.state.rects = [rect(2), rect(1)];
  await h.state.colorCanvas.emit('click', { clientX: 0, clientY: 0, detail: 1 });
  assert.deepEqual([...h.state.selectedNodeIds], [1]);
});

test('double-click does not open a different item moved under the pointer', async () => {
  const h = await harness();
  h.ui.initTreemapView();
  h.state.rects = [rect(1), rect(2)];
  const event = { clientX: 0, clientY: 0 };
  await h.state.colorCanvas.emit('pointerdown', event);
  await h.state.colorCanvas.emit('click', { ...event, detail: 1 });
  h.state.rects = [rect(2), rect(1)];
  await h.state.colorCanvas.emit('pointerdown', event);
  await h.state.colorCanvas.emit('click', { ...event, detail: 2 });
  await h.state.colorCanvas.emit('dblclick', { ...event, detail: 2 });
  assert.deepEqual(h.actions, []);
  assert.deepEqual([...h.state.selectedNodeIds], [1]);
});

test('new live rectangles flash white for half a second, existing rectangles do not', async () => {
  const h = await harness();
  h.state.liveScanPreview = true;
  h.state.navSession = 1;
  let draw = h.ui.redraw();
  h.requests.at(-1).resolve([rect(1)]);
  await draw;
  draw = h.ui.redraw();
  h.requests.at(-1).resolve([rect(1), rect(2), { ...rect(-1), is_free_space: true }]);
  await draw;
  const flashes = () => h.painted.filter(p => p.name === 'flash');
  assert.equal(flashes().length, 1);
  assert.equal(flashes()[0].args[0], 2);
  assert.equal(flashes()[0].fill, 'rgba(255,255,255,1)');
  h.advanceFrame(250);
  assert.equal(flashes().at(-1).fill, 'rgba(255,255,255,0.5)');
  h.advanceFrame(250);
  assert.equal(h.frames.size, 0);
  const count = flashes().length;
  draw = h.ui.redraw();
  h.requests.at(-1).resolve([rect(1), rect(2)]);
  await draw;
  assert.equal(flashes().length, count);
  h.state.node_id = 2;
  draw = h.ui.redraw();
  h.requests.at(-1).resolve([rect(2), rect(3)]);
  await draw;
  assert.equal(flashes().length, count, 'navigation should not flash existing contents');
});

test('flash stops when a cancelled scan restores another session', async () => {
  const h = await harness();
  h.state.liveScanPreview = true;
  h.state.navSession = 2;
  const draw = h.ui.redraw();
  h.requests.at(-1).resolve([rect(1), rect(2)]);
  await draw;
  assert.equal(h.frames.size, 1);
  const paints = h.painted.length;
  h.state.navSession = 1;
  h.advanceFrame(100);
  assert.equal(h.frames.size, 0);
  assert.equal(h.painted.length, paints);
});

test('scan markers show animated dots for pending nodes and ticks for ready nodes only during previews', async () => {
  const h = await harness();
  const pending = { node_id: 1, scan_incomplete: true };
  const ready = { node_id: 2 };
  const small = { node_id: -1, parent_id: 1, is_small_files: true };
  h.state.rects = [pending, ready, small];
  h.state.liveScanPreview = true;
  for (const dots of ['.', '..', '...', '.']) {
    h.state.scanDots = dots;
    assert.equal(h.ui.scanStatusMarker(pending), ` ${dots}`);
    assert.equal(h.ui.scanStatusMarker(small), ` ${dots}`);
    assert.equal(h.ui.scanStatusMarker(ready), ' \u2713');
  }
  pending.scan_incomplete = false;
  assert.equal(h.ui.scanStatusMarker(small), ' \u2713');
  h.state.liveScanPreview = false;
  for (const node of h.state.rects) assert.equal(h.ui.scanStatusMarker(node), '');
});


test('free-space scan message retains counts and returns to normal after completion', async () => {
  const h = await harness();
  Object.assign(h.state, { liveScanPreview: true, scanDots: '..', fileCount: 12, dirCount: 3 });
  const free = { ...rect(1), w: 400, h: 200, is_free_space: true, scan_incomplete: true, size: 50, disk_total: 100 };
  async function labels() {
    h.painted.length = 0;
    const draw = h.ui.redraw();
    h.requests.at(-1).resolve([free]);
    await draw;
    return h.painted.filter(paint => paint.text).map(paint => paint.text);
  }
  assert.deepEqual(await labels(), ['Scan in progress ..', 'Files: 12', 'Folders: 3']);
  h.state.liveScanPreview = false;
  free.scan_incomplete = false;
  assert.deepEqual(await labels(), ['Free Space: 50.0%', '50 Free', 'Files: 12', 'Folders: 3']);
});
