import { Layout } from "./wailsjs/go/main/App.js";
import { hideContextMenu, openRectWithDefault, showContextMenu } from "./file-actions.js";
import { debounce, formatCompactSize, formatCount, formatModTime, formatSize } from "./format.js";
import { navigateToSelected, updateNavButtons } from "./navigation.js";
import { hideRectToast, initNotifications } from "./notifications.js";
import { logDebug, logWarning } from "./logging.js";
import { AppState, AppearanceState, FONT_SIZE, activePalette, getScale } from "./state.js";

let redrawGeneration = 0;
let requestedHoverRectIndex = -1;
let renderedHoverRectIndex = -1;
let hoverAnimationFrame = null;

async function apiLayoutById(nodeId, w, h, scale) {
  const rects = await Layout(nodeId, w, h, scale);
  return { rects };
}

export async function redraw() {
  const generation = ++redrawGeneration;
  hideRectToast();
  const nodeId = AppState.node_id;
  if (nodeId == null) return;

  const w = AppState.colorCanvas.width;
  const h = AppState.colorCanvas.height;
  const scale = AppState.scale;

  const layoutStartedAt = performance.now();
  const payload = await apiLayoutById(nodeId, w, h, scale);
  logDebug(`layout fetch: ${(performance.now() - layoutStartedAt).toFixed(1)} ms`);

  const stateChanged = AppState.node_id !== nodeId
    || AppState.colorCanvas.width !== w
    || AppState.colorCanvas.height !== h
    || AppState.scale !== scale;
  if (generation !== redrawGeneration || stateChanged) return;

  const rects = payload?.rects;
  if (!Array.isArray(rects)) {
    logWarning("no rects from backend");
    return;
  }

  AppState.rects = rects;
  AppState.selectedRectIndex = null;

  const drawStartedAt = performance.now();
  drawTreemap(rects);
  logDebug(`treemap draw: ${(performance.now() - drawStartedAt).toFixed(1)} ms`);

  updateNavButtons();
}

function drawTreemap(rects) {
  const ctx = AppState.colorCtx;
  const idc = AppState.idCtx;
  ctx.clearRect(0, 0, AppState.colorCanvas.width, AppState.colorCanvas.height);
  idc.clearRect(0, 0, AppState.idCanvas.width, AppState.idCanvas.height);
  clearHoverOverlay();

  const parentRectIndexes = new Int32Array(rects.length);
  parentRectIndexes.fill(-1);
  for (let parentIndex = 0; parentIndex < rects.length; parentIndex++) {
    for (const childIndex of Array.isArray(rects[parentIndex].children) ? rects[parentIndex].children : []) {
      if (Number.isInteger(childIndex) && childIndex >= 0 && childIndex < rects.length) {
        parentRectIndexes[childIndex] = parentIndex;
      }
    }
  }
  AppState.parentRectIndexes = parentRectIndexes;

  for (let i = 0; i < rects.length; i++) {
    drawRect(rects[i], /*writeId*/true, ctx, i);
  }
}

function clearRenderedHoverRect() {
  AppState.hoverCtx.clearRect(0, 0, AppState.hoverCanvas.width, AppState.hoverCanvas.height);
  renderedHoverRectIndex = -1;
}

function drawHoverRect(ctx, rect, strength) {
  ctx.save();
  ctx.beginPath();
  addRectPath(ctx, rect.x, rect.y, rect.w, rect.h);
  for (const childIndex of Array.isArray(rect.children) ? rect.children : []) {
    const child = AppState.rects?.[childIndex];
    if (child) addRectPath(ctx, child.x, child.y, child.w, child.h);
  }
  ctx.clip("evenodd");
  ctx.fillStyle = `rgba(255,255,255,${strength})`;
  ctx.fillRect(rect.x, rect.y, rect.w, rect.h);
  ctx.restore();
}

function renderHoverOverlay() {
  hoverAnimationFrame = null;
  clearRenderedHoverRect();
  const strength = AppearanceState.hoverBrightness;
  const rect = AppState.rects?.[requestedHoverRectIndex];
  if (!(strength > 0) || !rect || rect.is_free_space || rect.w <= 0 || rect.h <= 0) return;

  const ctx = AppState.hoverCtx;
  const indexes = [requestedHoverRectIndex];
  if (AppearanceState.rollOverBoxes) {
    let parentIndex = AppState.parentRectIndexes?.[requestedHoverRectIndex] ?? -1;
    while (parentIndex >= 0) {
      indexes.push(parentIndex);
      parentIndex = AppState.parentRectIndexes[parentIndex] ?? -1;
    }
  }
  for (const rectIndex of indexes) {
    const current = AppState.rects?.[rectIndex];
    if (!current || current.is_free_space || current.node_id === AppState.selectedNodeId || current.w <= 0 || current.h <= 0) continue;
    drawHoverRect(ctx, current, strength);
  }
  renderedHoverRectIndex = requestedHoverRectIndex;
}

