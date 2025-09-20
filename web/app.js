/* Frontend when the Go backend owns tree + layout.

Backend contract:
  GET  /api/get_full_tree?path=...             -> { ok:true, root_id:<number> }
  GET  /api/layout?node_id=...&w=...&h=...     -> { rects:[ { x,y,w,h, node_id, parent_id, name, size,
                                                         is_folder, is_free_space, depth, full_path,
                                                         children:[rectIndex,...] } ] }
  POST /api/open_in_file_browser               body: { path }

Notes:
- We use rect ARRAY INDEX for picking (id buffer), and rect.node_id for app logic.
- children are rect indices into the SAME rects array.
*/

const AppState = {
  // focus & history (use node_id everywhere)
  node_id: null,
  navHistory: [],
  navIndex: -1,

  // canvases
  colorCanvas: null, colorCtx: null,
  idCanvas: null,    idCtx: null,
  tmpCanvas: null,   tmpCtx: null,
  maskCanvas: null,  maskCtx: null,

  // current layout
  rects: [],
  selectedRectIndex: null,
  selectedNodeId:null
};

const FONT_SIZE = 10;
const CORNER_RADII = 3;
const folderColors = ["#ff9b85","#ffbe76","#ffe066","#7bed9f","#70d6ff","#a29bfe","#dfe4ea"];

// ---------- API ----------


// web/app.js
import { GetFullTree, Layout, OpenInFileBrowser, DefaultPath } from "./wailsjs/go/main/App.js";

async function apiScan(path) {
  const rootId = await GetFullTree(path);
  return { ok: true, root_id: rootId };
}

async function apiLayoutById(nodeId, w, h) {
  const rects = await Layout(nodeId, w, h);
  return { rects };
}

async function apiOpenInFileBrowser(path) {
  await OpenInFileBrowser(path);
}

document.addEventListener("DOMContentLoaded", async () => {
  document.getElementById("analyzeButton")?.addEventListener("click", analyze);
  document.getElementById("triggerFolderSelectButton")?.addEventListener("click", triggerFolderSelect);
  document.getElementById("rootButton")?.addEventListener("click", goToRoot);
  document.getElementById("parentButton")?.addEventListener("click", goToParent);
  document.getElementById("backwardButton")?.addEventListener("click", goBackward);
  document.getElementById("forwardButton")?.addEventListener("click", goForward);

  try {
    const p = await DefaultPath();
    const el = document.getElementById("pathInput");
    if (el && p) el.value = p;
  } catch (e) {
    console.error(e);
  }
});


// ---------- Boot ----------
window.addEventListener("load", () => {
  AppState.colorCanvas = document.getElementById("colorCanvas");
  AppState.idCanvas    = document.getElementById("idCanvas");
  AppState.tmpCanvas   = document.getElementById("tmpCanvas");
  AppState.maskCanvas  = document.getElementById("maskCanvas");

  AppState.colorCtx = AppState.colorCanvas.getContext("2d");
  AppState.idCtx    = AppState.idCanvas.getContext("2d", { willReadFrequently: true });
  AppState.idCtx.imageSmoothingEnabled = false;
  AppState.tmpCtx  = AppState.tmpCanvas.getContext("2d", { alpha: true });
  AppState.maskCtx = AppState.maskCanvas.getContext("2d", { alpha: true });

  resizeCanvas();


  AppState.colorCanvas.addEventListener("click", (e) => {
    const { x, y } = getCanvasCoords(e);
    const rectIndex = rectIndexAtPoint(x, y);
    selectRectByIndex(rectIndex);
    hideContextMenu();
  });

  AppState.colorCanvas.addEventListener("contextmenu", (e) => {
    e.preventDefault();
    const { x, y } = getCanvasCoords(e);
    const rectIndex = rectIndexAtPoint(x, y);
    selectRectByIndex(rectIndex, /*dontDeselect=*/true);
    const r = getSelectedRect();
    if (r && r.is_folder && !r.is_free_space) {
      showContextMenu(e.pageX, e.pageY);
    } else {
      hideContextMenu();
    }
  });
  
  AppState.colorCanvas.addEventListener("dblclick", (e) => {
    const { x, y } = getCanvasCoords(e);
    const rectIndex = rectIndexAtPoint(x, y);
    const rect = AppState.rects[rectIndex];
    if (!rect.is_free_space){
      selectRectByIndex(rectIndex);
      navigateToSelected();
      hideContextMenu();
    }
  });
});


