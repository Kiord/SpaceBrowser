import assert from 'node:assert/strict';
import test from 'node:test';
import { loadUI, eventTarget } from './helpers/ui.mjs';

const element = () => ({ hidden: false, attributes: {}, textContent: '', disabled: true,
  setAttribute(name, value) { this.attributes[name] = value; },
  style: { setProperty(name, value) { this[name] = value; } } });

for (const state of ['running', 'paused', 'queued', 'completed', 'cancelled', 'failed']) {
  test(`scan status renders ${state} and keeps controls visible and disables unavailable actions`, async () => {
    const ui = await loadUI('scan-status.js', { './format.js': { formatDuration: ms => `${ms}ms` } });
    const refs = Object.fromEntries(['container','time','percent','bar','progress','actions','pause','cancel','refresh','icon'].map(name => [name, element()]));
    ui.updateScanStatus(refs, { state, progress: { elapsedMilliseconds: 1200, fraction: .5 } });
    assert.equal(refs.container.hidden, state === 'cancelled');
    assert.equal(refs.container.attributes['data-state'], state);
    assert.equal(refs.actions.hidden, false);
    for (const name of ['pause', 'cancel', 'refresh']) assert.equal(refs[name].hidden, false);
    assert.equal(refs.pause.disabled, !['running','paused','queued'].includes(state));
    assert.equal(refs.cancel.disabled, refs.pause.disabled);
    assert.equal(refs.refresh.disabled, state !== 'completed');
    if (state === 'paused') {
      assert.equal(refs.pause.attributes['aria-label'], 'Continue scan');
      assert.equal(refs.time.textContent, 'Paused · 1200ms');
    }
    if (state === 'completed') {
      assert.equal(refs.time.textContent, 'Done in 1.2s');
      assert.equal(refs.bar.style['--scan-progress'], '100%');
    }
  });
}

test('completed tile exposes a refresh action for its current job only', async () => {
  const nodes = new Map();
  const makeElement = () => ({ ...element(), ...eventTarget(), children: [],
    append(child) { this.children.push(child); },
    querySelector(selector) {
      if (!nodes.has(selector)) nodes.set(selector, makeElement());
      return nodes.get(selector);
    } });
  const ui = await loadUI('scan-status.js', { './format.js': { formatDuration: String } },
    { document: { createElement: makeElement } });
  const refreshed = [];
  const tile = ui.createTileScanStatus(() => {}, () => {}, job => refreshed.push(job.id));
  const actions = nodes.get('[data-scan="actions"]');
  const refresh = actions.children[0];
  tile.update({ id: 1, state: 'running' });
  assert.equal(refresh.hidden, false);
  assert.equal(refresh.disabled, true);
  tile.update({ id: 1, state: 'completed' });
  assert.equal(actions.hidden, false);
  assert.equal(refresh.hidden, false);
  assert.equal(nodes.get('[data-scan="pause"]').hidden, false);
  assert.equal(nodes.get('[data-scan="pause"]').disabled, true);
  assert.equal(nodes.get('[data-scan="cancel"]').hidden, false);
  assert.equal(nodes.get('[data-scan="cancel"]').disabled, true);
  await refresh.emit('click');
  assert.deepEqual(refreshed, [1]);
  tile.update({ id: 2, state: 'paused' });
  assert.equal(refresh.hidden, false);
  assert.equal(refresh.disabled, true);
  assert.equal(nodes.get('[data-scan="pause"]').hidden, false);
  await refresh.emit('click');
  assert.deepEqual(refreshed, [1]);
});


test('evicted scan hides its old progress bar and retains rescan control', async () => {
  const ui = await loadUI('scan-status.js', { './format.js': { formatDuration: String } });
  const refs = Object.fromEntries(['container','time','percent','bar','progress','actions','pause','cancel','refresh','icon'].map(name => [name, element()]));
  ui.updateScanStatus(refs, { state: 'completed', evicted: true });
  assert.equal(refs.time.textContent, 'Scan released');
  assert.equal(refs.progress.hidden, true);
  assert.equal(refs.refresh.disabled, false);
});
