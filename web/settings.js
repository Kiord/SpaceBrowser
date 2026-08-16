import {
  GetDefaultProfile,
  GetDefaultSettingsPath,
  GetProfile,
  GetSettingsPath,
  PickSettingsPath,
  SetProfile,
  SetSettingsPath,
} from "./wailsjs/go/main/App.js";
import { byId, queryAll } from "./dom.js";
import { SIZE_UNITS, splitSizeIntoUnit } from "./format.js";
import { shortcutFromEvent } from "./controls.js";
import { logError } from "./logging.js";
import {
  AppState,
  AppearanceState,
  PALETTES,
  SCALE_MAX,
  SCALE_MIN,
  getScale,
  setProfiles,
} from "./state.js";

let redraw = async () => {};
let draftControlBindings = {};
let capturingBindingButton = null;
let activeSettingsPath = "";
let defaultSettingsPath = "";
let pendingRestoreTab = "";

const SETTINGS_TAB_LABELS = Object.freeze({
  general: "General",
  appearance: "Appearance",
  controls: "Controls",
  misc: "Misc",
});

const CONTROL_BINDING_LABELS = Object.freeze({
  back: "Back",
  forward: "Forward",
  parent: "Parent folder",
  root: "Go to scan root",
  open: "Open",
  openWith: "Open with...",
  visitSelected: "Visit selected",
  delete: "Delete",
});

function defaultAppearance() {
  return AppState.defaultProfile?.appearance || AppearanceState;
}

export function normalizedAppearance(appearance) {
  const defaults = defaultAppearance();
  const source = appearance || defaults;
  const palette = PALETTES[source.palette] ? source.palette : defaults.palette;
  const zoom = Number(source.zoomFactor);
  const relief = Number(source.reliefStrength);
  const hoverBrightness = Number(source.hoverBrightness);
  return {
    palette,
    zoomFactor: Math.max(SCALE_MIN, Math.min(SCALE_MAX, Number.isFinite(zoom) ? zoom : defaults.zoomFactor)),
    cornerRadius: Math.max(0, Math.min(10, Math.round(Number(source.cornerRadius) || 0))),
    reliefStrength: Math.max(0, Math.min(0.5, Number.isFinite(relief) ? relief : defaults.reliefStrength)),
    hoverBrightness: Math.max(0, Math.min(0.3, Number.isFinite(hoverBrightness) ? hoverBrightness : defaults.hoverBrightness)),
  };
}

export async function applyAppearance(appearance, redrawNow = true) {
  Object.assign(AppearanceState, normalizedAppearance(appearance));
  AppState.zoomFactor = AppearanceState.zoomFactor;
  AppState.scale = getScale() * AppState.zoomFactor;
  if (redrawNow && AppState.node_id != null) await redraw();
}

function showSettingsTab(name) {
  stopControlBindingCapture();
  queryAll("[data-settings-tab]").forEach(tab => {
    const active = tab.dataset.settingsTab === name;
    tab.classList.toggle("is-active", active);
    tab.setAttribute("aria-selected", String(active));
  });
  queryAll("[data-settings-panel]").forEach(panel => {
    panel.hidden = panel.dataset.settingsPanel !== name;
  });
}

function normalizedControlBindings(bindings) {
  const source = bindings || {};
  return Object.fromEntries(Object.keys(CONTROL_BINDING_LABELS).map(name => [name, String(source[name] || "").trim()]));
}

function renderControlBindingButton(button) {
  const binding = draftControlBindings[button.dataset.controlBinding] || "";
  button.querySelector("span").textContent = binding || "Unassigned";
  button.setAttribute("aria-label", `${CONTROL_BINDING_LABELS[button.dataset.controlBinding]}: ${binding || "Unassigned"}`);
}

function populateControlBindingsForm(bindings) {
  stopControlBindingCapture();
  draftControlBindings = normalizedControlBindings(bindings);
  queryAll("[data-control-binding]").forEach(renderControlBindingButton);
}

function stopControlBindingCapture() {
  if (!capturingBindingButton) return;
  capturingBindingButton.classList.remove("is-capturing");
  renderControlBindingButton(capturingBindingButton);
  capturingBindingButton = null;
}

