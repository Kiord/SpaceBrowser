import assert from "node:assert/strict";
import test from "node:test";
import { eventTarget, loadUI, noop } from "./helpers/ui.mjs";

async function harness() {
  const root = eventTarget();
  const state = { homeVisible: false, node_id: 1, zoomFactor: 2, scale: 2 };
  const appearance = { zoomFactor: 2 };
  let dialogOpen = false, redraws = 0;
  class HTMLElement { closest() { return true; } }
  const controls = await loadUI("controls.js", {
    "./wailsjs/runtime/runtime.js": { EventsOn: noop },
  }, { HTMLElement, document: { querySelector: () => dialogOpen } });
  const zoom = await loadUI("zoom.js", {
    "./controls.js": { shortcutCanRun: controls.shortcutCanRun },
    "./logging.js": { logDebug: noop },
    "./state.js": { AppState: state, AppearanceState: appearance, SCALE_MIN: .5, SCALE_MAX: 5,
      SCALE_STEP_KEYS: 1.1, SCALE_SMOOTH_BASE: 1.0015, getScale: () => 1 },
  });
  zoom.initZoom({ redraw: async () => { redraws++; } }, root);
  return { root, state, appearance, redraws: () => redraws, input: new HTMLElement(),
    openDialog: () => { dialogOpen = true; } };
}

const events = [
  ["keydown", { ctrlKey: true, key: "+" }],
  ["keydown", { ctrlKey: true, key: "-" }],
  ["keydown", { metaKey: true, key: "0" }],
  ["keydown", { ctrlKey: true, code: "NumpadAdd" }],
  ["wheel", { ctrlKey: true, deltaY: -100, deltaMode: 0 }],
];

for (const context of ["home", "no tree", "dialog", "editable", "composing", "handled"]) {
  test(`zoom shortcuts do not change the treemap in ${context}`, async () => {
    const h = await harness();
    if (context === "home") h.state.homeVisible = true;
    if (context === "no tree") h.state.node_id = null;
    if (context === "dialog") h.openDialog();
    for (const [type, event] of events) {
      await h.root.emit(type, { ...event, target: context === "editable" ? h.input : null,
        isComposing: context === "composing", defaultPrevented: context === "handled" });
    }
    assert.equal(h.state.zoomFactor, 2);
    assert.equal(h.appearance.zoomFactor, 2);
    assert.equal(h.redraws(), 0);
  });
}

test("zoom keyboard and wheel shortcuts still work on a visible treemap", async () => {
  const h = await harness();
  for (const [type, event] of events) await h.root.emit(type, { ...event });
  assert.equal(h.redraws(), events.length);
  assert.equal(h.appearance.zoomFactor, h.state.zoomFactor);
});
