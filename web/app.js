let cachedTree = null;
let focusedCachedTree = null;
let cachedRects = null
let selectedRect = null;
let canvas = null;
let navHistory = [];
let navIndex = -1;

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
    canvas = document.getElementById("treemap");
    resizeCanvas();

    canvas.addEventListener("click", (e) => {
        const { x, y } = getCanvasCoords(e);
        const rect = rectAtPoint(x, y);
        if (rect && !rect.node.is_free_space) {
            selectedRect = rect;
            redraw();
        }else{
            selectedRect = null;
        }
    });

    canvas.addEventListener("contextmenu", (e) => {
        e.preventDefault();
        const { x, y } = getCanvasCoords(e);
        const clicked = rectAtPoint(x, y);
        if (clicked && !clicked.node.is_free_space) {
            selectedRect = clicked;
            showContextMenu(e.pageX, e.pageY, clicked);
            redraw();
        } else {
            hideContextMenu();
            selectedRect = null;
        }
    });
});

function getCanvasCoords(event) {
    const rect = canvas.getBoundingClientRect();
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
    const containerRect = canvas.parentElement.getBoundingClientRect();
    const controlsRect = document.querySelector(".controls").getBoundingClientRect();

    const width = containerRect.width;
    const height = window.innerHeight - controlsRect.height;

    canvas.width = width;
    canvas.height = height;

    canvas.style.width = `${width}px`;
    canvas.style.height = `${height}px`;
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
    cachedRects = layoutTree(focusedCachedTree, 0, 0, canvas.width, canvas.height);
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
    const ctx = canvas.getContext("2d");
    ctx.clearRect(0, 0, canvas.width, canvas.height);

    const folderColors = [
        "#ff7f7f", "#ffbf7f", "#ffff00", "#7fff7f", "#7fffff",
        "#bfbfff", "#bfbfbf"
    ];

   for (const rect of rects) {
        const isSelected = selectedRect && rect.node === selectedRect.node;

        // Background
        ctx.fillStyle = isSelected ? "#000" : folderColors[rect.node.depth % folderColors.length];
        if (rect.node.is_free_space)
            ctx.fillStyle = "#eee"
        ctx.fillRect(rect.x, rect.y, rect.w, rect.h);

        // Diagonal for files
        if (!rect.node.is_folder) {
            drawDiagonalCross(ctx, rect.x, rect.y, rect.w, rect.h, 6, isSelected ? "#fff" : "#555");
        }

        // Border
        ctx.strokeStyle = "#222";
        ctx.lineWidth = 1;
        ctx.strokeRect(rect.x + 0.5, rect.y + 0.5, rect.w - 1, rect.h - 1);

        // Label
        if (rect.w > 60 && rect.h > 15) {
            ctx.font = `${FONT_SIZE}px sans-serif`;
            const maxTextWidth = rect.w - 6;
            const label = truncateText(ctx, rect.node.name, maxTextWidth);
            ctx.fillStyle = isSelected ? "#fff" : "#000";
            ctx.fillText(label, rect.x + 4, rect.y + FONT_SIZE + 1);
        }
    }
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

function isInRect(x, y, rect){
    return  x >= rect.x && x <= rect.x + rect.w && y >= rect.y && y <= rect.y + rect.h
}


function nodeAtPoint(x, y, node){
    if (node.is_folder && node.children.length > 0){
        in_rect_child = node.children.find(n => n.rect && isInRect(x, y, n.rect))
        if (in_rect_child){
            return nodeAtPoint(x, y, in_rect_child)
        }
        return node;
    }
    return node;
}

function rectAtPoint(x, y){
    if (!focusedCachedTree)
        return null
    if (isInRect(x, y, focusedCachedTree.rect)){
        node = nodeAtPoint(x, y, focusedCachedTree)
        return node.rect
    }
}


function showContextMenu(x, y, rect) {
    selectedRect = rect;
    const menu = document.getElementById("contextMenu");
    menu.style.left = `${x}px`;
    menu.style.top = `${y}px`;
    menu.style.display = "block";
}



function hideContextMenu() {
    document.getElementById("contextMenu").style.display = "none";
    selectedRect = null;
}

window.addEventListener("click", () => hideContextMenu());

async function openInSystemBrowser() {
    if (selectedRect) {
        await eel.open_in_file_browser(selectedRect.node.full_path)();
        hideContextMenu();
    }
}

function navigateToSelected() {
    if (selectedRect?.node.is_folder) {
        visit(selectedRect.node)
        redraw()
    } else {
        console.warn("Not a folder:", selectedRect.node.full_path);
    }
}

function goToRoot() {
    if (!cachedTree) return;
    visit(cachedTree);
    redraw();
    updateNavButtons();
}

function goToParent() {
    if (!focusedCachedTree || !focusedCachedTree.parent) return;

    visit(focusedCachedTree.parent);
    redraw();
    updateNavButtons();
}




function visit(node) {
    if (node === focusedCachedTree)
        return

    // Truncate future history if we went back and then change focus
    navHistory = navHistory.slice(0, navIndex + 1);

    navHistory.push(node);
    navIndex = navHistory.length - 1;

    focusedCachedTree = node;
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
