import assert from "node:assert/strict";
import test from "node:test";
import { eventTarget, loadUI } from "./helpers/ui.mjs";

test("settings tabs and links work, and General settings save, cancel and restore defaults", async () => {
  const elements = new Map();
  const createElement = () => ({
    ...eventTarget(), value: "", checked: false, selectedOptions: [], dataset: {}, style: {}, open: false,
    get valueAsNumber() { return Number(this.value); },
    append() {}, replaceChildren() {}, setAttribute() {},
    querySelector(selector) { return selector === ".is-invalid" ? null : { textContent: "" }; },
    classList: { add() {}, remove() {}, toggle() {} },
    showModal() { this.open = true; }, close() { this.open = false; },
  });
  const byId = id => {
    if (!elements.has(id)) elements.set(id, createElement());
    return elements.get(id);
  };
  const appearance = { palette: "default", customThemes: [], zoomFactor: 1, boxPadding: 5, cornerRadius: 0, reliefStrength: 0, hoverBrightness: 0, rollOverBoxes: false };
  const defaults = { appearance, minFileSize: 0, updateIncludedScans: true, showBreadcrumbs: true };
  let profile = { ...defaults };
  const saved = [];
  const openedURLs = [];
  const tabs = ["general", "about"].map(name => ({ ...createElement(), dataset: { settingsTab: name } }));
  const panels = ["general", "about"].map(name => ({ ...createElement(), dataset: { settingsPanel: name } }));
  const links = ["https://github.com/Kiord/SpaceBrowser", "https://github.com/Kiord/SpaceBrowser/issues"].map(href => ({ ...eventTarget(), href }));
  const restore = { ...eventTarget(), dataset: { restoreSettings: "general" } };
  const ui = await loadUI("settings.js", {
    "./wailsjs/go/app/App.js": {
      GetCachingEnabled: async () => true, SetCachingEnabled() {}, GetCachedScans: async () => [], ClearCachedScan() {}, ClearAllCachedScans() {},
      GetDefaultProfile: async () => defaults, GetDefaultSettingsPath: async () => "settings.json", GetProfile: async () => profile,
      GetSettingsPath: async () => "settings.json", PickSettingsPath() {}, SetSettingsPath() {},
      SaveSettings: async (value, path) => { if (path === '/blocked') throw new Error('cannot relocate settings'); saved.push(value); profile = value; return { profile: value, path }; },
    },
    "./wailsjs/runtime/runtime.js": { BrowserOpenURL: url => openedURLs.push(url) },
    "./dom.js": { byId, queryAll: selector => ({
      "[data-restore-settings]": [restore], "[data-settings-tab]": tabs,
      "[data-settings-panel]": panels, "#settingsAboutPanel a": links,
    }[selector] || []) },
    "./format.js": { formatSize: String, SIZE_UNITS: { B: 1 }, splitSizeIntoUnit: value => ({ value, unit: "B" }) },
    "./controls.js": { addControlEventListeners() {}, shortcutFromEvent() {} },
    "./logging.js": { logError: (...args) => { throw new Error(args.join(" ")); } },
    "./state.js": { AppState: { profile, defaultProfile: defaults }, AppearanceState: { ...appearance }, PALETTES: { default: ["#000000"] }, SCALE_MAX: 5, SCALE_MIN: .5, getScale: () => 1, setProfiles() {} },
  }, {
    document: { addEventListener() {}, createElement },
    Option: function (label, value) { return { textContent: label, value, dataset: {} }; },
  });
  ui.initSettings({ redraw: async () => {} });
  await byId("settingsButton").emit("click");
  assert.equal(panels[1].hidden, true);
  await tabs[1].emit("click");
  assert.equal(panels[0].hidden, true);
  assert.equal(panels[1].hidden, false);
  let prevented = 0;
  for (const link of links) await link.emit("click", { preventDefault() { prevented++; } });
  assert.equal(prevented, 2);
  assert.deepEqual(openedURLs, links.map(link => link.href));
  assert.equal(byId("settingsDialog").open, true);
  await tabs[0].emit("click");
  assert.equal(panels[1].hidden, true);
  assert.equal(byId("settingsUpdateIncludedScans").checked, true);
  assert.equal(byId("settingsShowBreadcrumbs").checked, true);
  for (const field of ["Icon", "Path", "Size", "ModificationDate", "ScanDate"]) assert.equal(byId(`settingsTooltip${field}`).checked, true);
  for (const field of ["CreationDate", "Counts"]) assert.equal(byId(`settingsTooltip${field}`).checked, false);
  byId("settingsTooltipCreationDate").checked = true;
  byId("settingsUpdateIncludedScans").checked = false;
  byId("settingsShowBreadcrumbs").checked = false;
  await byId("cancelSettingsButton").emit("click");
  assert.equal(saved.length, 0);
  await byId("settingsButton").emit("click");
  assert.equal(byId("settingsUpdateIncludedScans").checked, true);
  assert.equal(byId("settingsShowBreadcrumbs").checked, true);
  assert.equal(byId("settingsTooltipCreationDate").checked, false);
  byId("settingsTooltipCreationDate").checked = true;
  byId("settingsTooltipIcon").checked = false;
  byId("settingsTooltipCounts").checked = true;
  byId("settingsUpdateIncludedScans").checked = false;
  byId("settingsShowBreadcrumbs").checked = false;
  await byId("settingsForm").emit("submit");
  assert.equal(saved.at(-1).updateIncludedScans, false);
  assert.equal(saved.at(-1).showBreadcrumbs, false);
  assert.equal(saved.at(-1).tooltipFields.creationDate, true);
  assert.equal(saved.at(-1).tooltipFields.icon, false);
  assert.equal(saved.at(-1).tooltipFields.counts, true);
  await byId("settingsButton").emit("click");
  assert.equal(byId("settingsUpdateIncludedScans").checked, false);
  assert.equal(byId("settingsShowBreadcrumbs").checked, false);
  assert.equal(byId("settingsTooltipCreationDate").checked, true);
  await restore.emit("click");
  await byId("confirmRestoreDefaultsButton").emit("click");
  assert.equal(byId("settingsUpdateIncludedScans").checked, true);
  assert.equal(byId("settingsShowBreadcrumbs").checked, true);
  assert.equal(byId("settingsTooltipCreationDate").checked, false);
  assert.equal(byId("settingsTooltipIcon").checked, true);
  assert.equal(byId("settingsTooltipCounts").checked, false);
  assert.equal(saved.length, 1);
  await byId("settingsForm").emit("submit");
  assert.equal(saved.at(-1).updateIncludedScans, true);
  assert.equal(saved.at(-1).showBreadcrumbs, true);
  await byId("settingsButton").emit("click");
  byId("settingsAllowPermanentDelete").checked = true;
  byId("settingsConfigPath").value = '/blocked';
  await byId("settingsForm").emit("submit");
  assert.equal(saved.length, 2);
  assert.equal(profile.allowPermanentDelete, false);
  assert.equal(byId("settingsDialog").open, true);
  assert.match(byId("settingsError").textContent, /cannot relocate/);
  await byId("cancelSettingsButton").emit("click");
  await byId("settingsButton").emit("click");
  assert.equal(byId("settingsAllowPermanentDelete").checked, false);
});

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
    "./wailsjs/runtime/runtime.js": { BrowserOpenURL() {} },
    "./wailsjs/go/app/App.js": { GetCachingEnabled: async () => true, SetCachingEnabled() {}, GetCachedScans: async () => [], ClearCachedScan() {}, ClearAllCachedScans() {}, GetDefaultProfile() {}, GetDefaultSettingsPath() {}, GetProfile() {}, GetSettingsPath() {}, PickSettingsPath() {}, SaveSettings() {} },
    "./dom.js": { byId, queryAll: () => [] },
    "./format.js": { formatSize: String, SIZE_UNITS: {}, splitSizeIntoUnit() {} },
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