window.addEventListener("resize", debounce(async () => {
  resizeCanvas();
  await redraw(); // re-request layout for current focus
}, 150));




// ---------- Analyze (scan then layout immediately) ----------
async function analyze() {
  const path = document.getElementById("pathInput").value?.trim();
  if (!path) return;

  setUIBusy(true);
  try {
    console.time("scan");
    const res = await apiScan(path);
    console.timeEnd("scan");
    if (res?.error) {
      console.error("scan error:", res.error);
      return;
    }
    const rootId = res?.root_id ?? res?.id;
    if (rootId == null) {
      console.error("scan: missing root_id");
      return;
    }

    // set focus & history
    AppState.node_id = rootId;
    AppState.navHistory = [rootId];
    AppState.navIndex = 0;
    AppState.selectedRectIndex = null;
    AppState.selectedNodeId = null;

    // immediately layout + draw
    await redraw();
  } catch (e) {
    console.error("analyze failed:", e);
  } finally {
    setUIBusy(false);
    updateNavButtons();
  }
}

// ---------- Layout + Draw ----------
async function redraw() {
  const nodeId = AppState.node_id;
  if (nodeId == null) return;

  const w = AppState.colorCanvas.width;
  const h = AppState.colorCanvas.height;

  console.time("layout(fetch)");
  const payload = await apiLayoutById(nodeId, w, h);
  console.timeEnd("layout(fetch)");

  const rects = payload?.rects;
  if (!Array.isArray(rects)) {
    console.warn("no rects from backend");
    return;
  }

  AppState.rects = rects;
  AppState.selectedRectIndex = null;

  console.time("draw");
  drawTreemap(rects);
  console.timeEnd("draw");

  updateNavButtons();
}

function formatModTime(sec) {
  if (!sec) return "";
  const d = new Date(sec * 1000);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function drawTreemap(rects) {
  const ctx = AppState.colorCtx;
  const idc = AppState.idCtx;
  ctx.clearRect(0, 0, AppState.colorCanvas.width, AppState.colorCanvas.height);
  idc.clearRect(0, 0, AppState.idCanvas.width, AppState.idCanvas.height);

  for (let i = 0; i < rects.length; i++) {
    drawRect(rects[i], /*writeId*/true, ctx, i);
  }
}

function textFits(ctx, text, maxw){
  return ctx.measureText(text).width <= maxw;
}

function ellipsize(ctx, text, maxw){
  if (!text) return '';
  if (textFits(ctx, text, maxw)) return text;

  const ell = '…';
  if (!textFits(ctx, ell, maxw)) return ''; 

  let lo = 0, hi = text.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    const s = text.slice(0, mid) + ell;
    textFits(ctx, s, maxw) ? (lo = mid + 1) : (hi = mid);
  }
  const cut = Math.max(0, lo - 1);
  return cut <= 0 ? '' : (text.slice(0, cut) + ell)
}

function getCtxFontBounds(ctx){
  const m = ctx.measureText("Mg"); // tall sample
  const ascent  = m.actualBoundingBoxAscent  ?? Math.round(FONT_SIZE * 0.8);
  const descent = m.actualBoundingBoxDescent ?? Math.max(1, Math.round(FONT_SIZE * 0.2));
  const textH   = ascent + descent;          // visual height of one line
  const lineH   = textH + 2;
  return { ascent, textH, lineH};
}

