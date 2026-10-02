package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"spacebrowser/internal/platform"
)

func TestPreviewSnapshotsAreIndependentAndRecomputeAncestorSizes(t *testing.T) {
	p := newScanPreviewTree()
	p.add(&Node{ID: 0, ParentID: -1, Name: "root", IsFolder: true})
	p.add(&Node{ID: 1, ParentID: 0, Name: "child", IsFolder: true})
	file := &Node{ID: 2, ParentID: 1, Name: "file", Size: 20}
	p.add(file)
	file.Size = 999 // Working nodes are never borrowed by the preview.
	p.smallFiles(1, 10, 3, 1024, 2)
	first, firstNodes, revision := p.snapshot()
	if first.Size != 30 || firstNodes[2].Size != 20 || !firstNodes[1].ScanIncomplete {
		t.Fatalf("bad preview: %+v", first)
	}
	p.add(&Node{ID: 3, ParentID: 1, Name: "new", Size: 40})
	p.complete(1)
	next, nodes, nextRevision := p.snapshot()
	if next.Size != 70 || nodes[1].ScanIncomplete || nextRevision <= revision {
		t.Fatalf("bad next preview: %+v", next)
	}
	if first.Size != 30 || !firstNodes[1].ScanIncomplete || len(firstNodes[1].Children) != 2 {
		t.Fatal("later preview mutated an earlier snapshot")
	}
}

func startBlockedPreviewScan(t *testing.T, a *App, root string) (<-chan *TreeInfo, <-chan error, func()) {
	t.Helper()
	block := &blockingReadDirPlatform{API: platform.Impl, path: filepath.Join(root, "pending"), entered: make(chan struct{}), release: make(chan struct{})}
	a.filesystem = block
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(block.release) }) }
	t.Cleanup(release)
	result, failure := make(chan *TreeInfo, 1), make(chan error, 1)
	go func() { tree, err := a.GetFullTree(root); result <- tree; failure <- err }()
	select {
	case <-block.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not reach blocked folder")
	}
	return result, failure, release
}

func previewFixture(t *testing.T) (*App, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "pending"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible"), make([]byte, 8192), 0600); err != nil {
		t.Fatal(err)
	}
	a := newAppWithDependencies("", "", NewSeverityLogger(0, os.Stderr), platform.Impl, platform.Impl, platform.Impl)
	a.profile.MinFileSize = 0
	a.profile.UseCache = false
	t.Cleanup(a.scanCache.Close)
	return a, root
}

func TestScanPreviewCanBeBrowsedAndKeepsIDsOnCompletion(t *testing.T) {
	a, root := previewFixture(t)
	result, failure, release := startBlockedPreviewScan(t, a, root)
	progress := a.GetScanProgress()
	if !progress.LivePreview || progress.RootPath != root {
		t.Fatalf("progress: %+v", progress)
	}
	preview := a.GetScanPreview(progress.Generation)
	if preview == nil {
		t.Fatal("no preview while scan is running")
	}
	a.store.mu.RLock()
	var fileID, folderID int
	for _, node := range a.store.nodes {
		if node.Name == "visible" {
			fileID = node.ID
		}
		if node.Name == "pending" {
			folderID = node.ID
		}
	}
	a.store.mu.RUnlock()
	rects, err := a.Layout(folderID, 800, 600, 1)
	if err != nil || len(rects) == 0 || !rects[0].ScanIncomplete {
		t.Fatalf("pending folder is not navigable: %v", err)
	}
	if _, err := a.DeleteNode(fileID); err == nil {
		t.Fatal("deletion allowed during scan")
	}
	if got := a.GetScanPreview(progress.Generation); got != preview {
		t.Fatal("preview was not throttled")
	}
	release()
	if err := <-failure; err != nil {
		t.Fatal(err)
	}
	final := <-result
	if final.RootID != preview.RootID {
		t.Fatal("root ID changed")
	}
	path, err := a.store.NodePath(fileID)
	if err != nil || path != filepath.Join(root, "visible") {
		t.Fatalf("file ID changed: %s %v", path, err)
	}
	if a.GetScanPreview(progress.Generation) != nil {
		t.Fatal("completed scan still publishes previews")
	}
	rects, err = a.Layout(folderID, 800, 600, 1)
	if err != nil || rects[0].ScanIncomplete {
		t.Fatal("completed folder still marked scanning")
	}
}

