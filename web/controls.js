const MODIFIER_KEYS = new Set(["Alt", "AltGraph", "Control", "Meta", "Shift"]);

const KEY_NAMES = Object.freeze({
  " ": "Space",
  Esc: "Escape",
  "+": "Plus",
  "-": "Minus",
});

const AUXILIARY_MOUSE_EVENTS = Object.freeze([
  "pointerdown",
  "mousedown",
  "pointerup",
  "mouseup",
  "auxclick",
]);

const AUXILIARY_EVENT_DEDUPLICATION_MS = 1000;

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

// Webviews do not consistently expose auxiliary mouse buttons through the
// same DOM event. Listen to all standard variants and dispatch each physical
// press once, preferring its earliest event.
export function addControlEventListeners(handler, options = {}) {
  const capture = !!options.capture;
  let lastAuxiliaryEvent = null;

  const handleAuxiliaryEvent = event => {
    if (!Number.isInteger(event.button) || event.button <= 2) return;
    const now = Number.isFinite(event.timeStamp) ? event.timeStamp : performance.now();
    const previous = lastAuxiliaryEvent;
    const samePress = previous
      && previous.button === event.button
      && now - previous.time >= 0
      && now - previous.time < AUXILIARY_EVENT_DEDUPLICATION_MS;
    const duplicate = samePress && (
      (event.type === "mousedown" && previous.type === "pointerdown")
      || (event.type === "pointerup" && (previous.type === "pointerdown" || previous.type === "mousedown"))
      || (event.type === "mouseup" && previous.type !== "mouseup" && previous.type !== "auxclick")
      || (event.type === "auxclick" && previous.type !== "auxclick")
    );
    if (duplicate) return;
    lastAuxiliaryEvent = { button: event.button, time: now, type: event.type };
    handler(event);
  };

  window.addEventListener("keydown", handler, capture);
  AUXILIARY_MOUSE_EVENTS.forEach(type => window.addEventListener(type, handleAuxiliaryEvent, capture));
}