function scheduleHoverRender() {
  if (hoverAnimationFrame == null) hoverAnimationFrame = requestAnimationFrame(renderHoverOverlay);
}

export function setHoveredRectIndex(rectIndex) {
  const nextIndex = Number.isInteger(rectIndex) ? rectIndex : -1;
  if (nextIndex === requestedHoverRectIndex) return;
  requestedHoverRectIndex = nextIndex;
  scheduleHoverRender();
}

function clearHoverOverlay() {
  if (hoverAnimationFrame != null) cancelAnimationFrame(hoverAnimationFrame);
  hoverAnimationFrame = null;
  requestedHoverRectIndex = -1;
  renderedHoverRectIndex = -1;
  AppState.hoverCtx?.clearRect(0, 0, AppState.hoverCanvas.width, AppState.hoverCanvas.height);
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

function getCtxFontBounds(ctx, fontPx){
  const m = ctx.measureText("Mg"); // tall sample
  const ascent  = m.actualBoundingBoxAscent  ?? Math.round(fontPx * 0.8);
  const descent = m.actualBoundingBoxDescent ?? Math.max(1, Math.round(fontPx * 0.2));
  const textH   = ascent + descent;
  const lineH   = textH + pxI(2);          // base gap ~2px at 1x
  return { ascent, textH, lineH };
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
    const x  = Math.round(rect.x + (rect.w - tw) / 2);  
    const y  = Math.round(baseY + i * lineH);           
    ctx.fillText(t, x, y);
  }
}

function pxI(base) {        
  const s = AppState.scale || 1;
  return Math.max(1, Math.round(base * s));
}

function pxF(base) { 
  const s = AppState.scale || 1;
  return Math.max(0.5, base * s);
}

function blendHexColor(hex, target, amount) {
  let value = hex.replace("#", "");
  if (/^[0-9a-f]{3}$/i.test(value)) value = value.split("").map(channel => channel + channel).join("");
  if (!/^[0-9a-f]{6}$/i.test(value)) return hex;
  const channels = [0, 2, 4].map(offset => parseInt(value.slice(offset, offset + 2), 16));
  const blended = channels.map(channel => Math.round(channel + (target - channel) * amount));
  return `rgb(${blended[0]}, ${blended[1]}, ${blended[2]})`;
}

function drawRectRelief(ctx, rect, fillColor, strokeWidth) {
  const brightness = 1 + AppearanceState.reliefStrength;
  if (brightness === 1.0) return;

  const amount = Math.min(1, Math.abs(brightness - 1));
  const lightColor = blendHexColor(fillColor, 255, amount);
  const darkColor = blendHexColor(fillColor, 0, amount);
  const reliefWidth = getScale() * Math.max(1, AppState.zoomFactor || 1);
  // The border path is inset by half a canvas pixel. Place the relief band
  // immediately after the border's inner edge instead of overlapping it.
  const reliefCenterInset = 0.5 + strokeWidth / 2 + reliefWidth / 2;
  const left = rect.x + reliefCenterInset;
  const top = rect.y + reliefCenterInset;
  const right = rect.x + rect.w - reliefCenterInset;
  const bottom = rect.y + rect.h - reliefCenterInset;
  if (right <= left || bottom <= top) return;

  ctx.save();
  ctx.beginPath();
  addRectPath(ctx, rect.x, rect.y, rect.w, rect.h);
  ctx.clip();
  ctx.lineWidth = reliefWidth;
  ctx.lineCap = "butt";
  ctx.lineJoin = "miter";

  ctx.beginPath();
  ctx.moveTo(left, bottom);
  ctx.lineTo(left, top);
  ctx.lineTo(right, top);
  ctx.strokeStyle = lightColor;
  ctx.stroke();

  ctx.beginPath();
  ctx.moveTo(right, top);
  ctx.lineTo(right, bottom);
  ctx.lineTo(left, bottom);
  ctx.strokeStyle = darkColor;
  ctx.stroke();
  ctx.restore();
}