function beginControlBindingCapture(button) {
  stopControlBindingCapture();
  capturingBindingButton = button;
  button.classList.add("is-capturing");
  button.querySelector("span").textContent = "Press a key or button...";
  byId("settingsError").textContent = "";
}

function captureControlBinding(event) {
  if (!capturingBindingButton) return;
  const shortcut = shortcutFromEvent(event);
  if (!shortcut) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  if (event.repeat) return;
  const name = capturingBindingButton.dataset.controlBinding;
  const conflict = Object.entries(draftControlBindings).find(([otherName, binding]) => otherName !== name && binding === shortcut);
  if (conflict) {
    byId("settingsError").textContent = `${shortcut} is already assigned to ${CONTROL_BINDING_LABELS[conflict[0]]}.`;
    stopControlBindingCapture();
    return;
  }
  draftControlBindings[name] = shortcut;
  stopControlBindingCapture();
}

function clearControlBinding(button) {
  stopControlBindingCapture();
  const name = button.dataset.clearControlBinding;
  draftControlBindings[name] = "";
  renderControlBindingButton(byId(`controlBinding${name[0].toUpperCase()}${name.slice(1)}`));
  byId("settingsError").textContent = "";
}

function updatePalettePreview(paletteName) {
  byId("settingsPalettePreview").replaceChildren(...(PALETTES[paletteName] || PALETTES.default).map(color => {
    const swatch = document.createElement("span");
    swatch.style.backgroundColor = color;
    return swatch;
  }));
}

function updateAppearanceFormOutputs() {
  const palette = byId("settingsPalette").value;
  const zoom = Number(byId("settingsZoomFactor").value);
  const radius = Number(byId("settingsCornerRadius").value);
  const relief = Number(byId("settingsReliefStrength").value);
  const hoverBrightness = Number(byId("settingsHoverBrightness").value);
  updatePalettePreview(palette);
  byId("settingsZoomFactorValue").textContent = `${zoom.toFixed(1)}×`;
  byId("settingsCornerRadiusValue").textContent = `${radius.toFixed(0)} px`;
  byId("settingsReliefStrengthValue").textContent = `${(1 + relief).toFixed(2)}×`;
  byId("settingsHoverBrightnessValue").textContent = `${(1 + hoverBrightness).toFixed(2)}×`;
}

function populateAppearanceForm(appearance, useCurrentZoom = true) {
  const values = normalizedAppearance(appearance);
  byId("settingsPalette").value = values.palette;
  byId("settingsZoomFactor").value = String(useCurrentZoom ? (AppState.zoomFactor || values.zoomFactor) : values.zoomFactor);
  byId("settingsCornerRadius").value = String(values.cornerRadius);
  byId("settingsReliefStrength").value = String(values.reliefStrength);
  byId("settingsHoverBrightness").value = String(values.hoverBrightness);
  updateAppearanceFormOutputs();
}

function populateGeneralForm(profile) {
  byId("settingsPlatform").textContent = profile.platformSystem || "";
  byId("settingsExcludedPaths").value = (profile.excludedPaths || []).join("\n");
  const threshold = splitSizeIntoUnit(profile.minFileSize ?? 0);
  byId("settingsMinFileSize").value = String(threshold.value);
  byId("settingsMinFileSizeUnit").value = threshold.unit;
  byId("settingsSkipHidden").checked = !!profile.skipHidden;
  byId("settingsFollowSymlinks").checked = !!profile.followSymlinks;
  byId("settingsSkipNetworkFS").checked = !!profile.skipNetworkFS;
  byId("settingsAllowDelete").checked = !!profile.allowDelete;
  byId("settingsAllowPermanentDelete").checked = !!profile.allowPermanentDelete;
  byId("settingsRescanOnDelete").checked = !!profile.rescanOnDelete;
}

function populateProfileForm(profile, useCurrentZoom = true) {
  populateGeneralForm(profile);
  populateAppearanceForm(profile.appearance, useCurrentZoom);
  populateControlBindingsForm(profile.controls);
}

