const elementCache = new Map();

export function byId(id) {
  if (!elementCache.has(id)) {
    const element = document.getElementById(id);
    if (!element) throw new Error(`Missing required UI element #${id}`);
    elementCache.set(id, element);
  }
  return elementCache.get(id);
}

export function optionalById(id) {
  return document.getElementById(id);
}

export function query(selector) {
  return document.querySelector(selector);
}

export function queryAll(selector) {
  return document.querySelectorAll(selector);
}
