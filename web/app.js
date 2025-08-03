let cachedTree = null;
let focusedCachedTree = null;
let cachedRects = null
let selectedRectIndex = -1;

let navHistory = [];
let navIndex = -1;

let colorCanvas = null, colorCtx = null;
let idCanvas = null, idCtx = null;
let tmpCanvas = null, tmpCtx = null;
let maskCanvas = null, maskCtx = null;

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
    colorCanvas = document.getElementById("colorCanvas");
    idCanvas = document.getElementById("idCanvas");
    tmpCanvas = document.getElementById("tmpCanvas");
    maskCanvas = document.getElementById("maskCanvas");

    colorCtx = colorCanvas.getContext("2d");
    idCtx = idCanvas.getContext("2d");
    tmpCtx = tmpCanvas.getContext("2d", {alpha: true});
    maskCtx = maskCanvas.getContext("2d", {alpha: true});

    resizeCanvas();

    colorCanvas.addEventListener("click", (e) => {
        const { x, y } = getCanvasCoords(e);
        const index = rectIdAtPoint(x, y);
        if (index >= 0)
            selectRect(index);

    });

    colorCanvas.addEventListener("contextmenu", (e) => {
        e.preventDefault();
        const { x, y } = getCanvasCoords(e);
        const index = rectIdAtPoint(x, y);
        selectRect(index, dontDeselect=true);
        if (index >= 0 && !cachedRects[index].node.is_free_space) {
            showContextMenu(e.pageX, e.pageY);
        } else {
            hideContextMenu();
        }
    });
});

function getCanvasCoords(event) {
    const rect = colorCanvas.getBoundingClientRect();
    return {
        x: event.clientX - rect.left,
        y: event.clientY - rect.top
    };
}

window.addEventListener("resize", () => {
    console.log("resizing!")
    resizeCanvas();
    redraw();
});

