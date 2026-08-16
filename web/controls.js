const MODIFIER_KEYS = new Set(["Alt", "AltGraph", "Control", "Meta", "Shift"]);

const KEY_NAMES = Object.freeze({
  " ": "Space",
  Esc: "Escape",
  "+": "Plus",
  "-": "Minus",
});

export function shortcutFromEvent(event) {
  if (!event || event.isComposing) return "";
  let key = "";
  if (Number.isInteger(event.button)) {
    if (event.button <= 2) return "";
    key = `Mouse ${event.button + 1}`;
  } else {
    if (MODIFIER_KEYS.has(event.key)) return "";
    key = KEY_NAMES[event.key] || event.key;
  }
  if (!key) return "";
  if (key.length === 1) key = key.toUpperCase();

  const parts = [];
  if (event.ctrlKey) parts.push("Ctrl");
  if (event.altKey) parts.push("Alt");
  if (event.shiftKey) parts.push("Shift");
  if (event.metaKey) parts.push("Meta");
  parts.push(key);
  return parts.join("+");
}

export function eventMatchesShortcut(event, shortcut) {
  return !!shortcut && shortcutFromEvent(event) === shortcut;
}

export function shortcutTargetIsEditable(event) {
  return event.target instanceof HTMLElement
    && !!event.target.closest("input, textarea, select, [contenteditable='true']");
}

export function shortcutCanRun(event) {
  return !event.isComposing
    && !event.repeat
    && !event.defaultPrevented
    && !document.querySelector("dialog[open]")
    && !shortcutTargetIsEditable(event);
}
