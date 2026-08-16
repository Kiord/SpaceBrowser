import { PickFolder } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { logWarning } from "./logging.js";
import { showErrorToast } from "./notifications.js";
import { AppState } from "./state.js";

async function chooseFolder() {
  if (AppState.pickingFolderDialogIsOpen) return;
  const button = byId("triggerFolderSelectButton");
  try {
    button.disabled = true;
    AppState.pickingFolderDialogIsOpen = true;
    const path = await PickFolder();
    if (!path) return;
    const input = byId("pathInput");
    input.value = path;
    setTimeout(() => {
      input.focus({ preventScroll: true });
      input.setSelectionRange(input.value.length, input.value.length);
    }, 0);
  } catch (error) {
    logWarning("folder pick failed:", error);
    showErrorToast(error);
  } finally {
    AppState.pickingFolderDialogIsOpen = false;
    button.disabled = false;
  }
}

export function initFolderPicker() {
  byId("triggerFolderSelectButton").addEventListener("click", chooseFolder);
}
