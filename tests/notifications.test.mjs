import assert from "node:assert/strict";
import test from "node:test";
import { loadUI, dom, eventTarget } from "./helpers/ui.mjs";
import { readFile } from "node:fs/promises";

test("rectangle tooltip shows its scan date below modification date", async () => {
  const { byId } = dom();
  const rect = { name: "file", full_path: "/folder/file", parent_id: 0, mtime: 1700000000, scan_time: 1800000000, size: 42 };
  const state = { rects: [rect], profile: { showTooltips: true }, colorCanvas: eventTarget() };
  const ui = await loadUI("notifications.js", {
    "./wailsjs/go/app/App.js": { GetAssociatedIcon: async () => "" },
    "./dom.js": { byId }, "./state.js": { AppState: state },
    "./format.js": { detailedByteSize: value => `${value} bytes`, formatSize: String, formatModTime: String },
  }, { window: { innerWidth: 1000, innerHeight: 800, addEventListener() {} }, setTimeout, clearTimeout });
  ui.initNotifications({ getCanvasCoords: () => ({ x: 1, y: 1 }), rectIndexAtPoint: () => 0 });
  ui.updateRectToast({ clientX: 10, clientY: 10 });
  assert.equal(byId("rectToastCreated").textContent, "Modification date : 1700000000");
  assert.equal(byId("rectToastScanned").textContent, `Scan date : ${new Date(rect.scan_time * 1000).toLocaleString()}`);
  const html = await readFile(new URL("../web/index.html", import.meta.url), "utf8");
  assert.match(html, /id="rectToastCreated"><\/span>\s*<span id="rectToastScanned">/);
  ui.hideRectToast();
  delete rect.scan_time;
  ui.updateRectToast({ clientX: 10, clientY: 10 });
  assert.equal(byId("rectToastScanned").textContent, "Scan date : unavailable");
});
