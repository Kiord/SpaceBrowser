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
	// Revalidating a clean cached directory counts as refreshing its scan date.
	next := NewScanner(&profile, 1)
	next.scanTime = scanner.scanTime + 100
	cloned := next.cloneCachedSubtree(root, 0, -1, &files, &dirs)
	if cloned.ScanTime != next.scanTime || cloned.Children[0].ScanTime != next.scanTime {
		t.Fatal("cache reuse did not refresh scan dates")
	}
	if root.ScanTime != scanner.scanTime {
		t.Fatal("cache reuse mutated the previous snapshot")
	}
}

func TestCompleteCacheReuseRefreshesScanDates(t *testing.T) {
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	t.Cleanup(func() { a.Shutdown(nil) })
	path := t.TempDir()
	root := &Node{ID: 0, ParentID: -1, FullPath: path, IsFolder: true, ScanTime: 100,
		Children: []*Node{{ID: 1, ParentID: 0, FullPath: filepath.Join(path, "file"), Size: 10, ScanTime: 100}}}
	nodes := []*Node{root, root.Children[0]}
	_, key := scanProfileCacheKey(a.GetProfile())
	a.scanCache.entries[scanMemoryCacheKey(path, key)] = &scanCacheEntry{rootPath: path, profileKey: key, root: root, nodes: nodes,
		fileCount: 1, dirCount: 1, directories: indexCachedDirectories(root)}
	started := time.Now().Unix()
	if _, err := a.GetFullTree(path); err != nil {
		t.Fatal(err)
	}
	snapshot := a.store.sharedSnapshot()
	if snapshot.root.ScanTime < started || snapshot.nodes[1].ScanTime < started {
		t.Fatal("complete cache reuse kept stale scan dates")
	}
	if root.ScanTime != 100 || nodes[1].ScanTime != 100 {
		t.Fatal("previous cache tree was mutated")
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

func TestFolderRefreshUpdatesOverlappingScanDates(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			a := newApp(filepath.Join(t.TempDir(), "settings.json"))
			t.Cleanup(func() { a.Shutdown(nil) })
			a.profile.MinFileSize = 0
			a.profile.UpdateIncludedScans = enabled
			a.scanCache.SetEnabled(false)
			c := t.TempDir()
			d, e := filepath.Join(c, "D"), filepath.Join(c, "E")
			for _, path := range []string{d, e} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "old"), []byte("data"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			queue := func(path string) *ScanJobInfo {
				t.Helper()
				job, err := a.QueueScan(path)
				if err != nil {
					t.Fatal(err)
				}
				waitJob(t, a, job.ID, "completed")
				return job
			}
			parent, child, sibling := queue(c), queue(d), queue(e)
			a.coldCache.writes.Wait()
			q := a.jobQueue()
			q.mu.Lock()
			for _, job := range q.jobs {
				snapshot := *job.snapshot
				snapshot.root, snapshot.nodes = cloneTreePreservingIDs(snapshot.root, len(snapshot.nodes))
				date := int64(100)
				if job.info.ID == sibling.ID {
					date = 999
				}
				setScanDate(snapshot.root, date)
				job.snapshot = &snapshot
			}
			q.mu.Unlock()
			if _, err := a.SelectScanJob(parent.ID); err != nil {
				t.Fatal(err)
			}
			var target FolderRefreshTarget
			for _, node := range a.store.nodes {
				if node != nil && node.FullPath == d {
					target = FolderRefreshTarget{NodeID: node.ID, Path: d}
				}
			}
			if err := os.Remove(filepath.Join(d, "old")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, "new"), []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
			started := time.Now().Unix()
			refresh, err := a.QueueFolderRefresh([]FolderRefreshTarget{target}, nil)
			if err != nil {
				t.Fatal(err)
			}
			completed := waitJob(t, a, refresh.ID, "completed")
			for _, id := range []uint64{parent.ID, child.ID} {
				view, err := a.SelectScanJob(id)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, rect := range mustScanDateLayout(t, a) {
					if rect.FullPath == filepath.Join(d, map[bool]string{true: "new", false: "old"}[enabled]) {
						found = true
						if enabled && rect.ScanTime < started {
							t.Fatal("overlapping refresh left tooltip date stale")
						}
						if !enabled && rect.ScanTime != 100 {
							t.Fatal("disabled overlap update changed date")
						}
					}
				}
				if !found {
					t.Fatal("overlapping scan kept the old subtree")
				}
				if enabled && view.Job.ScannedAt != completed.ScannedAt {
					t.Fatal("tile date did not follow refresh")
				}
			}
			if _, err := a.SelectScanJob(sibling.ID); err != nil {
				t.Fatal(err)
			}
			if a.store.root.ScanTime != 999 {
				t.Fatal("unrefreshed sibling scan was overwritten")
			}
			a.coldCache.writes.Wait()
			if enabled {
				saved, err := a.coldCache.load(d)
				if err != nil {
					t.Fatal(err)
				}
				if saved.root.ScanTime < started {
					t.Fatal("saved overlapping date is stale")
				}
			}
		})
	}
}

func mustScanDateLayout(t *testing.T, a *App) []Rect {
	t.Helper()
	rects, err := a.Layout(a.store.root.ID, 1000, 1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	return rects
}
