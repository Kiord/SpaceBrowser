import { GetDefaultProfile, GetProfile, SetProfile } from "./wailsjs/go/main/App.js";
import { byId, queryAll } from "./dom.js";
import { SIZE_UNITS, splitSizeIntoUnit } from "./format.js";
import { shortcutFromEvent } from "./key-bindings.js";
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
let draftKeyBindings = {};
let capturingBindingButton = null;

const KEY_BINDING_LABELS = Object.freeze({
  back: "Back",
  forward: "Forward",
  parent: "Parent folder",
  root: "Go to scan root",
  open: "Open",
  openWith: "Open with...",
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
  return {
    palette,
    zoomFactor: Math.max(SCALE_MIN, Math.min(SCALE_MAX, Number.isFinite(zoom) ? zoom : defaults.zoomFactor)),
    cornerRadius: Math.max(0, Math.min(10, Math.round(Number(source.cornerRadius) || 0))),
    reliefStrength: Math.max(0, Math.min(0.5, Number.isFinite(relief) ? relief : defaults.reliefStrength)),
  };
}

export async function applyAppearance(appearance, redrawNow = true) {
  Object.assign(AppearanceState, normalizedAppearance(appearance));
  AppState.zoomFactor = AppearanceState.zoomFactor;
  AppState.scale = getScale() * AppState.zoomFactor;
  if (redrawNow && AppState.node_id != null) await redraw();
}

function showSettingsTab(name) {
  stopKeyBindingCapture();
  queryAll("[data-settings-tab]").forEach(tab => {
    const active = tab.dataset.settingsTab === name;
    tab.classList.toggle("is-active", active);
    tab.setAttribute("aria-selected", String(active));
  });
  queryAll("[data-settings-panel]").forEach(panel => {
    panel.hidden = panel.dataset.settingsPanel !== name;
  });
}

function normalizedKeyBindings(bindings) {
  const source = bindings || {};
  return Object.fromEntries(Object.keys(KEY_BINDING_LABELS).map(name => [name, String(source[name] || "").trim()]));
}

function renderKeyBindingButton(button) {
  const binding = draftKeyBindings[button.dataset.keyBinding] || "";
  button.querySelector("span").textContent = binding || "Unassigned";
  button.setAttribute("aria-label", `${KEY_BINDING_LABELS[button.dataset.keyBinding]}: ${binding || "Unassigned"}`);
}

function populateKeyBindingsForm(bindings) {
  stopKeyBindingCapture();
  draftKeyBindings = normalizedKeyBindings(bindings);
  queryAll("[data-key-binding]").forEach(renderKeyBindingButton);
}

function stopKeyBindingCapture() {
  if (!capturingBindingButton) return;
  capturingBindingButton.classList.remove("is-capturing");
  renderKeyBindingButton(capturingBindingButton);
  capturingBindingButton = null;
}

function beginKeyBindingCapture(button) {
  stopKeyBindingCapture();
  capturingBindingButton = button;
  button.classList.add("is-capturing");
  button.querySelector("span").textContent = "Press a key...";
  byId("settingsError").textContent = "";
}

function captureKeyBinding(event) {
  if (!capturingBindingButton) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  if (event.repeat) return;
  if (event.key === "Escape") {
    stopKeyBindingCapture();
    return;
  }

  const name = capturingBindingButton.dataset.keyBinding;
  if (event.key === "Backspace" || event.key === "Delete") {
    draftKeyBindings[name] = "";
    stopKeyBindingCapture();
    return;
  }

  const shortcut = shortcutFromEvent(event);
  if (!shortcut) return;
  const conflict = Object.entries(draftKeyBindings).find(([otherName, binding]) => otherName !== name && binding === shortcut);
  if (conflict) {
    byId("settingsError").textContent = `${shortcut} is already assigned to ${KEY_BINDING_LABELS[conflict[0]]}.`;
    stopKeyBindingCapture();
    return;
  }
  draftKeyBindings[name] = shortcut;
  stopKeyBindingCapture();
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
  updatePalettePreview(palette);
  byId("settingsZoomFactorValue").textContent = `${zoom.toFixed(1)}×`;
  byId("settingsCornerRadiusValue").textContent = `${radius.toFixed(0)} px`;
  byId("settingsReliefStrengthValue").textContent = `${(1 + relief).toFixed(2)}×`;
}

function populateAppearanceForm(appearance, useCurrentZoom = true) {
  const values = normalizedAppearance(appearance);
  byId("settingsPalette").value = values.palette;
  byId("settingsZoomFactor").value = String(useCurrentZoom ? (AppState.zoomFactor || values.zoomFactor) : values.zoomFactor);
  byId("settingsCornerRadius").value = String(values.cornerRadius);
  byId("settingsReliefStrength").value = String(values.reliefStrength);
  updateAppearanceFormOutputs();
}

function populateProfileForm(profile, useCurrentZoom = true) {
  byId("settingsPlatform").textContent = profile.platformSystem || "";
  byId("settingsExcludedPaths").value = (profile.excludedPaths || []).join("\n");
  const threshold = splitSizeIntoUnit(profile.minFileSize ?? 0);
  byId("settingsMinFileSize").value = String(threshold.value);
  byId("settingsMinFileSizeUnit").value = threshold.unit;
  byId("settingsMinFileSizeUnit").dataset.previousUnit = threshold.unit;
  byId("settingsSkipHidden").checked = !!profile.skipHidden;
  byId("settingsFollowSymlinks").checked = !!profile.followSymlinks;
  byId("settingsSkipNetworkFS").checked = !!profile.skipNetworkFS;
  byId("settingsAllowDelete").checked = !!profile.allowDelete;
  byId("settingsRescanOnDelete").checked = !!profile.rescanOnDelete;
  populateAppearanceForm(profile.appearance, useCurrentZoom);
  populateKeyBindingsForm(profile.keyBindings);
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
    const [profile] = await Promise.all([GetProfile(), ensureDefaultProfile()]);
    AppState.profile = profile;
    populateProfileForm(profile);
    showSettingsTab("general");
    dialog.showModal();
  } catch (error) {
    console.error("loading settings failed:", error);
  }
}

