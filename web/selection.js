import { AppState } from "./state.js";

export function selectionIds() {
  return AppState.selectedNodeIds ??= new Set();
}

export function clearSelection() {
  selectionIds().clear();
}

export function isPassiveRect(rect) {
  return !!(rect?.is_free_space || rect?.is_small_files);
}

export function getSelectedRects() {
  const ids = selectionIds();
  return (AppState.rects || []).filter(rect => ids.has(rect.node_id) && !isPassiveRect(rect));
}

export function getSelectedRect() {
  const selected = getSelectedRects();
  return selected.length === 1 ? selected[0] : null;
}

// Return changed IDs so the canvas repaints just the affected rectangles.
export function selectRect(rectIndex, { additive = false, preserve = false } = {}) {
  const ids = selectionIds();
  const before = new Set(ids);
  const rect = AppState.rects?.[rectIndex];
  const selectable = rect && !isPassiveRect(rect) && rect.node_id >= 0;
  if (selectable && preserve && ids.has(rect.node_id)) return new Set();
  const toggleOff = selectable && !additive && !preserve && ids.size === 1 && ids.has(rect.node_id);
  if (!additive) ids.clear();
  if (selectable && !toggleOff) {
    if (additive && ids.has(rect.node_id)) ids.delete(rect.node_id);
    else ids.add(rect.node_id);
  }
  return new Set([...before, ...ids].filter(id => before.has(id) !== ids.has(id)));
}

// Collapse only directory ancestors, respecting path-component boundaries.
export function reduceDeletionTargets(rects, platform = AppState.profile?.platformSystem) {
  const key = path => {
    const normalized = platform === "windows" ? path.replaceAll("\\", "/").toLowerCase() : path;
    return normalized.replace(/\/+$/, "") || "/";
  };
  const unique = [...new Map(rects.map(rect => [key(rect.full_path), rect])).values()];
  return unique.filter(rect => !unique.some(parent => parent !== rect && parent.is_folder
    && key(rect.full_path).startsWith(key(parent.full_path).replace(/\/$/, "") + "/")));
}
