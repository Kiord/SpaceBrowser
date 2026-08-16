import {
  DeleteNode,
  GetDefaultApplicationName,
  GetTrashRestoreInfo,
  OpenInFileBrowser,
  OpenPath,
  OpenWith,
  RestoreNode,
  ShowProperties,
} from "./wailsjs/go/main/App.js";
import { byId } from "./dom.js";
import { detailedByteSize } from "./format.js";
import { eventMatchesShortcut, shortcutCanRun } from "./controls.js";
import { trimInvalidForwardNavigation, updateNavButtons, visit } from "./navigation.js";
import { hideRectToast, mousePosition, showErrorToast, showToastAt } from "./notifications.js";
import { analyze } from "./scan.js";
import { AppState } from "./state.js";

let redraw = async () => {};
let getSelectedRect = () => null;
let isPassiveRect = () => false;
let pendingDeletion = null;
let deletionInProgress = false;
let contextMenuRequest = 0;
const defaultApplicationNames = new Map();

function trashDestinationName() {
  return AppState.profile?.platformSystem === "windows" ? "Recycle Bin" : "Trash";
}

function requestSelectedDeletion() {
  hideContextMenu();
  hideRectToast();
  const rect = getSelectedRect();
  if (!rect) return;
  if (deletionInProgress) {
    showErrorToast("Another deletion is already in progress");
    return;
  }
  const emptyTrash = !!rect.is_trash_root;
  const permanent = !!rect.is_in_trash && !emptyTrash;
  const emptiesAllTrashLocations = emptyTrash && AppState.profile?.platformSystem !== "windows";
  if (!AppState.profile?.allowDelete) {
    showErrorToast(emptyTrash
      ? `Empty ${trashDestinationName()} is disabled. Enable Allow delete command in Settings`
      : "Delete commands are disabled. Enable Allow delete command in Settings");
    return;
  }
  if (permanent && !AppState.profile?.allowPermanentDelete) {
    showErrorToast("Permanent deletion is disabled. Enable Allow permanent deletion in Settings");
    return;
  }
  if (isPassiveRect(rect) || !rect.full_path) {
    showErrorToast("This item cannot be deleted");
    return;
  }
  if (!emptyTrash && rect.node_id === AppState.node_id) {
    showErrorToast("The current view cannot be deleted. Go to its parent first");
    return;
  }

  const action = emptyTrash ? "empty" : permanent ? "permanent" : "trash";
  pendingDeletion = { action, nodeId: rect.node_id, path: rect.full_path, size: rect.size };
  byId("deleteConfirmTitle").textContent = emptyTrash
    ? `Empty ${trashDestinationName()}?`
    : permanent ? "Permanently delete this item?" : `Move this item to ${trashDestinationName()}?`;
  byId("deleteConfirmPath").textContent = emptiesAllTrashLocations
    ? "All Trash locations for the current user will be emptied."
    : rect.full_path;
  byId("deleteConfirmSizeLabel").textContent = emptiesAllTrashLocations
    ? "Displayed size:"
    : emptyTrash ? "Contents size:" : "Size:";
  byId("deleteConfirmSize").textContent = detailedByteSize(rect.size);
  const confirmButton = byId("confirmDeleteButton");
  confirmButton.textContent = emptyTrash ? "Empty" : permanent ? "Delete permanently" : "Delete";
  confirmButton.classList.add("danger-button");
  const dialog = byId("deleteConfirmDialog");
  if (!dialog.open) dialog.showModal();
}

async function requestSelectedRestore() {
  hideContextMenu();
  hideRectToast();
  const rect = getSelectedRect();
  if (!rect?.is_in_trash || rect.is_trash_root || !rect.full_path) return;
  if (deletionInProgress) {
    showErrorToast("Another filesystem operation is already in progress");
    return;
  }
  try {
    const details = await GetTrashRestoreInfo(rect.node_id);
    pendingDeletion = { action: "restore", nodeId: rect.node_id, path: rect.full_path, size: rect.size };
    byId("deleteConfirmTitle").textContent = "Restore this item?";
    byId("deleteConfirmPath").textContent = `Original location: ${details.originalPath}`;
    byId("deleteConfirmSizeLabel").textContent = "Size:";
    byId("deleteConfirmSize").textContent = detailedByteSize(rect.size);
    const confirmButton = byId("confirmDeleteButton");
    confirmButton.textContent = "Restore";
    confirmButton.classList.remove("danger-button");
    const dialog = byId("deleteConfirmDialog");
    if (!dialog.open) dialog.showModal();
  } catch (error) {
    showErrorToast(error);
  }
}

function closeDeleteConfirmation() {
  if (deletionInProgress) return;
  const dialog = byId("deleteConfirmDialog");
  if (dialog.open) dialog.close();
  pendingDeletion = null;
}

