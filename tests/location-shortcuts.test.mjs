import assert from "node:assert/strict";
import test from "node:test";
import { eventTarget, loadUI } from "./helpers/ui.mjs";

async function harness(initiallyCollapsed = true) {
  const window = eventTarget();
  let collapsed = initiallyCollapsed, dialog = false, hasLocations = true;
  const changes = [], directions = [];
  const ui = await loadUI("location-shortcuts.js", {
    "./locations.js": { cycleLocation: direction => { directions.push(direction); return hasLocations; } },
  }, { window, document: { querySelector: () => dialog } });
  const controls = ui.initLocationShortcuts({ isCollapsed: () => collapsed, setCollapsed: value => { collapsed = value; changes.push(value); } });
  return { window, changes, directions, controls, collapsed: () => collapsed,
    setDialog: value => { dialog = value; }, setEmpty: () => { hasLocations = false; },
    async press(extra = {}) {
      let prevented = false, stopped = false;
      await window.emit("keydown", { key: "Tab", ctrlKey: true, preventDefault() { prevented = true; }, stopImmediatePropagation() { stopped = true; }, ...extra });
      return { prevented, stopped };
    } };
}

test('hidden locations stay visible through cycling until Control is released', async () => {
  const h = await harness();
  assert.deepEqual(await h.press(), { prevented: true, stopped: true });
  await h.press({ shiftKey: true });
  await h.window.emit('keyup', { key: 'Tab', ctrlKey: true });
  await h.window.emit('keyup', { key: 'Shift', ctrlKey: true });
  assert.equal(h.collapsed(), false);
  assert.deepEqual(h.changes, [false]);
  assert.deepEqual(h.directions, [1, -1]);
  await h.window.emit('keyup', { key: 'Control', ctrlKey: false });
  assert.deepEqual(h.changes, [false, true]);
});

test('visible locations remain visible and focus loss restores a hidden panel', async () => {
  const visible = await harness(false);
  await visible.press();
  await visible.window.emit('blur');
  assert.deepEqual(visible.changes, []);
  const hidden = await harness();
  await hidden.press();
  await hidden.window.emit('blur');
  assert.deepEqual(hidden.changes, [false, true]);
  await hidden.window.emit('keyup', { key: 'Control' });
  assert.deepEqual(hidden.changes, [false, true]);
});

test('cycling ignores dialogs and other combinations and handles empty locations', async () => {
  const h = await harness();
  for (const extra of [{ ctrlKey: false }, { altKey: true }, { metaKey: true }, { isComposing: true }]) {
    assert.equal((await h.press(extra)).prevented, false);
  }
  h.setDialog(true);
  assert.equal((await h.press()).prevented, false);
  assert.deepEqual(h.changes, []);
  h.setDialog(false);
  h.setEmpty();
  await h.press();
  assert.equal(h.collapsed(), true);
});

test('explicit visibility changes supersede temporary cycling visibility', async () => {
  const h = await harness();
  await h.press();
  h.controls.cancelPreview();
  await h.window.emit('blur');
  assert.deepEqual(h.changes, [false]);
});
