import assert from "node:assert/strict";
import test from "node:test";
import { deferred, eventTarget, loadUI, noop } from "./helpers/ui.mjs";

async function harness(appearanceOverrides = {}) {
  const window = { ...eventTarget(), devicePixelRatio: 1 };
  const requests = [], painted = [], actions = [], paths = [];
  const appearance = { reliefStrength: 0, cornerRadius: 0, boxPadding: 5, ...appearanceOverrides };
  let time = 1000, frameId = 0;
  const frames = new Map();
  const state = { node_id: 1, scale: 1, rects: [], selectedNodeIds: new Set() };
  for (const name of ["color", "id", "hover", "tmp", "mask", "flash"]) {
    let path = [];
    state[`${name}Ctx`] = { clearRect: noop, strokeRect: noop, save: noop, restore: noop, drawImage: noop,
      beginPath() { path = []; },
      ...Object.fromEntries(['rect', 'roundRect', 'moveTo', 'lineTo', 'arc'].map(op => [op, (...args) => {
        path.push({ op, args: args.map(arg => Array.isArray(arg) ? Array.from(arg) : arg) });
      }])),
      ...Object.fromEntries(['fill', 'stroke', 'clip'].map(op => [op, function (...args) {
        paths.push({ name, op, args, path: [...path], stroke: this.strokeStyle, lineWidth: this.lineWidth });
      }])),
      measureText: text => ({ width: text.length * 6, actualBoundingBoxAscent: 8, actualBoundingBoxDescent: 2 }),
      fillText(text, x, y) { painted.push({ name, text, x, y }); },
      getImageData: x => ({ data: [0, 0, x + 1, 255] }),
      fillRect(...args) { painted.push({ name, args, fill: this.fillStyle }); } };
    state[`${name}Canvas`] = { ...eventTarget(), width: 800, height: 600, style: {},
      parentElement: { getBoundingClientRect: () => ({ width: 800, height: 600 }) },
      getBoundingClientRect: () => ({ left: 0, top: 0 }), getContext: () => state[`${name}Ctx`] };
  }
  const ui = await loadUI("treemap-view.js", {
    "./wailsjs/go/app/App.js": { LayoutWithBoxPadding: (...args) => { const request = deferred(); requests.push({ ...request, args }); return request.promise; } },
    "./file-actions.js": { hideContextMenu: noop, openRectWithDefault: rect => actions.push(["open", rect.node_id]), showContextMenu: () => actions.push(["menu"]) },
    "./format.js": { debounce: fn => fn, formatCompactSize: String, formatCount: String, formatModTime: String, formatSize: String },
    "./navigation.js": { navigateToSelected: () => actions.push(["visit"]), updateNavButtons: noop },
    "./notifications.js": { hideRectToast: noop, initNotifications: noop },
    "./logging.js": { logDebug: noop, logWarning: noop },
    "./state.js": { AppState: state, AppearanceState: appearance, FONT_SIZE: 10, activePalette: () => ["#ffffff"], getScale: () => Math.max(1, window.devicePixelRatio) },
  }, { performance: { now: () => time }, cancelAnimationFrame: id => frames.delete(id), requestAnimationFrame: fn => { frames.set(++frameId, fn); return frameId; },
    window, document: { getElementById: id => state[id] } });
  return { ui, state, requests, painted, actions, paths, appearance, frames, window, advanceFrame(ms) { time += ms; const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(fn => fn(time)); } };
}

const rect = id => ({ node_id: id, parent_id: null, x: id, y: 0, w: 8, h: 8, children: [] });

async function paintLayout(h, rects) {
  const draw = h.ui.redraw();
  h.requests.at(-1).resolve(rects);
  await draw;
}

