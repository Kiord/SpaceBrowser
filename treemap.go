package main

import (
	"math"
)

// ---------- Tunables ----------
const (
	treemapPad        = 5.0  // inner padding
	treemapLabelH     = 10.0 // title strip height
	treemapMinSidePx  = 4.0  // don't recurse/layout children if box is smaller
	treemapMinAreaPx2 = 9.0  // children under this area get bucketed
	treemapTopK       = 200  // keep only largest K children per dir; rest -> [Other]
)

type frame struct {
	n     *Node
	x, y  float64
	w, h  float64
	depth int
}

// ---------- Data returned to the UI ----------
type Rect struct {
	X, Y, W, H float64 `json:"x" "y" "w" "h"`
	FullPath   string  `json:"full_path"`
	Name       string  `json:"name"`
	Size       int64   `json:"size"`
	IsFolder   bool    `json:"is_folder"`
	IsFree     bool    `json:"is_free_space"`
	Depth      int     `json:"depth"`
	Index      int     `json:"index"`
}

// ComputeTreemapRects lays out the entire tree rooted at 'root' into W×H.
// It assumes each directory's Children are already size-sorted (desc) by the scanner.
func ComputeTreemapRects(root *Node, W, H float64) []Rect {

	if W <= 0 || H <= 0 || root == nil {
		return nil
	}
	out := make([]Rect, 0, 4096)
	idx := 0

	st := make([]frame, 0, 128)
	st = append(st, frame{n: root, x: 0, y: 0, w: W, h: H, depth: 0})

	for len(st) > 0 {
		f := st[len(st)-1]
		st = st[:len(st)-1]

		// Leaf or too small → just emit the rect
		if f.w < treemapMinSidePx || f.h < treemapMinSidePx {
			emitRect(&out, &idx, f.n, f.x, f.y, f.w, f.h)
			continue
		}

		// Emit self
		emitRect(&out, &idx, f.n, f.x, f.y, f.w, f.h)

		// No children to lay out
		if !f.n.IsFolder || len(f.n.Children) == 0 {
			continue
		}

		// Interior area where children live
		ax := f.x + treemapPad
		ay := f.y + treemapPad + treemapLabelH
		aw := f.w - 2*treemapPad
		ah := f.h - 2*treemapPad - treemapLabelH
		if aw < treemapMinSidePx || ah < treemapMinSidePx {
			continue
		}

		// ---- Build child list with Top-K + [Other] bucket & tiny-area bucketing ----
		// Assume input already sorted by size desc.
		kids := f.n.Children
		kept := kids
		if len(kids) > treemapTopK {
			kept = kids[:treemapTopK]
		}

		totalSize := int64(0)
		for _, c := range kept {
			if c.Size > 0 {
				totalSize += c.Size
			}
		}
		// Aggregate the tail (and later tiny boxes) into "otherSize"
		otherSize := int64(0)
		for _, c := range kids[len(kept):] {
			if c.Size > 0 {
				otherSize += c.Size
			}
		}
		if totalSize == 0 && otherSize == 0 {
			continue
		}

		// scale values → pixel areas
		interiorArea := aw * ah
		invTotal := 1.0
		if totalSize+otherSize > 0 {
			invTotal = 1.0 / float64(totalSize+otherSize)
		}

		// Pre-size temporary arrays (Structure-of-Arrays)
		// areas/ptrs only for kept children; tiny ones will be bucketed into "otherSize"
		areas := make([]float64, 0, len(kept))
		ptrs := make([]*Node, 0, len(kept))

		// threshold in pixels for tiny boxes
		tinyThreshold := treemapMinAreaPx2

		// fill areas/ptrs, bucket tiny into otherSize
		for _, c := range kept {
			if c.Size <= 0 {
				continue
			}
			a := float64(c.Size) * invTotal * interiorArea
			if a < tinyThreshold {
				otherSize += c.Size
				totalSize -= c.Size
				continue
			}
			areas = append(areas, a)
			ptrs = append(ptrs, c)
		}

		// If "other" is meaningful, append a synthetic box at the end
		if otherSize > 0 {
			a := float64(otherSize) * invTotal * interiorArea
			areas = append(areas, a)
			// make a lightweight synthetic node; no FullPath as it represents a bucket
			ptrs = append(ptrs, &Node{
				Name:        "[Other]",
				Size:        otherSize,
				IsFolder:    false, // treat as leaf for now; could set true if I later support expanding it
				IsFreeSpace: false,
				Depth:       f.depth + 1,
				FullPath:    "",
				Children:    nil,
			})
		}

		// Lay children with Squarify (O(n) pass; O(1) worst-aspect test)
		squarifyInto(ptrs, areas, ax, ay, aw, ah, f.depth+1, &out, &idx, &st)
	}

	return out
}

// ---------- Helpers ----------

