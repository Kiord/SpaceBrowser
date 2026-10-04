import { getSelectedRect, isPassiveRect } from "./selection.js";
import { SetShowFreeSpace } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { addControlEventListeners, eventMatchesShortcut, shortcutCanRun } from "./controls.js";
import { logError } from "./logging.js";
import { AppState, AppearanceState, activePalette } from "./state.js";

const HISTORY_STATE_KEY = "spacebrowserNavigation";
let redraw = async () => {};
let hideHome = () => {};
let historyAliases = new Map();
let aliasSession;

export function remapNavigation(nodeIds, fallback) {
  if (aliasSession !== AppState.navSession) historyAliases = new Map();
  aliasSession = AppState.navSession;
  AppState.navHistory = AppState.navHistory.map((id, index) => {
    const aliases = historyAliases.get(index) || new Set();
    aliases.add(id);
    historyAliases.set(index, aliases);
    return nodeIds[id] ?? id;
  });
  AppState.node_id = nodeIds[AppState.node_id] ?? AppState.node_id;
  if (AppState.node_id < 0) {
    AppState.node_id = fallback;
    AppState.navHistory[AppState.navIndex] = fallback;
  }
  replaceBrowserHistoryEntry(AppState.node_id, AppState.navIndex);
}

function browserHistoryState(nodeId, navIndex) {
  return {
    [HISTORY_STATE_KEY]: true,
    session: AppState.navSession,
    nodeId,
    navIndex,
    position: AppState.browserHistoryPosition,
  };
}

export function replaceBrowserHistoryEntry(nodeId, navIndex) {
  window.history.replaceState(browserHistoryState(nodeId, navIndex), "");
}

export function pushBrowserHistoryEntry(nodeId, navIndex) {
  AppState.browserHistoryPosition++;
  window.history.pushState(browserHistoryState(nodeId, navIndex), "");
}

export function navigateToSelected() {
  const rect = getSelectedRect();
  if (!rect || !rect.is_folder || isPassiveRect(rect)) return;
  visit(rect.node_id);
}

export function goToRoot() {
  if (AppState.homeVisible) return;
  if (AppState.navHistory.length) visit(AppState.navHistory[0]);
}

export function goToParent() {
  if (AppState.homeVisible) return;
  const rootRect = AppState.rects?.[0];
  if (rootRect?.parent_id != null) visit(rootRect.parent_id);
}

export function rollbackBrowserHistory(position) {
  const delta = position - AppState.browserHistoryPosition;
  AppState.browserHistoryPosition = position;
  if (delta) window.history.go(delta);
  else replaceBrowserHistoryEntry(AppState.node_id, AppState.navIndex);
}

export function visit(nodeId) {
  if (nodeId == null || nodeId < 0 || nodeId === AppState.node_id) return;
  AppState.navHistory = AppState.navHistory.slice(0, AppState.navIndex + 1);
  for (const index of historyAliases.keys()) if (index > AppState.navIndex) historyAliases.delete(index);
  AppState.navHistory.push(nodeId);
  AppState.navIndex = AppState.navHistory.length - 1;
  AppState.node_id = nodeId;
  pushBrowserHistoryEntry(nodeId, AppState.navIndex);
  redraw();
}

export function goBackward() {
  if (AppState.homeVisible) return;
  if (AppState.navIndex > 0) window.history.back();
}

export function goForward() {
  if (AppState.homeVisible) return;
  if (AppState.navIndex < AppState.navHistory.length - 1) window.history.forward();
}

export async function toggleFreeSpace(event) {
  const button = event?.currentTarget ?? byId("toggleFreeSpaceButton");
  const wasChecked = button.getAttribute("aria-pressed") === "true";
  const checked = !wasChecked;
  button.setAttribute("aria-pressed", String(checked));
  try {
    await SetShowFreeSpace(checked);
    await redraw();
  } catch (error) {
    button.setAttribute("aria-pressed", String(wasChecked));
    logError("toggleFreeSpace failed:", error);
  }
}

export function updateNavButtons() {
  updateBreadcrumbs();
  byId("homeButton").disabled = !!AppState.homeVisible;
  if (AppState.homeVisible) {
    byId("rootButton").disabled = true;
    byId("parentButton").disabled = true;
    byId("forwardButton").disabled = true;
    byId("backwardButton").disabled = true;
    return;
  }
  byId("rootButton").disabled = AppState.node_id == null || !AppState.navHistory.length
    || AppState.node_id === AppState.navHistory[0];
  byId("parentButton").disabled = !(AppState.rects?.length && AppState.rects[0].parent_id != null);
  byId("backwardButton").disabled = AppState.navIndex <= 0;
  byId("forwardButton").disabled = AppState.navIndex >= AppState.navHistory.length - 1;
}

