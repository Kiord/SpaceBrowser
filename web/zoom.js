import { AppState, AppearanceState, SCALE_MAX, SCALE_MIN, SCALE_SMOOTH_BASE, SCALE_STEP_KEYS, getScale } from "./state.js";
import { logDebug } from "./logging.js";
import { shortcutCanRun } from "./controls.js";

let redraw = async () => {};

async function setScale(next, reason = "") {
  const old = AppState.zoomFactor || 1;
  const clamped = Math.max(SCALE_MIN, Math.min(SCALE_MAX, next));
  if (clamped === old) return;
  AppState.zoomFactor = clamped;
  AppearanceState.zoomFactor = clamped;
  AppState.scale = getScale() * clamped;
  logDebug(`scale ${reason}:`, old, "→", clamped);
  await redraw();
}

function factorFromWheelDelta(deltaY, deltaMode) {
  const multiplier = deltaMode === 1 ? 15 : deltaMode === 2 ? 120 : 1;
  return Math.pow(SCALE_SMOOTH_BASE, -deltaY * multiplier);
}

export function initZoom(options, root = window) {
  redraw = options.redraw;
  root.addEventListener("wheel", event => {
    if (!event.ctrlKey && !event.metaKey) return;
    const canZoom = !AppState.homeVisible && AppState.node_id != null && shortcutCanRun(event);
    event.preventDefault();
    if (!canZoom) return;
    setScale((AppState.zoomFactor || 1) * factorFromWheelDelta(event.deltaY, event.deltaMode), "wheel");
  }, { passive: false, capture: true });

  const zoomKeys = new Set(["+", "=", "-", "_", "0", "NumpadAdd", "NumpadSubtract", "Numpad0"]);
  root.addEventListener("keydown", event => {
    if ((!event.ctrlKey && !event.metaKey) || (!zoomKeys.has(event.key) && !zoomKeys.has(event.code))) return;
    const canZoom = !AppState.homeVisible && AppState.node_id != null && shortcutCanRun(event);
    event.preventDefault();
    if (!canZoom) return;
    if (event.key === "0" || event.code === "Numpad0") setScale(1, "keyboard reset");
    else if (event.key === "+" || event.key === "=" || event.code === "NumpadAdd") setScale((AppState.zoomFactor || 1) * SCALE_STEP_KEYS, "keyboard in");
    else setScale((AppState.zoomFactor || 1) / SCALE_STEP_KEYS, "keyboard out");
  }, { capture: true });
}
