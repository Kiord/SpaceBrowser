import { PickFolder } from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { logWarning } from "./logging.js";
import { showErrorToast } from "./notifications.js";
import { AppState } from "./state.js";

export async function chooseFolder({ focusInput = true } = {}) {
  if (AppState.pickingFolderDialogIsOpen) return "";
  const buttons = [byId("triggerFolderSelectButton"), document.getElementById("chooseLocationFolderButton")].filter(Boolean);
  const previousDisabledStates = buttons.map(button => button.disabled);
  try {
    buttons.forEach(button => { button.disabled = true; });
    AppState.pickingFolderDialogIsOpen = true;
    const path = await PickFolder();
    if (!path) return "";
    const input = byId("pathInput");
    input.value = path;
    if (focusInput) {
      setTimeout(() => {
        input.focus({ preventScroll: true });
        input.setSelectionRange(input.value.length, input.value.length);
      }, 0);
    }
    return path;
  } catch (error) {
    logWarning("folder pick failed:", error);
    showErrorToast(error);
    return "";
  } finally {
    AppState.pickingFolderDialogIsOpen = false;
    buttons.forEach((button, index) => { button.disabled = previousDisabledStates[index]; });
  }
}

export function initFolderPicker() {
  byId("triggerFolderSelectButton").addEventListener("click", () => chooseFolder());
}