func TestCancelledScanRestoresPreviousTreeAndRejectsLatePreviews(t *testing.T) {
	for _, hasPrevious := range []bool{false, true} {
		t.Run(map[bool]string{false: "home", true: "previous tree"}[hasPrevious], func(t *testing.T) {
			a, root := previewFixture(t)
			if hasPrevious {
				previous := t.TempDir()
				if _, err := a.GetFullTree(previous); err != nil {
					t.Fatal(err)
				}
			}
			before := a.store.snapshotState()
			_, failure, release := startBlockedPreviewScan(t, a, root)
			progress := a.GetScanProgress()
			if a.GetScanPreview(progress.Generation) == nil {
				t.Fatal("no preview")
			}
			a.CancelScan()
			release()
			if err := <-failure; err == nil {
				t.Fatal("cancelled scan succeeded")
			}
			// Even a late forced request cannot publish over the restored tree.
			if a.publishScanPreview(progress.Generation, true) != nil {
				t.Fatal("late preview returned")
			}
			after := a.store.snapshotState()
			if after.root != before.root || after.files != before.files || after.shared != before.shared {
				t.Fatal("pre-scan state not restored")
			}
			if a.GetScanProgress().Active {
				t.Fatal("scan remains active")
			}
		})
	}
}

func TestOldPreviewCannotReplaceNewScan(t *testing.T) {
	a, root := previewFixture(t)
	_, failure, release := startBlockedPreviewScan(t, a, root)
	generation := a.GetScanProgress().Generation
	if a.GetScanPreview(generation) == nil {
		t.Fatal("no preview")
	}
	newer := t.TempDir()
	if _, err := a.GetFullTree(newer); err != nil {
		t.Fatal(err)
	}
	release()
	<-failure
	a.publishScanPreview(generation, true)
	if a.store.root.FullPath != newer {
		t.Fatal("older scan overwrote new result")
	}
}

func TestConcurrentPreviewRecordsAndSnapshots(t *testing.T) {
	p := newScanPreviewTree()
	p.add(&Node{ID: 0, ParentID: -1, IsFolder: true})
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(base int) {
			defer workers.Done()
			for i := 1; i <= 100; i++ {
				p.add(&Node{ID: base + i, ParentID: 0, Size: 1})
			}
		}(worker * 100)
	}
	for i := 0; i < 10; i++ {
		p.snapshot()
	}
	workers.Wait()
	root, _, _ := p.snapshot()
	if root.Size != 400 {
		t.Fatalf("size = %d", root.Size)
	}
}

func TestVolumePreviewStartsWithFreeSpaceAndShrinksAsFilesArrive(t *testing.T) {
	p := newScanPreviewTree()
	p.diskTotal, p.diskFree = 1000, 200
	p.add(&Node{ID: 0, ParentID: -1, Name: "volume", IsFolder: true})
	root, _, _ := p.snapshot()
	if len(root.Children) != 1 || !root.Children[0].IsFreeSpace || root.Children[0].Size != 1000 {
		t.Fatal("volume does not start with its full unscanned area")
	}
	p.add(&Node{ID: 1, ParentID: 0, Size: 300})
	root, _, _ = p.snapshot()
	if root.DiskFree != 700 || root.Children[0].Size != 700 {
		t.Fatalf("remaining = %d", root.DiskFree)
	}
	p.add(&Node{ID: 2, ParentID: 0, Size: 500})
	root, _, _ = p.snapshot()
	if root.DiskFree != 200 {
		t.Fatalf("remaining = %d", root.DiskFree)
	}
	freeCount := 0
	for _, node := range root.Children {
		if node.IsFreeSpace {
			freeCount++
			if !node.ScanIncomplete {
				t.Fatal("placeholder should identify unscanned space")
			}
		}
	}
	if freeCount != 1 {
		t.Fatal("free-space node duplicated")
	}
}
