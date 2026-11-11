package app

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spacebrowser/internal/platform"
)

type pathSafetyDesktop struct {
	platform.API
	calls []string
}

func (d *pathSafetyDesktop) DeletePermanently(path string) error {
	d.calls = append(d.calls, path)
	return nil
}

func TestRelativeScanAndExclusionSurviveWorkingDirectoryChange(t *testing.T) {
	base := t.TempDir()
	firstDir, secondDir := filepath.Join(base, "first"), filepath.Join(base, "second")
	for _, dir := range []string{firstDir, secondDir} {
		if err := os.MkdirAll(filepath.Join(dir, "data", "excluded"), 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"item.bin", filepath.Join("excluded", "hidden.bin")} {
			if err := os.WriteFile(filepath.Join(dir, "data", name), []byte(dir), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Chdir(firstDir)
	config := filepath.Join(base, "settings.json")
	a := newApp(config)
	t.Cleanup(func() { a.Shutdown(nil) })
	profile := a.GetProfile()
	profile.ExcludedPaths = []string{filepath.Join("data", "excluded")}
	profile.MinFileSize, profile.UseMFT = 0, false
	profile.AllowDelete, profile.AllowPermanentDelete = true, true
	if err := a.SetProfile(profile); err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(firstDir, "data")
	if got := a.GetProfile().ExcludedPaths[0]; got != filepath.Join(wantRoot, "excluded") {
		t.Fatalf("exclusion = %q, want absolute exclusion under %q", got, wantRoot)
	}
	job, err := a.QueueScan("data")
	if err != nil {
		t.Fatal(err)
	}
	if job.Path != wantRoot {
		t.Fatalf("queued path = %q, want %q", job.Path, wantRoot)
	}
	waitJob(t, a, job.ID, "completed")
	a.coldCache.writes.Wait()
	if err := os.Chdir(secondDir); err != nil {
		t.Fatal(err)
	}
	restarted := newApp(config)
	t.Cleanup(func() { restarted.Shutdown(nil) })
	if got := restarted.GetProfile().ExcludedPaths[0]; got != filepath.Join(wantRoot, "excluded") {
		t.Fatalf("restart reinterpreted exclusion: %q", got)
	}
	jobs := restarted.GetScanJobs()
	if len(jobs) != 1 || jobs[0].Path != wantRoot {
		t.Fatalf("saved jobs after cwd change: %+v", jobs)
	}
	if _, err := restarted.SelectScanJob(jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	snapshot := restarted.store.snapshotState()
	var target DeletionTarget
	for _, node := range snapshot.nodes {
		if node == nil {
			continue
		}
		if !filepath.IsAbs(node.FullPath) || strings.Contains(node.FullPath, "excluded") {
			t.Fatalf("unexpected saved node: %+v", node)
		}
		if node.Name == "item.bin" {
			target = DeletionTarget{node.ID, node.FullPath, "permanent"}
		}
	}
	if target.Path != filepath.Join(wantRoot, "item.bin") {
		t.Fatalf("wrong deletion target: %+v", target)
	}
	desktop := &pathSafetyDesktop{API: platform.Impl}
	restarted.desktop = desktop
	result, err := restarted.DeleteNodes([]DeletionTarget{target})
	if err != nil || len(result.Deleted) != 1 || len(desktop.calls) != 1 || desktop.calls[0] != target.Path {
		t.Fatalf("deletion after restart: %+v, %v, calls %v", result, err, desktop.calls)
	}
	if data, err := os.ReadFile(filepath.Join(secondDir, "data", "item.bin")); err != nil || string(data) != secondDir {
		t.Fatalf("same-named file in new working directory changed: %q, %v", data, err)
	}
}

// Encode version-1 snapshots directly to exercise the pre-fix on-disk format.
func writePathSafetySnapshot(t *testing.T, cache *coldCache, infoPath, jobPath, rootPath, childPath string) {
	t.Helper()
	if err := os.MkdirAll(cache.dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(cache.filename(infoPath))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	encoder := gob.NewEncoder(f)
	header := coldHeader{Version: 1, Info: CachedScanInfo{Path: infoPath}, Job: ScanJobInfo{Path: jobPath}, RootID: 0, NodeCount: 2, RecordCount: 2, Files: 1, Dirs: 1}
	for _, record := range []any{header,
		coldNode{Node: Node{ID: 0, ParentID: -1, FullPath: rootPath, IsFolder: true}, ChildCount: 1},
		coldNode{Node: Node{ID: 1, ParentID: 0, FullPath: childPath}},
	} {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
}

func TestColdCacheRejectsRelativePathsInLegacySnapshots(t *testing.T) {
	base := t.TempDir()
	absolute := filepath.Join(base, "data")
	for _, field := range []string{"header", "job", "root", "child"} {
		t.Run(field, func(t *testing.T) {
			cache := &coldCache{dir: t.TempDir()}
			info, job, root, child := absolute, absolute, absolute, filepath.Join(absolute, "file")
			switch field {
			case "header":
				info, job, root, child = "data", "data", "data", filepath.Join("data", "file")
			case "job":
				job = "data"
			case "root":
				root = "data"
			case "child":
				child = filepath.Join("data", "file")
			}
			writePathSafetySnapshot(t, cache, info, job, root, child)
			t.Chdir(t.TempDir())
			if _, err := cache.load(info); err == nil || !strings.Contains(err.Error(), "rescan") {
				t.Fatalf("legacy relative %s accepted: %v", field, err)
			}
			if (field == "header" || field == "job") && len(cache.headers()) != 0 {
				t.Fatal("relative legacy snapshot was offered as a saved location")
			}
		})
	}
}

func TestRelativeStoredPathsCannotReachDeletionOrRestore(t *testing.T) {
	for _, action := range []string{"trash", "permanent", "empty", "trash content", "restore"} {
		t.Run(action, func(t *testing.T) {
			a := newApp(filepath.Join(t.TempDir(), "settings.json"))
			t.Cleanup(func() { a.Shutdown(nil) })
			a.profile.AllowDelete = true
			a.profile.AllowPermanentDelete = action == "permanent"
			path := filepath.Join("data", "item.bin")
			root := &Node{ID: 0, ParentID: -1, FullPath: "data", IsFolder: true}
			child := &Node{ID: 1, ParentID: 0, FullPath: path, IsFolder: action == "empty"}
			root.Children = []*Node{child}
			a.store.Replace(root, []*Node{root, child}, 1, 1)
			// A nil desktop makes any native lookup or mutation fail the test.
			a.desktop = nil
			var err error
			if action == "restore" {
				_, err = a.RestoreNode(TrashRestoreDetails{NodeID: 1, Path: path, TreeGeneration: fmt.Sprint(a.store.generation)})
			} else {
				_, err = a.DeleteNode(1)
				if err == nil {
					t.Fatal("single deletion accepted a relative target")
				}
				_, err = a.DeleteNodes([]DeletionTarget{{1, path, action}})
			}
			if err == nil || !strings.Contains(err.Error(), "absolute path") {
				t.Fatalf("action error = %v", err)
			}
		})
	}
}

func TestTreeStoreRejectsRelativeMutationTargets(t *testing.T) {
	root := &Node{ID: 0, ParentID: -1, FullPath: "data", IsFolder: true}
	child := &Node{ID: 1, ParentID: 0, FullPath: filepath.Join("data", "item"), IsFolder: true}
	root.Children = []*Node{child}
	store := &TreeStore{}
	store.Replace(root, []*Node{root, child}, 0, 2)
	for name, action := range map[string]func() (DeleteResult, error){
		"recycle":     func() (DeleteResult, error) { return store.DeleteNode(1, nil, nil, nil) },
		"permanent":   func() (DeleteResult, error) { return store.DeleteNodePermanently(1, nil, nil, nil) },
		"empty Trash": func() (DeleteResult, error) { return store.EmptyTrashNode(1, nil, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := action(); err == nil || !strings.Contains(err.Error(), "absolute path") {
				t.Fatalf("relative mutation target accepted: %v", err)
			}
		})
	}
}

func TestRestoreRejectsRelativeDestination(t *testing.T) {
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	t.Cleanup(func() { a.Shutdown(nil) })
	root := &Node{ID: 0, ParentID: -1, FullPath: t.TempDir(), IsFolder: true}
	child := &Node{ID: 1, ParentID: 0, FullPath: filepath.Join(root.FullPath, "file")}
	root.Children = []*Node{child}
	a.store.Replace(root, []*Node{root, child}, 1, 1)
	var restored string
	a.desktop = trashActionDesktop{path: root.FullPath, originalPath: "relative-destination", restored: &restored}
	details, err := a.GetTrashRestoreInfo(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RestoreNode(details); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative restore destination accepted: %v", err)
	}
	if restored != "" {
		t.Fatalf("native restore called with %q", restored)
	}
}

func TestColdCacheRefusesToSaveRelativeFilesystemPaths(t *testing.T) {
	for _, field := range []string{"header", "root", "child"} {
		t.Run(field, func(t *testing.T) {
			path := t.TempDir()
			cache := &coldCache{dir: t.TempDir()}
			snapshot := coldFixture(path)
			info := ScanJobInfo{Path: path}
			switch field {
			case "header":
				info.Path = "data"
			case "root":
				snapshot.root.FullPath = "data"
			case "child":
				snapshot.nodes[2].FullPath = filepath.Join("data", "file")
			}
			if err := cache.save(info, snapshot, cache.epoch(path)); err == nil || !strings.Contains(err.Error(), "rescan") {
				t.Fatalf("saved relative %s: %v", field, err)
			}
			if len(cache.headers()) != 0 {
				t.Fatal("invalid snapshot was published")
			}
		})
	}
}