const rootRect = children => ({ ...rect(0), x: 0, y: 0, w: 400, h: 300, children });
const tile = (id, x, y, w, h, extra = {}) => ({ ...rect(id), parent_id: 0, x, y, w, h, ...extra });
function paintedRadii(h, rect, name = 'color') {
  return h.paths.filter(p => p.name === name && p.op === 'fill').flatMap(p => p.path)
    .find(p => p.op === 'roundRect' && p.args[0] === rect.x && p.args[1] === rect.y && p.args[2] === rect.w && p.args[3] === rect.h)?.args[4] ?? [0, 0, 0, 0];
}

test('only exposed sibling corners round, including one-pixel layout overlaps', async () => {
  for (const overlap of [0, 1]) {
    const h = await harness({ cornerRadius: 8 });
    const left = tile(1, 10, 20, 100 + overlap, 100);
    const right = tile(2, 110, 20, 100, 100, { is_free_space: true });
    const root = { ...rootRect([1, 2]), parent_id: 999 };
    await paintLayout(h, [root, left, right]);
    assert.deepEqual(paintedRadii(h, root), [0, 0, 0, 0], 'the visible root stays square even after navigation');
    assert.deepEqual(paintedRadii(h, left), [8, 0, 0, 8]);
    assert.deepEqual(paintedRadii(h, right), [0, 8, 8, 0]);
  }
});

test('T-junctions and diagonal neighbors keep shared vertices square', async () => {
  const h = await harness({ cornerRadius: 8 });
  const tall = tile(1, 10, 20, 100, 200);
  const top = tile(2, 110, 20, 100, 100);
  const bottom = tile(3, 110, 120, 100, 100, { is_unaccounted: true });
  await paintLayout(h, [rootRect([1, 2, 3]), tall, top, bottom]);
  assert.deepEqual(paintedRadii(h, tall), [8, 0, 0, 8]);
  assert.deepEqual(paintedRadii(h, top), [0, 8, 0, 0]);
  assert.deepEqual(paintedRadii(h, bottom), [0, 0, 8, 0]);

  h.paths.length = 0;
  const diagonal = tile(4, 210, 120, 100, 100);
  await paintLayout(h, [rootRect([1, 2]), top, diagonal]);
  assert.deepEqual(paintedRadii(h, top), [8, 8, 0, 8]);
  assert.deepEqual(paintedRadii(h, diagonal), [0, 8, 8, 8]);
});

test('a surrounded tile stays square, and separate tiles keep all four rounded corners', async () => {
  const h = await harness({ cornerRadius: 6 });
  const tiles = Array.from({ length: 9 }, (_, i) => tile(i + 1, 10 + (i % 3) * 50, 20 + Math.floor(i / 3) * 50, 50, 50));
  await paintLayout(h, [rootRect(tiles.map((_, i) => i + 1)), ...tiles]);
  assert.deepEqual(paintedRadii(h, tiles[4]), [0, 0, 0, 0]);
  assert.deepEqual(paintedRadii(h, tiles[0]), [6, 0, 0, 0]);
  h.paths.length = 0;
  const isolated = tile(10, 200, 20, 50, 50);
  await paintLayout(h, [rootRect([1, 2]), tiles[0], isolated]);
  assert.deepEqual(paintedRadii(h, tiles[0]), [6, 6, 6, 6]);
  assert.deepEqual(paintedRadii(h, isolated), [6, 6, 6, 6]);
});

