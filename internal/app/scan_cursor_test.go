package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"spacebrowser/internal/platform"
)

type cursorTestInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (f cursorTestInfo) Name() string       { return f.name }
func (f cursorTestInfo) Size() int64        { return f.size }
func (f cursorTestInfo) Mode() os.FileMode  { return f.mode }
func (f cursorTestInfo) ModTime() time.Time { return time.Unix(123, 0) }
func (f cursorTestInfo) IsDir() bool        { return f.mode.IsDir() }
func (f cursorTestInfo) Sys() any           { return nil }

type cursorTestEntry struct{ cursorTestInfo }

func (e cursorTestEntry) Type() os.FileMode          { return e.mode.Type() }
func (e cursorTestEntry) Info() (os.FileInfo, error) { return e.cursorTestInfo, nil }

type cursorTestDirectory struct {
	path    string
	entries []platform.DirectoryEntry
	calls   atomic.Int32
}

func (d *cursorTestDirectory) ReadScanDirectory(path string, timing bool) ([]platform.DirectoryEntry, *platform.DirectoryReadDiagnostic, *platform.DirectoryReadTimings, error) {
	d.calls.Add(1)
	if path != d.path {
		return nil, nil, nil, fmt.Errorf("wrong cursor path: %s", path)
	}
	var profile *platform.DirectoryReadTimings
	if timing {
		profile = &platform.DirectoryReadTimings{Query: platform.DirectoryReadMetric{Calls: 1, Duration: time.Millisecond}, ExtendedAttempts: 1}
	}
	return d.entries, nil, profile, nil
}

type cursorTestFilesystem struct {
	platform.Default
	root *cursorTestDirectory
}

func (f cursorTestFilesystem) RootScanDirectory(path string) platform.ScanDirectory {
	if f.Canonicalize(path) == f.root.path {
		return f.root
	}
	return nil
}
func (f cursorTestFilesystem) ReadDir(string) ([]platform.DirectoryEntry, error) {
	return nil, fmt.Errorf("indexed traversal unexpectedly used path enumeration")
}

func TestIndexedDirectoryCursorsPreserveScannerPolicy(t *testing.T) {
	path := t.TempDir()
	entry := func(name string, logical, allocated int64, mode os.FileMode, id uint64) platform.DirectoryEntry {
		return platform.DirectoryEntry{DirEntry: cursorTestEntry{cursorTestInfo{name, logical, mode}}, Usage: platform.FileUsage{Identity: platform.FileIdentity{Volume: 1, Low: id}, HasIdentity: true, HasLinkCount: true, LinkCount: 2, AllocatedSize: allocated}, HasUsage: true, HasHidden: true}
	}
	child := &cursorTestDirectory{path: filepath.Join(path, "folder"), entries: []platform.DirectoryEntry{entry("alias", 5000, 4096, 0666, 30), entry("small", 3, 128, 0666, 31)}}
	folder := entry("folder", 0, 0, os.ModeDir|0755, 20)
	folder.Directory = child
	hidden := entry("hidden", 5000, 8192, 0666, 32)
	hidden.Hidden = true
	root := &cursorTestDirectory{path: path, entries: []platform.DirectoryEntry{folder, entry("first", 5000, 4096, 0666, 30), hidden, entry("excluded", 5000, 8192, 0666, 33), entry("symlink", 0, 0, os.ModeSymlink, 34)}}
	profile := defaultProfile()
	profile.MinFileSize = 1024
	profile.SkipHidden = true
	profile.ExcludedPaths = []string{filepath.Join(path, "excluded")}
	profile.ScanTiming = true
	scanner := NewScannerWithFilesystem(profile, 4, cursorTestFilesystem{root: root})
	var files, dirs int64
	tree, err := scanner.buildTree(path, 0, -1, &files, &dirs)
	if err != nil {
		t.Fatal(err)
	}
	report := scanner.Report()
	if files != 3 || dirs != 2 || tree.Size != 4224 || report.TotalErrors() != 0 || root.calls.Load() != 1 || child.calls.Load() != 1 {
		t.Fatalf("cursor traversal changed accounting: %d %d %+v %+v", files, dirs, tree, report)
	}
	for _, reason := range []scanSkipReason{scanSkipHidden, scanSkipExcluded, scanSkipSymlink, scanSkipDuplicateIdentity} {
		if report.Skipped[reason] != 1 {
			t.Fatalf("policy changed for %d: %+v", reason, report)
		}
	}
	for _, node := range tree.Children {
		if node.Name == "folder" {
			if len(node.Children) != 1 || !node.Children[0].IsSmallFiles || node.Children[0].SmallFileCount != 1 || node.Children[0].Size != 128 {
				t.Fatalf("small-file aggregate changed: %+v", node)
			}
		}
	}
	if scanner.timing.stages[scanTimingDirectoryQuery].calls.Load() != 2 {
		t.Fatal("cursor native timings were not aggregated")
	}
}

func TestNTFSCacheRevisionAndCachedSourceIsolation(t *testing.T) {
	profile := defaultProfile()
	profile.UseMFT = true
	key, current := scanProfileCacheKey(*profile)
	legacy := key
	legacy.ReaderVersion = 0
	data, _ := json.Marshal(legacy)
	old := fmt.Sprintf("%x", sha256.Sum256(data))
	if runtime.GOOS == "windows" && (key.ReaderVersion != ntfsReaderVersion || current == old) {
		t.Fatal("affected NTFS cache keys remain reusable")
	}
	manager := newScanCacheManager(nil)
	if runtime.GOOS == "windows" {
		path := t.TempDir()
		manager.entries[scanMemoryCacheKey(path, old)] = &scanCacheEntry{rootPath: path, profileKey: old}
		if plan := manager.Prepare(path, *profile); plan.source != nil || len(plan.directories) != 0 {
			t.Fatal("legacy reader cache accelerated a new scan")
		}
	}
	for _, source := range []string{scanMethodNTFS, scanMethodNTFSDirect} {
		entry := &scanCacheEntry{report: ScanReportSnapshot{Method: &ScanMethodInfo{Name: "Complete-tree cache reuse", SourceName: source, ReaderVersion: ntfsReaderVersion}}}
		if cacheEntryIncrementallySafe(entry, *profile) || cacheEntryCrossRootSafe(entry, *profile) {
			t.Fatal("cache reuse lost indexed reader provenance")
		}
		details := testScanReportDetails(time.Now())
		details.Report = entry.report
		text := formatScanReport(details, time.Now())
		if !strings.Contains(text, "Cached source scan method: "+source) || !strings.Contains(text, "NTFS reader version: 2") {
			t.Fatalf("cache origin omitted: %s", text)
		}
	}
}
