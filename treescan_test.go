package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScannerHonorsCancelledContext(t *testing.T) {
	profile := defaultProfile()
	scanner := NewScanner(profile, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scanner.SetContext(ctx, nil)

	var fileCount, dirCount int64
	_, err := scanner.buildTree(t.TempDir(), 0, -1, &fileCount, &dirCount)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("buildTree error = %v, want context.Canceled", err)
	}
}

func TestScanProgressLifecycleAndCancellation(t *testing.T) {
	app := NewApp()
	ctx, generation := app.beginScan("first", 0)

	progress := app.GetScanProgress()
	if !progress.Active || progress.Path != "first" {
		t.Fatalf("initial progress = %+v", progress)
	}

	app.updateScanPath(generation, "second")
	if got := app.GetScanProgress().Path; got != "second" {
		t.Fatalf("updated path = %q, want second", got)
	}

	app.CancelScan()
	if err := ctx.Err(); err == nil {
		t.Fatal("scan context was not cancelled")
	}

	app.finishScan(generation)
	if app.GetScanProgress().Active {
		t.Fatal("scan remained active after finish")
	}
}

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

	if fileCount != 4 {
		t.Fatalf("file count = %d, want 4", fileCount)
	}
	processed, discovered := scanner.WorkProgress()
	if discovered == 0 || processed != discovered {
		t.Fatalf("work progress = %d/%d, want a completed non-empty scan", processed, discovered)
	}

	directAggregate := func(folder *Node) *Node {
		for _, child := range folder.Children {
			if child.IsSmallFiles {
				return child
			}
		}
		return nil
	}

	rootAggregate := directAggregate(root)
	if rootAggregate == nil || rootAggregate.SmallFileCount != 2 {
		t.Fatalf("root aggregate = %+v, want 2 directly contained small files", rootAggregate)
	}

	var nested *Node
	for _, child := range root.Children {
		if child.IsFolder && child.Name == "nested" {
			nested = child
			break
		}
	}
	if nested == nil {
		t.Fatal("nested folder not found")
	}
	if nested.ModTime == 0 {
		t.Fatal("nested folder has no modification date")
	}
	nestedAggregate := directAggregate(nested)
	if nestedAggregate == nil || nestedAggregate.SmallFileCount != 1 {
		t.Fatalf("nested aggregate = %+v, want 1 directly contained small file", nestedAggregate)
	}

	for folder, aggregate := range map[*Node]*Node{root: rootAggregate, nested: nestedAggregate} {
		if aggregate.ParentID != folder.ID {
			t.Fatalf("aggregate parent = %d, want folder %d", aggregate.ParentID, folder.ID)
		}
		if aggregate.SmallFileLimit != profile.MinFileSize {
			t.Fatalf("small-files limit = %d, want %d", aggregate.SmallFileLimit, profile.MinFileSize)
		}
		if aggregate.ID != -1 || aggregate.IsFolder || aggregate.IsFreeSpace {
			t.Fatalf("aggregate node has unexpected identity or type: %+v", aggregate)
		}

		foundRect := false
		for _, rect := range ComputeTreemapRects(folder, 800, 600, 1) {
			if rect.IsSmallFiles && rect.ParentID != nil && *rect.ParentID == folder.ID {
				foundRect = true
				if rect.SmallFileCount != aggregate.SmallFileCount {
					t.Fatalf("rect small-files count = %d, want %d", rect.SmallFileCount, aggregate.SmallFileCount)
				}
			}
		}
		if !foundRect {
			t.Fatalf("small-files aggregate rectangle not found for %q", folder.Name)
		}
	}
}
