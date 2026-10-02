import assert from "node:assert/strict";
import test from "node:test";
import { loadUI, eventTarget } from "./helpers/ui.mjs";

async function renderLocation(capacity) {
  const element = () => ({
    ...eventTarget(), children: [], dataset: {}, attributes: {}, hidden: true,
    style: { setProperty(name, value) { this[name] = value; } },
    append(...children) { this.children.push(...children); },
    replaceChildren() { this.children = []; },
    setAttribute(name, value) { this.attributes[name] = value; },
  });
  const elements = new Map();
  const byId = id => {
    if (!elements.has(id)) elements.set(id, element());
    return elements.get(id);
  };
  const ui = await loadUI("locations.js", {
    "./wailsjs/go/main/App.js": { GetScanLocations: async () => [{ name: "Disk", path: "/volume", ...capacity }] },
    "./dom.js": { byId }, "./folder-picker.js": { chooseFolder() {} },
    "./logging.js": { logError() {} }, "./format.js": { formatSize: size => `${size} B` },
  }, { document: { createElement: element } });
  let scans = 0;
  ui.initLocationSelector({ analyze: async () => { scans++; } });
  await new Promise(resolve => setImmediate(resolve));
  const tile = byId("locationList").children[0];
  return { tile, usage: tile.children[1].children[2], byId, scans: () => scans };
}

for (const [used, level] of [[0, "normal"], [79.9, "normal"], [80, "warning"], [94.9, "warning"], [95, "critical"], [100, "critical"]]) {
  test(`volume tile displays ${used}% fullness as ${level}`, async () => {
    const h = await renderLocation({ diskTotal: 1000, diskFree: 1000 - used * 10 });
    const [label, bar] = h.usage.children;
    assert.equal(label.textContent, `${used * 10} B / 1000 B (${used.toFixed(1)}%)`);
    assert.equal(bar.dataset.level, level);
    assert.ok(Math.abs(Number(bar.attributes['aria-valuenow']) - used) < 0.0001);
    assert.ok(Math.abs(parseFloat(bar.children[0].style['--scan-progress']) - used) < 0.0001);
    await h.tile.emit('click');
    assert.equal(h.byId('pathInput').value, '/volume');
    assert.equal(h.scans(), 1);
  });
}

for (const capacity of [{}, { diskTotal: 0, diskFree: 0 }, { diskTotal: 10 }, { diskTotal: 10, diskFree: -1 }, { diskTotal: 10, diskFree: 11 }]) {
  test(`volume tile omits unavailable or invalid capacity: ${JSON.stringify(capacity)}`, async () => {
    const h = await renderLocation(capacity);
    assert.equal(h.usage, undefined);
    assert.equal(h.tile.children[1].children[0].textContent, 'Disk');
  });
}
