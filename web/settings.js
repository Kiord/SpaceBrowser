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
import { addControlEventListeners, shortcutFromEvent } from "./controls.js";
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
let draftCustomThemes = [];

const MAX_CUSTOM_THEMES = 32;
const MAX_THEME_COLORS = 32;
const BUILTIN_PALETTE_LABELS = Object.freeze({
  default: "Default",
  spacemonger: "SpaceMonger 1.4",
  ocean: "Ocean",
  earth: "Earth",
  retro: "Retro",
});

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
  const customThemes = Array.isArray(source.customThemes) ? source.customThemes.slice(0, MAX_CUSTOM_THEMES).map(theme => ({
    name: String(theme?.name || "").trim(),
    colors: Array.isArray(theme?.colors)
      ? theme.colors.slice(0, MAX_THEME_COLORS).map(normalizeHexColor).filter(Boolean)
      : [],
  })).filter(theme => theme.name && theme.colors.length) : [];
  const palette = (Object.hasOwn(PALETTES, source.palette) || customThemes.some(theme => theme.name === source.palette))
    ? source.palette
    : defaults.palette;
  const zoom = Number(source.zoomFactor);
  const relief = Number(source.reliefStrength);
  const hoverBrightness = Number(source.hoverBrightness);
  return {
    palette,
    customThemes,
    zoomFactor: Math.max(SCALE_MIN, Math.min(SCALE_MAX, Number.isFinite(zoom) ? zoom : defaults.zoomFactor)),
    cornerRadius: Math.max(0, Math.min(10, Math.round(Number(source.cornerRadius) || 0))),
    reliefStrength: Math.max(0, Math.min(0.5, Number.isFinite(relief) ? relief : defaults.reliefStrength)),
    hoverBrightness: Math.max(0, Math.min(0.3, Number.isFinite(hoverBrightness) ? hoverBrightness : defaults.hoverBrightness)),
    rollOverBoxes: !!source.rollOverBoxes,
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

function normalizeHexColor(color) {
  const value = String(color || "").trim().toLowerCase();
  if (/^#[0-9a-f]{6}$/.test(value)) return value;
  const short = value.match(/^#([0-9a-f])([0-9a-f])([0-9a-f])$/);
  return short ? `#${short[1]}${short[1]}${short[2]}${short[2]}${short[3]}${short[3]}` : "";
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

function paletteColors(paletteName) {
  const builtInPalette = Object.hasOwn(PALETTES, paletteName) ? PALETTES[paletteName] : null;
  return builtInPalette || draftCustomThemes.find(theme => theme.name === paletteName)?.colors || PALETTES.default;
}

function updatePalettePreview(paletteName) {
  byId("settingsPalettePreview").replaceChildren(...paletteColors(paletteName).map(color => {
    const swatch = document.createElement("span");
    swatch.style.backgroundColor = color;
    return swatch;
  }));
}

function populatePaletteSelect(selectedPalette) {
  const select = byId("settingsPalette");
  const builtInGroup = document.createElement("optgroup");
  builtInGroup.label = "Built-in";
  Object.entries(BUILTIN_PALETTE_LABELS).forEach(([value, label]) => {
    builtInGroup.append(new Option(label, value));
  });
  select.replaceChildren(builtInGroup);
  if (draftCustomThemes.length) {
    const customGroup = document.createElement("optgroup");
    customGroup.label = "Custom";
    draftCustomThemes.forEach((theme, index) => {
      const option = new Option(theme.name, theme.name);
      option.dataset.customThemeIndex = String(index);
      customGroup.append(option);
    });
    select.append(customGroup);
  }
  select.value = selectedPalette;
  if (!select.value) select.value = "default";
}

function selectedCustomThemeIndex() {
  const value = byId("settingsPalette").selectedOptions[0]?.dataset.customThemeIndex;
  if (value == null) return -1;
  const index = Number(value);
  return Number.isInteger(index) && index >= 0 && index < draftCustomThemes.length ? index : -1;
}

function previewDraftPalette() {
  if (AppState.node_id == null) return;
  void applyAppearance({
    ...(AppState.profile?.appearance || defaultAppearance()),
    palette: byId("settingsPalette").value,
    customThemes: draftCustomThemes,
  });
}

function renderCustomPaletteEditor() {
  const index = selectedCustomThemeIndex();
  const editor = byId("settingsCustomPaletteEditor");
  const deleteButton = byId("settingsDeletePalette");
  editor.hidden = index < 0;
  deleteButton.hidden = index < 0;
  if (index < 0) return;

  const theme = draftCustomThemes[index];
  byId("settingsCustomPaletteName").value = theme.name;
  const list = byId("settingsCustomPaletteColors");
  list.replaceChildren(...theme.colors.map((color, colorIndex) => {
    const row = document.createElement("div");
    row.className = "custom-palette-color";

    const picker = document.createElement("input");
    picker.type = "color";
    picker.value = color;
    picker.setAttribute("aria-label", `Colour ${colorIndex + 1}`);

    const textInput = document.createElement("input");
    textInput.type = "text";
    textInput.value = color;
    textInput.maxLength = 7;
    textInput.spellcheck = false;
    textInput.setAttribute("aria-label", `Colour ${colorIndex + 1} hexadecimal value`);

    const removeButton = document.createElement("button");
    removeButton.type = "button";
    removeButton.className = "custom-palette-color-remove";
    removeButton.textContent = "×";
    removeButton.disabled = theme.colors.length === 1;
    removeButton.setAttribute("aria-label", `Remove colour ${colorIndex + 1}`);

    picker.addEventListener("input", () => {
      theme.colors[colorIndex] = picker.value;
      textInput.value = picker.value;
      updatePalettePreview(theme.name);
      previewDraftPalette();
    });
    textInput.addEventListener("input", () => {
      const normalized = normalizeHexColor(textInput.value);
      textInput.classList.toggle("is-invalid", !normalized);
      if (!normalized) return;
      theme.colors[colorIndex] = normalized;
      picker.value = normalized;
      updatePalettePreview(theme.name);
      previewDraftPalette();
    });
    removeButton.addEventListener("click", () => {
      if (theme.colors.length <= 1) return;
      theme.colors.splice(colorIndex, 1);
      renderCustomPaletteEditor();
      updateAppearanceFormOutputs();
      previewDraftPalette();
    });
    row.append(picker, textInput, removeButton);
    return row;
  }));
  byId("settingsAddPaletteColor").disabled = theme.colors.length >= MAX_THEME_COLORS;
}

function addCustomTheme() {
  if (draftCustomThemes.length >= MAX_CUSTOM_THEMES) {
    byId("settingsError").textContent = `At most ${MAX_CUSTOM_THEMES} custom themes are allowed.`;
    return;
  }
  const usedNames = new Set([...Object.keys(BUILTIN_PALETTE_LABELS), ...draftCustomThemes.map(theme => theme.name.toLowerCase())]);
  let name = "Custom theme";
  for (let suffix = 2; usedNames.has(name.toLowerCase()); suffix++) name = `Custom theme ${suffix}`;
  draftCustomThemes.push({ name, colors: ["#8ea6b4"] });
  byId("settingsError").textContent = "";
  populatePaletteSelect(name);
  updateAppearanceFormOutputs();
  previewDraftPalette();
  byId("settingsCustomPaletteName").focus();
  byId("settingsCustomPaletteName").select();
}

function deleteCustomTheme() {
  const index = selectedCustomThemeIndex();
  if (index < 0) return;
  draftCustomThemes.splice(index, 1);
  byId("settingsError").textContent = "";
  populatePaletteSelect("default");
  updateAppearanceFormOutputs();
  previewDraftPalette();
}

function addCustomThemeColor() {
  const index = selectedCustomThemeIndex();
  if (index < 0 || draftCustomThemes[index].colors.length >= MAX_THEME_COLORS) return;
  const colors = draftCustomThemes[index].colors;
  colors.push(colors.at(-1) || "#8ea6b4");
  byId("settingsError").textContent = "";
  renderCustomPaletteEditor();
  updateAppearanceFormOutputs();
  previewDraftPalette();
}

function renameCustomTheme(event) {
  const index = selectedCustomThemeIndex();
  if (index < 0) return;
  const name = event.currentTarget.value;
  draftCustomThemes[index].name = name;
  byId("settingsError").textContent = "";
  const option = byId("settingsPalette").selectedOptions[0];
  if (option) {
    option.value = name;
    option.textContent = name || "Unnamed theme";
  }
  byId("settingsPalette").value = name;
  updatePalettePreview(name);
  previewDraftPalette();
}

function validateDraftCustomThemes() {
  const reserved = new Set(Object.keys(BUILTIN_PALETTE_LABELS));
  const seen = new Set();
  for (const theme of draftCustomThemes) {
    theme.name = String(theme.name || "").trim();
    if (!theme.name || [...theme.name].length > 64) return "Custom theme names must contain 1 to 64 characters.";
    const foldedName = theme.name.toLowerCase();
    if (reserved.has(foldedName)) return `“${theme.name}” is a reserved built-in theme name.`;
    if (seen.has(foldedName)) return `A custom theme named “${theme.name}” already exists.`;
    seen.add(foldedName);
    if (!theme.colors.length || theme.colors.length > MAX_THEME_COLORS) {
      return `“${theme.name}” must contain 1 to ${MAX_THEME_COLORS} colours.`;
    }
    const colors = theme.colors.map(normalizeHexColor);
    if (colors.some(color => !color)) return `“${theme.name}” contains an invalid hexadecimal colour.`;
    theme.colors = colors;
  }
  return "";
}

function updateAppearanceFormOutputs() {
  const palette = byId("settingsPalette").value;
  const zoom = Number(byId("settingsZoomFactor").value);
  const radius = Number(byId("settingsCornerRadius").value);
  const relief = Number(byId("settingsReliefStrength").value);
  const hoverBrightness = Number(byId("settingsHoverBrightness").value);
  updatePalettePreview(palette);
  renderCustomPaletteEditor();
  byId("settingsZoomFactorValue").textContent = `${zoom.toFixed(1)}×`;
  byId("settingsCornerRadiusValue").textContent = `${radius.toFixed(0)} px`;
  byId("settingsReliefStrengthValue").textContent = `${(1 + relief).toFixed(2)}×`;
  byId("settingsHoverBrightnessValue").textContent = `${(1 + hoverBrightness).toFixed(2)}×`;
}

function populateAppearanceForm(appearance, useCurrentZoom = true) {
  const values = normalizedAppearance(appearance);
  draftCustomThemes = values.customThemes.map(theme => ({ name: theme.name, colors: [...theme.colors] }));
  populatePaletteSelect(values.palette);
  byId("settingsZoomFactor").value = String(useCurrentZoom ? (AppState.zoomFactor || values.zoomFactor) : values.zoomFactor);
  byId("settingsCornerRadius").value = String(values.cornerRadius);
  byId("settingsReliefStrength").value = String(values.reliefStrength);
  byId("settingsHoverBrightness").value = String(values.hoverBrightness);
  byId("settingsRollOverBoxes").checked = values.rollOverBoxes;
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
  byId("settingsUseCache").checked = profile.useCache !== false;
  byId("settingsShowTooltips").checked = profile.showTooltips !== false;
  byId("settingsTooltipDelay").value = String(profile.tooltipDelayMs ?? 0);
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

function closePermanentDeleteWarning() {
  const dialog = byId("permanentDeleteWarningDialog");
  if (dialog.open) dialog.close();
  byId("settingsAllowPermanentDelete").checked = false;
}

function confirmPermanentDelete() {
  byId("settingsAllowPermanentDelete").checked = true;
  const dialog = byId("permanentDeleteWarningDialog");
  if (dialog.open) dialog.close();
}

function requestPermanentDeleteEnable(event) {
  if (!event.currentTarget.checked) return;
  event.currentTarget.checked = false;
  const dialog = byId("permanentDeleteWarningDialog");
  if (!dialog.open) dialog.showModal();
}

function closeSettings() {
  stopControlBindingCapture();
  closeRestoreDefaultsConfirmation();
  closePermanentDeleteWarning();
  const dialog = byId("settingsDialog");
  if (dialog.open) dialog.close();
  if (AppState.profile?.appearance) void applyAppearance(AppState.profile.appearance);
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
    previewDraftPalette();
  } else if (tabName === "general") populateGeneralForm(defaults);
  else if (tabName === "appearance") {
    populateAppearanceForm(defaults.appearance, false);
    previewDraftPalette();
  }
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
  const tooltipDelayMs = byId("settingsTooltipDelay").valueAsNumber;

  if (!Number.isFinite(sizeValue) || sizeValue < 0 || !Number.isSafeInteger(minFileSize)) {
    error.textContent = "Small-file threshold must resolve to a non-negative whole number of bytes.";
    return;
  }
  if (!Number.isInteger(tooltipDelayMs) || tooltipDelayMs < 0 || tooltipDelayMs > 1000) {
    error.textContent = "Tooltip spawn delay must be a whole number between 0 and 1000 milliseconds.";
    return;
  }
  const selectedThemeIndex = selectedCustomThemeIndex();
  if (byId("settingsCustomPaletteColors").querySelector(".is-invalid")) {
    error.textContent = "Enter each colour as #RGB or #RRGGBB.";
    return;
  }
  const themeError = validateDraftCustomThemes();
  if (themeError) {
    error.textContent = themeError;
    return;
  }
  const selectedPalette = selectedThemeIndex >= 0
    ? draftCustomThemes[selectedThemeIndex].name
    : byId("settingsPalette").value;

  const profile = {
    platformSystem: byId("settingsPlatform").textContent,
    excludedPaths: byId("settingsExcludedPaths").value.split(/\r?\n/).map(path => path.trim()).filter(Boolean),
    skipHidden: byId("settingsSkipHidden").checked,
    minFileSize,
    followSymlinks: byId("settingsFollowSymlinks").checked,
    skipNetworkFS: byId("settingsSkipNetworkFS").checked,
    useCache: byId("settingsUseCache").checked,
    showTooltips: byId("settingsShowTooltips").checked,
    tooltipDelayMs,
    allowDelete: byId("settingsAllowDelete").checked,
    allowPermanentDelete: byId("settingsAllowPermanentDelete").checked,
    rescanOnDelete: byId("settingsRescanOnDelete").checked,
    appearance: {
      palette: selectedPalette,
      customThemes: draftCustomThemes.map(theme => ({ name: theme.name, colors: [...theme.colors] })),
      zoomFactor: Number(byId("settingsZoomFactor").value),
      cornerRadius: Number(byId("settingsCornerRadius").value),
      reliefStrength: Number(byId("settingsReliefStrength").value),
      hoverBrightness: Number(byId("settingsHoverBrightness").value),
      rollOverBoxes: byId("settingsRollOverBoxes").checked,
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
  byId("settingsDialog").addEventListener("cancel", event => {
    event.preventDefault();
    closeSettings();
  });
  byId("closeSettingsButton").addEventListener("click", closeSettings);
  byId("cancelSettingsButton").addEventListener("click", closeSettings);
  byId("cancelRestoreDefaultsButton").addEventListener("click", closeRestoreDefaultsConfirmation);
  byId("confirmRestoreDefaultsButton").addEventListener("click", restoreDefaultSettings);
  byId("settingsAllowPermanentDelete").addEventListener("change", requestPermanentDeleteEnable);
  byId("cancelPermanentDeleteButton").addEventListener("click", closePermanentDeleteWarning);
  byId("confirmPermanentDeleteButton").addEventListener("click", confirmPermanentDelete);
  byId("permanentDeleteWarningDialog").addEventListener("cancel", event => {
    event.preventDefault();
    closePermanentDeleteWarning();
  });
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
  addControlEventListeners(captureControlBinding, { capture: true });
  byId("settingsPalette").addEventListener("change", () => {
    updateAppearanceFormOutputs();
    previewDraftPalette();
  });
  byId("settingsAddPalette").addEventListener("click", addCustomTheme);
  byId("settingsDeletePalette").addEventListener("click", deleteCustomTheme);
  byId("settingsAddPaletteColor").addEventListener("click", addCustomThemeColor);
  byId("settingsCustomPaletteName").addEventListener("input", renameCustomTheme);
  byId("settingsZoomFactor").addEventListener("input", updateAppearanceFormOutputs);
  byId("settingsCornerRadius").addEventListener("input", updateAppearanceFormOutputs);
  byId("settingsReliefStrength").addEventListener("input", updateAppearanceFormOutputs);
  byId("settingsHoverBrightness").addEventListener("input", updateAppearanceFormOutputs);
}
