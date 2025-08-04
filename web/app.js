const AppState = {
    cachedTree: null,
    focusedCachedTree: null,
    cachedRects: null,
    selectedNode: null,
    navHistory: [],
    navIndex: -1,
    colorCanvas: null,
    colorCtx: null,
    idCanvas: null,
    idCtx: null,
    tmpCanvas: null,
    tmpCtx: null,
    maskCanvas: null,
    maskCtx: null,
};

const folderColors = [
    "#ff7f7f", "#ffbf7f", "#ffff00", "#7fff7f", "#7fffff",
    "#bfbfff", "#bfbfbf"
];

const PADDING = 5;
const MIN_SIZE = 4;
const FONT_SIZE = 10;

async function triggerFolderSelect() {
    const folderPath = await eel.pick_folder()();
    if (!folderPath) return;

    document.getElementById("pathInput").value = folderPath;
    analyze();
}

document.getElementById("folderInput").addEventListener("change", function (event) {
    const files = event.target.files;
    if (files.length === 0) return;

    const relativePath = files[0].webkitRelativePath;
    const folderName = relativePath.split("/")[0];
    document.getElementById("pathInput").value = folderName;
    analyze();
});

function nodeFromRectId(index){
    if (AppState.cachedRects &&
        index >= 0 &&
        index < AppState.cachedRects.length){
        return AppState.cachedRects[index].node;
    }
    return null;
}

window.addEventListener("load", () => {
    AppState.colorCanvas = document.getElementById("colorCanvas");
    AppState.idCanvas = document.getElementById("idCanvas");
    AppState.tmpCanvas = document.getElementById("tmpCanvas");
    AppState.maskCanvas = document.getElementById("maskCanvas");

    AppState.colorCtx = AppState.colorCanvas.getContext("2d");
    AppState.idCtx = AppState.idCanvas.getContext("2d", { willReadFrequently: true });
    AppState.idCtx.imageSmoothingEnabled = false;
    AppState.tmpCtx = AppState.tmpCanvas.getContext("2d", {alpha: true});
    AppState.maskCtx = AppState.maskCanvas.getContext("2d", {alpha: true});

    resizeCanvas();

    AppState.colorCanvas.addEventListener("click", (e) => {
        const { x, y } = getCanvasCoords(e);
        const index = rectIdAtPoint(x, y);
        const node = nodeFromRectId(index);
        selectNode(node);
        hideContextMenu();    
    });

    AppState.colorCanvas.addEventListener("contextmenu", (e) => {
        e.preventDefault();
        const { x, y } = getCanvasCoords(e);
        const index = rectIdAtPoint(x, y);
        const node = nodeFromRectId(index);
        selectNode(node, dontDeselect=true);
        if (node && node.is_folder && !node.is_free_space ) {
            showContextMenu(e.pageX, e.pageY);
        }else {
            hideContextMenu();
        }
    });
});

function getCanvasCoords(event) {
    const rect = AppState.colorCanvas.getBoundingClientRect();
    return {
        x: event.clientX - rect.left,
        y: event.clientY - rect.top
    };
}

function debounce(func, delay) {
  let timer;
  return function (...args) {
    clearTimeout(timer);
    timer = setTimeout(() => func.apply(this, args), delay);
  };
}

window.addEventListener("resize", debounce(() => {
    resizeCanvas();
    redraw();
}, 150));

function resizeCanvas() {
    const containerRect = AppState.colorCanvas.parentElement.getBoundingClientRect();
    const controlsRect = document.querySelector(".controls").getBoundingClientRect();

    const width = containerRect.width;
    const height = window.innerHeight - controlsRect.height;
    const dpr = window.devicePixelRatio || 1;

    AppState.colorCanvas.width = width * dpr;
    AppState.colorCanvas.height = height * dpr;
    AppState.idCanvas.width = width * dpr;
    AppState.idCanvas.height = height * dpr;

    AppState.colorCanvas.style.width = `${width}px`;
    AppState.colorCanvas.style.height = `${height}px`;
    AppState.idCanvas.style.width = `${width}px`;
    AppState.idCanvas.style.height = `${height}px`;


    AppState.tmpCanvas.width = width * dpr;
    AppState.tmpCanvas.height = height * dpr;
    AppState.maskCanvas.width = width * dpr;
    AppState.maskCanvas.height = height * dpr;

}

function setUIBusy(state) {
    document.querySelectorAll(".controls button").forEach(btn => {
        btn.disabled = state;
    });
}

function trackParents(node){
    if (node.is_folder && node.children.length > 0){
        for (child of node.children){
            child.parent = node;
            trackParents(child);
        }
    } 
}

