import { DefaultPath, GetInitialScanPath } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { hideContextMenu, initFileActions, openRectWithDefault } from "./file-actions.js";
import { initFolderPicker } from "./folder-picker.js";
import { addControlEventListeners, eventMatchesShortcut, shortcutCanRun } from "./controls.js";
import { logError } from "./logging.js";
import { initLocationSelector, showLocationSelector, hideLocationSelector } from "./locations.js";
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
  byId("homeButton").addEventListener("click", () => {
    hideContextMenu();
    showLocationSelector({ refresh: true });
  });
  initFolderPicker();
  initZoom({ redraw });

  window.addEventListener("keydown", event => {
    if (event.isComposing) return;
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

  try {
    await loadSettingsState();
  } catch (error) {
    logError("loading settings failed:", error);
  }

  try {
    const initialPath = await GetInitialScanPath();
    const startPath = initialPath || await DefaultPath();
    if (startPath) byId("pathInput").value = startPath;
    if (initialPath) await analyze();
  } catch (error) {
    logError("loading initial path failed:", error);
  }
});