function updateBreadcrumbs() {
  const container = byId("pathBreadcrumbs");
  container.hidden = !!AppState.homeVisible;
  const root = AppState.rects?.[0];
  if (container.hidden || root?.node_id !== AppState.node_id) {
    container.replaceChildren();
    container.setAttribute("data-path-key", "");
    return;
  }
  const ancestors = root.breadcrumbs || [];
  const palette = activePalette();
  const key = JSON.stringify([AppState.navSession, ancestors, palette, AppearanceState]);
  // Preserve keyboard focus while the live scan refreshes the same path.
  if (container.getAttribute("data-path-key") === key) return;
  container.setAttribute("data-path-key", key);
  container.replaceChildren();
  for (const [index, ancestor] of ancestors.entries()) {
    if (index) {
      const separator = document.createElement("span");
      separator.className = "breadcrumb-separator";
      separator.textContent = "/";
      separator.setAttribute("aria-hidden", "true");
      container.append(separator);
    }
    const current = ancestor.node_id === AppState.node_id;
    const item = document.createElement(current ? "span" : "button");
    item.className = "breadcrumb-item";
    item.textContent = index === 0 ? ancestor.full_path : ancestor.name;
    item.setAttribute("data-full-label", item.textContent);
    item.setAttribute("aria-label", ancestor.full_path);
    item.title = ancestor.full_path;
    const color = index === 0 ? "#ffffff" : palette[index % palette.length];
    item.style.setProperty("--crumb-color", color);
    item.style.setProperty("--crumb-radius", `${AppearanceState.cornerRadius || 0}px`);
    item.style.setProperty("--crumb-padding", `${Math.max(4, AppearanceState.boxPadding || 5)}px`);
    item.style.setProperty("--crumb-light", breadcrumbShade(color, 255, AppearanceState.reliefStrength || 0));
    item.style.setProperty("--crumb-dark", breadcrumbShade(color, 0, AppearanceState.reliefStrength || 0));
    item.style.setProperty("--crumb-hover", breadcrumbShade(color, 255, AppearanceState.hoverBrightness || 0));
    if (current) item.setAttribute("aria-current", "location");
    else {
      item.type = "button";
      item.addEventListener("click", () => visit(ancestor.node_id));
    }
    container.append(item);
  }
  fitBreadcrumbs(container);
}

export function fitBreadcrumbs(container = byId("pathBreadcrumbs")) {
  if (container.hidden || !(container.clientWidth > 0)) return;
  const items = Array.from(container.children).filter(item => item.className === "breadcrumb-item");
  container.scrollLeft = 0;
  for (const item of items) {
    item.textContent = item.getAttribute("data-full-label");
    item.setAttribute("data-compact", "full");
  }
  // Shorten older ancestors first, keeping the current folder readable as long
  // as possible. Every box retains its full accessible label and tooltip.
  for (const mode of ["initial", "box"]) {
    for (const item of items) {
      if (container.scrollWidth <= container.clientWidth) return;
      item.textContent = mode === "initial" ? Array.from(item.getAttribute("data-full-label"))[0] || "" : "";
      item.setAttribute("data-compact", mode);
    }
  }
  container.scrollLeft = container.scrollWidth;
}

function breadcrumbShade(color, target, amount) {
  let hex = color.replace("#", "");
  if (hex.length === 3) hex = hex.split("").map(char => char + char).join("");
  return `rgb(${[0, 2, 4].map(offset => {
    const channel = parseInt(hex.slice(offset, offset + 2), 16);
    return Math.round(channel + (target - channel) * amount);
  }).join(", ")})`;
}

export function trimInvalidForwardNavigation() {
  const hadForwardHistory = AppState.navIndex < AppState.navHistory.length - 1;
  AppState.navHistory = AppState.navHistory.slice(0, AppState.navIndex + 1);
  if (hadForwardHistory) pushBrowserHistoryEntry(AppState.node_id, AppState.navIndex);
  else replaceBrowserHistoryEntry(AppState.node_id, AppState.navIndex);
}

function handlePopState(event) {
  const state = event.state;
  if (state?.[HISTORY_STATE_KEY] && state.session === AppState.navSession
    && Number.isInteger(state.navIndex) && AppState.navHistory[state.navIndex] === -1) {
    window.history.go(state.position < AppState.browserHistoryPosition ? -1 : 1);
    return;
  }
  const isCurrentNavigation = state?.[HISTORY_STATE_KEY]
    && state.session === AppState.navSession
    && Number.isInteger(state.navIndex)
    && AppState.navHistory[state.navIndex] >= 0
    && (AppState.navHistory[state.navIndex] === state.nodeId
      || (aliasSession === AppState.navSession && historyAliases.get(state.navIndex)?.has(state.nodeId)));

  if (!isCurrentNavigation) {
    const stalePosition = Number(state?.position);
    window.history.go(Number.isFinite(stalePosition) && stalePosition > AppState.browserHistoryPosition ? -1 : 1);
    return;
  }
  AppState.browserHistoryPosition = state.position;
  AppState.navIndex = state.navIndex;
  AppState.node_id = AppState.navHistory[state.navIndex];
  hideHome();
  redraw();
}

function handleNavigationShortcut(event) {
  if (!shortcutCanRun(event)) return;
  const bindings = AppState.profile?.controls;
  if (eventMatchesShortcut(event, bindings?.back)) goBackward();
  else if (eventMatchesShortcut(event, bindings?.forward)) goForward();
  else if (eventMatchesShortcut(event, bindings?.parent)) goToParent();
  else if (eventMatchesShortcut(event, bindings?.root)) goToRoot();
  else return;
  event.preventDefault();
}

export function initNavigation(options) {
  redraw = options.redraw;
  hideHome = options.hideHome || (() => {});
  const breadcrumbs = byId("pathBreadcrumbs");
  if (typeof ResizeObserver !== "undefined") {
    const observer = new ResizeObserver(() => fitBreadcrumbs(breadcrumbs));
    observer.observe(breadcrumbs);
  }
  window.addEventListener("resize", () => fitBreadcrumbs(breadcrumbs));
  replaceBrowserHistoryEntry(null, -1);
  byId("rootButton").addEventListener("click", goToRoot);
  byId("parentButton").addEventListener("click", goToParent);
  byId("backwardButton").addEventListener("click", goBackward);
  byId("forwardButton").addEventListener("click", goForward);
  byId("toggleFreeSpaceButton").addEventListener("click", toggleFreeSpace);
  window.addEventListener("popstate", handlePopState);
  addControlEventListeners(handleNavigationShortcut);
}