function populateMiscForm(settingsPath, defaultPath) {
  activeSettingsPath = String(settingsPath || "");
  defaultSettingsPath = String(defaultPath || "");
  const input = byId("settingsConfigPath");
  input.value = activeSettingsPath;
  input.title = activeSettingsPath;
}

async function ensureDefaultProfile() {
  if (!AppState.defaultProfile) AppState.defaultProfile = await GetDefaultProfile();
  return AppState.defaultProfile;
}

export async function loadSettingsState() {
  const [defaultProfile, profile] = await Promise.all([GetDefaultProfile(), GetProfile()]);
  setProfiles(profile, defaultProfile);
  await applyAppearance(profile.appearance, false);
}

async function openSettings() {
  const dialog = byId("settingsDialog");
  if (dialog.open) return;

  byId("settingsError").textContent = "";
  try {
    const [profile, , settingsPath, defaultPath] = await Promise.all([
      GetProfile(),
      ensureDefaultProfile(),
      GetSettingsPath(),
      GetDefaultSettingsPath(),
    ]);
    AppState.profile = profile;
    populateProfileForm(profile);
    populateMiscForm(settingsPath, defaultPath);
    showSettingsTab("general");
    dialog.showModal();
  } catch (error) {
    logError("loading settings failed:", error);
  }
}

function closeRestoreDefaultsConfirmation() {
  const dialog = byId("restoreDefaultsDialog");
  if (dialog.open) dialog.close();
  pendingRestoreTab = "";
}

function closeSettings() {
  stopControlBindingCapture();
  closeRestoreDefaultsConfirmation();
  const dialog = byId("settingsDialog");
  if (dialog.open) dialog.close();
}

function openRestoreDefaultsConfirmation(tabName) {
  if (tabName === "all") {
    pendingRestoreTab = tabName;
    byId("restoreDefaultsTitle").textContent = "Restore all defaults?";
    byId("restoreDefaultsMessage").textContent = "All settings tabs will be reset. Changes are applied only after you save.";
    const dialog = byId("restoreDefaultsDialog");
    if (!dialog.open) dialog.showModal();
    return;
  }
  const label = SETTINGS_TAB_LABELS[tabName];
  if (!label) return;
  pendingRestoreTab = tabName;
  byId("restoreDefaultsTitle").textContent = `Restore ${label} default?`;
  byId("restoreDefaultsMessage").textContent = `Only the ${label} tab will be reset. Changes are applied only after you save.`;
  const dialog = byId("restoreDefaultsDialog");
  if (!dialog.open) dialog.showModal();
}

async function restoreDefaultSettings() {
  const tabName = pendingRestoreTab;
  if (!tabName) return;
  const defaults = await ensureDefaultProfile();
  if (tabName === "all") {
    populateProfileForm(defaults, false);
    useDefaultConfigPath();
  } else if (tabName === "general") populateGeneralForm(defaults);
  else if (tabName === "appearance") populateAppearanceForm(defaults.appearance, false);
  else if (tabName === "controls") populateControlBindingsForm(defaults.controls);
  else if (tabName === "misc") useDefaultConfigPath();
  byId("settingsError").textContent = "";
  closeRestoreDefaultsConfirmation();
}