test('nested tiles, hover holes, flashes, and selection masks share corner geometry', async () => {
  const h = await harness({ cornerRadius: 8, hoverBrightness: .2 });
  const folder = tile(1, 10, 20, 100, 100, { children: [3], is_folder: true, name: 'folder' });
  const neighbor = tile(2, 110, 20, 100, 100);
  const child = tile(3, 20, 40, 80, 70, { parent_id: 1 });
  h.state.liveScanPreview = true;
  h.state.navSession = 1;
  await paintLayout(h, [rootRect([1, 2]), folder, neighbor, child]);
  assert.deepEqual(paintedRadii(h, child), [8, 8, 8, 8], 'ancestors do not count as neighbors');
  const flash = h.paths.find(p => p.name === 'flash' && p.op === 'clip');
  assert.deepEqual(flash.path.filter(p => p.op === 'roundRect').map(p => p.args[4]), [[8, 0, 0, 8], [8, 8, 8, 8]]);
  h.ui.setHoveredRectIndex(1);
  h.advanceFrame(16);
  const hover = h.paths.find(p => p.name === 'hover' && p.op === 'clip');
  assert.deepEqual(hover.path.filter(p => p.op === 'roundRect').map(p => p.args[4]), [[8, 0, 0, 8], [8, 8, 8, 8]]);
  h.ui.selectRectByIndex(1);
  assert.deepEqual(paintedRadii(h, folder, 'tmp'), [8, 0, 0, 8]);
  assert.deepEqual(paintedRadii(h, folder, 'mask'), [8, 0, 0, 8]);
  assert.deepEqual(paintedRadii(h, child, 'mask'), [8, 8, 8, 8]);
});

test('rounded relief follows inset corner arcs while shared edges remain straight', async () => {
  const h = await harness({ cornerRadius: 8, reliefStrength: .3 });
  const left = tile(1, 10, 20, 100, 100);
  const right = tile(2, 110, 20, 100, 100);
  await paintLayout(h, [rootRect([1, 2]), left, right]);
  const clips = h.paths.filter(p => p.name === 'color' && p.op === 'clip');
  assert.equal(clips[0].path[0].op, 'rect', 'outermost relief remains square');
  assert.deepEqual(clips[1].path[0], { op: 'roundRect', args: [11, 21, 98, 98, [7, 0, 0, 7]] });
  const strokes = h.paths.filter(p => p.name === 'color' && p.op === 'stroke');
  const border = strokes.find(p => p.path[0].op === 'roundRect');
  assert.deepEqual(border.path[0].args, [10.5, 20.5, 99, 99, [7.5, 0, 0, 7.5]]);
  const arcs = strokes.flatMap(p => p.path).filter(p => p.op === 'arc');
  assert.ok(arcs.length > 0);
  assert.ok(arcs.every(p => p.args[2] === 6.5), 'relief radius contracts by its 1.5 pixel inset');
  assert.ok(arcs.every(p => p.args[0] < 100 || p.args[0] > 150), 'shared corners have no relief arcs');
});

test('large radii on small tiles stay concentric at different display scales', async () => {
  for (const dpr of [1, 1.5, 2]) {
    const h = await harness({ cornerRadius: 10, reliefStrength: .3 });
    h.window.devicePixelRatio = dpr;
    h.state.scale = dpr * 2;
    h.state.zoomFactor = 2;
    const small = tile(1, 10, 20, 8, 6);
    await paintLayout(h, [rootRect([1]), small]);
    assert.deepEqual(paintedRadii(h, small), [3, 3, 3, 3]);
    for (const paint of h.paths) {
      for (const path of paint.path) {
        if (path.op === 'roundRect') assert.ok(path.args[4].every(radius => radius >= 0));
        if (path.op === 'arc') assert.ok(path.args[2] > 0);
      }
    }
  }
});

test("updated layouts fill the map when unaccounted space is hidden", async () => {
  const h = await harness();
  h.state.profile = { showUnaccounted: true };
  const shown = [
    { ...rect(0), x: 0, y: 0, w: 800, h: 600, children: [1, 2, 3] },
    { ...rect(1), parent_id: 0, x: 5, y: 20, w: 350, h: 570 },
    { ...rect(-2), parent_id: 0, is_unaccounted: true, x: 355, y: 20, w: 150, h: 570 },
    { ...rect(-1), parent_id: 0, is_free_space: true, x: 505, y: 20, w: 290, h: 570 },
  ];
  const first = h.ui.redraw();
  h.requests[0].resolve(shown);
  await first;
  assert.equal(h.painted.some(p => p.fill === "#d6d9dc"), true);
  h.painted.length = 0;
  h.state.profile.showUnaccounted = false;
  const hidden = [
    { ...shown[0], children: [1, 2] },
    { ...shown[1], w: 400 },
    { ...shown[3], x: 405, w: 390 },
  ];
  const second = h.ui.redraw();
  h.requests[1].resolve(hidden);
  await second;
  assert.equal(h.painted.some(p => p.fill === "#d6d9dc"), false);
  assert.equal(h.painted.some(p => p.name === "color" && p.args?.[0] === 5 && p.args[2] === 400), true);
  assert.equal(h.painted.some(p => p.name === "color" && p.args?.[0] === 405 && p.args[2] === 390), true);
  assert.equal(h.state.rects, hidden);
});

