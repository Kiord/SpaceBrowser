package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNarrowerScanUpdatesSavedAncestor(t *testing.T) {
	for _, tc := range []struct {
		name             string
		enabled, restart bool
	}{
		{"loaded", true, false}, {"unloaded", true, true}, {"disabled", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := filepath.Join(t.TempDir(), "settings.json")
			a := newApp(settings)
			t.Cleanup(func() { a.Shutdown(nil) })
			a.scanCache.SetEnabled(false)
			a.profile.MinFileSize = 0
			a.profile.UpdateIncludedScans = tc.enabled
			c := filepath.Join(t.TempDir(), "A", "B", "C")
			d := filepath.Join(c, "D")
			if err := os.MkdirAll(d, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(d, "old"))
			write(filepath.Join(c, "sibling"))
			first, err := a.QueueScan(c)
			if err != nil {
				t.Fatal(err)
			}
			before := waitJob(t, a, first.ID, "completed")
			if _, err := a.SelectScanJob(first.ID); err != nil {
				t.Fatal(err)
			}
			original := a.store.sharedSnapshot()
			if tc.restart {
				a.coldCache.writes.Wait()
				a.Shutdown(nil)
				a = newApp(settings)
				a.scanCache.SetEnabled(false)
				a.profile.MinFileSize = 0
				first.ID = a.GetScanJobs()[0].ID
			}
			if err := os.Remove(filepath.Join(d, "old")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(d, "nested"), 0700); err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(d, "new"))
			write(filepath.Join(d, "nested", "new"))
			second, err := a.QueueScan(d)
			if err != nil {
				t.Fatal(err)
			}
			completed := waitJob(t, a, second.ID, "completed")
			view, err := a.SelectScanJob(first.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantFiles := 2
			if tc.enabled {
				wantFiles = 3
			}
			if view.Tree.FileCount != wantFiles {
				t.Fatalf("ancestor files=%d want %d", view.Tree.FileCount, wantFiles)
			}
			if tc.enabled && view.Job.ScannedAt != completed.ScannedAt {
				t.Fatal("ancestor timestamp was not updated")
			}
			if !tc.enabled && view.Job.ScannedAt != before.ScannedAt {
				t.Fatal("disabled update changed timestamp")
			}
			paths := map[string]bool{}
			for _, node := range a.store.nodes {
				if node != nil {
					paths[node.FullPath] = true
				}
			}
			if !paths[filepath.Join(c, "sibling")] || paths[filepath.Join(d, "new")] != tc.enabled || paths[filepath.Join(d, "old")] == tc.enabled {
				t.Fatalf("incorrect ancestor subtree: %v", paths)
			}
			if original.files != 2 {
				t.Fatal("previous snapshot was mutated")
			}
			a.coldCache.writes.Wait()
			saved, err := a.coldCache.load(c)
			if err != nil {
				t.Fatal(err)
			}
			if saved.files != wantFiles {
				t.Fatal("ancestor update was not persisted")
			}
			if tc.enabled {
				for _, h := range a.coldCache.headers() {
					if h.Info.Path == c && h.Info.ScannedAt != completed.ScannedAt {
						t.Fatal("saved ancestor timestamp is stale")
					}
				}
			}
		})
	}
}

