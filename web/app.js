import { DefaultPath } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { hideContextMenu, initFileActions } from "./file-actions.js";
import { initFolderPicker } from "./folder-picker.js";
import { initNavigation, navigateToSelected } from "./navigation.js";
import { analyze, initScan } from "./scan.js";
import { initSettings, loadSettingsState } from "./settings.js";
import { getSelectedRect, initTreemapView, isPassiveRect, redraw } from "./treemap-view.js";
import { initZoom } from "./zoom.js";

document.addEventListener("DOMContentLoaded", async () => {
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
    if (event.isComposing || event.key !== "Enter") return;
    if (document.activeElement === byId("pathInput")) {
      event.preventDefault();
      analyze();
      return;
    }
    navigateToSelected();
  });

  try {
    await loadSettingsState();
  } catch (error) {
    console.error("loading settings failed:", error);
  }

  try {
    const defaultPath = await DefaultPath();
    if (defaultPath) byId("pathInput").value = defaultPath;
  } catch (error) {
    console.error("loading default path failed:", error);
  }
});
