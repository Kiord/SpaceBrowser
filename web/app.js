const AppState = {
    cachedTree: null,
    focusedCachedTree: null,
    cachedRects: null,
    selectedRectIndex: -1,
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
        if (index >= 0)
            selectRect(index);

    });

    AppState.colorCanvas.addEventListener("contextmenu", (e) => {
        e.preventDefault();
        const { x, y } = getCanvasCoords(e);
        const index = rectIdAtPoint(x, y);
        selectRect(index, dontDeselect=true);
        if (index >= 0 && !AppState.cachedRects[index].node.is_free_space) {
            showContextMenu(e.pageX, e.pageY);
        } else {
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
    AppState.maskCanvas.width = width;
    AppState.maskCanvas.height = height;

    AppState.tmpCtx.setTransform(1, 0, 0, 1, 0, 0);
    AppState.tmpCtx.scale(dpr, dpr);


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
            trackParents(tree);
            tree.parent = null;
            AppState.cachedTree = tree;
            AppState.navHistory = [];
            AppState.navIndex = -1;
            AppState.selectedRectIndex = -1
            visit(AppState.cachedTree);
            redraw();
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
    AppState.cachedRects = layoutTree(AppState.focusedCachedTree, 0, 0, AppState.colorCanvas.width, AppState.colorCanvas.height);
    drawTreemap(AppState.cachedRects);
}

function layoutTree(node, x, y, w, h) {
    const rects = [];
    if (w < MIN_SIZE || h < MIN_SIZE) return rects;

    rect = {x, y, w, h, node: node}
    node.rect = rect
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

    squarify(boxes, x + PADDING, y + PADDING + FONT_SIZE, w - 2 * PADDING, h - 2 * PADDING - FONT_SIZE, rects);
    return rects;
}

function drawTreemap(rects) {
    const colorCtx = AppState.colorCanvas.getContext("2d");
    colorCtx.clearRect(0, 0, AppState.colorCanvas.width, AppState.colorCanvas.height);

    for (const [index, rect] of rects.entries()){
        drawRect(index, rect, true);
    }
}

function colorToId(color){
    return ((color[0] << 16) | (color[1] << 8) | color[2]) - 1;
}

function idToColor(id){
    const code = id + 1
    return idColor = [(code >> 16) & 0xff, (code >> 8) & 0xff, code & 0xff];
}

function drawRect(index, rect, drawId = true, ctxOverride = null) {
    const ctx = ctxOverride || AppState.colorCtx;
    // const idCtx = idCanvas.getContext("2d");
    
    const isSelected = index === AppState.selectedRectIndex;
    ctx.fillStyle = isSelected ? "#000" : folderColors[rect.node.depth % folderColors.length];
    if (rect.node.is_free_space)
        ctx.fillStyle = "#eee";

    ctx.fillRect(rect.x, rect.y, rect.w, rect.h);

    if (!rect.node.is_folder) {
        drawDiagonalCross(ctx, rect.x, rect.y, rect.w, rect.h, 6, isSelected ? "#fff" : "#555");
    }

    ctx.strokeStyle = "#222";
    ctx.lineWidth = 1;
    ctx.strokeRect(rect.x + 0.5, rect.y + 0.5, rect.w - 1, rect.h - 1);

    if (rect.w > 60 && rect.h > 15) {
        ctx.font = `${FONT_SIZE}px sans-serif`;
        const maxTextWidth = rect.w - 6;
        const label = truncateText(ctx, rect.node.name, maxTextWidth);
        ctx.fillStyle = isSelected ? "#fff" : "#000";
        ctx.fillText(label, rect.x + 4, rect.y + FONT_SIZE + 1);
    }

    if (drawId && ctx === AppState.colorCanvas.getContext("2d")) {
        const idColor = idToColor(index)
        AppState.idCtx.fillStyle = `rgb(${idColor[0]},${idColor[1]},${idColor[2]})`;
        AppState.idCtx.fillRect(Math.round(rect.x), Math.round(rect.y), Math.round(rect.w), Math.round(rect.h));
    }
}


function reDrawRect(index) {

    const rect = AppState.cachedRects[index];
    if (!rect || rect.w <= 0 || rect.h <= 0) return;
    const { x, y, w, h, node } = rect;

    // Pixels of the drawn rect
    drawRect(index, rect, false, AppState.tmpCtx);  

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

function drawDiagonalCross(ctx, x, y, w, h, size, color) {
    ctx.save();
    ctx.strokeStyle = color;
    ctx.lineWidth = 1;
    const maxSize = Math.min(w, h);
    const clampedSize = Math.min(size, maxSize);
    const half = clampedSize / 2;
    const cx = x + w / 2;
    const cy = y + h / 2;

    ctx.beginPath();
    ctx.moveTo(cx - half, cy - half);
    ctx.lineTo(cx + half, cy + half);
    ctx.moveTo(cx + half, cy - half);
    ctx.lineTo(cx - half, cy + half);
    ctx.stroke();
    ctx.restore();
}

function squarify(items, x, y, w, h, rects) {
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
            layoutRow(row, x, y, w, h, rects);
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
        layoutRow(row, x, y, w, h, rects);
    }
}

function layoutRow(row, x, y, w, h, rects) {
    const totalArea = row.reduce((sum, r) => sum + r.area, 0);
    const horizontal = w >= h;
    const length = horizontal ? w : h;
    const thickness = totalArea / length;
    const padding = -1

    let offset = 0;
    for (const box of row) {
        const breadth = box.area / thickness;

        const bw = horizontal ? breadth - padding : thickness - padding;
        const bh = horizontal ? thickness - padding : breadth - padding;
        const bx = horizontal ? x + offset : x;
        const by = horizontal ? y : y + offset;

        rects.push(...layoutTree(box.node, bx + 0.5 * padding, by + 0.5 * padding, bw, bh));
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

// function rectIdAtPoint(x, y){
//     const idCtx = idCanvas.getContext("2d");
//     const pixel = idCtx.getImageData(x, y, 1, 1).data;
//     const id = ((pixel[0] << 16) | (pixel[1] << 8) | pixel[2]) - 1;
//     return id;
// }

function rectIdAtPoint(x, y) {
    const dpr = window.devicePixelRatio || 1;
    const px = Math.round(x * dpr);
    const py = Math.round(y * dpr);
    const pixel = AppState.idCtx.getImageData(px, py, 1, 1).data;
    const id = colorToId(pixel)// ((pixel[0] << 16) | (pixel[1] << 8) | pixel[2]) - 1;
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
    //selectRect(-1)
}

function selectRect(rectIndex, dontDeselect=false){
    if (AppState.selectedRectIndex == rectIndex)
        if(rectIndex >= 0 && !dontDeselect){
            rectIndex = -1;
        }else{
            return;
        }
    prev_selectedRectIndex = AppState.selectedRectIndex
    AppState.selectedRectIndex = rectIndex
  
    reDrawRect(prev_selectedRectIndex)
    reDrawRect(AppState.selectedRectIndex)
}

window.addEventListener("click", () => hideContextMenu());

async function openInSystemBrowser() {
    if (AppState.selectedRectIndex >= 0 && AppState.selectedRectIndex < AppState.cachedRects.length) {
        node = AppState.cachedRects[AppState.selectedRectIndex].node
        await eel.open_in_file_browser(node.full_path)();
        hideContextMenu();
    }
}

function navigateToSelected() {
    if (AppState.selectedRectIndex >= 0 && AppState.selectedRectIndex < AppState.cachedRects.length) {
        node = AppState.cachedRects[AppState.selectedRectIndex].node;
        visit(node);
    }
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
    if (node === AppState.focusedCachedTree || !node.is_folder)
        return

    // Truncate future history if we went back and then change focus
    AppState.navHistory = AppState.navHistory.slice(0, AppState.navIndex + 1);

    AppState.navHistory.push(node);
    AppState.navIndex = AppState.navHistory.length - 1;

    AppState.focusedCachedTree = node;

    AppState.selectedRectIndex = -1;

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
