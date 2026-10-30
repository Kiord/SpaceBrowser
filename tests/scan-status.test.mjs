import assert from "node:assert/strict";
import test from "node:test";
import { loadUI, eventTarget, deferred } from "./helpers/ui.mjs";

const element = () => ({ hidden: false, attributes: {}, textContent: "", disabled: false,
  setAttribute(name, value) { this.attributes[name] = value; },
  style: { setProperty(name, value) { this[name] = value; } } });

const states = { idle: "Scan", running: "Pause", paused: "Resume", queued: "Scan", completed: "Rescan", cancelled: "Scan", failed: "Retry" };
test("saved scans show their recorded duration and omit unavailable legacy timing", async () => {
  const ui = await loadUI("scan-status.js", { "./format.js": { formatDuration: String } });
  const refs = Object.fromEntries(["container", "label", "time", "percent", "bar", "progress", "date", "primary", "cancel"].map(name => [name, element()]));
  for (const [cached, elapsedMilliseconds, text] of [
    [true, 12345, "Done in 12.3s"],
    [true, 0, "Done"],
    [true, undefined, "Done"],
    [false, 0, "Done in 0.0s"],
  ]) {
    ui.updateScanStatus(refs, { state: "completed", cached, progress: { elapsedMilliseconds } });
    assert.equal(refs.time.textContent, text);
  }
});

for (const [state, action] of Object.entries(states)) {
  test("tile presents the appropriate action for " + state, async () => {
    const ui = await loadUI("scan-status.js", { "./format.js": { formatDuration: ms => ms + "ms" } });
    const refs = Object.fromEntries(["container", "label", "time", "percent", "bar", "progress", "date", "primary", "cancel"].map(name => [name, element()]));
    ui.updateScanStatus(refs, { state, progress: { elapsedMilliseconds: 1200, fraction: .5 } });
    assert.equal(refs.container.hidden, false);
    assert.equal(refs.primary.textContent, action);
    assert.equal(refs.primary.hidden, state === "queued");
    assert.equal(refs.cancel.hidden, !["running", "paused", "queued"].includes(state));
    assert.equal(refs.progress.hidden, ["idle", "cancelled"].includes(state));
    if (state === "paused") assert.equal(refs.time.textContent, "Paused · 1200ms");
    if (state === "completed") {
      assert.equal(refs.time.textContent, "Done in 1.2s");
      assert.equal(refs.bar.style["--scan-progress"], "100%");
    }
  });
}

async function harness(onScan) {
  const nodes = new Map();
  const makeElement = () => ({ ...element(), ...eventTarget(),
    querySelector(selector) {
      if (!nodes.has(selector)) nodes.set(selector, makeElement());
      return nodes.get(selector);
    } });
  const ui = await loadUI("scan-status.js", { "./format.js": { formatDuration: String } },
    { document: { createElement: makeElement } });
  const calls = [];
  const tile = ui.createTileScanStatus(job => calls.push(["pause", job.id]),
    job => calls.push(["cancel", job.id]), onScan || (job => calls.push(["scan", job.path])));
  return { tile, calls, primary: nodes.get('[data-scan="primary"]'), cancel: nodes.get('[data-scan="cancel"]') };
}

test("one primary button routes to scan, rescan, retry, pause and resume", async () => {
  const h = await harness();
  for (const state of ["idle", "completed", "failed", "cancelled", "running", "paused"]) {
    h.tile.update({ id: 3, path: "/disk", state });
    await h.primary.emit("click");
  }
  assert.deepEqual(h.calls, [["scan", "/disk"], ["scan", "/disk"], ["scan", "/disk"], ["scan", "/disk"], ["pause", 3], ["pause", 3]]);
  h.tile.update({ id: 3, path: "/disk", state: "queued" });
  await h.primary.emit("click");
  await h.cancel.emit("click");
  assert.deepEqual(h.calls.at(-1), ["cancel", 3]);
  h.tile.update({ id: 3, path: "/disk", state: "completed" });
  await h.cancel.emit("click");
  assert.equal(h.calls.length, 7);
});

test("a pending action cannot be submitted again while scan updates arrive", async () => {
  const pending = deferred();
  let requests = 0;
  const h = await harness(() => { requests++; return pending.promise; });
  h.tile.update({ path: "/disk", state: "idle" });
  const first = h.primary.emit("click");
  h.tile.update({ id: 1, path: "/disk", state: "running" });
  assert.equal(h.primary.disabled, true);
  assert.equal(h.cancel.disabled, true);
  await h.primary.emit("click");
  await h.cancel.emit("click");
  assert.equal(requests, 1);
  assert.deepEqual(h.calls, []);
  pending.resolve();
  await first;
  assert.equal(h.primary.disabled, false);
  assert.equal(h.primary.textContent, "Pause");
});
