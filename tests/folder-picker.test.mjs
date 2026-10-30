import assert from "node:assert/strict";
import test from "node:test";
import { loadUI, dom, deferred } from "./helpers/ui.mjs";

test("Add folder opens the picker once and selects the chosen folder without scanning", async () => {
  const { byId } = dom();
  const pending = deferred();
  const added = [];
  let requests = 0;
  const ui = await loadUI("folder-picker.js", {
    "./path-autocomplete.js": { initPathAutocomplete() {} },
    "./wailsjs/go/app/App.js": { PickFolder: () => { requests++; return pending.promise; } },
    "./dom.js": { byId }, "./logging.js": { logWarning() {} },
    "./notifications.js": { showErrorToast() {} }, "./state.js": { AppState: {} },
  });
  ui.initFolderPicker({ onAdd: path => added.push(path) });
  const first = byId("addFolderButton").emit("click");
  assert.equal(byId("addFolderButton").disabled, true);
  assert.equal(byId("triggerFolderSelectButton").disabled, true);
  await byId("addFolderButton").emit("click");
  assert.equal(requests, 1);
  pending.resolve("/new-folder");
  await first;
  assert.deepEqual(added, ["/new-folder"]);
  assert.equal(byId("pathInput").value, "/new-folder");
  assert.equal(byId("addFolderButton").disabled, false);
  assert.equal(byId("triggerFolderSelectButton").disabled, false);
});
