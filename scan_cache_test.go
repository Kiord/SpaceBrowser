package main

import (
	"os"
	"path/filepath"
	"spacebrowser/internal/platform"
	"sync"
	"testing"
)

type countingCacheFilesystem struct {
	platform.API
	mu    sync.Mutex
	reads map[string]int
}

func (filesystem *countingCacheFilesystem) ReadDir(path string) ([]platform.DirectoryEntry, error) {
	filesystem.mu.Lock()
	filesystem.reads[canonicalCachePath(path)]++
	filesystem.mu.Unlock()
	return filesystem.API.ReadDir(path)
}

func (filesystem *countingCacheFilesystem) readCount(path string) int {
	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	return filesystem.reads[canonicalCachePath(path)]
}

func scanTestTree(t *testing.T, root string, filesystem platform.ScannerFilesystem) (*Node, []*Node, int64, int64) {
	t.Helper()
	profile := *defaultProfile()
	profile.MinFileSize = 0
	scanner := NewScannerWithFilesystem(&profile, 1, filesystem)
	var files, dirs int64
	tree, err := scanner.buildTree(root, 0, -1, &files, &dirs)
	if err != nil {
		t.Fatal(err)
	}
	return tree, scanner.Nodes(), files, dirs
}

func TestScannerIncrementalCacheReusesOnlyCleanSubtrees(t *testing.T) {
	root := t.TempDir()
	dirtyDirectory := filepath.Join(root, "dirty")
	cleanDirectory := filepath.Join(root, "clean")
	for _, directory := range []string{dirtyDirectory, cleanDirectory} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "file.bin"), []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cachedRoot, _, expectedFiles, expectedDirs := scanTestTree(t, root, platform.Impl)

	filesystem := &countingCacheFilesystem{API: platform.Impl, reads: make(map[string]int)}
	profile := *defaultProfile()
	profile.MinFileSize = 0
	scanner := NewScannerWithFilesystem(&profile, 1, filesystem)
	scanner.SetIncrementalCache(indexCachedDirectories(cachedRoot), []string{dirtyDirectory})
	var files, dirs int64
	rescanned, err := scanner.buildTree(root, 0, -1, &files, &dirs)
	if err != nil {
		t.Fatal(err)
	}
	if rescanned == nil || files != expectedFiles || dirs != expectedDirs {
		t.Fatalf("incremental result = tree:%v files:%d dirs:%d, want files:%d dirs:%d", rescanned != nil, files, dirs, expectedFiles, expectedDirs)
	}
	if filesystem.readCount(root) == 0 || filesystem.readCount(dirtyDirectory) == 0 {
		t.Fatalf("read counts root=%d dirty=%d", filesystem.readCount(root), filesystem.readCount(dirtyDirectory))
	}
	if filesystem.readCount(cleanDirectory) != 0 {
		t.Fatalf("clean subtree was read %d times", filesystem.readCount(cleanDirectory))
	}
	if scanner.ReusedDirectories() == 0 {
		t.Fatal("no cached directory was reported as reused")
	}
}

func TestPersistedScanSnapshotRoundTrip(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file.bin"), []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, _, files, dirs := scanTestTree(t, rootPath, platform.Impl)
	profile := *defaultProfile()
	profile.MinFileSize = 0
	manager := newScanCacheManager(filepath.Join(t.TempDir(), "SpaceBrowser", "settings.json"), nil)
	report := ScanReportSnapshot{}
	if err := manager.SaveSnapshot(rootPath, profile, root, int(files), int(dirs), report); err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.LoadSnapshot(rootPath, profile)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.root == nil || canonicalCachePath(loaded.root.FullPath) != canonicalCachePath(rootPath) {
		t.Fatalf("loaded root = %#v", loaded.root)
	}
	if loaded.files != int(files) || loaded.dirs != int(dirs) || len(loaded.nodes) == 0 {
		t.Fatalf("loaded snapshot files=%d dirs=%d nodes=%d", loaded.files, loaded.dirs, len(loaded.nodes))
	}
}

func TestPersistedScanSnapshotRejectsPathsOutsideRoot(t *testing.T) {
	rootPath := t.TempDir()
	root := &Node{
		ID: 0, ParentID: -1, FullPath: rootPath, IsFolder: true, EntryDirs: 1,
		Children: []*Node{{ID: 1, ParentID: 0, FullPath: filepath.Join(filepath.Dir(rootPath), "outside.bin"), Size: 1}},
	}
	profile := *defaultProfile()
	manager := newScanCacheManager(filepath.Join(t.TempDir(), "SpaceBrowser", "settings.json"), nil)
	if err := manager.SaveSnapshot(rootPath, profile, root, 1, 1, ScanReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LoadSnapshot(rootPath, profile); err == nil {
		t.Fatal("snapshot containing an out-of-root path was accepted")
	}
}

func TestScanCacheProfileKeyIgnoresAppearanceButIncludesScanSettings(t *testing.T) {
	base := *defaultProfile()
	_, original := scanProfileCacheKey(base)
	base.Appearance.CornerRadius++
	_, appearanceChanged := scanProfileCacheKey(base)
	if appearanceChanged != original {
		t.Fatal("appearance unexpectedly invalidated the scan cache")
	}
	base.SkipHidden = !base.SkipHidden
	_, scanChanged := scanProfileCacheKey(base)
	if scanChanged == original {
		t.Fatal("scan settings did not invalidate the scan cache")
	}
}

func TestScanCacheWatcherChangeMarksAffectedPathDirty(t *testing.T) {
	root := t.TempDir()
	profile := *defaultProfile()
	_, profileKey := scanProfileCacheKey(profile)
	entry := &scanCacheEntry{
		rootPath: root, profileKey: profileKey,
		root:        &Node{ID: 0, ParentID: -1, FullPath: root, IsFolder: true},
		directories: map[string]*Node{canonicalCachePath(root): {ID: 0, FullPath: root, IsFolder: true}},
		dirty:       make(map[string]struct{}),
	}
	manager := &scanCacheManager{entry: entry}
	changed := filepath.Join(root, "folder", "file.bin")
	manager.markChanged(entry, changed)
	plan := manager.Prepare(root, profile)
	if len(plan.dirty) == 0 || manager.StillClean(entry, 0) {
		t.Fatalf("watcher change did not dirty cache: %+v", plan.dirty)
	}
}