function resizeCanvas() {
    const containerRect = colorCanvas.parentElement.getBoundingClientRect();
    const controlsRect = document.querySelector(".controls").getBoundingClientRect();

    const width = containerRect.width;
    const height = window.innerHeight - controlsRect.height;
    const dpr = window.devicePixelRatio || 1;

    colorCanvas.width = width * dpr;
    colorCanvas.height = height * dpr;
    idCanvas.width = width * dpr;
    idCanvas.height = height * dpr;

    colorCanvas.style.width = `${width}px`;
    colorCanvas.style.height = `${height}px`;
    idCanvas.style.width = `${width}px`;
    idCanvas.style.height = `${height}px`;


    tmpCanvas.width = width * dpr;
    tmpCanvas.height = height * dpr;
    maskCanvas.width = width;
    maskCanvas.height = height;

    tmpCtx.setTransform(1, 0, 0, 1, 0, 0);
    tmpCtx.scale(dpr, dpr);


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
        const tree = await eel.get_full_tree(path)();
        if (tree.error) {
            console.error("Backend error:", tree.error);
        } else {
            trackParents(tree);
            tree.parent = null;
            cachedTree = tree;
            navHistory = [];
            navIndex = -1;
            selectedRectIndex = -1
            visit(cachedTree);
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
    if (!focusedCachedTree) return;
    cachedRects = layoutTree(focusedCachedTree, 0, 0, colorCanvas.width, colorCanvas.height);
    drawTreemap(cachedRects);
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
    const colorCtx = colorCanvas.getContext("2d");
    colorCtx.clearRect(0, 0, colorCanvas.width, colorCanvas.height);

    for (const [index, rect] of rects.entries()){
        drawRect(index, rect, true);
    }
}

function drawRect(index, rect, drawId = true, ctxOverride = null) {
    const ctx = ctxOverride || colorCtx;
    // const idCtx = idCanvas.getContext("2d");
    
    const isSelected = index === selectedRectIndex;
    console.log("Drawing index:", index, "Selected:", selectedRectIndex, "=>", isSelected);
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

    if (drawId && ctx === colorCanvas.getContext("2d")) {
        const id = index + 1;
        const idColor = [(id >> 16) & 0xff, (id >> 8) & 0xff, id & 0xff];
        idCtx.fillStyle = `rgb(${idColor[0]},${idColor[1]},${idColor[2]})`;
        idCtx.fillRect(rect.x, rect.y, rect.w, rect.h);
    }
}


function reDrawRect(index) {

    const rect = cachedRects[index];
    if (!rect || rect.w <= 0 || rect.h <= 0) return;
    const { x, y, w, h, node } = rect;

    // Pixels of the drawn rect
    drawRect(index, rect, false, tmpCtx);  

    // Creating a mask to only blit this rect
    maskCtx.clearRect(0, 0, maskCanvas.width, maskCanvas.height);
    maskCtx.fillStyle = 'rgba(0,0,0,1)';
    maskCtx.fillRect(x, y, w, h);

    maskCtx.save();
    maskCtx.globalCompositeOperation = 'destination-out';
  
    // carving out the rect childs from the mask !
    if (node.is_folder && node.children) {
        for (const child of node.children) {
            if (child.rect) {
                const cr = child.rect;
                maskCtx.fillRect(cr.x, cr.y, cr.w, cr.h);
                console.log('masking', cr.x, cr.y, cr.w, cr.h)
            }
        }
    }
    maskCtx.restore();

    tmpCtx.globalCompositeOperation = 'destination-in';
    tmpCtx.drawImage(maskCanvas, 0, 0);
    tmpCtx.globalCompositeOperation = 'source-over';

    colorCtx.drawImage(tmpCanvas, 0, 0);

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

function rectIdAtPoint(x, y){
    const idCtx = idCanvas.getContext("2d");
    const pixel = idCtx.getImageData(x, y, 1, 1).data;
    const id = ((pixel[0] << 16) | (pixel[1] << 8) | pixel[2]) - 1;
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
    if (selectedRectIndex == rectIndex)
        if(rectIndex >= 0 && !dontDeselect){
            rectIndex = -1;
        }else{
            return;
        }
    prev_selectedRectIndex = selectedRectIndex
    selectedRectIndex = rectIndex
  
    reDrawRect(prev_selectedRectIndex)
    reDrawRect(selectedRectIndex)
}

window.addEventListener("click", () => hideContextMenu());

async function openInSystemBrowser() {
    if (selectedRectIndex > 0 && selectedRectIndex < cachedRects.length) {
        node = cachedRects[selectedRectIndex].node
        await eel.open_in_file_browser(node.full_path)();
        hideContextMenu();
    }
}

function navigateToSelected() {
    if (selectedRectIndex > 0 && selectedRectIndex < cachedRects.length) {
        node = cachedRects[selectedRectIndex].node;
        visit(node);
    }
}

function goToRoot() {
    if (!cachedTree) return;
    visit(cachedTree);
}

function goToParent() {
    if (!focusedCachedTree || !focusedCachedTree.parent) return;

    visit(focusedCachedTree.parent);
}


function visit(node) {
    if (node === focusedCachedTree || !node.is_folder)
        return

    // Truncate future history if we went back and then change focus
    navHistory = navHistory.slice(0, navIndex + 1);

    navHistory.push(node);
    navIndex = navHistory.length - 1;

    focusedCachedTree = node;

    selectedRectIndex = -1;

    redraw();
    updateNavButtons();
}


function goBackward() {
    if (navIndex > 0) {
        navIndex--;
        focusedCachedTree = navHistory[navIndex];
        redraw();
        updateNavButtons();
    }
}

function goForward() {
    if (navIndex < navHistory.length - 1) {
        navIndex++;
        focusedCachedTree = navHistory[navIndex];
        redraw();
        updateNavButtons();
    }
}

function updateNavButtons() {
    document.getElementById("rootButton").disabled = !cachedTree || focusedCachedTree === cachedTree;
    document.getElementById("parentButton").disabled = !cachedTree || !focusedCachedTree?.parent;

    document.getElementById("backwardButton").disabled = !cachedTree || navIndex <= 0;
    document.getElementById("forwardButton").disabled = !cachedTree || navIndex >= navHistory.length - 1;
}
