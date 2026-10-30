import assert from "node:assert/strict";
import test from "node:test";
import { loadUI, dom, eventTarget, deferred } from "./helpers/ui.mjs";
import { readFile } from "node:fs/promises";

test("rectangle tooltip shows its scan date below modification date", async () => {
  const { byId } = dom();
  const rect = { name: "file", full_path: "/folder/file", parent_id: 0, mtime: 1700000000, scan_time: 1800000000, size: 42 };
  const state = { rects: [rect], profile: { showTooltips: true }, colorCanvas: eventTarget() };
  const ui = await loadUI("notifications.js", {
    "./wailsjs/go/app/App.js": { GetAssociatedIcon: async () => "", GetTooltipDetails: async () => ({}) },
    "./dom.js": { byId }, "./state.js": { AppState: state },
    "./format.js": { detailedByteSize: value => `${value} bytes`, formatSize: String },
  }, { window: { innerWidth: 1000, innerHeight: 800, addEventListener() {} }, setTimeout, clearTimeout });
  ui.initNotifications({ getCanvasCoords: () => ({ x: 1, y: 1 }), rectIndexAtPoint: () => 0 });
  ui.updateRectToast({ clientX: 10, clientY: 10 });
  assert.equal(byId("rectToastCreated").textContent, `Modification date : ${new Date(rect.mtime * 1000).toLocaleString()}`);
  assert.equal(byId("rectToastScanned").textContent, `Scan date : ${new Date(rect.scan_time * 1000).toLocaleString()}`);
  const html = await readFile(new URL("../web/index.html", import.meta.url), "utf8");
  assert.match(html, /id="rectToastCreated"><\/span>\s*<span id="rectToastScanned">/);
  state.rects = [{ ...rect, scan_time: rect.scan_time + 100, size: 99 }];
  ui.updateRectToast({ clientX: 11, clientY: 11 });
  assert.equal(byId("rectToastScanned").textContent, `Scan date : ${new Date(state.rects[0].scan_time * 1000).toLocaleString()}`);
  assert.equal(byId("rectToastSize").textContent, "99 (99 bytes)");
  state.rects = [rect];
  ui.hideRectToast();
  delete rect.scan_time;
  delete rect.mtime;
  ui.updateRectToast({ clientX: 10, clientY: 10 });
  assert.equal(byId("rectToastScanned").textContent, "Scan date : unavailable");
  assert.equal(byId("rectToastCreated").textContent, "Modification date : unavailable");
});

async function tooltipHarness(fields, getDetails = async () => ({})) {
  const { byId } = dom();
  const rect = { node_id: 1, name: "folder", full_path: "/folder", parent_id: 0, is_folder: true, size: 42 };
  const state = { rects: [rect], profile: { tooltipFields: fields }, navSession: 1, colorCanvas: eventTarget() };
  let icons = 0;
  const ui = await loadUI("notifications.js", {
    "./wailsjs/go/app/App.js": { GetAssociatedIcon: async () => { icons++; return ""; }, GetTooltipDetails: getDetails },
    "./dom.js": { byId }, "./state.js": { AppState: state },
    "./format.js": { detailedByteSize: String, formatSize: String },
  }, { window: { innerWidth: 1000, innerHeight: 800, addEventListener() {} }, setTimeout, clearTimeout });
  ui.initNotifications({ getCanvasCoords: () => ({ x: 1, y: 1 }), rectIndexAtPoint: () => 0 });
  const hover = () => ui.updateRectToast({ clientX: 10, clientY: 10 });
  return { ui, byId, state, hover, icons: () => icons };
}

test("tooltip defaults keep five fields visible without fetching optional details", async () => {
  let requests = 0;
  const h = await tooltipHarness(undefined, async () => { requests++; return {}; });
  h.hover();
  for (const field of ["Icon", "Path", "Size", "Created", "Scanned"]) assert.equal(h.byId(`rectToast${field}`).hidden, false);
  for (const field of ["CreationDate", "Counts"]) assert.equal(h.byId(`rectToast${field}`).hidden, true);
  assert.equal(requests, 0);
});

test("tooltip toggles fields, displays counts and creation date, and skips hidden icons", async () => {
  const fields = { icon: false, path: false, size: false, modificationDate: false, scanDate: false,
    creationDate: true, counts: true };
  const requests = [];
  const h = await tooltipHarness(fields, async (...args) => { requests.push(args); return {
    creationTime: 1700000000, recursiveFiles: 12, recursiveFolders: 3, directFiles: 5, directFolders: 2,
  }; });
  h.hover();
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(requests, [[1, "/folder", true]]);
  assert.equal(h.icons(), 0);
  assert.equal(h.byId("rectToastPath").hidden, true);
  assert.equal(h.byId("rectToastCreated").hidden, true);
  assert.equal(h.byId("rectToastCreationDate").textContent, `Creation date : ${new Date(1700000000000).toLocaleString()}`);
  assert.equal(h.byId("rectToastCounts").textContent, "5/12 files, 2/3 folders");
  h.state.rects = [{ ...h.state.rects[0], is_folder: false }];
  h.hover();
  assert.equal(h.byId("rectToastCounts").hidden, true);
});

test("late tooltip details cannot overwrite a newer hover or navigation session", async () => {
  const pending = deferred();
  const h = await tooltipHarness({ counts: true }, () => pending.promise);
  h.hover();
  h.state.navSession++;
  pending.resolve({ directFiles: 99 });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.byId("rectToastCounts").textContent, "Files and folders : Loading…");
  h.ui.hideRectToast();
  assert.equal(h.byId("rectToast").hidden, true);
});

test("unavailable details are explicit and disabling every field hides the tooltip", async () => {
  const h = await tooltipHarness({ creationDate: true, counts: true }, async () => { throw Error("unavailable"); });
  h.hover();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.byId("rectToastCreationDate").textContent, "Creation date : unavailable");
  assert.equal(h.byId("rectToastCounts").textContent, "Files and folders : unavailable");
  h.state.profile.tooltipFields = Object.fromEntries(["icon", "path", "size", "modificationDate", "scanDate", "creationDate", "counts"].map(name => [name, false]));
  h.ui.hideRectToast();
  h.hover();
  assert.equal(h.byId("rectToast").hidden, true);
});