function waitForNextPaint() {
  return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
}

async function confirmSelectedDeletion() {
  if (!pendingDeletion || deletionInProgress) return;
  const target = pendingDeletion;
  const confirmButton = byId("confirmDeleteButton");
  const cancelButton = byId("cancelDeleteButton");
  deletionInProgress = true;
  confirmButton.disabled = true;
  cancelButton.disabled = true;
  byId("deleteConfirmDialog").close();
  pendingDeletion = null;
  const actionText = target.action === "empty"
    ? `Emptying ${trashDestinationName()}...`
    : target.action === "permanent" ? "Deleting permanently..."
      : target.action === "restore" ? "Restoring..." : `Moving to ${trashDestinationName()}...`;
  const dismissMovingToast = showToastAt(mousePosition.x, mousePosition.y, actionText, 30000);

  try {
    await waitForNextPaint();
    const result = target.action === "restore" ? await RestoreNode(target.nodeId) : await DeleteNode(target.nodeId);
    dismissMovingToast();
    AppState.selectedRectIndex = null;
    AppState.selectedNodeId = null;
    if (AppState.profile?.rescanOnDelete || result.rescanRequired) {
      if (AppState.scanRootPath) byId("pathInput").value = AppState.scanRootPath;
      await analyze();
    } else {
      AppState.fileCount = result.fileCount;
      AppState.dirCount = result.dirCount;
      trimInvalidForwardNavigation();
      await redraw();
      updateNavButtons();
      const completedText = target.action === "empty"
        ? `${trashDestinationName()} emptied`
        : target.action === "permanent" ? "Permanently deleted"
          : target.action === "restore" ? "Restored" : `Moved to ${trashDestinationName()}`;
      showToastAt(mousePosition.x, mousePosition.y, completedText, 1600);
    }
  } catch (error) {
    dismissMovingToast();
    showErrorToast(error);
  } finally {
    dismissMovingToast();
    deletionInProgress = false;
    confirmButton.disabled = false;
    cancelButton.disabled = false;
  }
}

function placeContextMenu(menu, x, y) {
  const margin = 8;
  const cursorGap = 3;
  menu.style.visibility = "hidden";
  menu.style.display = "block";
  menu.style.left = "0";
  menu.style.top = "0";

  const bounds = menu.getBoundingClientRect();
  const availableWidth = window.innerWidth;
  const availableHeight = window.innerHeight;
  let left = x + cursorGap;
  let top = y + cursorGap;

  if (left + bounds.width > availableWidth - margin) left = x - bounds.width - cursorGap;
  if (top + bounds.height > availableHeight - margin) top = y - bounds.height - cursorGap;

  left = Math.max(margin, Math.min(left, availableWidth - bounds.width - margin));
  top = Math.max(margin, Math.min(top, availableHeight - bounds.height - margin));
  menu.style.left = `${left}px`;
  menu.style.top = `${top}px`;
  menu.style.visibility = "visible";
}

export function showContextMenu(x, y) {
  const menu = byId("contextMenu");
  const rect = getSelectedRect();
  const request = ++contextMenuRequest;
  const goTo = menu.querySelector('[data-action="goto"]');
  if (goTo) goTo.classList.toggle("disabled", !rect?.is_folder);
  const properties = menu.querySelector('[data-action="properties"]');
  if (properties) properties.classList.toggle("disabled", !rect?.full_path || isPassiveRect(rect));
  const deleteAction = menu.querySelector('[data-action="delete"]');
  const deleteLabel = deleteAction?.querySelector("span");
  const trashItem = !!rect?.is_in_trash && !rect?.is_trash_root;
  const restoreAction = menu.querySelector('[data-action="restore"]');
  if (restoreAction) restoreAction.hidden = !trashItem;
  if (deleteAction) {
    deleteAction.classList.remove("disabled");
    deleteAction.classList.add("context-menu-delete");
  }
  if (deleteLabel) {
    deleteLabel.textContent = rect?.is_trash_root
      ? `Empty ${trashDestinationName()}`
      : trashItem ? "Delete permanently" : "Delete";
  }
  const defaultOpen = menu.querySelector('[data-action="open-default"]');
  const defaultOpenLabel = defaultOpen?.querySelector("span");
  if (defaultOpen) defaultOpen.hidden = !rect || rect.is_folder;
  if (defaultOpenLabel) defaultOpenLabel.textContent = "Open with default application";

  placeContextMenu(menu, x, y);
  if (!rect || rect.is_folder || !defaultOpenLabel) return;

  const cachedName = defaultApplicationNames.get(rect.full_path);
  if (cachedName !== undefined) {
    if (cachedName) defaultOpenLabel.textContent = `Open with ${cachedName}`;
    placeContextMenu(menu, x, y);
    return;
  }

  GetDefaultApplicationName(rect.full_path)
    .then(name => {
      const applicationName = String(name || "").trim();
      defaultApplicationNames.set(rect.full_path, applicationName);
      if (request !== contextMenuRequest || menu.style.display === "none" || getSelectedRect()?.full_path !== rect.full_path) return;
      if (applicationName) defaultOpenLabel.textContent = `Open with ${applicationName}`;
      placeContextMenu(menu, x, y);
    })
    .catch(() => defaultApplicationNames.set(rect.full_path, ""));
}

