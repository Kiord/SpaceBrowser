package main

import (
	"math"
	"testing"
)

func TestLayoutRowPlacesFreeSpaceAtOuterEdge(t *testing.T) {
	regular := &Node{ID: 1, Name: "regular", Size: 50}
	free := &Node{ID: -1, Name: "free", Size: 50, IsFreeSpace: true}

	out := []Rect{{}}
	stack := []frame{}
	layoutRow(
		[]*Node{free, regular},
		[]float64{1000, 1000},
		0, 0, 100, 20, 1, 0, &out, &stack, true,
	)

	var regularRect, freeRect *Rect
	for i := range out {
		switch out[i].Name {
		case "regular":
			regularRect = &out[i]
		case "free":
			freeRect = &out[i]
		}
	}
	if regularRect == nil || freeRect == nil {
		t.Fatal("expected both row rectangles")
	}
	if freeRect.X <= regularRect.X {
		t.Fatalf("free space x = %v, regular x = %v; free space should be last", freeRect.X, regularRect.X)
	}
	if freeRect.X+freeRect.W < 100 {
		t.Fatalf("free space right edge = %v, want at least 100", freeRect.X+freeRect.W)
	}
}

func TestTreemapLayoutStructuralAndGeometryInvariants(t *testing.T) {
	root := &Node{ID: 0, ParentID: -1, Name: "root", Size: 1000, IsFolder: true, Depth: 0}
	folder := &Node{ID: 1, ParentID: 0, Name: "folder", Size: 500, IsFolder: true, Depth: 1}
	folder.Children = []*Node{
		{ID: 4, ParentID: 1, Name: "nested-a", Size: 300, Depth: 2},
		{ID: 5, ParentID: 1, Name: "nested-b", Size: 200, Depth: 2},
	}
	root.Children = []*Node{
		folder,
		{ID: 2, ParentID: 0, Name: "file", Size: 300, Depth: 1},
		{ID: 3, ParentID: 0, Name: "free", Size: 200, IsFreeSpace: true, Depth: 1},
	}

	const width, height = 1000.0, 700.0
	rects := ComputeTreemapRects(root, width, height, 1)
	if len(rects) != 6 {
		t.Fatalf("rectangle count = %d, want one rectangle per node (6)", len(rects))
	}
	if rects[0].NodeID != root.ID || rects[0].X != 0 || rects[0].Y != 0 || rects[0].W != width || rects[0].H != height {
		t.Fatalf("root rectangle = %+v", rects[0])
	}

	seenNodeIDs := make(map[int]int, len(rects))
	referenced := make(map[int]int, len(rects)-1)
	for index, rect := range rects {
		for _, value := range []float64{rect.X, rect.Y, rect.W, rect.H} {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("rectangle %d has non-finite geometry: %+v", index, rect)
			}
		}
		if rect.W <= 0 || rect.H <= 0 {
			t.Fatalf("rectangle %d has non-positive size: %+v", index, rect)
		}
		if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > width || rect.Y+rect.H > height {
			t.Fatalf("rectangle %d lies outside the canvas: %+v", index, rect)
		}
		if previous, exists := seenNodeIDs[rect.NodeID]; exists {
			t.Fatalf("node %d emitted twice at rectangle indices %d and %d", rect.NodeID, previous, index)
		}
		seenNodeIDs[rect.NodeID] = index

		for _, childIndex := range rect.Children {
			if childIndex <= 0 || childIndex >= len(rects) {
				t.Fatalf("rectangle %d has invalid child index %d", index, childIndex)
			}
			child := rects[childIndex]
			if child.ParentID == nil || *child.ParentID != rect.NodeID {
				t.Fatalf("rectangle %d child %d has parent %+v, want node %d", index, childIndex, child.ParentID, rect.NodeID)
			}
			if child.Depth != rect.Depth+1 {
				t.Fatalf("rectangle %d child %d depth = %d, want %d", index, childIndex, child.Depth, rect.Depth+1)
			}
			referenced[childIndex]++
		}

		for first := 0; first < len(rect.Children); first++ {
			for second := first + 1; second < len(rect.Children); second++ {
				a := rects[rect.Children[first]]
				b := rects[rect.Children[second]]
				overlapW := math.Min(a.X+a.W, b.X+b.W) - math.Max(a.X, b.X)
				overlapH := math.Min(a.Y+a.H, b.Y+b.H) - math.Max(a.Y, b.Y)
				if overlapW > 1 && overlapH > 1 {
					t.Fatalf("siblings overlap by more than the one-pixel drawing bleed: %+v and %+v", a, b)
				}
			}
		}
	}

	for index := 1; index < len(rects); index++ {
		if referenced[index] != 1 {
			t.Fatalf("rectangle %d is referenced by %d parents, want exactly 1", index, referenced[index])
		}
	}
}

func TestTreemapLayoutRejectsInvalidInputs(t *testing.T) {
	root := &Node{ID: 0, ParentID: -1, Name: "root", Size: 1, IsFolder: true}
	for name, rects := range map[string][]Rect{
		"nil root":       ComputeTreemapRects(nil, 100, 100, 1),
		"zero width":     ComputeTreemapRects(root, 0, 100, 1),
		"negative width": ComputeTreemapRects(root, -1, 100, 1),
		"zero height":    ComputeTreemapRects(root, 100, 0, 1),
	} {
		if rects != nil {
			t.Fatalf("%s returned %+v, want nil", name, rects)
		}
	}
}
