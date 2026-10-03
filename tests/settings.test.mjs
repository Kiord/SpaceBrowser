import assert from "node:assert/strict";
import test from "node:test";
import { eventTarget, loadUI } from "./helpers/ui.mjs";

test("appearance slider drafts are included in the live treemap preview", async () => {
  const values = new Map([
    ["settingsPalette", "ocean"],
    ["settingsZoomFactor", "1.8"],
    ["settingsBoxPadding", "3"],
    ["settingsCornerRadius", "6"],
    ["settingsReliefStrength", "0.24"],
    ["settingsHoverBrightness", "0.17"],
  ]);
  const elements = new Map();
  const byId = id => {
    if (!elements.has(id)) elements.set(id, {
      ...eventTarget(), value: values.get(id) ?? "", selectedOptions: [], hidden: false, checked: false,
      textContent: "", replaceChildren() {}, setAttribute() {}, querySelector() { return { textContent: "" }; },
      classList: { add() {}, remove() {}, toggle() {} }, showModal() {}, close() {},
    });
    return elements.get(id);
  };
  const appearance = { palette: "default", customThemes: [], zoomFactor: 1, boxPadding: 5, cornerRadius: 0, reliefStrength: 0, hoverBrightness: 0, rollOverBoxes: false };
  const appState = { node_id: 0, profile: { appearance }, defaultProfile: { appearance } };
  const ui = await loadUI("settings.js", {
    "./wailsjs/go/main/App.js": { GetDefaultProfile() {}, GetDefaultSettingsPath() {}, GetProfile() {}, GetSettingsPath() {}, PickSettingsPath() {}, SetProfile() {}, SetSettingsPath() {} },
    "./dom.js": { byId, queryAll: () => [] },
    "./format.js": { SIZE_UNITS: {}, splitSizeIntoUnit() {} },
    "./controls.js": { addControlEventListeners() {}, shortcutFromEvent() {} },
    "./logging.js": { logError() {} },
    "./state.js": { AppState: appState, AppearanceState: appearance, PALETTES: { default: ["#000000"], ocean: ["#ffffff"] }, SCALE_MAX: 5, SCALE_MIN: .5, getScale: () => 1, setProfiles() {} },
  }, { document: { addEventListener() {}, createElement: () => ({ style: {}, classList: { add() {}, remove() {}, toggle() {} }, setAttribute() {} }) } });
  ui.initSettings({ redraw: async () => {} });
  await byId("settingsZoomFactor").emit("input");
  const draft = ui.draftAppearance();
  assert.equal(draft.palette, "ocean");
  assert.equal(draft.zoomFactor, 1.8);
  assert.equal(draft.boxPadding, 3);
  assert.equal(draft.cornerRadius, 6);
  assert.equal(draft.reliefStrength, .24);
  assert.equal(draft.hoverBrightness, .17);
});