export function hideContextMenu() {
  contextMenuRequest++;
  byId("contextMenu").style.display = "none";
}

async function openRectWithDefault(rect = getSelectedRect()) {
  if (!rect?.full_path || isPassiveRect(rect)) return;
  hideContextMenu();
  hideRectToast();
  try {
    await OpenPath(rect.full_path);
  } catch (error) {
    showErrorToast(error);
  }
}

async function openRectWithChooser(rect = getSelectedRect()) {
  if (!rect?.full_path || isPassiveRect(rect)) return;
  hideContextMenu();
  hideRectToast();
  try {
    await OpenWith(rect.full_path);
  } catch (error) {
    showErrorToast(error);
  }
}

async function copySelectedPathAt(position) {
  const rect = getSelectedRect();
  if (!rect?.full_path) return;
  try {
    await navigator.clipboard.writeText(rect.full_path);
  } catch {
    const textarea = document.createElement("textarea");
    textarea.value = rect.full_path;
    textarea.style.position = "fixed";
    textarea.style.opacity = "0";
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    document.execCommand("copy");
    textarea.remove();
  }
  const point = position || mousePosition;
  showToastAt(point.x, point.y);
}

async function handleContextMenuAction(event) {
  const item = event.target.closest("li");
  const rect = getSelectedRect();
  if (!item || !rect) return;
  if (item.dataset.action === "copy") {
    await copySelectedPathAt({ x: event.clientX, y: event.clientY });
  } else if (item.dataset.action === "delete") {
    requestSelectedDeletion();
  } else if (item.dataset.action === "restore") {
    await requestSelectedRestore();
  } else if (item.dataset.action === "open" && rect.full_path) {
    await OpenInFileBrowser(rect.full_path);
  } else if (item.dataset.action === "open-default" && !rect.is_folder) {
    await openRectWithDefault(rect);
  } else if (item.dataset.action === "open-with") {
    await openRectWithChooser(rect);
  } else if (item.dataset.action === "properties" && rect.full_path && !isPassiveRect(rect)) {
    try {
      await ShowProperties(rect.full_path);
    } catch (error) {
      showErrorToast(error);
    }
  } else if (item.dataset.action === "goto") {
    visit(rect.node_id);
  }
}

export function initFileActions(options) {
  redraw = options.redraw;
  getSelectedRect = options.getSelectedRect;
  isPassiveRect = options.isPassiveRect;
  byId("cancelDeleteButton").addEventListener("click", closeDeleteConfirmation);
  byId("confirmDeleteButton").addEventListener("click", confirmSelectedDeletion);
  byId("deleteConfirmDialog").addEventListener("cancel", event => {
    event.preventDefault();
    closeDeleteConfirmation();
  });
  byId("contextMenu").addEventListener("click", handleContextMenuAction);
  window.addEventListener("click", hideContextMenu);
  const handleOpenShortcut = event => {
    if (!shortcutCanRun(event)) return;
    const bindings = AppState.profile?.controls;
    const openWith = eventMatchesShortcut(event, bindings?.openWith);
    const open = eventMatchesShortcut(event, bindings?.open);
    if (!openWith && !open) return;
    const rect = getSelectedRect();
    if (!rect?.full_path || isPassiveRect(rect)) return;
    event.preventDefault();
    if (openWith) openRectWithChooser(rect);
    else openRectWithDefault(rect);
  };
  window.addEventListener("keydown", handleOpenShortcut);
  window.addEventListener("mousedown", handleOpenShortcut);
  window.addEventListener("keydown", event => {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "c" && getSelectedRect()?.full_path) {
      event.preventDefault();
      copySelectedPathAt();
    }
  });
  const handleDeleteShortcut = event => {
    if (!shortcutCanRun(event) || !eventMatchesShortcut(event, AppState.profile?.controls?.delete)) return;
    if (!getSelectedRect()) return;
    event.preventDefault();
    requestSelectedDeletion();
  };
  window.addEventListener("keydown", handleDeleteShortcut);
  window.addEventListener("mousedown", handleDeleteShortcut);
}
