import { ListDirectory } from "./wailsjs/go/app/App.js";
import { byId } from "./dom.js";
import { AppState } from "./state.js";
import { formatSize } from "./format.js";
import { getSelectedRects, isPassiveRect, selectionIds } from "./selection.js";

let options;
let request = 0, offset = 0, scope = "";
let checks = new Map(), focusTargets = new Map();

export function clearFileList() {
  request++;
  scope = "";
  AppState.listItems = [];
  byId("fileList").hidden = true;
  byId("fileListRows").replaceChildren();
  checks.clear(); focusTargets.clear();
}

export function syncFileListSelection() {
  for (const [id, check] of checks) check.checked = selectionIds().has(id);
  const selected = getSelectedRects();
  const summary = selected.length ? `${selected.length} selected, ${formatSize(selected.reduce((total, item) => total + item.size, 0))}` : "No items selected";
  if (byId("fileListSelection").textContent !== summary) byId("fileListSelection").textContent = summary;
  for (const action of ["open", "delete", "restore", "refresh", "properties"]) {
    byId(`fileList-${action}`).disabled = !selected.length ||
      (action === "delete" && (!AppState.profile?.allowDelete || AppState.scanInProgress)) ||
      (action === "restore" && (selected.length !== 1 || !selected[0].is_in_trash || selected[0].is_trash_root || AppState.scanInProgress)) ||
      (action === "refresh" && (selected.some(item => !item.is_folder) || AppState.scanInProgress)) ||
      (action === "properties" && selected.length !== 1);
  }
}

export async function refreshFileList() {
  if (!options) return;
  if (AppState.node_id == null || AppState.homeVisible) { clearFileList(); return; }
  const nextScope = `${AppState.navSession}:${AppState.node_id}`;
  if (nextScope !== scope) { clearFileList(); scope = nextScope; offset = 0; }
  const token = ++request;
  const wasHidden = byId("fileList").hidden;
  byId("fileList").hidden = false;
  if (wasHidden) options.resize?.();
  try {
    const listing = await ListDirectory(AppState.node_id, byId("fileListSort").value, offset);
    if (token !== request || nextScope !== `${AppState.navSession}:${AppState.node_id}`) return;
    AppState.listItems = listing.items;
    const focused = document.activeElement?.dataset?.fileListFocus;
    checks = new Map(); focusTargets = new Map();
    const rows = listing.items.map(item => {
      const row = document.createElement("tr");
      const selectCell = document.createElement("td"), nameCell = document.createElement("td"), sizeCell = document.createElement("td");
      const check = document.createElement("input");
      check.type = "checkbox";
      check.disabled = isPassiveRect(item);
      check.setAttribute("aria-label", `Select ${item.is_folder ? 'folder' : 'file'} ${item.name}`);
      check.dataset.fileListFocus = `select:${item.node_id}`;
      check.addEventListener("change", () => options.selectItem(item, { additive: true }));
      checks.set(item.node_id, check);
      focusTargets.set(check.dataset.fileListFocus, check);
      const open = document.createElement("button");
      open.type = "button";
      open.textContent = item.name;
      open.title = item.full_path || item.name;
      open.disabled = isPassiveRect(item);
      open.setAttribute("aria-label", `${item.is_folder ? 'Enter folder' : 'Open file'} ${item.name}`);
      open.dataset.fileListFocus = `open:${item.node_id}`;
      focusTargets.set(open.dataset.fileListFocus, open);
      open.addEventListener("click", () => {
        options.selectItem(item, { preserve: true });
        if (item.is_folder) options.visit(item.node_id);
        else options.runAction("open-default");
      });
      selectCell.append(check); nameCell.append(open);
      sizeCell.textContent = formatSize(item.size);
      row.append(selectCell, nameCell, sizeCell);
      return row;
    });
    byId("fileListRows").replaceChildren(...rows);
    byId("fileListStatus").textContent = `${listing.path}: ${listing.total} entries${listing.total ? `, showing ${offset + 1}–${offset + rows.length}` : ''}`;
    byId("fileListPrevious").disabled = offset === 0;
    byId("fileListNext").disabled = offset + rows.length >= listing.total;
    byId("fileListParent").disabled = listing.parentId < 0;
    byId("fileListParent").onclick = () => options.visit(listing.parentId);
    syncFileListSelection();
    if (focused) focusTargets.get(focused)?.focus();
  } catch (error) {
    if (token !== request) return;
    AppState.listItems = [];
    byId("fileListRows").replaceChildren();
    byId("fileListStatus").textContent = `Unable to list this folder: ${error}`;
  }
}

export function initFileList(callbacks) {
  options = callbacks;
  const changePage = async delta => {
    offset = Math.max(0, offset + delta);
    options.selectItem(null);
    await refreshFileList();
  };
  byId("fileListPrevious").addEventListener("click", () => changePage(-200));
  byId("fileListNext").addEventListener("click", () => changePage(200));
  byId("fileListSort").addEventListener("change", () => changePage(-offset));
  for (const action of ["open", "delete", "restore", "refresh", "properties"]) {
    byId(`fileList-${action}`).addEventListener("click", () => options.runAction(action));
  }
  byId("fileList").addEventListener("keydown", event => {
    // Native buttons and checkboxes own their activation keys, independently
    // of the application's configurable Enter/Space shortcuts.
    if (event.key === "Enter" || event.key === " ") event.stopPropagation();
    const targets = [...checks.values()].filter(check => !check.disabled);
    const index = targets.indexOf(document.activeElement);
    if (index < 0) return;
    const next = { ArrowDown: Math.min(index + 1, targets.length - 1), ArrowUp: Math.max(0, index - 1), Home: 0, End: targets.length - 1 }[event.key];
    if (next != null) { event.preventDefault(); event.stopPropagation(); targets[next]?.focus(); }
  });
}