func TestBroaderScanUpdatesIncludedFolder(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			a := newApp(filepath.Join(t.TempDir(), "settings.json"))
			t.Cleanup(func() { a.Shutdown(nil) })
			a.profile.MinFileSize = 0
			a.scanCache.SetEnabled(false)
			a.profile.UpdateIncludedScans = enabled
			root := t.TempDir()
			child := filepath.Join(root, "folder")
			if err := os.Mkdir(child, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(name, data string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(child, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("old", "old")
			first, err := a.QueueScan(child)
			if err != nil {
				t.Fatal(err)
			}
			waitJob(t, a, first.ID, "completed")
			before, err := a.SelectScanJob(first.ID)
			if err != nil {
				t.Fatal(err)
			}
			original := a.store.sharedSnapshot()
			// A changed folder must be reflected even when the original saved
			// snapshot is the foreground view during the broader scan.
			if err := os.Remove(filepath.Join(child, "old")); err != nil {
				t.Fatal(err)
			}
			write("new", "new contents")
			write("extra", "extra")
			broader, err := a.QueueScan(root)
			if err != nil {
				t.Fatal(err)
			}
			waitJob(t, a, broader.ID, "completed")
			if _, err := a.SelectScanJob(broader.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := a.SelectScanJob(first.ID); err != nil {
				t.Fatal(err)
			}
			view, err := a.GetScanJobView(first.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantFiles := 1
			if enabled {
				wantFiles = 2
			}
			if view.Tree.FileCount != wantFiles || a.store.root.FullPath != child || a.store.root.Depth != 0 || a.store.root.ParentID != -1 {
				t.Fatalf("included view: %+v root=%+v", view, a.store.root)
			}
			if enabled && (view.Key == before.Key || view.Job.ScannedAt == before.Job.ScannedAt || view.Job.Report != nil) {
				t.Fatalf("included metadata was not updated: %+v", view)
			}
			if original.files != 1 || original.root.Children[0].Name != "old" {
				t.Fatal("previous snapshot was mutated")
			}
			// Switching away and back must not restore the stale foreground tree.
			if _, err := a.SelectScanJob(broader.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := a.SelectScanJob(first.ID); err != nil {
				t.Fatal(err)
			}
			if a.store.fileCount != wantFiles {
				t.Fatal("navigation restored stale snapshot")
			}
			a.coldCache.writes.Wait()
			saved, err := a.coldCache.load(child)
			if err != nil {
				t.Fatal(err)
			}
			if saved.files != wantFiles {
				t.Fatalf("saved files = %d, want %d", saved.files, wantFiles)
			}
		})
	}
}

func TestBroaderScanUpdatesUnloadedSavedFolder(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	first := newApp(settings)
	first.scanCache.SetEnabled(false)
	job, err := first.QueueScan(child)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, first, job.ID, "completed")
	first.coldCache.writes.Wait()
	first.Shutdown(nil)
	if err := os.WriteFile(filepath.Join(child, "new"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	second := newApp(settings)
	t.Cleanup(func() { second.Shutdown(nil) })
	second.scanCache.SetEnabled(false)
	jobs := second.GetScanJobs()
	if len(jobs) != 1 || !jobs[0].Cached || second.jobs.jobs[0].snapshot != nil {
		t.Fatal("saved folder was not loaded lazily")
	}
	broader, err := second.QueueScan(root)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, second, broader.ID, "completed")
	view, err := second.SelectScanJob(jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Tree.FileCount != 1 || second.store.root.FullPath != child {
		t.Fatalf("unloaded folder was not updated: %+v", view)
	}
	second.coldCache.writes.Wait()
	saved, err := second.coldCache.load(child)
	if err != nil {
		t.Fatal(err)
	}
	if saved.files != 1 {
		t.Fatal("updated folder was not persisted")
	}
}

func TestIncludedScanOnlyUpdatesCompletedPresentDescendants(t *testing.T) {
	rootPath := t.TempDir()
	childPath := filepath.Join(rootPath, "child")
	child := &Node{ID: 1, FullPath: childPath, IsFolder: true, ParentID: 0, Depth: 1}
	root := &Node{ID: 0, FullPath: rootPath, IsFolder: true, Children: []*Node{child}}
	source := &scanJob{info: ScanJobInfo{ID: 1, Path: rootPath, State: "completed", ScannedAt: scanTimestamp()}, snapshot: &treeStoreSnapshot{root: root}}
	for _, tc := range []struct {
		name, path, state string
		update            bool
	}{
		{"included", childPath, "completed", true},
		{"unloaded saved folder", childPath, "completed", true},
		{"paused", childPath, "paused", false},
		{"failed", childPath, "failed", false},
		{"cancelled", childPath, "cancelled", false},
		{"same root", rootPath, "completed", false},
		{"missing or excluded", filepath.Join(rootPath, "missing"), "completed", false},
		{"path prefix sibling", rootPath + "-other", "completed", false},
		{"ancestor", filepath.Dir(rootPath), "completed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := &scanJob{info: ScanJobInfo{ID: 2, Path: tc.path, State: tc.state, Cached: true}}
			q := &scanJobQueue{jobs: []*scanJob{source, job}}
			updated := q.updateIncludedScansLocked(source)
			if (len(updated) == 1) != tc.update {
				t.Fatalf("updated = %d", len(updated))
			}
			if tc.update && (job.snapshot.root.FullPath != childPath || job.snapshot.dirs != 1) {
				t.Fatal("incorrect subtree snapshot")
			}
		})
	}
}

func TestIncludedScanSnapshotPreservesSmallFilesAndRemapsIDs(t *testing.T) {
	path := t.TempDir()
	small := &Node{ID: -1, IsSmallFiles: true, SmallFileCount: 3, Size: 6}
	file := &Node{ID: 7, FullPath: filepath.Join(path, "file"), Size: 12}
	root := &Node{ID: 5, FullPath: path, IsFolder: true, Size: 18, Children: []*Node{small, file}}
	oldRoot := &Node{ID: 0, FullPath: path}
	oldFile := &Node{ID: 1, FullPath: file.FullPath}
	removed := &Node{ID: 2, FullPath: filepath.Join(path, "removed")}
	snapshot, ids := includedScanSnapshot(root, &treeStoreSnapshot{nodes: []*Node{oldRoot, oldFile, removed}})
	if snapshot.files != 4 || snapshot.dirs != 1 || len(snapshot.nodes) != 3 || snapshot.nodes[2] != nil || ids[0] != 0 || ids[1] != 1 || ids[2] != -1 {
		t.Fatalf("snapshot=%+v ids=%v", snapshot, ids)
	}
	if snapshot.root.Children[0].SmallFileCount != 3 || snapshot.nodes[1].ParentID != 0 || snapshot.nodes[1].Depth != 1 {
		t.Fatal("lost small files or incorrect local IDs")
	}
	snapshot.root.Children[0].Size = 99
	if small.Size != 6 {
		t.Fatal("subtree aliases source")
	}
	// A later update must retain surviving IDs, including holes for removed paths.
	root.Children = append(root.Children, &Node{ID: 8, FullPath: filepath.Join(path, "added")})
	next, nextIDs := includedScanSnapshot(root, snapshot)
	if next.nodes[1].FullPath != file.FullPath || next.nodes[2] != nil || next.nodes[3].FullPath != filepath.Join(path, "added") || nextIDs[2] != -1 {
		t.Fatal("later update reused a removed ID or changed a surviving ID")
	}
}

func TestMergeIncludedScanPreservesVolumeAndSibling(t *testing.T) {
	path := t.TempDir()
	d := &Node{ID: 1, ParentID: 0, FullPath: filepath.Join(path, "D"), IsFolder: true, Size: 10, EntryFiles: 1, EntryDirs: 1}
	sibling := &Node{ID: 2, ParentID: 0, FullPath: filepath.Join(path, "sibling"), Size: 20}
	free := &Node{ID: -1, ParentID: 0, IsFreeSpace: true, Size: 100}
	root := &Node{ID: 0, ParentID: -1, FullPath: path, IsFolder: true, Size: 30, EntryFiles: 2, EntryDirs: 2, DiskTotal: 1000, Children: []*Node{free, sibling, d}}
	previous := &treeStoreSnapshot{root: root, nodes: []*Node{root, d, sibling}, files: 2, dirs: 2, hasDiskUsage: true, diskTotal: 1000, diskFree: 100}
	replacement := &Node{ID: 0, FullPath: d.FullPath, IsFolder: true, Size: 40, EntryFiles: 2, EntryDirs: 1,
		Children: []*Node{{ID: -1, IsSmallFiles: true, SmallFileCount: 2, Size: 40}}}
	next, ids := mergeIncludedScan(previous, replacement)
	if next == nil || next.files != 3 || next.dirs != 2 || next.root.Size != 60 || next.nodes[1].Size != 40 || ids[2] != 2 {
		t.Fatalf("incorrect merge: %+v %v", next, ids)
	}
	if !next.hasDiskUsage || next.diskTotal != 1000 || next.diskFree != 100 || !next.root.Children[0].IsFreeSpace {
		t.Fatal("volume free space was lost")
	}
	if next.nodes[2].Size != 20 || root.Size != 30 || d.Size != 10 {
		t.Fatal("sibling changed or old snapshot mutated")
	}
}
