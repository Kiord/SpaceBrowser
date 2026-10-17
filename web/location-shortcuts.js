import { cycleLocation } from "./locations.js";

export function initLocationShortcuts({ isCollapsed, setCollapsed }) {
  let temporary = false, cycling = false;
  const finish = () => {
    cycling = false;
    if (!temporary) return;
    temporary = false;
    setCollapsed(true);
  };
  window.addEventListener("keydown", event => {
    if (event.key !== "Tab" || !event.ctrlKey || event.altKey || event.metaKey
      || event.isComposing || document.querySelector("dialog[open]")) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (isCollapsed()) {
      temporary = true;
      setCollapsed(false);
    }
    const selected = cycleLocation(event.shiftKey ? -1 : 1, cycling);
    cycling = selected;
    if (!selected) finish();
  }, true);
  window.addEventListener("keyup", event => {
    if (event.key === "Control" || !event.ctrlKey) finish();
  });
  window.addEventListener("blur", finish);
  // An explicit toggle supersedes the temporary visibility change.
  return { cancelPreview() { temporary = false; cycling = false; } };
}