function drawRect(rect, writeId, ctx, rectIndex) {
  const isSelected = AppState.selectedNodeId == rect.node_id;
  const isRoot = rect.parent_id == null;
  const palette = activePalette();
  if (isSelected && rectIndex >= 0) AppState.selectedRectIndex = rectIndex;

  //  scaled UI constants  
  const PAD          = pxI(4);            
  const FONT_PX      = pxI(FONT_SIZE);    
  const STROKE_PX    = pxF(1);            
  const LABEL_MIN_W  = pxI(40);           
  const LABEL_MIN_H  = pxI(FONT_SIZE + 4);
  const FOLDER_W_MIN = pxI(60);           
  const FOLDER_H_MIN = pxI(15);

  // fill
  const fillColor = isSelected ? "#000000"
    : (rect.is_free_space || isRoot ? "#fff"
      : (rect.is_small_files ? "#e6dac5" : palette[(rect.depth || 0) % palette.length]));
  ctx.fillStyle = fillColor;
  fillRoundedRect(ctx, rect.x, rect.y, rect.w, rect.h);

  // stroke
  ctx.strokeStyle = "#222";
  ctx.lineWidth = STROKE_PX;
  strokeRoundedRect(ctx, rect.x + 0.5, rect.y + 0.5, rect.w - 1, rect.h - 1);
  drawRectRelief(ctx, rect, fillColor, STROKE_PX);

  //  ID buffer 
  if (writeId) {
    const rgb = idToColor(rectIndex);
    AppState.idCtx.fillStyle = `rgb(${rgb[0]},${rgb[1]},${rgb[2]})`;
    AppState.idCtx.fillRect(Math.round(rect.x), Math.round(rect.y), Math.round(rect.w), Math.round(rect.h));
  }

  //  too small for labels?
  if (rect.w < LABEL_MIN_W || rect.h < LABEL_MIN_H) return;

  //  text setup 
  ctx.font = `${FONT_PX}px sans-serif`;
  ctx.textBaseline = "alphabetic";
  ctx.fillStyle = isSelected ? "#fff" : "#000";

  const sizeStr = formatSize(rect.size || 0);
  const fontBounds = getCtxFontBounds(ctx, FONT_PX);

  const anonymize = false;

  if (rect.is_free_space) {
    const percent = 100 * rect.size / rect.disk_total;
    const fileCount = AppState.fileCount == null ? "?" : formatCount(AppState.fileCount);
    const dirCount = AppState.dirCount == null ? "?" : formatCount(AppState.dirCount);
    const lines = [
      {text:`Free Space: ${percent.toFixed(1)}%`, ellipsize:false},
      {text:`${formatSize(rect.size || 0, 1)} Free`, ellipsize:false},
      {text:`Files: ${fileCount}`, ellipsize:false},
      {text:`Folders: ${dirCount}`, ellipsize:false}
    ];
    writeCenteredLinesInRect(ctx, lines, fontBounds, rect);
  }
  else if (rect.is_small_files) {
    const count = formatCount(rect.small_file_count);
    const limit = formatCompactSize(rect.small_file_limit || 0);
    writeCenteredLinesInRect(ctx, [
      { text: `${count} <${limit} files`, ellipsize: false },
      { text: sizeStr, ellipsize: false },
    ], fontBounds, rect);
  }
  else if (rect.is_folder) {
    if (rect.w > FOLDER_W_MIN && rect.h > FOLDER_H_MIN) {
      let display = `${anonymize ? "A folder" : rect.name} (${sizeStr})`;
      if (isRoot && rect.disk_total > 0) {
        const used = Math.max(0, rect.disk_total - (rect.disk_free || 0));
        display = `${rect.name} (${formatSize(used)} / ${formatSize(rect.disk_total)})`;
      }
      const label = ellipsize(ctx, display, rect.w - PAD*2);
      const y = Math.round(rect.y + PAD + fontBounds.ascent);
      const x = Math.round(rect.x + PAD);
      ctx.fillText(label, x, y);
    }
  }
  else { // file
    const dateStr = rect.mtime ? formatModTime(rect.mtime) : "";
    writeCenteredLinesInRect(ctx, [
      { text: anonymize ? "A file" : rect.name,  ellipsize: true  },
      { text: sizeStr,    ellipsize: false },
      { text: dateStr,    ellipsize: false },
    ], fontBounds, rect);
  }
}

export function getSelectedRect() {
  const i = AppState.selectedRectIndex;
  return (i == null) ? null : AppState.rects?.[i] || null;
}

export function isPassiveRect(rect) {
  return !!(rect?.is_free_space || rect?.is_small_files);
}

