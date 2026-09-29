import { readFile } from "node:fs/promises";
import vm from "node:vm";

export const noop = () => {};

export function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

// Load the actual application module in isolation. Only its dependencies and
// browser boundaries are replaced; no production source is rewritten for tests.
export async function loadUI(file, dependencies, globals = {}) {
  const context = vm.createContext(globals);
  const source = await readFile(new URL(`../../web/${file}`, import.meta.url), "utf8");
  const module = new vm.SourceTextModule(source, { context, identifier: file });
  const linked = new Map();
  const link = async name => {
    if (linked.has(name)) return linked.get(name);
    if (name === "./selection.js" && !Object.hasOwn(dependencies, name)) {
      const source = await readFile(new URL("../../web/selection.js", import.meta.url), "utf8");
      const selected = new vm.SourceTextModule(source, { context, identifier: name });
      linked.set(name, selected);
      await selected.link(link);
      return selected;
    }
    if (!Object.hasOwn(dependencies, name)) throw new Error(`Missing test dependency: ${name}`);
    const exports = dependencies[name];
    const dependency = new vm.SyntheticModule(Object.keys(exports), function () {
      for (const [key, value] of Object.entries(exports)) this.setExport(key, value);
    }, { context });
    linked.set(name, dependency);
    return dependency;
  };
  await module.link(link);
  await module.evaluate();
  return module.namespace;
}

export function eventTarget() {
  const listeners = new Map();
  return {
    addEventListener(name, callback) {
      if (!listeners.has(name)) listeners.set(name, []);
      listeners.get(name).push(callback);
    },
    async emit(name, event = {}) {
      event.preventDefault ??= noop;
      for (const callback of listeners.get(name) || []) await callback(event);
    },
  };
}

export function dom() {
  const elements = new Map();
  const byId = id => {
    if (!elements.has(id)) {
      const attributes = new Map();
      const classes = new Set();
      elements.set(id, {
        ...eventTarget(), hidden: true, open: false, disabled: false, textContent: "", value: "",
        style: { setProperty: noop },
        classList: { add: value => classes.add(value), remove: value => classes.delete(value), contains: value => classes.has(value),
          toggle(value, force = !classes.has(value)) { if (force) classes.add(value); else classes.delete(value); return force; } },
        setAttribute: (key, value) => attributes.set(key, String(value)),
        getAttribute: key => attributes.get(key) ?? null,
        showModal() { this.open = true; }, close() { this.open = false; },
      });
    }
    return elements.get(id);
  };
  return { byId };
}