function filterLinesByAvailableSpace(ctx, lines, fontBounds, maxW, maxH){
  const { lineH, textH } = fontBounds;

  if (maxW <= 0 || maxH <= 0 || !lines || !lines.length) return [];

  const maxLines = Math.max(0, Math.floor((maxH + (lineH - textH)) / lineH));
  if (maxLines <= 0) return [];

  const out = [];

  for (let i = 0; i < lines.length; i++) {
    if (out.length >= maxLines) break;

    const spec = lines[i] || {};

    if (spec.ellipsize) {
      const text = ellipsize(ctx, spec.text, maxW);
      if (text.length > 0)
        out.push(text);
    }
    else if(textFits(ctx, spec.text, maxW)){
      out.push(spec.text);
    }
  }

  return out;
}

function writeCenteredLinesInRect(ctx, lines, fontBounds, rect){
  if (!lines || !lines.length) return;

  const text_lines = filterLinesByAvailableSpace(ctx, lines, fontBounds, rect.w - 2, rect.h - 2);

  const { ascent, lineH, textH } = fontBounds;

  const blockH = text_lines.length * lineH - (lineH - textH);

  const baseY = rect.y + (rect.h - blockH) / 2 + ascent;

  for (let i = 0; i < text_lines.length; i++) {
    const t = text_lines[i];
    if (!t) continue;
    const tw = ctx.measureText(t).width;
    const x  = rect.x + (rect.w - tw) / 2;  
    const y  = baseY + i * lineH;           
    ctx.fillText(t, x, y);
  }
}

function drawRect(rect, writeId, ctx, rectIndex) {
  const isSelected = AppState.selectedNodeId == rect.node_id;
  if (isSelected && rectIndex >= 0) AppState.selectedRectIndex = rectIndex;

  // Fill
  ctx.fillStyle = isSelected ? "#000"
    : (rect.is_free_space ? "#fff" : folderColors[(rect.depth || 0) % folderColors.length]);
  fillRoundedRect(ctx, rect.x, rect.y, rect.w, rect.h);

  // Border
  ctx.strokeStyle = "#222";
  ctx.lineWidth = 1;
  strokeRoundedRect(ctx, rect.x + 0.5, rect.y + 0.5, rect.w - 1, rect.h - 1);

  // ID buffer (rect index encoded as RGB)
  if (writeId) {
    const rgb = idToColor(rectIndex);
    AppState.idCtx.fillStyle = `rgb(${rgb[0]},${rgb[1]},${rgb[2]})`;
    AppState.idCtx.fillRect(Math.round(rect.x), Math.round(rect.y), Math.round(rect.w), Math.round(rect.h));
  }

  // Rect is too small, bye labels
  if (rect.w < 40 || rect.h < FONT_SIZE + 4)
    return;

  // Label
  ctx.font = `${FONT_SIZE}px sans-serif`;
  ctx.textBaseline = "top";
  ctx.fillStyle = isSelected ? "#fff" : "#000";

  const sizeStr = formatSize(rect.size || 0);
  const fontBounds = getCtxFontBounds(ctx);

  if (rect.is_free_space) {
    writeCenteredLinesInRect(ctx,  [
      { text: "Free Space", ellipsize: false },
      { text: sizeStr,      ellipsize: false },
    ], fontBounds, rect);
  } 
  else if (rect.is_folder) {
    if (rect.w > 60 && rect.h > 15) {
      let display = `${rect.name} (${sizeStr})`;
  
      const isRoot = rect.parent_id == null;
      if (isRoot && rect.disk_total > 0) {
        const used = Math.max(0, rect.disk_total - (rect.disk_free || 0));
        display = `${rect.name} (${formatSize(used)} / ${formatSize(rect.disk_total)})`;
      }
  
      const label = ellipsize(ctx, display, rect.w);
      ctx.fillText(label, rect.x + 4, rect.y + 4);
    }
  } else { // File
    const dateStr = rect.mtime ? formatModTime(rect.mtime) : "";
    writeCenteredLinesInRect(ctx,  [
      { text: rect.name,  ellipsize: true  },
      { text: sizeStr,    ellipsize: false },
      { text: dateStr,    ellipsize: false },
    ], fontBounds, rect);
  }
}

