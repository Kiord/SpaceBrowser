import { DefaultPath, GetInitialScanPath } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { hideContextMenu, initFileActions } from "./file-actions.js";
import { initFolderPicker } from "./folder-picker.js";
import { eventMatchesShortcut, shortcutCanRun } from "./key-bindings.js";
import { logError } from "./logging.js";
import { initNavigation, navigateToSelected } from "./navigation.js";
import { analyze, initScan } from "./scan.js";
import { initSettings, loadSettingsState } from "./settings.js";
import { getSelectedRect, initTreemapView, isPassiveRect, redraw } from "./treemap-view.js";
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
  initNavigation({ redraw, getSelectedRect, isPassiveRect });
  initSettings({ redraw });
  initFileActions({ redraw, getSelectedRect, isPassiveRect });
  initScan({ redraw, hideContextMenu });
  initFolderPicker();
  initZoom({ redraw });

  window.addEventListener("keydown", event => {
    if (event.isComposing) return;
    if (event.key === "Enter" && document.activeElement === byId("pathInput")) {
      event.preventDefault();
      analyze();
      return;
    }
    if (!shortcutCanRun(event) || !eventMatchesShortcut(event, AppState.profile?.keyBindings?.visitSelected)) return;
    event.preventDefault();
    navigateToSelected();
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
    if (initialPath) await analyze();
  } catch (error) {
    logError("loading initial path failed:", error);
  }
});