async function analyze() {
    const path = document.getElementById("pathInput").value;
    if (!path || path.trim() === "") return;

    setUIBusy(true);
    try {
        console.time("get_full_tree");
        const tree = await eel.get_full_tree(path)();
        console.timeEnd("get_full_tree");
        if (tree.error) {
            console.error("Backend error:", tree.error);
        } else {
            console.time("trackParents");
            trackParents(tree);
            console.timeEnd("trackParents");
            tree.parent = null;
            AppState.cachedTree = tree;
            AppState.navHistory = [];
            AppState.navIndex = -1;
            AppState.selectedNode = null;
            visit(AppState.cachedTree);
        }
    } catch (err) {
        console.error("Eel call failed:", err);
    } finally {
        setUIBusy(false);
    }
    updateNavButtons();
}

function redraw() {
    if (!AppState.focusedCachedTree) return;
    console.time("layoutTree");

    AppState.cachedRects = layoutTree(AppState.focusedCachedTree, 0, 0, AppState.colorCanvas.width, AppState.colorCanvas.height);
    console.timeEnd("layoutTree")
    console.log(`layoutTree produced ${AppState.cachedRects.length} rects.`)
    console.time("drawTreemap");
    drawTreemap(AppState.cachedRects);
    console.timeEnd("drawTreemap");
}

function layoutTree(node, x, y, w, h, indexRef = { value: 0 }) {
    const rects = [];
    if (w < MIN_SIZE || h < MIN_SIZE) return rects;

    const rect = { x, y, w, h, node: node, index: indexRef.value++ };
    node.rect = rect;
    rects.push(rect);

    if (!node.is_folder || !node.children || node.children.length === 0) {
        return rects;
    }

    const children = node.children.filter(c => c.size > 0);
    if (children.length === 0) return rects;

    const total = children.reduce((sum, c) => sum + c.size, 0);
    const area = (w - 2 * PADDING) * (h - 2 * PADDING - FONT_SIZE);
    const boxes = children.map(c => ({
        node: c,
        area: (c.size / total) * area
    }));

    squarify(boxes, x + PADDING, y + PADDING + FONT_SIZE, w - 2 * PADDING, h - 2 * PADDING - FONT_SIZE, rects, indexRef);
    return rects;
}

function drawTreemap(rects) {
    const colorCtx = AppState.colorCanvas.getContext("2d");
    colorCtx.clearRect(0, 0, AppState.colorCanvas.width, AppState.colorCanvas.height);
    for (const rect of rects){

        drawRect(rect, true);
    }
}

function colorToId(color){
    return ((color[0] << 16) | (color[1] << 8) | color[2]) - 1;
}

function idToColor(id){
    const code = id + 1
    return idColor = [(code >> 16) & 0xff, (code >> 8) & 0xff, code & 0xff];
}

function formatSize(bytes) {
  if (bytes === 0) return '0 B';

  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const size = bytes / Math.pow(1024, i);

  return `${size.toFixed(size < 10 ? 1 : 0)} ${units[i]}`;
}

function drawRect(rect, drawId = true, ctxOverride = null) {
    const ctx = ctxOverride || AppState.colorCtx;
    const isSelected = rect.node === AppState.selectedNode;

    ctx.fillStyle = isSelected ? "#000" : folderColors[rect.node.depth % folderColors.length];
    if (rect.node.is_free_space) ctx.fillStyle = "#fff";

    ctx.fillRect(rect.x, rect.y, rect.w, rect.h);

    ctx.strokeStyle = "#222";
    ctx.lineWidth = 1;
    ctx.strokeRect(rect.x + 0.5, rect.y + 0.5, rect.w - 1, rect.h - 1);

    ctx.font = `${FONT_SIZE}px sans-serif`;
    ctx.textBaseline = "top";
    ctx.fillStyle = isSelected ? "#fff" : "#000";

    if (rect.node.is_free_space) {
        const percent = 100 * rect.node.size / AppState.cachedTree.size;
        const sizeString = formatSize(rect.node.size);
        const fileCount = AppState.cachedTree.file_count ?? '?';
        const folderCount = AppState.cachedTree.folder_count ?? '?';

        const lines = [
        `Free Space: ${percent.toFixed(1)}%`,
        `${sizeString} Free`,
        `Files: ${fileCount}`,
        `Folders: ${folderCount}`
        ];

        const lineHeight = FONT_SIZE + 2;
        const totalHeight = lines.length * lineHeight;
        const yStart = rect.y + (rect.h - totalHeight) / 2;

        for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        const textWidth = ctx.measureText(line).width;
        const xText = rect.x + (rect.w - textWidth) / 2;
        const yText = yStart + i * lineHeight;
        ctx.fillText(line, xText, yText);
        }
    }

    else if (rect.node.is_folder) {
        if (rect.w > 60 && rect.h > 15) {
        const label = truncateText(ctx, rect.node.name, rect.w - 6);
        ctx.fillText(label, rect.x + 4, rect.y + 4);
        }
    }

    else {
    if (rect.w > 60 && rect.h > FONT_SIZE * 2 + 6) {
        const name = truncateText(ctx, rect.node.name, rect.w - 6);
        const sizeStr = formatSize(rect.node.size);

        const lines = [name, sizeStr];
        const lineHeight = FONT_SIZE + 2;
        const totalHeight = lines.length * lineHeight;
        const yStart = rect.y + (rect.h - totalHeight) / 2;

        for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        const textWidth = ctx.measureText(line).width;
        const xText = rect.x + (rect.w - textWidth) / 2;
        const yText = yStart + i * lineHeight;
        ctx.fillText(line, xText, yText);
        }
    } else if (rect.h > FONT_SIZE + 4 && rect.w > 30) {
        // Show only size if not enough space for both
        const sizeStr = formatSize(rect.node.size);
        const textWidth = ctx.measureText(sizeStr).width;
        const xText = rect.x + (rect.w - textWidth) / 2;
        const yText = rect.y + (rect.h - FONT_SIZE) / 2;
        ctx.fillText(sizeStr, xText, yText);
    }
    }

    if (drawId && ctx === AppState.colorCanvas.getContext("2d")) {
        const idColor = idToColor(rect.index);
        AppState.idCtx.fillStyle = `rgb(${idColor[0]},${idColor[1]},${idColor[2]})`;
        AppState.idCtx.fillRect(
        Math.round(rect.x),
        Math.round(rect.y),
        Math.round(rect.w),
        Math.round(rect.h)
    );
    }
}