export function selectRectByIndex(rectIndex, dontDeselect=false) {
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
  if (isPassiveRect(rect)) {
    if (!dontDeselect) {
      const prevIdx = AppState.selectedRectIndex;
      AppState.selectedRectIndex = null;
      AppState.selectedNodeId = null;
      if (prevIdx != null) reDrawRectByIndex(prevIdx);
    }
    return;
  }

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
  scheduleHoverRender();
}

export function getCanvasCoords(event) {
  const rect = AppState.colorCanvas.getBoundingClientRect();
  return { x: event.clientX - rect.left, y: event.clientY - rect.top };
}

export function resizeCanvas() {
  const containerRect = AppState.colorCanvas.parentElement.getBoundingClientRect();
  const width  = containerRect.width;
  const height = containerRect.height;
  const dpr = window.devicePixelRatio || 1;

  for (const c of [AppState.colorCanvas, AppState.hoverCanvas, AppState.idCanvas, AppState.tmpCanvas, AppState.maskCanvas]) {
    c.width  = Math.max(1, Math.floor(width  * dpr));
    c.height = Math.max(1, Math.floor(height * dpr));
    c.style.width  = `${width}px`;
    c.style.height = `${height}px`;
  }
}

function idToColor(id){ const code = id + 1; return [(code>>16)&255,(code>>8)&255,code&255]; }

function colorToId(color){ return ((color[0]<<16)|(color[1]<<8)|color[2]) - 1; }

export function rectIndexAtPoint(x, y) {
  const dpr = window.devicePixelRatio || 1;
  const px = Math.round(x * dpr), py = Math.round(y * dpr);
  const pixel = AppState.idCtx.getImageData(px, py, 1, 1).data;
  return colorToId(pixel);
}

function currentCornerRadius() {
  return AppearanceState.cornerRadius * getScale();
}

function addRectPath(ctx, x, y, w, h) {
  const radius = currentCornerRadius();
  if (radius === 0) ctx.rect(x, y, w, h);
  else ctx.roundRect(x, y, w, h, radius);
}

function fillRoundedRect(ctx, x, y, w, h) {
  if (currentCornerRadius() === 0) {
    ctx.fillRect(x, y, w, h);
    return;
  }
  ctx.beginPath();
  addRectPath(ctx, x, y, w, h);
  ctx.fill();
}

function strokeRoundedRect(ctx, x, y, w, h) {
  if (currentCornerRadius() === 0) {
    ctx.strokeRect(x, y, w, h);
    return;
  }
  ctx.beginPath();
  addRectPath(ctx, x, y, w, h);
  ctx.stroke();
}

export function initTreemapView() {
  AppState.colorCanvas = document.getElementById("colorCanvas");
  AppState.hoverCanvas = document.getElementById("hoverCanvas");
  AppState.idCanvas = document.getElementById("idCanvas");
  AppState.tmpCanvas = document.getElementById("tmpCanvas");
  AppState.maskCanvas = document.getElementById("maskCanvas");

  AppState.colorCtx = AppState.colorCanvas.getContext("2d");
  AppState.hoverCtx = AppState.hoverCanvas.getContext("2d", { alpha: true });
  AppState.idCtx = AppState.idCanvas.getContext("2d", { willReadFrequently: true });
  AppState.idCtx.imageSmoothingEnabled = false;
  AppState.tmpCtx = AppState.tmpCanvas.getContext("2d", { alpha: true });
  AppState.maskCtx = AppState.maskCanvas.getContext("2d", { alpha: true });

  resizeCanvas();

  AppState.colorCanvas.addEventListener("click", event => {
    const { x, y } = getCanvasCoords(event);
    selectRectByIndex(rectIndexAtPoint(x, y));
    hideContextMenu();
  });
  AppState.colorCanvas.addEventListener("contextmenu", event => {
    event.preventDefault();
    const { x, y } = getCanvasCoords(event);
    selectRectByIndex(rectIndexAtPoint(x, y), true);
    const rect = getSelectedRect();
    if (rect && !isPassiveRect(rect)) showContextMenu(event.clientX, event.clientY);
    else hideContextMenu();
  });
  AppState.colorCanvas.addEventListener("dblclick", event => {
    const { x, y } = getCanvasCoords(event);
    const rectIndex = rectIndexAtPoint(x, y);
    const rect = AppState.rects[rectIndex];
    if (!rect || isPassiveRect(rect)) return;
    selectRectByIndex(rectIndex, true);
    if (rect.is_folder) navigateToSelected();
    else openRectWithDefault(rect);
    hideContextMenu();
  });

  initNotifications({ getCanvasCoords, rectIndexAtPoint, setHoveredRectIndex });
  window.addEventListener("resize", debounce(async () => {
    resizeCanvas();
    await redraw();
  }, 150));
}
