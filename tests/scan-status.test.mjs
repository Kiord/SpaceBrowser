import assert from 'node:assert/strict';
import test from 'node:test';
import { loadUI } from './helpers/ui.mjs';

const element = () => ({ hidden: false, attributes: {}, textContent: '', disabled: true,
  setAttribute(name, value) { this.attributes[name] = value; },
  style: { setProperty(name, value) { this[name] = value; } } });

for (const state of ['running', 'paused', 'queued', 'completed', 'cancelled', 'failed']) {
  test(`scan status renders ${state} and only exposes relevant controls`, async () => {
    const ui = await loadUI('scan-status.js', { './format.js': { formatDuration: ms => `${ms}ms` } });
    const refs = Object.fromEntries(['container','time','percent','bar','progress','actions','pause','icon'].map(name => [name, element()]));
    ui.updateScanStatus(refs, { state, progress: { elapsedMilliseconds: 1200, fraction: .5 } });
    assert.equal(refs.container.hidden, state === 'cancelled');
    assert.equal(refs.container.attributes['data-state'], state);
    assert.equal(refs.actions.hidden, !['running','paused','queued'].includes(state));
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
