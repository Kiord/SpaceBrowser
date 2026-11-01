package app

import (
	"os"
	"path/filepath"
	"spacebrowser/internal/platform"
	"testing"
)

type aliasScanFilesystem struct {
	*countingCacheFilesystem
	aliases    []platform.DirectoryAlias
	hidden     string
	unreadable bool
}

func (f *aliasScanFilesystem) DirectoryAliases() []platform.DirectoryAlias { return f.aliases }

func (f *aliasScanFilesystem) ReadDir(path string) ([]platform.DirectoryEntry, error) {
	if f.unreadable && (path == f.aliases[0].Preferred || path == f.aliases[0].Backing) {
		f.mu.Lock()
		f.reads[canonicalCachePath(path)]++
		f.mu.Unlock()
		return nil, os.ErrPermission
	}
	entries, err := f.countingCacheFilesystem.ReadDir(path)
	if f.hidden != "" {
		for index := range entries {
			entries[index].Hidden = f.IsHidden(filepath.Join(path, entries[index].Name()))
			entries[index].HasHidden = true
		}
	}
	return entries, err
}

func (f *aliasScanFilesystem) IsHidden(path string) bool {
	return canonicalCachePath(path) == canonicalCachePath(f.hidden)
}

func newAliasScanFilesystem(t *testing.T) (string, *aliasScanFilesystem) {
	t.Helper()
	root := t.TempDir()
	preferred := filepath.Join(root, "Users")
	backing := filepath.Join(root, "System", "Volumes", "Data", "Users")
	for _, directory := range []string{preferred, backing} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "content.txt"), []byte("test content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, &aliasScanFilesystem{
		countingCacheFilesystem: &countingCacheFilesystem{API: platform.Impl, reads: make(map[string]int)},
		aliases:                 []platform.DirectoryAlias{{Preferred: preferred, Backing: backing}},
	}
}

func scanAliasTree(t *testing.T, root string, f *aliasScanFilesystem, profile *Profile, cache map[string]*Node) (*Node, *Scanner, int64, int64) {
	t.Helper()
	scanner := NewScannerWithFilesystem(profile, 8, f)
	scanner.SetIncrementalCache(cache, nil)
	var files, dirs int64
	tree, err := scanner.buildTree(root, 0, -1, &files, &dirs)
	if err != nil {
		t.Fatal(err)
	}
	return tree, scanner, files, dirs
}

func TestScannerVisitsFirmlinkDirectoryOnce(t *testing.T) {
	for _, followLinks := range []bool{false, true} {
		t.Run(map[bool]string{false: "without_symlinks", true: "with_symlinks"}[followLinks], func(t *testing.T) {
			root, f := newAliasScanFilesystem(t)
			data := filepath.Dir(f.aliases[0].Backing)
			unique := filepath.Join(data, "data-only")
			if err := os.Mkdir(unique, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(unique, "unique.txt"), []byte("unique"), 0o600); err != nil {
				t.Fatal(err)
			}
			profile := defaultProfile()
			profile.MinFileSize = 0
			profile.FollowSymlinks = followLinks
			tree, scanner, files, dirs := scanAliasTree(t, root, f, profile, nil)
			if files != 2 || dirs != 6 {
				t.Fatalf("scan counts = %d files / %d directories, want 2 / 6", files, dirs)
			}
			if f.readCount(f.aliases[0].Preferred) != 1 || f.readCount(f.aliases[0].Backing) != 0 || f.readCount(unique) != 1 {
				t.Fatalf("directory reads = %+v", f.reads)
			}
			if report := scanner.Report(); report.Skipped[scanSkipRepeatedDirectory] != 1 {
				t.Fatalf("scan report = %+v", report)
			}
			if !tree.HasDirectoryAliases || !indexCachedDirectories(tree)[canonicalCachePath(data)].HasDirectoryAliases {
				t.Fatal("omitted directory aliases were not propagated to ancestors")
			}
		})
	}
}

func TestScannerCanScanBackingVolumeDirectly(t *testing.T) {
	for _, scanAliasRoot := range []bool{false, true} {
		root, f := newAliasScanFilesystem(t)
		root = filepath.Dir(f.aliases[0].Backing)
		if scanAliasRoot {
			root = f.aliases[0].Backing
		}
		profile := defaultProfile()
		_, scanner, files, _ := scanAliasTree(t, root, f, profile, nil)
		if files != 1 || f.readCount(f.aliases[0].Backing) != 1 || scanner.Report().Skipped[scanSkipRepeatedDirectory] != 0 {
			t.Fatalf("direct backing scan lost contents: files=%d reads=%+v report=%+v", files, f.reads, scanner.Report())
		}
	}
}

func TestScannerReportsFirmlinkPermissionFailureOnce(t *testing.T) {
	root, f := newAliasScanFilesystem(t)
	f.unreadable = true
	_, scanner, _, _ := scanAliasTree(t, root, f, defaultProfile(), nil)
	report := scanner.Report()
	if report.Errors[scanErrorReadDirectory] != 1 || report.TotalErrors() != 1 || report.Skipped[scanSkipRepeatedDirectory] != 1 {
		t.Fatalf("permission failure was duplicated: %+v", report)
	}
	if len(report.Examples) != 1 || report.Examples[0].Path != f.aliases[0].Preferred || f.readCount(f.aliases[0].Backing) != 0 {
		t.Fatalf("failure was not attributed to the visible path: report=%+v reads=%+v", report, f.reads)
	}
}

