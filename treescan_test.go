package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScannerAggregatesSmallFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"empty.txt": nil,
		"tiny.txt":  make([]byte, 100),
		"large.bin": make([]byte, 2048),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	subdir := filepath.Join(dir, "nested")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "nested-tiny.txt"), make([]byte, 200), 0o600); err != nil {
		t.Fatal(err)
	}

	profile := defaultProfile()
	profile.MinFileSize = 1024
	profile.SkipNetworkFS = false
	scanner := NewScanner(profile, 1)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(dir, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}
	scanner.addSmallFilesAggregate(root)

	if fileCount != 4 {
		t.Fatalf("file count = %d, want 4", fileCount)
	}

	var aggregate *Node
	aggregateCount := 0
	var visit func(*Node)
	visit = func(node *Node) {
		if node.IsSmallFiles {
			aggregate = node
			aggregateCount++
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(root)
	if aggregate == nil {
		t.Fatal("small-files aggregate node not found")
	}
	if aggregateCount != 1 {
		t.Fatalf("aggregate node count = %d, want 1", aggregateCount)
	}
	if aggregate.ParentID != root.ID {
		t.Fatalf("aggregate parent = %d, want root %d", aggregate.ParentID, root.ID)
	}
	if aggregate.SmallFileCount != 3 {
		t.Fatalf("small-files count = %d, want 3", aggregate.SmallFileCount)
	}
	if aggregate.SmallFileLimit != profile.MinFileSize {
		t.Fatalf("small-files limit = %d, want %d", aggregate.SmallFileLimit, profile.MinFileSize)
	}
	if aggregate.ID != -1 || aggregate.IsFolder || aggregate.IsFreeSpace {
		t.Fatalf("aggregate node has unexpected identity or type: %+v", aggregate)
	}

	rects := ComputeTreemapRects(root, 800, 600, 1)
	foundRect := false
	for _, rect := range rects {
		if rect.IsSmallFiles {
			foundRect = true
			if rect.SmallFileCount != 3 {
				t.Fatalf("rect small-files count = %d, want 3", rect.SmallFileCount)
			}
			if rect.SmallFileLimit != profile.MinFileSize {
				t.Fatalf("rect small-files limit = %d, want %d", rect.SmallFileLimit, profile.MinFileSize)
			}
		}
	}
	if !foundRect {
		t.Fatal("small-files aggregate rectangle not found")
	}
}
