import assert from 'node:assert/strict';
import test from 'node:test';
import { dom, eventTarget, loadUI, deferred } from './helpers/ui.mjs';

async function harness() {
  const { byId } = dom();
  const document = { activeElement: null, createElement() {
    const element = { ...eventTarget(), children: [], dataset: {}, setAttribute(k,v) { this[k]=v; }, append(...items) { this.children.push(...items); }, focus() { document.activeElement=this; } };
    return element;
  } };
  const state = { node_id: 0, navSession: 1, rects: [], selectedNodeIds: new Set(), profile: { allowDelete:true } };
  const folder = { node_id:1, name:'folder', full_path:'/root/folder', size:20, is_folder:true };
  const tiny = { node_id:2, name:'tiny.txt', full_path:'/root/tiny.txt', size:1 };
  const calls = [], backend = { ListDirectory: async (...args) => { calls.push(args); return { items:[folder,tiny], total:202, path:'/root', parentId:9 }; } };
  byId('fileListSort').value='size';
  const selection = await loadUI('selection.js', { './state.js': { AppState:state } });
  const ui = await loadUI('file-list.js', {
    './wailsjs/go/app/App.js': { ListDirectory: (...args) => backend.ListDirectory(...args) },
    './dom.js':{byId}, './state.js':{AppState:state}, './format.js':{formatSize:String},
    './selection.js': Object.fromEntries(Object.keys(selection).map(key=>[key,selection[key]])),
  }, {document});
  const actions=[];
  ui.initFileList({ selectItem(item, options) { selection.selectItem(item, options); ui.syncFileListSelection(); }, visit:id=>actions.push(['visit',id]), runAction:action=>actions.push(['action',action]) });
  return {ui,byId,state,backend,calls,actions,document,selection,folder,tiny};
}

test('keyboard list selects tiny files, synchronizes canvas selection, and exposes actions', async()=>{
  const h=await harness(); await h.ui.refreshFileList();
  const rows=h.byId('fileListRows').children;
  const folderCheck=rows[0].children[0].children[0], tinyCheck=rows[1].children[0].children[0];
  assert.equal(tinyCheck['aria-label'],'Select file tiny.txt');
  tinyCheck.focus(); await tinyCheck.emit('change');
  assert.equal(h.selection.getSelectedRect().node_id,2);
  assert.equal(h.byId('fileList-delete').disabled,false);
  await h.byId('fileList-delete').emit('click');
  assert.deepEqual(h.actions,[['action','delete']]);
  h.state.selectedNodeIds=new Set([1]); h.ui.syncFileListSelection();
  assert.equal(folderCheck.checked,true); assert.equal(tinyCheck.checked,false);
  let stopped=false;
  folderCheck.focus();
  await h.byId('fileList').emit('keydown',{key:'ArrowDown',stopPropagation(){stopped=true;}});
  assert.equal(h.document.activeElement,tinyCheck); assert.equal(stopped,true);
  await rows[0].children[1].children[0].emit('click');
  assert.deepEqual(h.actions.at(-1),['visit',1]);
  await h.byId('fileListParent').onclick();
  assert.deepEqual(h.actions.at(-1),['visit',9]);
});

test('list preserves focus, paginates, sorts and ignores stale replies',async()=>{
  const h=await harness(); await h.ui.refreshFileList();
  h.byId('fileListRows').children[1].children[0].children[0].focus();
  await h.ui.refreshFileList();
  assert.equal(h.document.activeElement,h.byId('fileListRows').children[1].children[0].children[0]);
  await h.byId('fileListNext').emit('click');
  assert.deepEqual(h.calls.at(-1),[0,'size',200]);
  h.byId('fileListSort').value='name'; await h.byId('fileListSort').emit('change');
  assert.deepEqual(h.calls.at(-1),[0,'name',0]);
  const slow=deferred(); h.backend.ListDirectory=()=>slow.promise;
  const pending=h.ui.refreshFileList(); h.ui.clearFileList();
  slow.resolve({items:[h.tiny],total:1,path:'/stale',parentId:-1}); await pending;
  assert.equal(h.byId('fileList').hidden,true); assert.equal(h.state.listItems.length,0);
});
