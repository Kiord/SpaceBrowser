import { GetAssociatedIcon, OpenPath } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { detailedByteSize, formatModTime } from "./format.js";
import { AppState } from "./state.js";

let canvasCoords = null;
let rectAtPoint = null;
let hoveredRect = null;
let hoveredRectIndex = -1;
const associatedIconCache = new Map();

export const mousePosition = { x: 0, y: 0 };

function rectSupportsDetailsToast(rect) {
  return !!(rect?.full_path && rect.parent_id != null && !rect.is_free_space);
}

function associatedIconKey(rect) {
  if (rect.is_folder) return "<folder>";
  const name = String(rect.name || "").toLocaleLowerCase();
  const dot = name.lastIndexOf(".");
  const extension = dot > 0 ? name.slice(dot) : "<file>";
  return extension === ".exe" || extension === ".lnk" || extension === ".ico"
    ? String(rect.full_path).toLocaleLowerCase()
    : extension;
}

function showFallbackIcon(isFolder) {
  const image = byId("rectToastAssociatedIcon");
  const fallback = byId("rectToastFallbackIcon");
  image.classList.add("is-hidden");
  fallback.classList.remove("is-hidden");
  byId("rectToastFallbackShape").setAttribute("d", isFolder ? "M3 8V5h7l2 3h9v11H3z" : "M6 3h8l4 4v14H6z");
  byId("rectToastFallbackDetail").setAttribute("d", isFolder ? "M3 9h18" : "M14 3v5h5");
}

function applyAssociatedIcon(rect, icon) {
  if (!icon || hoveredRect?.full_path !== rect.full_path) return;
  const image = byId("rectToastAssociatedIcon");
  image.src = icon;
  image.classList.remove("is-hidden");
  byId("rectToastFallbackIcon").classList.add("is-hidden");
}

function updateAssociatedIcon(rect) {
  const key = associatedIconKey(rect);
  const cached = associatedIconCache.get(key);
  if (typeof cached === "string") {
    if (cached) applyAssociatedIcon(rect, cached);
    else showFallbackIcon(rect.is_folder);
    return;
  }
  showFallbackIcon(rect.is_folder);
  if (cached) {
    cached.then(icon => applyAssociatedIcon(rect, icon));
    return;
  }
  const request = Promise.resolve(GetAssociatedIcon(rect.full_path, !!rect.is_folder))
    .then(icon => {
      const value = String(icon || "");
      associatedIconCache.set(key, value);
      return value;
    })
    .catch(() => {
      associatedIconCache.set(key, "");
      return "";
    });
  associatedIconCache.set(key, request);
  request.then(icon => applyAssociatedIcon(rect, icon));
}

function placeRectToast(x, y) {
  const toast = byId("rectToast");
  if (toast.hidden) return;
  const pad = 8;
  const offset = 14;
  const left = x + offset + toast.offsetWidth <= window.innerWidth - pad ? x + offset : x - toast.offsetWidth - offset;
  const top = y + offset + toast.offsetHeight <= window.innerHeight - pad ? y + offset : y - toast.offsetHeight - offset;
  toast.style.left = `${Math.max(pad, left)}px`;
  toast.style.top = `${Math.max(pad, top)}px`;
}

export function hideRectToast() {
  byId("rectToast").hidden = true;
  hoveredRect = null;
  hoveredRectIndex = -1;
}

export function updateRectToast(event) {
  if (!canvasCoords || !rectAtPoint) return;
  const { x, y } = canvasCoords(event);
  const rectIndex = rectAtPoint(x, y);
  const rect = AppState.rects?.[rectIndex];
  if (!rectSupportsDetailsToast(rect)) {
    hideRectToast();
    return;
  }
  if (rectIndex === hoveredRectIndex && !byId("rectToast").hidden) {
    placeRectToast(event.clientX, event.clientY);
    return;
  }

  hoveredRect = rect;
  hoveredRectIndex = rectIndex;
  const name = String(rect.name || "");
  const fullPath = String(rect.full_path);
  const suffix = fullPath.slice(-name.length);
  const nameIsPathSuffix = !!name && suffix.toLocaleLowerCase() === name.toLocaleLowerCase();
  byId("rectToastPathPrefix").textContent = nameIsPathSuffix ? fullPath.slice(0, -name.length) : fullPath;
  byId("rectToastName").textContent = nameIsPathSuffix ? name : "";
  byId("rectToastSize").textContent = detailedByteSize(rect.size);
  byId("rectToastCreated").textContent = `Modification date : ${rect.mtime ? formatModTime(rect.mtime) : "unavailable"}`;
  updateAssociatedIcon(rect);
  byId("rectToast").hidden = false;
  placeRectToast(event.clientX, event.clientY);
}

export function showToastAt(x, y, message = "Copied path", duration = 1000, variant = "default") {
  const toast = byId("toast");
  toast.textContent = message;
  toast.classList.toggle("is-error", variant === "error");
  const pad = 8;
  toast.style.left = `${Math.max(pad, Math.min(x + pad, window.innerWidth - toast.offsetWidth - pad))}px`;
  toast.style.top = `${Math.max(pad, Math.min(y + pad, window.innerHeight - toast.offsetHeight - pad))}px`;
  toast.style.opacity = "1";
  toast.style.transform = "translateY(0)";
  clearTimeout(toast._hideTimer);
  toast._hideTimer = setTimeout(() => {
    toast.style.opacity = "0";
    toast.style.transform = "translateY(6px)";
  }, duration);
}

export function showErrorToast(error) {
  let message = String(error?.message || error || "Unable to analyze this path.").replace(/^Error:\s*/i, "").trim();
  if (message) {
    message = message[0].toUpperCase() + message.slice(1);
    if (!/[.!?]$/.test(message)) message += ".";
  }
  const toast = byId("toast");
  toast.textContent = message;
  const topbarBottom = byId("topbar").getBoundingClientRect().bottom || 38;
  showToastAt((window.innerWidth - toast.offsetWidth) / 2 - 8, topbarBottom, message, 2600, "error");
}

export function initNotifications(options) {
  canvasCoords = options.getCanvasCoords;
  rectAtPoint = options.rectIndexAtPoint;
  window.addEventListener("mousemove", event => {
    mousePosition.x = event.clientX;
    mousePosition.y = event.clientY;
  });
  AppState.colorCanvas.addEventListener("pointermove", updateRectToast);
  AppState.colorCanvas.addEventListener("pointerleave", event => {
    if (!event.relatedTarget?.closest?.("#rectToast")) hideRectToast();
  });
  byId("rectToastOpen").addEventListener("click", async event => {
    event.stopPropagation();
    if (hoveredRect?.full_path) await OpenPath(hoveredRect.full_path);
  });
  byId("rectToast").addEventListener("pointerleave", event => {
    if (event.relatedTarget !== AppState.colorCanvas) hideRectToast();
  });
}