function reDrawRect(rect) {
    

    if (!rect || rect.w <= 0 || rect.h <= 0) return;
    const { x, y, w, h, node } = rect;

    // Pixels of the drawn rect
    drawRect(rect, false, AppState.tmpCtx);  

    // Creating a mask to only blit this rect
    AppState.maskCtx.clearRect(0, 0, AppState.maskCanvas.width, AppState.maskCanvas.height);
    AppState.maskCtx.fillStyle = 'rgba(0,0,0,1)';
    AppState.maskCtx.fillRect(x, y, w, h);

    AppState.maskCtx.save();
    AppState.maskCtx.globalCompositeOperation = 'destination-out';
  
    // carving out the rect childs from the mask !
    if (node.is_folder && node.children) {
        for (const child of node.children) {
            if (child.rect) {
                const cr = child.rect;
                AppState.maskCtx.fillRect(cr.x, cr.y, cr.w, cr.h);
            }
        }
    }
    AppState.maskCtx.restore();

    AppState.tmpCtx.globalCompositeOperation = 'destination-in';
    AppState.tmpCtx.drawImage(AppState.maskCanvas, 0, 0);
    AppState.tmpCtx.globalCompositeOperation = 'source-over';

    AppState.colorCtx.drawImage(AppState.tmpCanvas, 0, 0);
}


function truncateText(ctx, text, maxWidth) {
    if (ctx.measureText(text).width <= maxWidth) return text;

    const ellipsis = '…';
    let low = 0;
    let high = text.length;

    while (low < high) {
        const mid = Math.floor((low + high) / 2);
        const substr = text.slice(0, mid) + ellipsis;
        const width = ctx.measureText(substr).width;

        if (width <= maxWidth) {
            low = mid + 1;
        } else {
            high = mid;
        }
    }

    return text.slice(0, low - 1) + ellipsis;
}

// function drawDiagonalCross(ctx, x, y, w, h, size, color) {
//     ctx.save();
//     ctx.strokeStyle = color;
//     ctx.lineWidth = 1;
//     const maxSize = Math.min(w, h);
//     const clampedSize = Math.min(size, maxSize);
//     const half = clampedSize / 2;
//     const cx = x + w / 2;
//     const cy = y + h / 2;

//     ctx.beginPath();
//     ctx.moveTo(cx - half, cy - half);
//     ctx.lineTo(cx + half, cy + half);
//     ctx.moveTo(cx + half, cy - half);
//     ctx.lineTo(cx - half, cy + half);
//     ctx.stroke();
//     ctx.restore();
// }

