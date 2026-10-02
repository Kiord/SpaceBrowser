import assert from "node:assert/strict";
import test from "node:test";
import { deferred, dom, eventTarget, loadUI, noop } from "./helpers/ui.mjs";

async function harness(profile = {}) {
  const { byId } = dom();
  const menuItem = action => {
    const item = byId(`menu-${action}`);
    item.querySelector = () => byId(`label-${action}`);
    return item;
  };
  byId("contextMenu").querySelector = selector => menuItem(selector.match(/data-action="([^"]+)"/)[1]);
  byId("contextMenu").getBoundingClientRect = () => ({ width: 240, height: 320 });
  const state = { node_id: 0, scanRootPath: "D:\\", selectedNodeIds: new Set([1]),
    fileCount: 10, dirCount: 3, profile: { allowDelete: true, platformSystem: "windows", ...profile } };
  const selected = { node_id: 1, parent_id: 0, full_path: "D:\\selected.bin", size: 42 };
  state.rects = [selected];
  const calls = [], errors = [], pending = deferred(), started = deferred();
  const mutation = kind => id => { calls.push([kind, id]); started.resolve(); return pending.promise; };
  const backend = {
      DeleteNodes: async targets => {
        for (const target of targets) calls.push(["delete", target.nodeId]);
        started.resolve();
        return { ...(await pending.promise), deleted: targets.map(target => target.nodeId), failures: [] };
      }, RestoreNode: mutation("restore"),
      GetTrashRestoreInfo: async () => ({ originalPath: "D:\\original.bin" }),
      GetDefaultApplicationName: async () => "Editor", OpenInFileBrowser: noop,
      OpenPath: async path => calls.push(["open", path]), OpenWith: noop, ShowProperties: noop,
    };
  const ui = await loadUI("file-actions.js", {
    "./wailsjs/go/main/App.js": Object.fromEntries(Object.keys(backend).map(key => [key, (...args) => backend[key](...args)])),
    "./dom.js": { byId }, "./format.js": { detailedByteSize: String },
    "./controls.js": { addControlEventListeners: noop, eventMatchesShortcut: noop, shortcutCanRun: noop },
    "./logging.js": { logError: noop },
    "./navigation.js": { trimInvalidForwardNavigation: () => calls.push(["trim"]), updateNavButtons: noop, visit: id => calls.push(["visit", id]) },
    "./notifications.js": { hideRectToast: noop, mousePosition: { x: 0, y: 0 }, showErrorToast: error => errors.push(error), showToastAt: () => noop },
    "./scan.js": { analyze: async () => calls.push(["scan", byId("pathInput").value]), refreshSelectedFolders: async () => calls.push(["refresh"]) },
    "./state.js": { AppState: state },
  }, { window: { ...eventTarget(), innerWidth: 1024, innerHeight: 768 }, requestAnimationFrame: callback => queueMicrotask(callback),
    navigator: { clipboard: { writeText: async text => calls.push(["copy", text]) } } });
  ui.initFileActions({ redraw: async () => calls.push(["redraw"]) });
  const action = name => byId("contextMenu").emit("click", { target: { closest: () => ({ dataset: { action: name } }) } });
  return { ui, menuItem, byId, state, calls, errors, pending, backend, started: started.promise, action,
    select: rect => { state.rects = Array.isArray(rect) ? rect : [rect]; state.selectedNodeIds = new Set(state.rects.map(item => item.node_id)); }, confirm: () => byId("confirmDeleteButton").emit("click") };
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
  assert.equal(h.state.selectedNodeIds.size, 0);
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
  assert.ok(h.state.selectedNodeIds.has(1));
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
    assert.equal(h.errors.length, kind === "virtual" ? 0 : 1);
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

test("batch confirmation lists only top-level targets and keeps the confirmed selection", async () => {
  const h = await harness();
  h.select([
    { node_id: 3, parent_id: 2, full_path: "D:\\folder\\child", size: 2 },
    { node_id: 2, parent_id: 0, full_path: "D:\\folder", is_folder: true, size: 10 },
    { node_id: 4, parent_id: 0, full_path: "D:\\folder-other", size: 20 },
  ]);
  await h.action("delete");
  assert.equal(h.byId("deleteConfirmPath").textContent, "D:\\folder\nD:\\folder-other");
  assert.equal(h.byId("deleteConfirmSize").textContent, "30");
  assert.match(h.byId("deleteConfirmTitle").textContent, /2 items/);
  h.select({ node_id: 99, full_path: "D:\\unrelated" });
  const run = h.confirm();
  await h.started;
  assert.deepEqual(h.calls, [["delete", 2], ["delete", 4]]);
  h.pending.resolve({ fileCount: 5, dirCount: 2 });
  await run;
  assert.equal(h.calls.filter(call => call[0] === "redraw").length, 1);
});

test("opening a group visits every path even if one application fails", async () => {
  const h = await harness();
  h.select([{ node_id: 1, full_path: "D:\\one" }, { node_id: 2, full_path: "D:\\two" }]);
  h.backend.OpenPath = async path => {
    h.calls.push(["open", path]);
    if (path.endsWith("one")) throw new Error("no application");
  };
  await h.action("open-default");
  assert.deepEqual(h.calls, [["open", "D:\\one"], ["open", "D:\\two"]]);
  assert.match(h.byId("fileActionErrors").textContent, /one.*no application/);
  assert.equal(h.byId("fileActionErrorsDialog").open, true);
});

test("visit, chooser, properties and restore do not pick an arbitrary member of a group", async () => {
  const h = await harness();
  h.select([{ node_id: 1, full_path: "D:\\one", is_folder: true }, { node_id: 2, full_path: "D:\\two", is_folder: true }]);
  h.backend.OpenWith = () => assert.fail("chooser invoked for a group");
  h.backend.ShowProperties = () => assert.fail("properties invoked for a group");
  for (const action of ["goto", "open-with", "properties", "restore"]) await h.action(action);
  assert.equal(h.calls.length, 0);
  assert.equal(h.byId("deleteConfirmDialog").open, false);
});

test("partial deletion errors refresh once and identify the failed paths", async () => {
  const h = await harness();
  h.select([{ node_id: 1, full_path: "D:\\one", size: 10 }, { node_id: 2, full_path: "D:\\two", size: 20 }]);
  h.backend.DeleteNodes = async () => ({ deleted: [1], failures: [{ path: "D:\\two", error: "access denied" }], rescanRequired: true });
  await h.action("delete");
  await h.confirm();
  assert.deepEqual(h.calls, [["scan", "D:\\"]]);
  assert.match(h.byId("fileActionErrors").textContent, /two.*access denied/);
  assert.equal(h.byId("fileActionErrorsDialog").open, true);
});

test("mixing Trash roots with other items is rejected before confirmation", async () => {
  const h = await harness();
  h.select([{ node_id: 1, full_path: "D:\\one", size: 1 }, { node_id: 2, full_path: "D:\\$Recycle.Bin", is_trash_root: true, size: 2 }]);
  await h.action("delete");
  assert.equal(h.byId("deleteConfirmDialog").open, false);
  assert.match(String(h.errors[0]), /separately/);
});

test("mixed deletion modes are explicit for each path in confirmation", async () => {
  const h = await harness();
  h.select([{ node_id: 1, full_path: "D:\\one", size: 1 }, { node_id: 2, full_path: "D:\\$Recycle.Bin\\two", is_in_trash: true, size: 2 }]);
  await h.action("delete");
  assert.match(h.byId("deleteConfirmTitle").textContent, /permanently deleted/);
  assert.match(h.byId("deleteConfirmPath").textContent, /Move to Recycle Bin: D:\\one/);
  assert.match(h.byId("deleteConfirmPath").textContent, /Permanently delete: D:\\\$Recycle.Bin\\two/);
});

test("copy writes every selected path on a separate line", async () => {
  const h = await harness();
  h.select([{ node_id: 1, full_path: "first-path" }, { node_id: 2, full_path: "second-path" }]);
  await h.action("copy");
  assert.deepEqual(h.calls, [["copy", "first-path\nsecond-path"]]);
});

test("context menu disables single-item commands for a group", async () => {
  const h = await harness({ allowPermanentDelete: true });
  h.select([{ node_id: 1, full_path: "first", is_folder: true }, { node_id: 2, full_path: "second" }]);
  h.ui.showContextMenu(100, 100);
  for (const action of ["goto", "open-with", "properties"]) assert.ok(h.menuItem(action).classList.contains("disabled"));
  assert.equal(h.menuItem("restore").hidden, true);
  assert.equal(h.menuItem("open-default").hidden, false);
  assert.match(h.byId("label-open-default").textContent, /2 selected/);
  assert.equal(h.byId("label-delete").textContent, "Delete permanently");
  assert.equal(h.byId("label-copy").textContent, "Copy paths");
});

test('folder refresh menu supports one or multiple folders only', async () => {
  const h = await harness();
  const folder = { node_id: 1, is_folder: true, full_path: 'D:\\a' };
  h.select(folder);
  h.ui.showContextMenu(0, 0);
  assert.equal(h.menuItem('refresh').hidden, false);
  await h.action('refresh');
  assert.deepEqual(h.calls, [['refresh']]);
  h.select([folder, { node_id: 2, is_folder: true, full_path: 'D:\\b' }]);
  h.ui.showContextMenu(0, 0);
  assert.equal(h.menuItem('refresh').hidden, false);
  h.state.rects[1].is_folder = false;
  h.ui.showContextMenu(0, 0);
  assert.equal(h.menuItem('refresh').hidden, true);
});