function closeRestoreDefaultsConfirmation() {
  const dialog = byId("restoreDefaultsDialog");
  if (dialog.open) dialog.close();
}

function closeSettings() {
  stopKeyBindingCapture();
  closeRestoreDefaultsConfirmation();
  const dialog = byId("settingsDialog");
  if (dialog.open) dialog.close();
}

function openRestoreDefaultsConfirmation() {
  const dialog = byId("restoreDefaultsDialog");
  if (!dialog.open) dialog.showModal();
}

async function restoreDefaultSettings() {
  const defaults = await ensureDefaultProfile();
  populateProfileForm(defaults, false);
  byId("settingsError").textContent = "";
  closeRestoreDefaultsConfirmation();
}

async function saveSettings(event) {
  event.preventDefault();
  stopKeyBindingCapture();
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
    rescanOnDelete: byId("settingsRescanOnDelete").checked,
    appearance: {
      palette: byId("settingsPalette").value,
      zoomFactor: Number(byId("settingsZoomFactor").value),
      cornerRadius: Number(byId("settingsCornerRadius").value),
      reliefStrength: Number(byId("settingsReliefStrength").value),
    },
    keyBindings: normalizedKeyBindings(draftKeyBindings),
  };

  const saveButton = event.submitter;
  error.textContent = "";
  if (saveButton) saveButton.disabled = true;
  try {
    await SetProfile(profile);
    AppState.profile = profile;
    dialog.close();
    await applyAppearance(profile.appearance);
  } catch (saveError) {
    error.textContent = String(saveError || "Unable to save settings.");
  } finally {
    if (saveButton) saveButton.disabled = false;
  }
}

function convertSettingsSizeUnit(event) {
  const select = event.currentTarget;
  const input = byId("settingsMinFileSize");
  const oldUnit = select.dataset.previousUnit || "B";
  const bytes = input.valueAsNumber * SIZE_UNITS[oldUnit];
  if (Number.isFinite(bytes)) input.value = String(bytes / SIZE_UNITS[select.value]);
  select.dataset.previousUnit = select.value;
}

export function initSettings(options) {
  redraw = options.redraw;
  byId("settingsButton").addEventListener("click", openSettings);
  byId("settingsForm").addEventListener("submit", saveSettings);
  byId("closeSettingsButton").addEventListener("click", closeSettings);
  byId("cancelSettingsButton").addEventListener("click", closeSettings);
  byId("restoreDefaultsButton").addEventListener("click", openRestoreDefaultsConfirmation);
  byId("cancelRestoreDefaultsButton").addEventListener("click", closeRestoreDefaultsConfirmation);
  byId("confirmRestoreDefaultsButton").addEventListener("click", restoreDefaultSettings);
  byId("restoreDefaultsDialog").addEventListener("cancel", event => {
    event.preventDefault();
    closeRestoreDefaultsConfirmation();
  });
  byId("settingsMinFileSizeUnit").addEventListener("change", convertSettingsSizeUnit);
  queryAll("[data-settings-tab]").forEach(tab => {
    tab.addEventListener("click", () => showSettingsTab(tab.dataset.settingsTab));
  });
  queryAll("[data-key-binding]").forEach(button => {
    button.addEventListener("click", () => beginKeyBindingCapture(button));
  });
  window.addEventListener("keydown", captureKeyBinding, true);
  byId("settingsPalette").addEventListener("change", updateAppearanceFormOutputs);
  byId("settingsZoomFactor").addEventListener("input", updateAppearanceFormOutputs);
  byId("settingsCornerRadius").addEventListener("input", updateAppearanceFormOutputs);
  byId("settingsReliefStrength").addEventListener("input", updateAppearanceFormOutputs);
}
