import { getSelectedRect, isPassiveRect } from "./selection.js";
import { SetShowFreeSpace } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { addControlEventListeners, eventMatchesShortcut, shortcutCanRun } from "./controls.js";
import { logError } from "./logging.js";
import { AppState } from "./state.js";

const HISTORY_STATE_KEY = "spacebrowserNavigation";
let redraw = async () => {};
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
  if (AppState.navHistory.length) visit(AppState.navHistory[0]);
}

export function goToParent() {
  const rootRect = AppState.rects?.[0];
  if (rootRect?.parent_id != null) visit(rootRect.parent_id);
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
  if (AppState.navIndex > 0) window.history.back();
}

export function goForward() {
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
  byId("rootButton").disabled = AppState.navIndex === 0;
  byId("parentButton").disabled = !(AppState.rects?.length && AppState.rects[0].parent_id != null);
  byId("backwardButton").disabled = AppState.navIndex <= 0;
  byId("forwardButton").disabled = AppState.navIndex >= AppState.navHistory.length - 1;
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
  replaceBrowserHistoryEntry(null, -1);
  byId("rootButton").addEventListener("click", goToRoot);
  byId("parentButton").addEventListener("click", goToParent);
  byId("backwardButton").addEventListener("click", goBackward);
  byId("forwardButton").addEventListener("click", goForward);
  byId("toggleFreeSpaceButton").addEventListener("click", toggleFreeSpace);
  window.addEventListener("popstate", handlePopState);
  addControlEventListeners(handleNavigationShortcut);
}
