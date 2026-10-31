import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { deferred, eventTarget, loadUI } from './helpers/ui.mjs';

async function harness(platform = 'windows', names = ['Projects', 'SpaceBrowser', 'internal']) {
  const elements = new Map(), calls = [], errors = [];
  let width = 1000;
  const document = { activeElement: null, createElement: () => element() };
  const element = () => {
    let text = '';
    return { ...eventTarget(), children: [], attributes: {}, hidden: false, style: {}, offsetWidth: 220, offsetHeight: 25,
      get textContent() { return this.children.length ? this.children.map(child => child.textContent).join('') : text; },
      set textContent(value) { text = value; this.children = []; },
      replaceChildren(...children) { this.children = children; text = ''; },
      setAttribute(key, value) { this.attributes[key] = value; },
      getAttribute(key) { return this.attributes[key]; },
      get clientWidth() { return width; },
      get scrollWidth() { return Array.from(this.textContent).length * 8; },
      getBoundingClientRect() { return { left: 10, top: 7, bottom: 25 }; },
      focus() { document.activeElement = this; },
    };
  };
  const byId = id => { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); };
  byId('breadcrumbs').hidden = true;
  byId('breadcrumbTooltip').hidden = true;
  const separator = platform === 'windows' ? '\\' : '/';
  const crumbs = names.map((name, id) => ({ node_id: id, parent_id: id ? id - 1 : null, name, full_path: names.slice(0, id + 1).join(separator), is_folder: true, size: 100 - id }));
  const state = { node_id: crumbs.at(-1).node_id, navSession: 1, scanRootPath: crumbs[0].full_path,
    rects: [crumbs.at(-1)], selectedNodeIds: new Set(), profile: { platformSystem: platform } };
  const backend = { GetBreadcrumbs: async (...args) => { calls.push(['load', ...args]); return crumbs; } };
  const window = eventTarget();
  window.innerWidth = 1000;
  window.innerHeight = 700;
  const ui = await loadUI('breadcrumbs.js', {
    './wailsjs/go/app/App.js': { GetBreadcrumbs: (...args) => backend.GetBreadcrumbs(...args) },
    './dom.js': { byId }, './state.js': { AppState: state }, './logging.js': { logError: (...args) => errors.push(args) },
  }, { document, window });
  ui.initBreadcrumbs({ visit: id => calls.push(['visit', id]), select: item => { state.selectedNodeIds = new Set([item.node_id]); calls.push(['select', item.full_path]); },
    showContextMenu: (x,y) => calls.push(['menu', x, y]), hideContextMenu: () => calls.push(['hide menu']), visibilityChanged: () => calls.push(['resize']) });
  return { ui, state, crumbs, calls, errors, backend, byId, window, document,
    buttons: () => byId('breadcrumbs').children.filter(child => child.type === 'button'),
    width: value => { width = value; } };
}

test('breadcrumbs contract earlier names first and restore full names when widened', async () => {
  const h = await harness();
  h.width(230); await h.ui.updateBreadcrumbs();
  assert.deepEqual(h.buttons().map(button => button.textContent), ['P', 'SpaceBrowser', 'internal']);
  assert.equal(h.byId('breadcrumbs').textContent, 'P > SpaceBrowser > internal');
  h.width(150); h.ui.fitBreadcrumbs();
  assert.deepEqual(h.buttons().map(button => button.textContent), ['P', 'S', 'internal']);
  h.width(40); await h.window.emit('resize');
  assert.deepEqual(h.buttons().map(button => button.textContent), ['P', 'S', 'i']);
  h.width(1000); h.ui.fitBreadcrumbs();
  assert.deepEqual(h.buttons().map(button => button.textContent), ['Projects', 'SpaceBrowser', 'internal']);
  assert.equal(h.buttons()[1].getAttribute('aria-label'), 'Projects\\SpaceBrowser');
  assert.equal(h.buttons()[2].getAttribute('aria-current'), 'location');
  assert.equal(h.calls.filter(call => call[0] === 'load').length, 1);
});

test('arrow separators work on every OS and Unicode initials stay intact', async () => {
  for (const [platform, names, expected] of [
    ['windows', ['C:\\', 'Users', 'Documents'], 'C:\\ > Users > Documents'],
    ['linux', ['/', 'home', 'Documents'], '/ > home > Documents'],
    ['darwin', ['/', 'Users', 'Documents'], '/ > Users > Documents'],
    ['linux', ['folder>', 'child'], 'folder> > child'],
  ]) {
    const h = await harness(platform, names); await h.ui.updateBreadcrumbs();
    assert.equal(h.byId('breadcrumbs').textContent, expected);
  }
  const h = await harness('linux', ['e\u0301clair', '文件', '<folder>']);
  h.width(20); await h.ui.updateBreadcrumbs();
  assert.equal(h.buttons()[0].textContent, 'e\u0301');
  assert.equal(h.buttons()[1].textContent, '文');
  assert.equal(h.buttons()[2].getAttribute('aria-label'), 'e\u0301clair/文件/<folder>');
  assert.equal(h.buttons().length, 3); // extremely narrow paths retain every target
});

