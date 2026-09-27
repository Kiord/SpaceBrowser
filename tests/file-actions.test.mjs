import assert from "node:assert/strict";
import test from "node:test";
import { deferred, dom, eventTarget, loadUI, noop } from "./helpers/ui.mjs";

async function harness(profile = {}) {
  const { byId } = dom();
  const state = { node_id: 0, scanRootPath: "D:\\", selectedNodeId: 1, selectedRectIndex: 0,
    fileCount: 10, dirCount: 3, profile: { allowDelete: true, platformSystem: "windows", ...profile } };
  let selected = { node_id: 1, parent_id: 0, full_path: "D:\\selected.bin", size: 42 };
  const calls = [], errors = [], pending = deferred(), started = deferred();
  const mutation = kind => id => { calls.push([kind, id]); started.resolve(); return pending.promise; };
  const ui = await loadUI("file-actions.js", {
    "./wailsjs/go/main/App.js": {
      DeleteNode: mutation("delete"), RestoreNode: mutation("restore"),
      GetTrashRestoreInfo: async () => ({ originalPath: "D:\\original.bin" }),
      GetDefaultApplicationName: async () => "Editor", OpenInFileBrowser: noop,
      OpenPath: noop, OpenWith: noop, ShowProperties: noop,
    },
    "./dom.js": { byId }, "./format.js": { detailedByteSize: String },
    "./controls.js": { addControlEventListeners: noop, eventMatchesShortcut: noop, shortcutCanRun: noop },
    "./logging.js": { logError: noop },
    "./navigation.js": { trimInvalidForwardNavigation: () => calls.push(["trim"]), updateNavButtons: noop, visit: noop },
    "./notifications.js": { hideRectToast: noop, mousePosition: { x: 0, y: 0 }, showErrorToast: error => errors.push(error), showToastAt: () => noop },
    "./scan.js": { analyze: async () => calls.push(["scan", byId("pathInput").value]) },
    "./state.js": { AppState: state },
  }, { window: eventTarget(), requestAnimationFrame: callback => queueMicrotask(callback) });
  ui.initFileActions({ redraw: async () => calls.push(["redraw"]), getSelectedRect: () => selected,
    isPassiveRect: rect => rect.is_free_space || rect.is_small_files });
  const action = name => byId("contextMenu").emit("click", { target: { closest: () => ({ dataset: { action: name } }) } });
  return { byId, state, calls, errors, pending, started: started.promise, action,
    select: rect => { selected = rect; }, confirm: () => byId("confirmDeleteButton").emit("click") };
}

test("cancelling confirmation never invokes deletion", async () => {
  const h = await harness();
  await h.action("delete");
  assert.equal(h.byId("deleteConfirmDialog").open, true);
  assert.equal(h.calls.length, 0);
  let prevented = false;
  await h.byId("deleteConfirmDialog").emit("cancel", { preventDefault: () => { prevented = true; } });
  await h.confirm();
  assert.equal(prevented, true);
  assert.equal(h.byId("deleteConfirmDialog").open, false);
  assert.equal(h.calls.length, 0);
});

test("confirmation keeps its original target and prevents duplicate submissions", async () => {
  const h = await harness();
  await h.action("delete");
  h.select({ node_id: 2, full_path: "D:\\another.bin" });
  const run = h.confirm();
  await h.started;
  await h.confirm();
  await h.action("delete");
  assert.deepEqual(h.calls, [["delete", 1]]);
  assert.equal(h.byId("confirmDeleteButton").disabled, true);
  h.pending.resolve({ fileCount: 9, dirCount: 3, rescanRequired: false });
  await run;
  assert.equal(h.state.fileCount, 9);
  assert.equal(h.state.selectedNodeId, null);
  assert.deepEqual(h.calls, [["delete", 1], ["trim"], ["redraw"]]);
  assert.equal(h.byId("confirmDeleteButton").disabled, false);
});

for (const mode of ["backend-required", "user-setting"]) {
  test(`deletion rescans the original scan root when ${mode}`, async () => {
    const h = await harness({ rescanOnDelete: mode === "user-setting" });
    h.byId("pathInput").value = "unrelated input";
    await h.action("delete");
    const run = h.confirm();
    await h.started;
    h.pending.resolve({ fileCount: 9, dirCount: 3, rescanRequired: mode === "backend-required" });
    await run;
    assert.deepEqual(h.calls, [["delete", 1], ["scan", "D:\\"]]);
  });
}

test("failed deletion preserves the view and allows another confirmation", async () => {
  const h = await harness();
  await h.action("delete");
  const run = h.confirm();
  await h.started;
  h.pending.reject(new Error("access denied"));
  await run;
  assert.equal(h.state.fileCount, 10);
  assert.equal(h.state.selectedNodeId, 1);
  assert.deepEqual(h.calls, [["delete", 1]]);
  assert.match(String(h.errors[0]), /access denied/);
  assert.equal(h.byId("confirmDeleteButton").disabled, false);
  assert.equal(h.byId("cancelDeleteButton").disabled, false);
  await h.action("delete");
  assert.equal(h.byId("deleteConfirmDialog").open, true);
});

for (const kind of ["disabled", "root", "current folder", "virtual"]) {
  test(`deletion rejects ${kind} targets before confirmation`, async () => {
    const h = await harness({ allowDelete: kind !== "disabled" });
    if (kind === "root") h.select({ node_id: 0, full_path: "D:\\", parent_id: null });
    if (kind === "current folder") { h.state.node_id = 1; }
    if (kind === "virtual") h.select({ node_id: -1, is_small_files: true });
    await h.action("delete");
    await h.confirm();
    assert.equal(h.byId("deleteConfirmDialog").open, false);
    assert.equal(h.errors.length, 1);
    assert.equal(h.calls.length, 0);
  });
}

test("permanent deletion and empty trash have explicit confirmation labels", async () => {
  const h = await harness({ allowPermanentDelete: true });
  await h.action("delete");
  assert.equal(h.byId("confirmDeleteButton").textContent, "Delete permanently");
  await h.byId("cancelDeleteButton").emit("click");
  h.select({ node_id: 3, is_trash_root: true, full_path: "D:\\$Recycle.Bin", size: 99 });
  await h.action("delete");
  assert.equal(h.byId("confirmDeleteButton").textContent, "Empty");
  assert.match(h.byId("deleteConfirmTitle").textContent, /Empty Recycle Bin/);
});

test("restore confirmation routes to RestoreNode and refreshes when required", async () => {
  const h = await harness();
  h.select({ node_id: 4, is_in_trash: true, full_path: "D:\\$Recycle.Bin\\item", size: 99 });
  await h.action("restore");
  assert.match(h.byId("deleteConfirmPath").textContent, /original.bin/);
  assert.equal(h.byId("confirmDeleteButton").textContent, "Restore");
  const run = h.confirm();
  await h.started;
  h.pending.resolve({ fileCount: 10, dirCount: 3, rescanRequired: true });
  await run;
  assert.deepEqual(h.calls, [["restore", 4], ["scan", "D:\\"]]);
});
