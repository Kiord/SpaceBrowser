export function getScale() {
  return Math.max(1, window.devicePixelRatio || 1);
}

export const AppState = {
  node_id: null,
  navHistory: [],
  navIndex: -1,
  navSession: 0,
  browserHistoryPosition: 0,
  scanRootPath: "",

  colorCanvas: null,
  colorCtx: null,
  idCanvas: null,
  idCtx: null,
  tmpCanvas: null,
  tmpCtx: null,
  maskCanvas: null,
  maskCtx: null,

  rects: [],
  selectedRectIndex: null,
  selectedNodeId: null,
  profile: null,
  defaultProfile: null,

  zoomFactor: 1,
  scale: getScale(),
  pickingFolderDialogIsOpen: false,
};

export const PALETTES = Object.freeze({
  default: ["#ff9b85", "#ffbe76", "#ffe066", "#7bed9f", "#70d6ff", "#a29bfe", "#dfe4ea"],
  legacy: ["#ff7f7f", "#ffbf7f", "#ffff00", "#7fff7f", "#7fffff", "#bfbfff", "#bfbfbf", "#ff7fff"],
  single: ["#9fc5d8"],
  duotone: ["#f2c078", "#78a6c8"],
  tricolor: ["#e8846b", "#e3bf62", "#78a88b"],
  playful: ["#ff6b6b", "#ffd93d", "#6bcb77", "#4d96ff", "#c77dff", "#ff8fab", "#72efdd"],
  monochrome: ["#f0f0f0", "#dedede", "#cccccc", "#bababa", "#a8a8a8", "#969696", "#848484"],
  earth: ["#d9c7a6", "#c3a982", "#ad8b63", "#96a17b", "#7f9168", "#b98268", "#8f7559"],
  ocean: ["#c6e8e5", "#a8dadc", "#8ecfd1", "#73c0c5", "#78b7d0", "#91a8d0", "#a7c4bc"],
  retro: ["#e49a78", "#e5bd63", "#a8b47d", "#78a0a8", "#a58aa8", "#c9ae88", "#87949a"],
});

// These are safe boot values only. User-facing defaults come from Go via
// GetDefaultProfile, so Restore defaults and first launch cannot diverge.
export const AppearanceState = {
  palette: "default",
  zoomFactor: 1,
  cornerRadius: 0,
  reliefStrength: 0,
};

export const FONT_SIZE = 10;
export const SCALE_MIN = 0.5;
export const SCALE_MAX = 5.0;
export const SCALE_STEP_KEYS = 1.1;
export const SCALE_SMOOTH_BASE = 1.0015;

export function setProfiles(profile, defaultProfile) {
  if (profile) AppState.profile = profile;
  if (defaultProfile) AppState.defaultProfile = defaultProfile;
}

export function activePalette() {
  const palette = PALETTES[AppearanceState.palette];
  return Array.isArray(palette) && palette.length > 0 ? palette : PALETTES.default;
}