// ---------- Selection & partial redraw ----------
function getSelectedRect() {
  const i = AppState.selectedRectIndex;
  return (i == null) ? null : AppState.rects?.[i] || null;
}

function selectRectByIndex(rectIndex, dontDeselect=false) {
  const count = AppState.rects?.length || 0;

  if (rectIndex == null || rectIndex < 0 || rectIndex >= count) {
    if (!dontDeselect) {
      const prevIdx = AppState.selectedRectIndex;
      AppState.selectedRectIndex = null;
      AppState.selectedNodeId = null;
      if (prevIdx != null) reDrawRectByIndex(prevIdx);
    }
    return;
  }

  if (AppState.selectedRectIndex === rectIndex) {
    if (dontDeselect) return;
    const prevIdx = AppState.selectedRectIndex;
    AppState.selectedRectIndex = null;
    AppState.selectedNodeId = null;
    if (prevIdx != null) reDrawRectByIndex(prevIdx);
    return;
  }

  const rect = AppState.rects[rectIndex];
  if (rect.is_free_space) return;

  const prevIdx = AppState.selectedRectIndex;
  AppState.selectedRectIndex = rectIndex;
  AppState.selectedNodeId = AppState.rects[rectIndex].node_id;

  if (prevIdx != null) reDrawRectByIndex(prevIdx);
  reDrawRectByIndex(rectIndex);
}

function reDrawRectByIndex(idx) {
  const r = AppState.rects?.[idx];
  if (!r || r.w <= 0 || r.h <= 0) return;

  // 1) draw rect into tmp
  drawRect(r, /*writeId*/false, AppState.tmpCtx, -1);

  // 2) mask: start with rect, punch out child rects (r.children are rect indices)
  AppState.maskCtx.clearRect(0, 0, AppState.maskCanvas.width, AppState.maskCanvas.height);
  AppState.maskCtx.fillStyle = 'rgba(0,0,0,1)';
  fillRoundedRect(AppState.maskCtx, r.x, r.y, r.w, r.h);

  AppState.maskCtx.save();
  AppState.maskCtx.globalCompositeOperation = 'destination-out';

  const childrenIdx = Array.isArray(r.children) ? r.children : [];
  for (let i = 0; i < childrenIdx.length; i++) {
    const cr = AppState.rects[childrenIdx[i]];
    fillRoundedRect(AppState.maskCtx, cr.x, cr.y, cr.w, cr.h);
  }
  AppState.maskCtx.restore();

  // 3) apply mask & blit
  AppState.tmpCtx.globalCompositeOperation = 'destination-in';
  AppState.tmpCtx.drawImage(AppState.maskCanvas, 0, 0);
  AppState.tmpCtx.globalCompositeOperation = 'source-over';

  AppState.colorCtx.drawImage(AppState.tmpCanvas, 0, 0);
}

// ---------- Navigation ----------
function navigateToSelected() {
  const r = getSelectedRect();
  if (!r || !r.is_folder || r.is_free_space) return;
  visit(r.node_id);
}

function goToRoot() {
  if (!AppState.navHistory.length) return;
  visit(AppState.navHistory[0]);
}

function goToParent() {
  if (!AppState.rects?.length) return;
  const rootRect = AppState.rects[0];
  if (rootRect.parent_id == null) return;
  visit(rootRect.parent_id);
}

function visit(nodeId) {
  if (nodeId == null || nodeId < 0) return;
  if (nodeId === AppState.node_id) return;

  // trim forward history
  AppState.navHistory = AppState.navHistory.slice(0, AppState.navIndex + 1);

  AppState.navHistory.push(nodeId);
  AppState.navIndex = AppState.navHistory.length - 1;
  AppState.node_id = nodeId;
  AppState.selectedRectIndex = null;
  redraw();
}

