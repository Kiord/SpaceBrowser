// treemap.go
package main

import (
	"math"
)

// Treemap rendering constants
const (
	treemapPad       = 5.0  // inner padding inside folder rects
	treemapLabelH    = 10.0 // space reserved for the folder title strip
	treemapMinSidePx = 4.0  // drop (do not emit) any rect whose rounded width or height is < 4 px
)

// internal stack frame for iterative traversal
type frame struct {
	n     *Node
	x, y  float64
	w, h  float64
	depth int
}

// Rect is the draw-ready rectangle returned to the frontend.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`

	NodeID   int   `json:"node_id"`
	ParentID *int  `json:"parent_id,omitempty"`
	Children []int `json:"children,omitempty"` // indices into THIS rects array

	FullPath string `json:"full_path"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsFolder bool   `json:"is_folder"`
	IsFree   bool   `json:"is_free_space"`
	Depth    int    `json:"depth"`

	// on root rect when scanning a mount
	DiskTotal int64 `json:"disk_total,omitempty"`
	DiskFree  int64 `json:"disk_free,omitempty"`

	// on leaf rects
	MTime int64 `json:"mtime"`
}

// ComputeTreemapRects lays out the subtree rooted at 'root' into a W×H rectangle.
// Important behavior:
//   - ALL children contribute to area scaling (so whitespace appears for tiny items),
//   - BUT a child is only EMITTED if its FINAL ROUNDED width AND height are >= treemapMinSidePx,
//   - Rows whose thickness would render < 4 px are skipped entirely, leaving a blank band.
func ComputeTreemapRects(root *Node, W, H, scale float64) []Rect {
	if root == nil || W <= 0 || H <= 0 {
		return nil
	}

	out := make([]Rect, 0, 4096)
	st := make([]frame, 0, 128)

	// Push root
	st = append(st, frame{n: root, x: 0, y: 0, w: W, h: H, depth: 0})

	for len(st) > 0 {
		// Pop
		f := st[len(st)-1]
		st = st[:len(st)-1]

		// Emit the container rect for this node; record its rect index for attaching children later
		parentRectIdx := emitRect(&out, f.n, f.x, f.y, f.w, f.h)

		// If this node has no visible inner area or no children, continue
		if !f.n.IsFolder || len(f.n.Children) == 0 {
			continue
		}

		// Compute interior area for children (padding + label strip)
		scaledPad := treemapPad * scale
		scaledlabelH := treemapLabelH * scale
		scaledMinSidePx := treemapMinSidePx * scale

		ax := f.x + scaledPad
		ay := f.y + scaledPad + scaledlabelH
		aw := f.w - 2*scaledPad
		ah := f.h - 2*scaledPad - scaledlabelH
		if aw < scaledMinSidePx || ah < scaledMinSidePx {
			continue
		}

		// Build areas from ALL children (so omitted tiny ones still consume space as whitespace)
		kids := f.n.Children // already size-sorted desc by the scanner
		var totalSize int64
		for _, c := range kids {
			if c.Size > 0 {
				totalSize += c.Size
			}
		}
		if totalSize == 0 {
			continue
		}

		interiorArea := aw * ah
		invTotal := 1.0 / float64(totalSize)

		areas := make([]float64, 0, len(kids))
		ptrs := make([]*Node, 0, len(kids))
		for _, c := range kids {
			if c.Size <= 0 {
				continue
			}
			areas = append(areas, float64(c.Size)*invTotal*interiorArea)
			ptrs = append(ptrs, c)
		}
		if len(ptrs) == 0 {
			continue
		}

		// Lay out children into the interior, recording indices of EMITTED children
		squarifyInto(ptrs, areas, ax, ay, aw, ah, f.depth+1, parentRectIdx, &out, &st)
	}

	return out
}

// emitRect appends a Rect to 'out' using drawing-space rounding and returns its index.
func emitRect(out *[]Rect, n *Node, x, y, w, h float64) int {
	// Snap to integer pixels for crisp rendering
	x1 := math.Round(x)
	y1 := math.Round(y)
	x2 := math.Round(x + w)
	y2 := math.Round(y + h)
	rw := math.Max(0, x2-x1)
	rh := math.Max(0, y2-y1)

	var parentPtr *int
	if n.ParentID >= 0 {
		val := n.ParentID
		parentPtr = &val
	}

	idx := len(*out)
	*out = append(*out, Rect{
		X: x1, Y: y1, W: rw, H: rh,

		NodeID:   n.ID,
		ParentID: parentPtr,
		Children: nil,

		FullPath: n.FullPath,
		Name:     n.Name,
		Size:     n.Size,
		IsFolder: n.IsFolder,
		IsFree:   n.IsFreeSpace,
		Depth:    n.Depth,

		DiskTotal: n.DiskTotal,
		DiskFree:  n.DiskFree,

		MTime: n.ModTime,
	})
	return idx
}

// roundedWH returns the final pixel width/height after rounding the rect corners like emitRect.
func roundedWH(x, y, w, h float64) (rw, rh float64) {
	x1 := math.Round(x)
	y1 := math.Round(y)
	x2 := math.Round(x + w)
	y2 := math.Round(y + h)
	return math.Max(0, x2-x1), math.Max(0, y2-y1)
}

// squarifyInto lays out 'nodes' with given 'areas' into (x,y,w,h), appending EMITTED child rect
// indices to out[parentRect].Children, and pushing visible folders onto the traversal stack.
func squarifyInto(nodes []*Node, areas []float64, x, y, w, h float64, depth int, parentRect int, out *[]Rect, st *[]frame) {
	if len(nodes) == 0 || w <= 0 || h <= 0 {
		return
	}

	i := 0
	cx, cy, cw, ch := x, y, w, h

	for i < len(nodes) {
		// Start a new row
		rowStart := i
		rowSum := 0.0
		rowMin := math.MaxFloat64
		rowMax := 0.0

		// We lay along the long side, so the short side is the row thickness
		L := math.Max(cw, ch)

		// Greedily add until worst aspect would get worse
		for i < len(nodes) {
			a := areas[i]
			sNew := rowSum + a
			minNew := rowMin
			if a < minNew {
				minNew = a
			}
			maxNew := rowMax
			if a > maxNew {
				maxNew = a
			}

			if rowSum > 0 && worseAfter(rowSum, rowMin, rowMax, sNew, minNew, maxNew, L) {
				break
			}
			rowSum = sNew
			rowMin = minNew
			rowMax = maxNew
			i++
		}

		if rowSum <= 0 {
			break
		}

		// Compute row thickness in pixels; if < 4 px after flooring, skip entire band
		horizontal := cw >= ch
		thickness := rowSum / L
		if math.Floor(thickness) < treemapMinSidePx {
			if horizontal {
				cy += thickness
				ch -= thickness
			} else {
				cx += thickness
				cw -= thickness
			}
			// Continue with remaining area; this leaves a blank strip (whitespace)
			continue
		}

		// Place the row
		if horizontal {
			layoutRow(nodes[rowStart:i], areas[rowStart:i], cx, cy, cw, thickness, depth, parentRect, out, st, true)
			cy += thickness
			ch -= thickness
		} else {
			layoutRow(nodes[rowStart:i], areas[rowStart:i], cx, cy, thickness, ch, depth, parentRect, out, st, false)
			cx += thickness
			cw -= thickness
		}

		// Stop if what's left is too small to be meaningful
		if cw < treemapMinSidePx || ch < treemapMinSidePx {
			break
		}
	}
}

// worseAfter compares the worst aspect ratio of the current row vs adding the next item.
func worseAfter(s, amin, amax, sNew, aminNew, amaxNew, L float64) bool {
	T := s / L
	if T <= 0 || amin <= 0 {
		return false
	}
	worstBefore := math.Max(amax/(T*T), (T*T)/amin)

	Tn := sNew / L
	if Tn <= 0 || aminNew <= 0 {
		return false
	}
	worstAfter := math.Max(amaxNew/(Tn*Tn), (Tn*Tn)/aminNew)

	return worstAfter > worstBefore
}

// layoutRow places one row (or column) of boxes.
// It EMITS only children with rounded width & height >= treemapMinSidePx.
// Invisible children still consume space (offset increases), so their area becomes blank whitespace.
func layoutRow(nodes []*Node, areas []float64, x, y, w, h float64, depth int, parentRect int, out *[]Rect, st *[]frame, horizontal bool) {
	const negPad = -1.0 // small negative to hide hairline gaps after rounding

	// Sum of areas in this row
	total := 0.0
	for _, a := range areas {
		total += a
	}
	if total <= 0 {
		return
	}

	// Row dimensions
	length := w
	if !horizontal {
		length = h
	}
	thickness := total / length

	// Place each item in the row
	offset := 0.0
	for k, n := range nodes {
		breadth := areas[k] / thickness

		// Compute child box
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

		// Decide emission by FINAL rounded pixel size
		rw, rh := roundedWH(bx+0.5*negPad, by+0.5*negPad, bw, bh)
		if rw >= treemapMinSidePx && rh >= treemapMinSidePx {
			// Emit child rect and record its index under the parent
			childIdx := emitRect(out, n, bx+0.5*negPad, by+0.5*negPad, bw, bh)
			(*out)[parentRect].Children = append((*out)[parentRect].Children, childIdx)

			// If it's a folder with children, queue it for inner layout (only if visible at this level)
			if n.IsFolder && len(n.Children) > 0 {
				*st = append(*st, frame{
					n: n, x: bx + 0.5*negPad, y: by + 0.5*negPad, w: bw, h: bh, depth: depth,
				})
			}
		}
		// Always advance offset so invisible children still consume space (blank area)
		offset += breadth
	}
}