test("breadcrumb context selection replaces the group and repaints visible selections", async () => {
  const h = await harness();
  h.state.rects = [rect(2), rect(3)];
  h.state.selectedNodeIds = new Set([2, 3]);
  const ancestor = { ...rect(1), full_path: '/ancestor', is_folder: true };
  h.state.breadcrumbRects = [ancestor];
  h.ui.selectBreadcrumb(ancestor);
  assert.deepEqual([...h.state.selectedNodeIds], [1]);
  assert.equal(h.ui.getSelectedRect().full_path, '/ancestor');
  assert.equal(h.painted.filter(item => item.name === 'tmp').length, 2);
  h.ui.selectBreadcrumb(ancestor);
  assert.deepEqual([...h.state.selectedNodeIds], [1]);
});

test("live labels coalesce frames, crop compositing and skip unchanged markers", async () => {
  const h = await harness();
  Object.assign(h.state, { liveScanPreview: true, scanDots: '.', scanPaused: true,
    rects: Array.from({ length: 100 }, (_, i) => ({ ...rect(i), parent_id: 999, w: 60, h: 20, is_folder: true, scan_incomplete: true, name: 'pending' })) });
  const clears = [], blits = [], clips = [];
  h.state.maskCtx.clearRect = (...args) => clears.push(args);
  h.state.colorCtx.drawImage = (...args) => blits.push(args);
  h.state.tmpCtx.rect = (...args) => clips.push(args);
  h.ui.repaintScanLabels(); h.ui.repaintScanLabels();
  assert.equal(blits.length, 0);
  h.advanceFrame(16);
  assert.equal(blits.length, 100);
  assert.ok(clears.every(([, , w, height]) => w < 100 && height < 30));
  assert.ok(blits.every(args => args.length === 9 && args[3] < 100 && args[4] < 30));
  assert.equal(clips.length, 100);
  assert.ok(clips.every(([, , w, height]) => w < 100 && height < 30));
  h.ui.repaintScanLabels(); h.advanceFrame(16);
  assert.equal(blits.length, 100);
  h.state.scanDots = '..';
  h.ui.repaintScanLabels(); h.advanceFrame(16);
  assert.equal(blits.length, 200);
});

test("moving between display densities keeps layout scale and zoom synchronized", async () => {
  const h = await harness();
  h.state.zoomFactor = 1.5;
  h.window.devicePixelRatio = 2;
  h.ui.resizeCanvas();
  assert.equal(h.state.colorCanvas.width, 1600);
  assert.equal(h.state.scale, 3);
  const redraw = h.ui.redraw();
  assert.equal(h.requests[0].args[3], 3);
  h.requests[0].resolve([]); await redraw;
  h.window.devicePixelRatio = 1;
  h.ui.resizeCanvas();
  assert.equal(h.state.scale, 1.5);
});

test("an older layout response cannot overwrite or paint over the latest redraw", async () => {
  const h = await harness();
  const first = h.ui.redraw(), second = h.ui.redraw();
  const latest = [rect(2)];
  h.requests[1].resolve(latest);
  await second;
  assert.equal(h.requests[1].args[4], 5);
  assert.equal(h.requests[1].args.length, 5);
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
