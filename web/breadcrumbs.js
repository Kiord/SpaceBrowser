import { GetBreadcrumbs } from "./wailsjs/go/app/App.js";
import { byId } from "./dom.js";
import { AppState } from "./state.js";
import { logError } from "./logging.js";

let options;
let entries = [];
let request = 0;
let lastRoot;
let lastScope = "";
let pending = false;
const segmenter = typeof Intl.Segmenter === "function" ? new Intl.Segmenter(undefined, { granularity: "grapheme" }) : null;
const firstLetter = name => segmenter ? segmenter.segment(name)[Symbol.iterator]().next().value?.segment || name : Array.from(name)[0] || name;
const scope = () => `${AppState.navSession}:${AppState.node_id}:${AppState.scanRootPath}`;

function setHidden(hidden) {
  const row = byId("breadcrumbs");
  if (row.hidden === hidden) return;
  row.hidden = hidden;
  options.visibilityChanged?.();
}

function hideTooltip() {
  byId("breadcrumbTooltip").hidden = true;
}

function showTooltip(entry) {
  const tooltip = byId("breadcrumbTooltip");
  const bounds = entry.button.getBoundingClientRect();
  tooltip.textContent = entry.rect.full_path;
  tooltip.hidden = false;
  tooltip.style.left = `${Math.max(8, Math.min(bounds.left, window.innerWidth - tooltip.offsetWidth - 8))}px`;
  const top = bounds.bottom + 7;
  tooltip.style.top = `${Math.max(8, top + tooltip.offsetHeight > window.innerHeight - 8 ? bounds.top - tooltip.offsetHeight - 7 : top)}px`;
}

// Restore full names first so widening the window reverses contraction.
// Once every item is one letter, horizontal scrolling keeps even extremely
// deep paths reachable without removing any navigation targets.
export function fitBreadcrumbs() {
  hideTooltip();
  const row = byId("breadcrumbs");
  if (row.hidden) return;
  for (const entry of entries) entry.button.textContent = entry.label;
  for (const entry of entries) {
    if (row.scrollWidth <= row.clientWidth) break;
    entry.button.textContent = firstLetter(entry.label);
  }
}

function clearBreadcrumbs() {
  hideTooltip();
  request++;
  lastRoot = undefined;
  lastScope = "";
  pending = false;
  entries = [];
  AppState.breadcrumbRects = [];
  byId("breadcrumbs").replaceChildren();
  setHidden(true);
}

function renderBreadcrumbs(crumbs, root, viewScope) {
  const row = byId("breadcrumbs");
  const labels = crumbs.map(crumb => crumb.name);
  const sameItems = entries.length === crumbs.length && entries.every((entry, index) => entry.rect.node_id === crumbs[index].node_id && entry.rect.full_path === crumbs[index].full_path && entry.label === labels[index]);
  AppState.breadcrumbRects = crumbs;
  if (sameItems) {
    entries.forEach((entry, index) => { entry.rect = crumbs[index]; entry.root = root; entry.scope = viewScope; });
    fitBreadcrumbs();
    return;
  }
  const focusedID = entries.find(entry => entry.button === document.activeElement)?.rect.node_id;
  const children = [];
  entries = crumbs.map((rect, index) => {
    if (index > 0) {
      const divider = document.createElement("span");
      divider.textContent = " > ";
      divider.setAttribute("aria-hidden", "true");
      children.push(divider);
    }
    const button = document.createElement("button");
    button.type = "button";
    button.setAttribute("aria-label", rect.full_path);
    button.setAttribute("aria-describedby", "breadcrumbTooltip");
    if (index === crumbs.length - 1) button.setAttribute("aria-current", "location");
    const entry = { rect, button, label: labels[index], root, scope: viewScope };
    const current = () => !pending && !row.hidden && entry.scope === scope() && entry.root === AppState.rects?.[0];
    const revealTooltip = () => { if (current()) showTooltip(entry); };
    button.addEventListener("mouseenter", revealTooltip);
    button.addEventListener("focus", revealTooltip);
    button.addEventListener("mouseleave", hideTooltip);
    button.addEventListener("blur", hideTooltip);
    button.addEventListener("click", () => {
      hideTooltip();
      if (!current()) return;
      options.hideContextMenu();
      options.visit(entry.rect.node_id);
    });
    const contextMenu = (x, y) => {
      hideTooltip();
      if (!current()) return;
      options.select(entry.rect);
      options.showContextMenu(x, y);
    };
    button.addEventListener("contextmenu", event => {
      event.preventDefault();
      contextMenu(event.clientX, event.clientY);
    });
    button.addEventListener("keydown", event => {
      if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
        event.preventDefault();
        const bounds = button.getBoundingClientRect();
        contextMenu(bounds.left, bounds.bottom);
      }
    });
    children.push(button);
    return entry;
  });
  row.replaceChildren(...children);
  fitBreadcrumbs();
  if (focusedID != null) entries.find(entry => entry.rect.node_id === focusedID)?.button.focus();
}

export async function updateBreadcrumbs() {
  if (!options) return;
  const root = AppState.rects?.[0];
  if (AppState.profile?.showBreadcrumbs === false || AppState.homeVisible || AppState.node_id == null || !root?.full_path || root.node_id !== AppState.node_id) {
    clearBreadcrumbs();
    return;
  }
  const viewScope = scope();
  if (lastRoot === root && lastScope === viewScope) return;
  lastRoot = root;
  lastScope = viewScope;
  const token = ++request;
  pending = true;
  hideTooltip();
  setHidden(false);
  try {
    const crumbs = await GetBreadcrumbs(root.node_id, root.full_path);
    if (token !== request || viewScope !== scope() || root !== AppState.rects?.[0]) return;
    pending = false;
    renderBreadcrumbs(crumbs, root, viewScope);
  } catch (error) {
    if (token !== request) return;
    pending = false;
    entries = [];
    AppState.breadcrumbRects = [];
    byId("breadcrumbs").replaceChildren();
    logError("loading breadcrumbs failed:", error);
  }
}

export function initBreadcrumbs(callbacks) {
  options = callbacks;
  byId("breadcrumbs").addEventListener("keydown", event => {
    // Let the native buttons own activation rather than also running the
    // application's configurable Enter/Space selection shortcuts.
    if (event.key === "Enter" || event.key === " ") event.stopPropagation();
  });
  byId("breadcrumbs").addEventListener("scroll", hideTooltip);
  window.addEventListener("resize", fitBreadcrumbs);
  if (typeof ResizeObserver !== "undefined") new ResizeObserver(fitBreadcrumbs).observe(byId("breadcrumbs"));
}
