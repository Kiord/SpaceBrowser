import { initLocationShortcuts } from "./location-shortcuts.js";
import { DefaultPath, GetInitialScanPath } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { hideContextMenu, initFileActions, openRectWithDefault } from "./file-actions.js";
import { initFolderPicker } from "./folder-picker.js";
import { addControlEventListeners, eventMatchesShortcut, shortcutCanRun } from "./controls.js";
import { logError } from "./logging.js";
import { initLocationSelector, hideLocationSelector } from "./locations.js";
import { initNavigation, navigateToSelected, updateNavButtons } from "./navigation.js";
import { analyze, openLocation, initScan, updateScanVisibility } from "./scan.js";
import { initSettings, loadSettingsState } from "./settings.js";
import { initTreemapView, redraw, resizeCanvas, repaintScanLabels } from "./treemap-view.js";
import { getSelectedRects } from "./selection.js";
import { initZoom } from "./zoom.js";
import { AppState } from "./state.js";

function initButtonFocusVisibility() {
  const keyboardClass = "keyboard-navigation";
  window.addEventListener("keydown", event => {
    if (event.key === "Tab") document.documentElement.classList.add(keyboardClass);
  }, true);
  window.addEventListener("pointerdown", () => {
    document.documentElement.classList.remove(keyboardClass);
  }, true);
}

document.addEventListener("DOMContentLoaded", async () => {
  initButtonFocusVisibility();
  byId("pathGroup").removeAttribute("title");
  const analyzeButton = byId("analyzeButton");
  analyzeButton.removeAttribute("title");
  analyzeButton.dataset.tooltip = "Scan folder";

  initTreemapView();
  initNavigation({ redraw, hideHome: hideLocationSelector });
  initSettings({ redraw });
  initFileActions({ redraw });
  initScan({ redraw, hideContextMenu, repaintScanLabels });
  initLocationSelector({ analyze: openLocation, visibilityChanged(homeVisible) {
    AppState.homeVisible = homeVisible;
    updateNavButtons();
    updateScanVisibility();
  } });
  initFolderPicker();
  initZoom({ redraw });
  const locationsCollapsed = () => byId("workspace").getAttribute("data-sidebar-collapsed") === "true";
  const setLocationsCollapsed = async collapsed => {
    const workspace = byId("workspace");
    workspace.setAttribute("data-sidebar-collapsed", String(collapsed));
    const button = byId("toggleLocationsButton");
    button.setAttribute("aria-expanded", String(!collapsed));
    button.setAttribute("aria-label", collapsed ? "Show locations" : "Hide locations");
    button.setAttribute("data-tooltip", collapsed ? "Show locations" : "Hide locations");
    hideContextMenu();
    resizeCanvas();
    await redraw();
  };
  const locationShortcuts = initLocationShortcuts({ isCollapsed: locationsCollapsed, setCollapsed: setLocationsCollapsed });
  const toggleLocations = () => {
    locationShortcuts.cancelPreview();
    return setLocationsCollapsed(!locationsCollapsed());
  };
  byId("toggleLocationsButton").addEventListener("click", toggleLocations);

  window.addEventListener("keydown", event => {
    if (event.isComposing || event.defaultPrevented) return;
    if (event.key === "Enter" && document.activeElement === byId("pathInput")) {
      event.preventDefault();
      analyze();
      return;
    }
  });

  const handleVisitShortcut = event => {
    if (!shortcutCanRun(event) || !eventMatchesShortcut(event, AppState.profile?.controls?.visitSelected)) return;
    const selected = getSelectedRects();
    if (!selected.length || (selected.length > 1 && selected.some(rect => rect.is_folder))) return;
    event.preventDefault();
    if (selected[0].is_folder) navigateToSelected();
    else openRectWithDefault();
  };
  addControlEventListeners(handleVisitShortcut);
  addControlEventListeners(event => {
    if (!shortcutCanRun(event) || !eventMatchesShortcut(event, AppState.profile?.controls?.toggleLocations)) return;
    event.preventDefault();
    return toggleLocations();
  });

  try {
    await loadSettingsState();
  } catch (error) {
    logError("loading settings failed:", error);
  }

  try {
    const initialPath = await GetInitialScanPath();
    const startPath = initialPath || await DefaultPath();
    if (startPath) byId("pathInput").value = startPath;
    if (initialPath) {
      hideLocationSelector();
      await analyze();
    }
  } catch (error) {
    logError("loading initial path failed:", error);
  }
});
