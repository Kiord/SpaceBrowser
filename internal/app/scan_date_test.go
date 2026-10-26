package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanDatesReachPreviewLayoutAndSavedSnapshots(t *testing.T) {
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	profile := *defaultProfile()
	profile.MinFileSize = 0
	scanner := NewScanner(&profile, 1)
	scanner.scanTime = 1800000000
	scanner.preview = newScanPreviewTree()
	var files, dirs int64
	root, err := scanner.buildTree(path, 0, -1, &files, &dirs)
	if err != nil {
		t.Fatal(err)
	}
	preview, _, _ := scanner.preview.snapshot()
	for _, tree := range []*Node{root, preview} {
		rects := ComputeTreemapRects(tree, 800, 600, 1)
		if len(rects) != 2 {
			t.Fatalf("rect count = %d", len(rects))
		}
		for _, rect := range rects {
			if rect.ScanTime != scanner.scanTime {
				t.Fatalf("lost scan date on %+v", rect)
			}
		}
	}
	cache := &coldCache{dir: t.TempDir()}
	snapshot := &treeStoreSnapshot{root: root, nodes: scanner.Nodes(), files: int(files), dirs: int(dirs)}
	info := ScanJobInfo{Path: path, State: "completed", ScannedAt: "2030-01-01T00:00:00Z"}
	if err := cache.save(info, snapshot, cache.epoch(path)); err != nil {
		t.Fatal(err)
	}
	loaded, err := cache.load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range loaded.nodes {
		if node.ScanTime != scanner.scanTime {
			t.Fatal("saved scan date was lost or replaced by tile date")
		}
	}
	// A clean reused directory retains the date its data was enumerated.
	next := NewScanner(&profile, 1)
	next.scanTime = scanner.scanTime + 100
	cloned := next.cloneCachedSubtree(root, 0, -1, &files, &dirs)
	if cloned.ScanTime != scanner.scanTime || cloned.Children[0].ScanTime != scanner.scanTime {
		t.Fatal("cache reuse changed scan dates")
	}
}

func TestOlderSnapshotsUseSavedScanDate(t *testing.T) {
	cache := &coldCache{dir: t.TempDir()}
	path := t.TempDir()
	info := ScanJobInfo{Path: path, ScannedAt: "2026-10-26T12:00:00Z"}
	if err := cache.save(info, coldFixture(path), cache.epoch(path)); err != nil {
		t.Fatal(err)
	}
	loaded, err := cache.load(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := time.Parse(time.RFC3339, info.ScannedAt)
	for _, node := range loaded.nodes {
		if node != nil && node.ScanTime != want.Unix() {
			t.Fatal("legacy snapshot scan date was not restored")
		}
	}
}

func TestNarrowerScanPreservesUnchangedScanDates(t *testing.T) {
	path := t.TempDir()
	d := &Node{ID: 1, ParentID: 0, FullPath: filepath.Join(path, "D"), IsFolder: true, ScanTime: 100}
	sibling := &Node{ID: 2, ParentID: 0, FullPath: filepath.Join(path, "sibling"), Size: 10, ScanTime: 100}
	root := &Node{ID: 0, ParentID: -1, FullPath: path, IsFolder: true, Size: 10, ScanTime: 100, Children: []*Node{d, sibling}}
	previous := &treeStoreSnapshot{root: root, nodes: []*Node{root, d, sibling}, files: 1, dirs: 2}
	replacement := &Node{ID: 0, FullPath: d.FullPath, IsFolder: true, ScanTime: 200,
		Children: []*Node{{ID: 1, FullPath: filepath.Join(d.FullPath, "new"), ScanTime: 200}}}
	next, _ := mergeIncludedScan(previous, replacement)
	if next.nodes[1].ScanTime != 200 || next.nodes[2].ScanTime != 100 || next.nodes[3].ScanTime != 200 {
		t.Fatal("subtree scan dates were not preserved")
	}
}
