package main

import "testing"

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