func TestScannerRetainsBackingDirectoryWhenPreferredPathIsFiltered(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		root, f := newAliasScanFilesystem(t)
		profile := defaultProfile()
		if hidden {
			profile.SkipHidden = true
			f.hidden = f.aliases[0].Preferred
		} else {
			profile.ExcludedPaths = []string{f.aliases[0].Preferred}
		}
		_, scanner, files, _ := scanAliasTree(t, root, f, profile, nil)
		if files != 1 || f.readCount(f.aliases[0].Backing) != 1 || scanner.Report().Skipped[scanSkipRepeatedDirectory] != 0 {
			t.Fatalf("filtered preferred path lost backing contents: files=%d reads=%+v report=%+v", files, f.reads, scanner.Report())
		}
	}
}

func TestScannerDoesNotCloneBackingAliasesFromChildCache(t *testing.T) {
	root, f := newAliasScanFilesystem(t)
	profile := defaultProfile()
	backingRoot := filepath.Dir(f.aliases[0].Backing)
	cached, _, _, _ := scanAliasTree(t, backingRoot, f, profile, nil)
	_, scanner, files, _ := scanAliasTree(t, root, f, profile, indexCachedDirectories(cached))
	if files != 1 || f.readCount(backingRoot) != 2 || f.readCount(f.aliases[0].Backing) != 1 || scanner.Report().Skipped[scanSkipRepeatedDirectory] != 1 {
		t.Fatalf("broader scan reused backing aliases: files=%d reads=%+v report=%+v", files, f.reads, scanner.Report())
	}
}

func TestCacheWithOmittedDirectoryAliasesCannotSupplyChildScans(t *testing.T) {
	root, f := newAliasScanFilesystem(t)
	profile := *defaultProfile()
	tree, scanner, _, _ := scanAliasTree(t, root, f, &profile, nil)
	manager := newScanCacheManager(nil)
	_, key := scanProfileCacheKey(profile)
	entry := &scanCacheEntry{
		rootPath: root, profileKey: key, root: tree, directories: indexCachedDirectories(tree),
		report: scanner.Report(), dirty: make(map[string]struct{}),
	}
	manager.entries[scanMemoryCacheKey(root, key)] = entry
	if cacheEntryCrossRootSafe(entry, profile) {
		t.Fatal("a tree with omitted backing aliases was accepted for cross-root reuse")
	}
	plan := manager.Prepare(filepath.Dir(f.aliases[0].Backing), profile)
	if len(plan.directories) != 0 {
		t.Fatal("narrower backing scan reused a broader tree with missing aliases")
	}
}

func TestFolderRefreshPreservesDirectoryAliasScope(t *testing.T) {
	root, f := newAliasScanFilesystem(t)
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	t.Cleanup(func() { a.Shutdown(nil) })
	a.filesystem = f
	a.scanCache.SetEnabled(false)
	if _, err := a.GetFullTree(root); err != nil {
		t.Fatal(err)
	}
	data := filepath.Dir(f.aliases[0].Backing)
	target := indexCachedDirectories(a.store.root)[canonicalCachePath(data)]
	result, err := a.RefreshFolders([]FolderRefreshTarget{{NodeID: target.ID, Path: data}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 1 || f.readCount(f.aliases[0].Preferred) != 2 || f.readCount(f.aliases[0].Backing) != 0 {
		t.Fatalf("refresh restored backing duplicates: files=%d reads=%+v", result.FileCount, f.reads)
	}
}

func TestIncludedScansKeepIndependentDirectoryAliasScopes(t *testing.T) {
	root, f := newAliasScanFilesystem(t)
	profile := defaultProfile()
	broad, _, _, _ := scanAliasTree(t, root, f, profile, nil)
	data := filepath.Dir(f.aliases[0].Backing)
	narrow, _, _, _ := scanAliasTree(t, data, f, profile, nil)
	broadSnapshot, _ := includedScanSnapshot(broad, nil)
	narrowSnapshot, _ := includedScanSnapshot(narrow, nil)
	if merged, _ := mergeIncludedScan(broadSnapshot, narrow); merged != nil {
		t.Fatal("standalone backing scan replaced a subtree that omits aliases")
	}
	if merged, _ := mergeIncludedScan(narrowSnapshot, indexCachedDirectories(broad)[canonicalCachePath(data)]); merged != nil {
		t.Fatal("subtree with omitted aliases replaced a standalone backing scan")
	}
	parent := &scanJob{info: ScanJobInfo{ID: 1, Path: root, State: "completed"}, snapshot: broadSnapshot}
	child := &scanJob{info: ScanJobInfo{ID: 2, Path: data, State: "completed"}, snapshot: narrowSnapshot}
	queue := &scanJobQueue{jobs: []*scanJob{parent, child}}
	if updated := queue.updateIncludedScansLocked(parent); len(updated) != 0 || child.snapshot != narrowSnapshot {
		t.Fatal("broader scan removed aliases from a saved standalone backing scan")
	}
	if updated := queue.updateIncludedScansLocked(child); len(updated) != 0 || parent.snapshot != broadSnapshot {
		t.Fatal("standalone backing scan restored aliases in a saved broader scan")
	}
}