// Add the rect for node n; snap to integer pixels (crisp lines)
func emitRect(out *[]Rect, idx *int, n *Node, x, y, w, h float64) {
	x1 := math.Round(x)
	y1 := math.Round(y)
	x2 := math.Round(x + w)
	y2 := math.Round(y + h)
	rw := math.Max(0, x2-x1)
	rh := math.Max(0, y2-y1)

	*out = append(*out, Rect{
		X: x1, Y: y1, W: rw, H: rh,
		FullPath: n.FullPath,
		Name:     n.Name,
		Size:     n.Size,
		IsFolder: n.IsFolder,
		IsFree:   n.IsFreeSpace,
		Depth:    n.Depth,
		Index:    *idx,
	})
	*idx++
}

// Squarify children into container (x,y,w,h), pushing child frames for folders.
func squarifyInto(nodes []*Node, areas []float64, x, y, w, h float64, depth int, out *[]Rect, idx *int, st *[]frame) {
	if len(nodes) == 0 || w <= 0 || h <= 0 {
		return
	}

	// Iterate through items with a cursor; build a row and place it when adding next would worsen worst aspect.
	i := 0
	cx, cy, cw, ch := x, y, w, h

	for i < len(nodes) {
		// Start a new row
		rowStart := i
		rowSum := 0.0
		rowMin := math.MaxFloat64
		rowMax := 0.0

		L := math.Max(cw, ch) // layout along the long side
		// Add items while worst aspect improves
		for i < len(nodes) {
			a := areas[i]
			// candidate stats
			sNew := rowSum + a
			minNew := rowMin
			if a < minNew {
				minNew = a
			}
			maxNew := rowMax
			if a > maxNew {
				maxNew = a
			}

			if rowSum > 0 { // compare with previous row stats
				if worseAfter(rowSum, rowMin, rowMax, sNew, minNew, maxNew, L) {
					break // placing current item would worsen row; stop here
				}
			}
			// accept item into row
			rowSum = sNew
			rowMin = minNew
			rowMax = maxNew
			i++
		}

		// Layout the row into a strip
		if rowSum <= 0 {
			break
		}
		horizontal := cw >= ch
		thickness := rowSum / L // strip thickness along the short side
		if horizontal {
			// row consumes a band at top of (cx,cy,cw,ch)
			layoutRow(nodes[rowStart:i], areas[rowStart:i], cx, cy, cw, thickness, depth, out, idx, st, horizontal)
			cy += thickness
			ch -= thickness
		} else {
			// column consumes a band at left
			layoutRow(nodes[rowStart:i], areas[rowStart:i], cx, cy, thickness, ch, depth, out, idx, st, horizontal)
			cx += thickness
			cw -= thickness
		}

		// Stop if the remaining container is too small
		if cw < treemapMinSidePx || ch < treemapMinSidePx {
			break
		}
	}
}

// Return true if the row's worst aspect ratio would worsen when adding next item
func worseAfter(s, amin, amax, sNew, aminNew, amaxNew, L float64) bool {
	// previous
	T := s / L
	// avoid div by zero (shouldn't happen since s>0)
	if T <= 0 || amin <= 0 {
		return false
	}
	worstBefore := math.Max(amax/(T*T), (T*T)/amin)

	// candidate
	Tn := sNew / L
	if Tn <= 0 || aminNew <= 0 {
		return false
	}
	worstAfter := math.Max(amaxNew/(Tn*Tn), (Tn*Tn)/aminNew)

	return worstAfter > worstBefore
}

// Place one row (or column) of boxes; push folder frames for recursive layout later (iterative).
func layoutRow(nodes []*Node, areas []float64, x, y, w, h float64, depth int, out *[]Rect, idx *int, st *[]frame, horizontal bool) {
	// negate padding a hair to hide hairline gaps after rounding
	const negPad = -1.0

	total := 0.0
	for _, a := range areas {
		total += a
	}
	if total <= 0 {
		return
	}

	// length along the row; the other dimension is "thickness"
	length := w
	if !horizontal {
		length = h
	}
	thickness := total / length

	// accumulate along the row
	offset := 0.0
	for k, n := range nodes {
		breadth := areas[k] / thickness

		var bx, by, bw, bh float64
		if horizontal {
			bx = x + offset
			by = y
			bw = breadth - negPad
			bh = thickness - negPad
		} else {
			bx = x
			by = y + offset
			bw = thickness - negPad
			bh = breadth - negPad
		}

		// Emit child rect now
		emitRect(out, idx, n, bx+0.5*negPad, by+0.5*negPad, bw, bh)

		// If it's a folder, queue its children for later (iterative traversal)
		if n.IsFolder && len(n.Children) > 0 && bw >= treemapMinSidePx && bh >= treemapMinSidePx {
			*st = append(*st, frame{
				n: n, x: bx + 0.5*negPad, y: by + 0.5*negPad, w: bw, h: bh, depth: depth,
			})
		}

		offset += breadth
	}
}