test('full-path tooltips use the shared toast, stay in the viewport and hide with the target', async () => {
  const h = await harness(); await h.ui.updateBreadcrumbs();
  const button = h.buttons()[1], tooltip = h.byId('breadcrumbTooltip');
  assert.equal(button.title, undefined); // no native OS tooltip over the styled one
  assert.equal(button.getAttribute('aria-describedby'), 'breadcrumbTooltip');
  h.width(40); h.ui.fitBreadcrumbs();
  await button.emit('mouseenter');
  assert.equal(tooltip.hidden, false);
  assert.equal(tooltip.textContent, 'Projects\\SpaceBrowser');
  assert.equal(tooltip.style.top, '32px');
  await button.emit('mouseleave');
  assert.equal(tooltip.hidden, true);
  h.window.innerWidth = 230;
  h.window.innerHeight = 50;
  await button.emit('focus');
  assert.equal(tooltip.hidden, false);
  assert.equal(tooltip.style.left, '8px');
  assert.equal(tooltip.style.top, '8px');
  await button.emit('blur');
  assert.equal(tooltip.hidden, true);
  await button.emit('mouseenter');
  await h.byId('breadcrumbs').emit('scroll');
  assert.equal(tooltip.hidden, true);
  await button.emit('mouseenter');
  h.state.profile.showBreadcrumbs = false;
  await h.ui.updateBreadcrumbs();
  assert.equal(tooltip.hidden, true);
  await button.emit('mouseenter');
  assert.equal(tooltip.hidden, true); // detached buttons cannot show stale paths
});

test('crumbs navigate by ID and context menus target the clicked ancestor', async () => {
  const h = await harness(); await h.ui.updateBreadcrumbs();
  h.width(40); h.ui.fitBreadcrumbs();
  await h.buttons()[1].emit('click');
  assert.deepEqual(h.calls.at(-1), ['visit', 1]);
  h.state.selectedNodeIds = new Set([2]);
  let prevented = false;
  await h.buttons()[0].emit('contextmenu', { clientX: 60, clientY: 40, preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.deepEqual([...h.state.selectedNodeIds], [0]);
  assert.deepEqual(h.calls.slice(-2), [['select', 'Projects'], ['menu', 60, 40]]);
  await h.buttons()[1].emit('keydown', { key: 'F10', shiftKey: true });
  assert.deepEqual(h.calls.at(-1), ['menu', 10, 25]);
  h.state.node_id = 99;
  const before = h.calls.length;
  await h.buttons()[0].emit('click');
  await h.buttons()[0].emit('contextmenu', { clientX: 1, clientY: 1 });
  assert.equal(h.calls.length, before);
});

test('visibility toggles resize the treemap and reject late breadcrumb replies', async () => {
  const h = await harness();
  const pending = deferred(); h.backend.GetBreadcrumbs = () => pending.promise;
  const loading = h.ui.updateBreadcrumbs();
  assert.equal(h.byId('breadcrumbs').hidden, false);
  h.state.profile.showBreadcrumbs = false;
  await h.ui.updateBreadcrumbs();
  pending.resolve(h.crumbs); await loading;
  assert.equal(h.byId('breadcrumbs').hidden, true);
  assert.equal(h.buttons().length, 0);
  assert.equal(h.state.breadcrumbRects.length, 0);
  h.backend.GetBreadcrumbs = async () => h.crumbs;
  h.state.profile.showBreadcrumbs = true;
  await h.ui.updateBreadcrumbs();
  assert.equal(h.buttons().length, 3);
  assert.equal(h.calls.filter(call => call[0] === 'resize').length, 3);
  h.state.homeVisible = true; await h.ui.updateBreadcrumbs();
  assert.equal(h.byId('breadcrumbs').hidden, true);
});

test('refresh updates metadata without losing focus and ignores older responses', async () => {
  const h = await harness(); await h.ui.updateBreadcrumbs();
  h.buttons()[1].focus(); const focused = h.document.activeElement;
  const slow = deferred(); h.backend.GetBreadcrumbs = () => slow.promise;
  h.state.rects = [{ ...h.crumbs.at(-1) }];
  const old = h.ui.updateBreadcrumbs();
  h.state.rects = [{ ...h.crumbs.at(-1) }];
  h.backend.GetBreadcrumbs = async () => h.crumbs.map(item => ({ ...item, size: 500 }));
  await h.ui.updateBreadcrumbs();
  slow.resolve([]); await old;
  assert.equal(h.document.activeElement, focused);
  assert.equal(h.state.breadcrumbRects[1].size, 500);
  assert.equal(h.buttons().length, 3);
});

test('breadcrumb visibility checkbox is enabled by default and only in General settings', async () => {
  const html = await readFile(new URL('../web/index.html', import.meta.url), 'utf8');
  const generalPanel = html.match(/<section id="settingsGeneralPanel"[\s\S]*?<\/section>/)[0];
  assert.match(generalPanel, /id="settingsShowBreadcrumbs"[^>]*\bchecked\b/);
  assert.equal((html.match(/id="settingsShowBreadcrumbs"/g) || []).length, 1);
  assert.doesNotMatch(html, /id="showBreadcrumbs"/);
});