function squarify(items, x, y, w, h, rects, indexRef) {
    if (items.length === 0) return;

    let row = [];
    let rest = items.slice();
    let rowArea = 0;

    while (rest.length > 0) {
        const item = rest[0];
        row.push(item);
        rowArea += item.area;

        const aspectBefore = worstAspect(row.slice(0, -1), rowArea - item.area, w, h);
        const aspectAfter = worstAspect(row, rowArea, w, h);

        if (aspectAfter > aspectBefore && row.length > 1) {
            row.pop();
            rowArea -= item.area;
            layoutRow(row, x, y, w, h, rects, indexRef);
            const rowBreadth = row.reduce((sum, r) => sum + r.area, 0) / (w >= h ? w : h);
            if (w >= h) {
                y += rowBreadth;
                h -= rowBreadth;
            } else {
                x += rowBreadth;
                w -= rowBreadth;
            }
            row = [];
            rowArea = 0;
        } else {
            rest.shift();
        }
    }

    if (row.length > 0) {
        layoutRow(row, x, y, w, h, rects, indexRef);
    }
}

function layoutRow(row, x, y, w, h, rects, indexRef) {
    const totalArea = row.reduce((sum, r) => sum + r.area, 0);
    const horizontal = w >= h;
    const length = horizontal ? w : h;
    const thickness = totalArea / length;
    const padding = -1;

    let offset = 0;
    for (const box of row) {
        const breadth = box.area / thickness;

        const bw = horizontal ? breadth - padding : thickness - padding;
        const bh = horizontal ? thickness - padding : breadth - padding;
        const bx = horizontal ? x + offset : x;
        const by = horizontal ? y : y + offset;

        rects.push(...layoutTree(box.node, bx + 0.5 * padding, by + 0.5 * padding, bw, bh, indexRef));
        offset += breadth;
    }
}

function worstAspect(row, area, w, h) {
    if (row.length === 0) return Infinity;
    const length = w >= h ? w : h;
    const breadth = area / length;

    let worst = 0;
    for (const r of row) {
        const s = r.area;
        const side1 = s / breadth;
        const side2 = breadth;
        const aspect = Math.max(side1 / side2, side2 / side1);
        worst = Math.max(worst, aspect);
    }
    return worst;
}


function rectIdAtPoint(x, y) {
    const dpr = window.devicePixelRatio || 1;
    const px = Math.round(x * dpr);
    const py = Math.round(y * dpr);
    const pixel = AppState.idCtx.getImageData(px, py, 1, 1).data;
    const id = colorToId(pixel)
    return id;
}


function showContextMenu(x, y,) {
    const menu = document.getElementById("contextMenu");
    menu.style.left = `${x}px`;
    menu.style.top = `${y}px`;
    menu.style.display = "block";
}


function hideContextMenu() {
    document.getElementById("contextMenu").style.display = "none";
}


function selectNode(node, dontDeselect=false){
    if (AppState.selectedNode === node){
        if(node && dontDeselect){
            return;
        }
        node = null;
    }

    previousNode = AppState.selectedNode
    AppState.selectedNode = node
 
    reDrawRect(previousNode?.rect)
    reDrawRect(AppState.selectedNode?.rect)
}

window.addEventListener("click", () => hideContextMenu());

async function openInSystemBrowser() {

    if (AppState.selectedNode){
        await eel.open_in_file_browser(AppState.selectedNode.full_path)();
        hideContextMenu();
    }

}

function navigateToSelected() {
    visit(AppState.selectedNode);
}

function goToRoot() {
    if (!AppState.cachedTree) return;
    visit(AppState.cachedTree);
}

function goToParent() {
    if (!AppState.focusedCachedTree || !AppState.focusedCachedTree.parent) return;

    visit(AppState.focusedCachedTree.parent);
}


function visit(node) {
    if (!node || node === AppState.focusedCachedTree || !node.is_folder)
        return

    // Truncate future history if we went back and then change focus
    AppState.navHistory = AppState.navHistory.slice(0, AppState.navIndex + 1);

    AppState.navHistory.push(node);
    AppState.navIndex = AppState.navHistory.length - 1;

    AppState.focusedCachedTree = node;

    redraw();
    updateNavButtons();
}


function goBackward() {
    if (AppState.navIndex > 0) {
        AppState.navIndex--;
        AppState.focusedCachedTree = AppState.navHistory[AppState.navIndex];
        redraw();
        updateNavButtons();
    }
}

function goForward() {
    if (AppState.navIndex < AppState.navHistory.length - 1) {
        AppState.navIndex++;
        AppState.focusedCachedTree = AppState.navHistory[AppState.navIndex];
        redraw();
        updateNavButtons();
    }
}

function updateNavButtons() {
    document.getElementById("rootButton").disabled = !AppState.cachedTree || AppState.focusedCachedTree === AppState.cachedTree;
    document.getElementById("parentButton").disabled = !AppState.cachedTree || !AppState.focusedCachedTree?.parent;

    document.getElementById("backwardButton").disabled = !AppState.cachedTree || AppState.navIndex <= 0;
    document.getElementById("forwardButton").disabled = !AppState.cachedTree || AppState.navIndex >= AppState.navHistory.length - 1;
}
