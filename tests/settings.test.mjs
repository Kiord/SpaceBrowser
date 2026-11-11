import assert from "node:assert/strict";
import test from "node:test";
import { eventTarget, loadUI, deferred } from "./helpers/ui.mjs";

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
  const defaults = { platformSystem: "windows", appearance, minFileSize: 0, updateIncludedScans: true, showBreadcrumbs: true };
  let profile = { ...defaults };
  let elevated = true, elevationChecks = 0, restarts = 0, restartError, elevationPending, elevationError;
  const saved = [];
  const openedURLs = [];
  const tabs = ["general", "about"].map(name => ({ ...createElement(), dataset: { settingsTab: name } }));
  const panels = ["general", "about"].map(name => ({ ...createElement(), dataset: { settingsPanel: name } }));
  const links = ["https://github.com/Kiord/SpaceBrowser", "https://github.com/Kiord/SpaceBrowser/issues"].map(href => ({ ...eventTarget(), href }));
  const restore = { ...eventTarget(), dataset: { restoreSettings: "general" } };
  const restoreAdvanced = { ...eventTarget(), dataset: { restoreSettings: "misc" } };
  const ui = await loadUI("settings.js", {
    "./wailsjs/go/app/App.js": {
      GetCachingEnabled: async () => true, SetCachingEnabled() {}, GetCachedScans: async () => [], ClearCachedScan() {}, ClearAllCachedScans() {},
      GetDefaultProfile: async () => defaults, GetDefaultSettingsPath: async () => "settings.json", GetProfile: async () => profile,
      GetSettingsPath: async () => "settings.json", PickSettingsPath() {}, SetSettingsPath() {},
      IsElevated: async () => { elevationChecks++; if (elevationError) throw elevationError; return elevationPending ? elevationPending.promise : elevated; },
      RestartAsAdministrator: async () => { restarts++; if (restartError) throw restartError; },
      SaveSettings: async (value, path) => { if (path === '/blocked') throw new Error('cannot relocate settings'); saved.push(value); profile = value; return { profile: value, path }; },
    },
    "./wailsjs/runtime/runtime.js": { BrowserOpenURL: url => openedURLs.push(url) },
    "./dom.js": { byId, queryAll: selector => ({
      "[data-restore-settings]": [restore, restoreAdvanced], "[data-settings-tab]": tabs,
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
  assert.equal(byId("settingsShowUnaccounted").checked, false);
  assert.equal(byId("settingsScanTiming").checked, false);
  assert.equal(byId("settingsShowScanPreview").checked, true);
  assert.equal(byId("settingsUseMFT").checked, true);
  assert.equal(byId("settingsUseMFTLabel").hidden, false);
  assert.equal(byId("settingsTooltipOptions").disabled, false);
  assert.equal(byId("settingsDeletionOptions").disabled, true);
  byId("settingsShowTooltips").checked = false;
  await byId("settingsShowTooltips").emit("change");
  assert.equal(byId("settingsTooltipOptions").disabled, true);
  byId("settingsShowTooltips").checked = true;
  await byId("settingsShowTooltips").emit("change");
  assert.equal(byId("settingsTooltipOptions").disabled, false);
  byId("settingsRescanOnDelete").checked = true;
  byId("settingsAllowDelete").checked = true;
  await byId("settingsAllowDelete").emit("change");
  assert.equal(byId("settingsDeletionOptions").disabled, false);
  assert.equal(byId("settingsRescanOnDelete").checked, true, 'enabling deletion preserves its dependent options');
  byId("settingsAllowDelete").checked = false;
  await byId("settingsAllowDelete").emit("change");
  assert.equal(byId("settingsDeletionOptions").disabled, true);
  assert.equal(byId("settingsExcludedPathsDetails").open, false);
  assert.equal(byId("settingsExcludedPathsCount").textContent, '(none)');
  byId("settingsExcludedPaths").value = '  /one\n\n/two\n  ';
  await byId("settingsExcludedPaths").emit("input");
  assert.equal(byId("settingsExcludedPathsCount").textContent, '(2)');
  for (const field of ["Icon", "Path", "Size", "ModificationDate", "ScanDate"]) assert.equal(byId(`settingsTooltip${field}`).checked, true);
  for (const field of ["CreationDate", "Counts"]) assert.equal(byId(`settingsTooltip${field}`).checked, false);
  byId("settingsTooltipCreationDate").checked = true;
  byId("settingsUpdateIncludedScans").checked = false;
  byId("settingsShowBreadcrumbs").checked = false;
  byId("settingsShowUnaccounted").checked = true;
  byId("settingsScanTiming").checked = true;
  byId("settingsShowScanPreview").checked = false;
  byId("settingsUseMFT").checked = false;
  await byId("cancelSettingsButton").emit("click");
  assert.equal(saved.length, 0);
  await byId("settingsButton").emit("click");
  assert.equal(byId("settingsUpdateIncludedScans").checked, true);
  assert.equal(byId("settingsShowBreadcrumbs").checked, true);
  assert.equal(byId("settingsShowUnaccounted").checked, false);
  assert.equal(byId("settingsScanTiming").checked, false, 'cancelling discards the timing draft');
  assert.equal(byId("settingsUseMFT").checked, true);
  assert.equal(byId("settingsShowScanPreview").checked, true);
  assert.equal(byId("settingsTooltipCreationDate").checked, false);
  byId("settingsTooltipCreationDate").checked = true;
  byId("settingsTooltipIcon").checked = false;
  byId("settingsTooltipCounts").checked = true;
  byId("settingsUpdateIncludedScans").checked = false;
  byId("settingsShowBreadcrumbs").checked = false;
  byId("settingsShowUnaccounted").checked = true;
  byId("settingsScanTiming").checked = true;
  byId("settingsShowScanPreview").checked = false;
  byId("settingsUseMFT").checked = false;
  await byId("settingsForm").emit("submit");
  assert.equal(saved.at(-1).updateIncludedScans, false);
  assert.equal(saved.at(-1).showBreadcrumbs, false);
  assert.equal(saved.at(-1).showUnaccounted, true);
  assert.equal(saved.at(-1).scanTiming, true);
  assert.equal(saved.at(-1).showScanPreview, false);
  assert.equal(saved.at(-1).useMft, false);
  assert.equal(saved.at(-1).tooltipFields.creationDate, true);
  assert.equal(saved.at(-1).tooltipFields.icon, false);
  assert.equal(saved.at(-1).tooltipFields.counts, true);
  await byId("settingsButton").emit("click");
  assert.equal(byId("settingsUpdateIncludedScans").checked, false);
  assert.equal(byId("settingsShowBreadcrumbs").checked, false);
  assert.equal(byId("settingsShowUnaccounted").checked, true);
  assert.equal(byId("settingsScanTiming").checked, true);
  assert.equal(byId("settingsUseMFT").checked, false);
  assert.equal(byId("settingsShowScanPreview").checked, false);
  assert.equal(byId("settingsTooltipCreationDate").checked, true);
  await restore.emit("click");
  await byId("confirmRestoreDefaultsButton").emit("click");
  assert.equal(byId("settingsUpdateIncludedScans").checked, true);
  assert.equal(byId("settingsShowBreadcrumbs").checked, true);
  assert.equal(byId("settingsShowUnaccounted").checked, false);
  assert.equal(byId("settingsScanTiming").checked, true, 'General defaults leave Advanced diagnostics unchanged');
  assert.equal(byId("settingsUseMFT").checked, true);
  assert.equal(byId("settingsShowScanPreview").checked, true);
  assert.equal(byId("settingsTooltipCreationDate").checked, false);
  assert.equal(byId("settingsTooltipIcon").checked, true);
  assert.equal(byId("settingsTooltipCounts").checked, false);
  assert.equal(saved.length, 1);
  byId("settingsShowBreadcrumbs").checked = false;
  await restoreAdvanced.emit("click");
  assert.equal(byId("restoreDefaultsTitle").textContent, 'Restore Advanced default?');
  await byId("confirmRestoreDefaultsButton").emit("click");
  assert.equal(byId("settingsScanTiming").checked, false, 'Advanced defaults reset scan timing');
  assert.equal(byId("settingsShowBreadcrumbs").checked, false, 'Advanced defaults leave General settings unchanged');
  byId("settingsShowBreadcrumbs").checked = true;
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
  await byId("cancelSettingsButton").emit("click");
  for (const platformSystem of ["linux", "darwin", "windows"]) {
    profile = { ...profile, platformSystem };
    await byId("settingsButton").emit("click");
    assert.equal(byId("settingsUseMFTLabel").hidden, platformSystem !== "windows");
    assert.equal(byId("settingsUseMFT").checked, platformSystem === "windows");
    assert.equal(byId("settingsUseMFTElevationButton").hidden, true);
    await byId("cancelSettingsButton").emit("click");
  }
  elevated = false;
  await byId("settingsButton").emit("click");
  const elevationLink = byId("settingsUseMFTElevationButton");
  assert.equal(byId("settingsUseMFT").checked, true, 'loading preserves the saved preference without elevation');
  assert.equal(elevationLink.hidden, false);
  assert.equal(byId("scanElevationDialog").open, false, 'loading settings does not request a restart');
  byId("settingsShowBreadcrumbs").checked = false;
  await byId("settingsForm").emit("submit");
  assert.equal(saved.at(-1).useMft, true, 'saving an unrelated setting must preserve the NTFS preference');
  assert.equal(restarts, 0);
  await byId("settingsButton").emit("click");
  assert.equal(byId("settingsUseMFT").checked, true);
  await restore.emit("click");
  await byId("confirmRestoreDefaultsButton").emit("click");
  assert.equal(byId("settingsUseMFT").checked, true, 'General defaults restore the preference independently of elevation');
  await byId("restoreAllDefaultsButton").emit("click");
  await byId("confirmRestoreDefaultsButton").emit("click");
  assert.equal(byId("settingsUseMFT").checked, true, 'all defaults also preserve the configured preference');
  assert.equal(elevationLink.hidden, false);
  elevationChecks = 0;
  byId("settingsUseMFT").checked = false;
  await byId("settingsUseMFT").emit("change");
  assert.equal(elevationLink.hidden, true);
  byId("settingsUseMFT").checked = true;
  await byId("settingsUseMFT").emit("change");
  assert.equal(elevationLink.hidden, false);
  assert.equal(elevationChecks, 0, 'changing the preference does not query elevation or request a restart');
  assert.equal(byId("scanElevationDialog").open, false);
  elevationPending = deferred();
  const pendingCheck = elevationLink.emit("click");
  assert.equal(byId("settingsUseMFT").checked, true, 'an elevation check must not change the preference');
  assert.equal(elevationLink.disabled, true);
  await elevationLink.emit("click");
  assert.equal(elevationChecks, 1);
  elevationPending.resolve(false);
  await pendingCheck;
  elevationPending = null;
  assert.equal(elevationLink.disabled, false);
  assert.equal(byId("scanElevationDialog").open, true);
  assert.equal(byId("settingsUseMFT").checked, true);
  await byId("scanElevationDialog").emit("cancel");
  assert.equal(byId("settingsUseMFT").checked, true, 'Escape cancels the restart without disabling the feature');
  await elevationLink.emit("click");
  await byId("cancelScanElevationButton").emit("click");
  assert.equal(byId("settingsUseMFT").checked, true, 'Cancel also preserves the preference');
  assert.equal(byId("scanElevationDialog").open, false);
  assert.equal(restarts, 0);
  const savesBeforeElevation = saved.length;
  await elevationLink.emit("click");
  byId("settingsConfigPath").value = '/blocked';
  await byId("confirmScanElevationButton").emit("click", { currentTarget: byId("confirmScanElevationButton") });
  assert.equal(saved.length, savesBeforeElevation);
  assert.equal(restarts, 0, 'a failed settings save must not restart the application');
  assert.equal(byId("settingsDialog").open, true);
  assert.equal(byId("settingsUseMFT").checked, true, 'a failed save preserves the checkbox draft');
  byId("settingsConfigPath").value = 'settings.json';
  await elevationLink.emit("click");
  restartError = new Error("administrator request cancelled");
  await byId("confirmScanElevationButton").emit("click", { currentTarget: byId("confirmScanElevationButton") });
  assert.equal(saved.length, savesBeforeElevation + 1);
  assert.equal(saved.at(-1).useMft, true);
  assert.equal(restarts, 1);
  assert.equal(byId("settingsDialog").open, true);
  assert.equal(byId("settingsUseMFT").checked, true, 'UAC cancellation does not undo the saved preference');
  assert.equal(elevationLink.hidden, false);
  assert.match(byId("settingsError").textContent, /administrator request cancelled/);
  restartError = null;
  await elevationLink.emit("click");
  await byId("confirmScanElevationButton").emit("click", { currentTarget: byId("confirmScanElevationButton") });
  assert.equal(restarts, 2);
  assert.equal(byId("settingsDialog").open, false);
  elevated = true;
  await byId("settingsButton").emit("click");
  assert.equal(byId("settingsUseMFT").checked, true);
  assert.equal(elevationLink.hidden, true, 'an elevated session does not need a restart link');
  await byId("cancelSettingsButton").emit("click");
  elevated = false;
  await byId("settingsButton").emit("click");
  elevationError = new Error("cannot query session elevation");
  await elevationLink.emit("click");
  assert.equal(byId("settingsUseMFT").checked, true, 'an elevation query failure must preserve the preference');
  assert.equal(elevationLink.disabled, false);
  assert.equal(elevationLink.hidden, false);
  assert.match(byId("settingsError").textContent, /cannot query session elevation/);
  elevationError = null;
  elevationPending = deferred();
  const cancelledCheck = elevationLink.emit("click");
  byId("settingsUseMFT").checked = false;
  await byId("settingsUseMFT").emit("change");
  elevationPending.resolve(false);
  await cancelledCheck;
  elevationPending = null;
  assert.equal(byId("scanElevationDialog").open, false, 'unchecking while elevation is queried cancels the restart prompt');
  assert.equal(elevationLink.hidden, true);
  await byId("settingsForm").emit("submit");
  assert.equal(saved.at(-1).useMft, false, 'an explicit uncheck still persists without elevation');
  assert.equal(restarts, 2);
  await byId("settingsButton").emit("click");
  assert.equal(byId("settingsUseMFT").checked, false);
  assert.equal(elevationLink.hidden, true);
  byId("settingsUseMFT").checked = true;
  await byId("settingsUseMFT").emit("change");
  assert.equal(elevationLink.hidden, false);
  await byId("cancelSettingsButton").emit("click");
  await byId("settingsButton").emit("click");
  assert.equal(byId("settingsUseMFT").checked, false, 'Cancel discards an unsaved preference change');
  assert.equal(elevationLink.hidden, true);
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
    "./wailsjs/go/app/App.js": { GetCachingEnabled: async () => true, SetCachingEnabled() {}, GetCachedScans: async () => [], ClearCachedScan() {}, ClearAllCachedScans() {}, GetDefaultProfile() {}, GetDefaultSettingsPath() {}, GetProfile() {}, GetSettingsPath() {}, IsElevated() {}, RestartAsAdministrator() {}, PickSettingsPath() {}, SaveSettings() {} },
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