function goBackward() {
  if (AppState.navIndex > 0) {
    AppState.navIndex--;
    AppState.node_id = AppState.navHistory[AppState.navIndex];
    AppState.selectedRectIndex = null;
    redraw();
  }
}
function goForward() {
  if (AppState.navIndex < AppState.navHistory.length - 1) {
    AppState.navIndex++;
    AppState.node_id = AppState.navHistory[AppState.navIndex];
    AppState.selectedRectIndex = null;
    redraw();
  }
}
function updateNavButtons() {
  const atRoot = AppState.navIndex === 0;
  document.getElementById("rootButton").disabled = atRoot;

  const hasParent = !!(AppState.rects && AppState.rects.length && AppState.rects[0].parent_id != null);
  document.getElementById("parentButton").disabled = !hasParent;

  document.getElementById("backwardButton").disabled = AppState.navIndex <= 0;
  document.getElementById("forwardButton").disabled = AppState.navIndex >= AppState.navHistory.length - 1;
}

// ---------- Context menu ----------
window.addEventListener("click", () => hideContextMenu());
function showContextMenu(x, y) {
  const m = document.getElementById("contextMenu");
  m.style.left = `${x}px`; m.style.top = `${y}px`; m.style.display = "block";
}
function hideContextMenu() {
  document.getElementById("contextMenu").style.display = "none";
}

async function openInSystemBrowser() {
  const r = getSelectedRect();
  if (r?.full_path) await apiOpenInFileBrowser(r.full_path);
  hideContextMenu();
}

// ---------- Utilities ----------
function getCanvasCoords(event) {
  const rect = AppState.colorCanvas.getBoundingClientRect();
  return { x: event.clientX - rect.left, y: event.clientY - rect.top };
}
function debounce(func, delay) {
  let t; return (...args) => { clearTimeout(t); t = setTimeout(() => func.apply(this, args), delay); };
}
function resizeCanvas() {
  const containerRect = AppState.colorCanvas.parentElement.getBoundingClientRect();
  const controlsRect  = document.querySelector(".controls").getBoundingClientRect();
  const width  = containerRect.width;
  const height = window.innerHeight - controlsRect.height;
  const dpr = window.devicePixelRatio || 1;

  for (const c of [AppState.colorCanvas, AppState.idCanvas, AppState.tmpCanvas, AppState.maskCanvas]) {
    c.width  = Math.max(1, Math.floor(width  * dpr));
    c.height = Math.max(1, Math.floor(height * dpr));
    c.style.width  = `${width}px`;
    c.style.height = `${height}px`;
  }
}
function setUIBusy(state) {
  document.querySelectorAll(".controls button").forEach(btn => btn.disabled = state);
}

// ID buffer helpers (rect-index <-> RGB)
function idToColor(id){ const code = id + 1; return [(code>>16)&255,(code>>8)&255,code&255]; }
function colorToId(color){ return ((color[0]<<16)|(color[1]<<8)|color[2]) - 1; }
function rectIndexAtPoint(x, y) {
  const dpr = window.devicePixelRatio || 1;
  const px = Math.round(x * dpr), py = Math.round(y * dpr);
  const pixel = AppState.idCtx.getImageData(px, py, 1, 1).data;
  return colorToId(pixel);
}

function fillRoundedRect(ctx, x, y, w, h) { ctx.beginPath(); ctx.roundRect(x, y, w, h, CORNER_RADII); ctx.fill(); }
function strokeRoundedRect(ctx, x, y, w, h) { ctx.beginPath(); ctx.roundRect(x, y, w, h, CORNER_RADII); ctx.stroke(); }

function formatSize(bytes) {
  if (!bytes) return '0 B';
  const u = ['B','KB','MB','GB','TB','PB']; const i = Math.floor(Math.log(bytes)/Math.log(1024));
  const n = bytes / Math.pow(1024, i);
  return `${n.toFixed(n < 10 ? 1 : 0)} ${u[i]}`;
}

// ---------- Optional folder picker (fallback) ----------
async function triggerFolderSelect() {
  const path = prompt("Enter a folder path to scan:");
  if (!path) return;
  document.getElementById("pathInput").value = path;
  analyze();
}
