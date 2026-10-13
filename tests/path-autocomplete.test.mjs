import assert from "node:assert/strict";
import test from "node:test";
import { deferred, dom, eventTarget, loadUI } from "./helpers/ui.mjs";

async function harness() {
  const { byId } = dom();
  const input = byId("pathInput"), list = byId("pathSuggestions");
  input.removeAttribute = () => {};
  input.focus = () => {};
  input.setSelectionRange = () => {};
  const requests = [], timers = new Map();
  let next = 0;
  const ui = await loadUI("path-autocomplete.js", {
    "./dom.js": { byId },
    "./wailsjs/go/main/App.js": { SuggestFolders: query => { const request = deferred(); requests.push({ query, ...request }); return request.promise; } },
  }, { setTimeout: fn => { timers.set(++next, fn); return next; }, clearTimeout: id => timers.delete(id),
    document: { createElement: () => ({ ...eventTarget(), setAttribute() {}, scrollIntoView() {} }) } });
  ui.initPathAutocomplete();
  return { input, list, requests,
    async type(query) { input.value = query; await input.emit("input"); const fn = [...timers.values()].at(-1); timers.clear(); return { pending: fn() }; },
    async key(key) { let prevented = false; await input.emit("keydown", { key, preventDefault() { prevented = true; } }); return prevented; } };
}

test("autocomplete ignores stale results and accepts keyboard selection without scanning", async () => {
  const h = await harness();
  const first = await h.type('/a');
  const second = await h.type('/b');
  h.requests[1].resolve(['/beta/', '/bravo/']); await second.pending;
  h.requests[0].resolve(['/alpha/']); await first.pending;
  assert.equal(h.list.children[0].textContent, '/beta/');
  await h.key('ArrowDown');
  await h.key('ArrowDown');
  assert.equal(await h.key('Enter'), true);
  assert.equal(h.input.value, '/bravo/');
  assert.equal(h.list.hidden, true);
});

test("Tab completes the first folder and blur or Escape dismiss pending suggestions", async () => {
  const h = await harness();
  const first = await h.type('/a');
  h.requests[0].resolve(['/alpha/']); await first.pending;
  assert.equal(await h.key('Tab'), true);
  assert.equal(h.input.value, '/alpha/');
  const second = await h.type('/b');
  await h.input.emit('blur');
  h.requests[2].resolve(['/beta/']); await second.pending;
  assert.equal(h.list.hidden, true);
  const third = await h.type('/c');
  h.requests[3].resolve(['/charlie/']); await third.pending;
  assert.equal(await h.key('Escape'), true);
  assert.equal(h.list.hidden, true);
});


test("accepting folders queries each next level for keyboard-only navigation", async () => {
  const h = await harness();
  const first = await h.type('/a');
  h.requests[0].resolve(['/alpha/']); await first.pending;
  await h.key('Tab');
  assert.equal(h.requests[1].query, '/alpha/');
  h.requests[1].resolve(['/alpha/one/', '/alpha/two/']);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.list.hidden, false);
  await h.key('ArrowDown');
  await h.key('ArrowDown');
  await h.key('Tab');
  assert.equal(h.input.value, '/alpha/two/');
  assert.equal(h.requests[2].query, '/alpha/two/');
  await h.key('Escape');
  h.requests[2].resolve(['/alpha/two/child/']);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.list.hidden, true);
});