async function saveSettings(event) {
  event.preventDefault();
  stopControlBindingCapture();
  const dialog = byId("settingsDialog");
  const error = byId("settingsError");
  const sizeValue = byId("settingsMinFileSize").valueAsNumber;
  const sizeUnit = byId("settingsMinFileSizeUnit").value;
  const minFileSize = sizeValue * SIZE_UNITS[sizeUnit];

  if (!Number.isFinite(sizeValue) || sizeValue < 0 || !Number.isSafeInteger(minFileSize)) {
    error.textContent = "Small-file threshold must resolve to a non-negative whole number of bytes.";
    return;
  }

  const profile = {
    platformSystem: byId("settingsPlatform").textContent,
    excludedPaths: byId("settingsExcludedPaths").value.split(/\r?\n/).map(path => path.trim()).filter(Boolean),
    skipHidden: byId("settingsSkipHidden").checked,
    minFileSize,
    followSymlinks: byId("settingsFollowSymlinks").checked,
    skipNetworkFS: byId("settingsSkipNetworkFS").checked,
    allowDelete: byId("settingsAllowDelete").checked,
    allowPermanentDelete: byId("settingsAllowPermanentDelete").checked,
    rescanOnDelete: byId("settingsRescanOnDelete").checked,
    appearance: {
      palette: byId("settingsPalette").value,
      zoomFactor: Number(byId("settingsZoomFactor").value),
      cornerRadius: Number(byId("settingsCornerRadius").value),
      reliefStrength: Number(byId("settingsReliefStrength").value),
      hoverBrightness: Number(byId("settingsHoverBrightness").value),
    },
    controls: normalizedControlBindings(draftControlBindings),
  };

  const saveButton = event.submitter;
  error.textContent = "";
  if (saveButton) saveButton.disabled = true;
  try {
    await SetProfile(profile);
    const requestedSettingsPath = byId("settingsConfigPath").value;
    if (requestedSettingsPath && requestedSettingsPath !== activeSettingsPath) {
      await SetSettingsPath(requestedSettingsPath);
      activeSettingsPath = requestedSettingsPath;
    }
    AppState.profile = profile;
    dialog.close();
    await applyAppearance(profile.appearance);
  } catch (saveError) {
    error.textContent = String(saveError || "Unable to save settings.");
  } finally {
    if (saveButton) saveButton.disabled = false;
  }
}

async function browseConfigPath() {
  const error = byId("settingsError");
  error.textContent = "";
  try {
    const path = await PickSettingsPath();
    if (!path) return;
    const input = byId("settingsConfigPath");
    input.value = path;
    input.title = path;
  } catch (browseError) {
    error.textContent = String(browseError || "Unable to choose a configuration file location.");
  }
}

function useDefaultConfigPath() {
  if (!defaultSettingsPath) return;
  const input = byId("settingsConfigPath");
  input.value = defaultSettingsPath;
  input.title = defaultSettingsPath;
  byId("settingsError").textContent = "";
}

export function initSettings(options) {
  redraw = options.redraw;
  byId("settingsButton").addEventListener("click", openSettings);
  byId("settingsForm").addEventListener("submit", saveSettings);
  byId("closeSettingsButton").addEventListener("click", closeSettings);
  byId("cancelSettingsButton").addEventListener("click", closeSettings);
  byId("cancelRestoreDefaultsButton").addEventListener("click", closeRestoreDefaultsConfirmation);
  byId("confirmRestoreDefaultsButton").addEventListener("click", restoreDefaultSettings);
  byId("restoreAllDefaultsButton").addEventListener("click", () => openRestoreDefaultsConfirmation("all"));
  byId("restoreDefaultsDialog").addEventListener("cancel", event => {
    event.preventDefault();
    closeRestoreDefaultsConfirmation();
  });
  byId("browseConfigPathButton").addEventListener("click", browseConfigPath);
  queryAll("[data-restore-settings]").forEach(button => {
    button.addEventListener("click", () => openRestoreDefaultsConfirmation(button.dataset.restoreSettings));
  });
  queryAll("[data-settings-tab]").forEach(tab => {
    tab.addEventListener("click", () => showSettingsTab(tab.dataset.settingsTab));
  });
  queryAll("[data-control-binding]").forEach(button => {
    button.addEventListener("click", () => beginControlBindingCapture(button));
  });
  queryAll("[data-clear-control-binding]").forEach(button => {
    button.addEventListener("click", () => clearControlBinding(button));
  });
  window.addEventListener("keydown", captureControlBinding, true);
  window.addEventListener("mousedown", captureControlBinding, true);
  byId("settingsPalette").addEventListener("change", updateAppearanceFormOutputs);
  byId("settingsZoomFactor").addEventListener("input", updateAppearanceFormOutputs);
  byId("settingsCornerRadius").addEventListener("input", updateAppearanceFormOutputs);
  byId("settingsReliefStrength").addEventListener("input", updateAppearanceFormOutputs);
  byId("settingsHoverBrightness").addEventListener("input", updateAppearanceFormOutputs);
}
