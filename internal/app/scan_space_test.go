package app

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"spacebrowser/internal/platform"
)

func TestScanSpaceUsageKeepsSignedDifferenceAndCoverage(t *testing.T) {
	measured := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	for _, scanned := range []int64{70, 100, 120} {
		root := &Node{FullPath: "volume", Size: scanned}
		var report ScanReportSnapshot
		report.Errors[scanErrorReadDirectory] = 3
		report.Errors[scanErrorDirectoryMetadata] = 2
		report.Skipped[scanSkipNestedVolume] = 1
		usage := scanSpaceUsage(root, report, &disk.UsageStat{Total: 200, Free: 100}, "", measured)
		if usage.Difference != 100-scanned || usage.ScannedBytes != scanned || usage.VolumeUsed != 100 || !usage.VolumeAvailable {
			t.Fatalf("incorrect accounting for %d scanned bytes: %+v", scanned, usage)
		}
		if usage.UnreadableDirectories != 3 || usage.ErrorCount != 5 || usage.ExcludedVolumes != 1 || usage.MeasuredAt != measured.Format(time.RFC3339Nano) {
			t.Fatalf("scan measurements lost coverage or timestamp: %+v", usage)
		}
	}
	for _, volume := range []*disk.UsageStat{nil, {Total: 100, Free: 101}, {}} {
		usage := scanSpaceUsage(&Node{Size: 70}, ScanReportSnapshot{}, volume, "", measured)
		if usage.VolumeAvailable || usage.Difference != 0 {
			t.Fatalf("unavailable volume produced a comparison: %+v", usage)
		}
	}
}

func spaceLayoutStore(scanned int64) *TreeStore {
	root := &Node{ID: 0, ParentID: -1, FullPath: "volume", IsFolder: true, Size: scanned, DiskTotal: 200, DiskFree: 100}
	file := &Node{ID: 1, ParentID: 0, FullPath: "volume/file", Name: "file", Size: scanned}
	root.Children = []*Node{file, {ID: -1, ParentID: 0, IsFreeSpace: true, Size: 100, DiskTotal: 200}}
	root.SpaceUsage = scanSpaceUsage(root, ScanReportSnapshot{}, &disk.UsageStat{Total: 200, Free: 100}, "", time.Now())
	store := &TreeStore{}
	store.Replace(root, []*Node{root, file}, 1, 1)
	return store
}

func TestSpaceVisibilityRescalesVisibleBlocksWithoutGaps(t *testing.T) {
	for _, dimensions := range [][2]int{{800, 600}, {1600, 400}, {400, 1600}} {
		for _, showFree := range []bool{false, true} {
			for _, showUnaccounted := range []bool{false, true} {
				store := spaceLayoutStore(70)
				rects, err := store.LayoutWithBoxPadding(0, dimensions[0], dimensions[1], 1, showFree, showUnaccounted, 5)
				if err != nil {
					t.Fatal(err)
				}
				var unaccounted, free *Rect
				area := 0.0
				for index := 1; index < len(rects); index++ {
					rect := &rects[index]
					area += rect.W * rect.H
					if rect.IsUnaccounted {
						unaccounted = rect
					}
					if rect.IsFree {
						free = rect
					}
				}
				if (unaccounted != nil) != showUnaccounted || (free != nil) != showFree {
					t.Fatalf("incorrect visibility: free=%t unaccounted=%t, rects=%+v", showFree, showUnaccounted, rects)
				}
				if unaccounted != nil && (unaccounted.Size != 30 || unaccounted.NodeID >= 0 || unaccounted.FullPath != "" || unaccounted.IsFolder) {
					t.Fatalf("unaccounted block is actionable: %+v", unaccounted)
				}
				if unaccounted != nil && free != nil {
					verticalEdge := unaccounted.X+unaccounted.W == free.X+1 && unaccounted.Y == free.Y && unaccounted.H == free.H
					horizontalEdge := unaccounted.Y+unaccounted.H == free.Y+1 && unaccounted.X == free.X && unaccounted.W == free.W
					if !verticalEdge && !horizontalEdge {
						t.Fatal("visible space blocks have no shared edge")
					}
				}
				denominator := 70.0
				if showFree {
					denominator += 100
				}
				if showUnaccounted {
					denominator += 30
				}
				for _, rect := range rects[1:] {
					if math.Abs(rect.W*rect.H/area-float64(rect.Size)/denominator) > 0.005 {
						t.Fatalf("incorrect visible area proportion for %+v, denominator %v, area %v", rect, denominator, area)
					}
					if rect.IsFree && rect.Size != 100 {
						t.Fatal("resizing changed the actual free byte count")
					}
				}
				interiorArea := float64((dimensions[0] - 10) * (dimensions[1] - 20))
				if math.Abs(area/interiorArea-1) > 0.008 {
					t.Fatalf("layout left a gap: visible area %v, interior %v", area, interiorArea)
				}
				if len(store.root.Children) != 2 || store.root.Size != 70 || len(store.nodes) != 2 || len(rects[0].Children) != len(rects)-1 {
					t.Fatal("layout mutated the scan or emitted a grouping container")
				}
			}
		}
	}
}

func TestAppLayoutsUseUnaccountedVisibilitySetting(t *testing.T) {
	store := spaceLayoutStore(70)
	a := newApp("")
	a.desktop = nil
	a.store.Replace(store.root, store.nodes, 1, 1)
	for _, showFree := range []bool{true, false} {
		a.SetShowFreeSpace(showFree)
		for _, show := range []bool{false, true, false} {
			a.settingsMu.Lock()
			a.profile.ShowUnaccounted = show
			a.settingsMu.Unlock()
			for _, layout := range []func() ([]Rect, error){
				func() ([]Rect, error) { return a.Layout(0, 800, 600, 1) },
				func() ([]Rect, error) { return a.LayoutWithBoxPadding(0, 800, 600, 1, 5) },
			} {
				rects, err := layout()
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, rect := range rects {
					if rect.IsUnaccounted {
						found = true
					}
				}
				if found != show {
					t.Fatalf("layout ignored ShowUnaccounted=%t", show)
				}
			}
		}
	}
}

func TestSavedMapDoesNotMixLiveFreeSpaceWithRecordedUnaccountedSpace(t *testing.T) {
	store := spaceLayoutStore(70)
	before, err := store.Layout(0, 800, 600, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	store.UpdateDiskUsage(200, 80)
	after, err := store.Layout(0, 800, 600, 1, true)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("live free-space measurement distorted the saved scan")
	}
}

func TestUnaccountedBlockRequiresComparableVolumeSnapshot(t *testing.T) {
	for _, state := range []string{"excess", "matched", "folder", "stale", "unknown-scope"} {
		t.Run(state, func(t *testing.T) {
			store := spaceLayoutStore(70)
			switch state {
			case "excess":
				store.root.SpaceUsage.Difference = -30
			case "matched":
				store.root.SpaceUsage.Difference = 0
			case "folder":
				store.root.SpaceUsage.VolumeAvailable = false
			case "stale":
				store.root.SpaceUsage.Stale = true
			case "unknown-scope":
				store.root.SpaceUsage.ScopeNote = "mount enumeration failed"
			}
			rects, err := store.LayoutWithBoxPadding(0, 800, 600, 1, true, true, 5)
			if err != nil {
				t.Fatal(err)
			}
			for _, rect := range rects {
				if rect.IsUnaccounted {
					t.Fatalf("%s produced a misleading block", state)
				}
			}
		})
	}
}

func TestSpaceMeasurementSurvivesSavedScanWithoutUsingLiveUsage(t *testing.T) {
	path := t.TempDir()
	store := spaceLayoutStore(70)
	store.root.FullPath = path
	snapshot := store.sharedSnapshot()
	want := *snapshot.root.SpaceUsage
	cache := &coldCache{dir: t.TempDir()}
	info := ScanJobInfo{Path: path, State: "completed", ScannedAt: want.MeasuredAt}
	if err := cache.save(info, snapshot, cache.epoch(path)); err != nil {
		t.Fatal(err)
	}
	loaded, err := cache.load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.root.SpaceUsage, &want) {
		t.Fatalf("saved space measurement changed: %+v", loaded.root.SpaceUsage)
	}
	store.mu.Lock()
	store.ensureOwnedLocked()
	store.markSpaceUsageStaleLocked()
	store.mu.Unlock()
	if !store.root.SpaceUsage.Stale || snapshot.root.SpaceUsage.Stale || loaded.root.SpaceUsage.Stale {
		t.Fatal("marking the working tree stale changed a saved measurement")
	}
}

type volumeBoundaryFilesystem struct {
	platform.API
	root, mount string
	mountError  error
	ignoreMount bool
}

func (f volumeBoundaryFilesystem) IsMountRoot(path string) bool {
	return path == f.root || path == f.mount
}
func (f volumeBoundaryFilesystem) NestedVolumeMounts(string) ([]string, error) {
	if f.ignoreMount {
		return nil, f.mountError
	}
	return []string{f.mount}, f.mountError
}

func TestFolderRefreshDoesNotImportNewlyMountedVolume(t *testing.T) {
	root := t.TempDir()
	mount := filepath.Join(root, "new-volume")
	if err := os.Mkdir(mount, 0700); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{root, mount} {
		if err := os.WriteFile(filepath.Join(folder, "file.txt"), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	filesystem := &volumeBoundaryFilesystem{API: platform.Impl, root: root, mount: mount, ignoreMount: true}
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	t.Cleanup(func() { a.Shutdown(nil) })
	a.filesystem = filesystem
	a.scanCache.SetEnabled(false)
	if _, err := a.GetFullTree(root); err != nil {
		t.Fatal(err)
	}
	target := indexCachedDirectories(a.store.root)[canonicalCachePath(mount)]
	filesystem.ignoreMount = false
	result, err := a.RefreshFolders([]FolderRefreshTarget{{NodeID: target.ID, Path: mount}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 1 || indexCachedDirectories(a.store.root)[canonicalCachePath(mount)] != nil {
		t.Fatal("folder refresh imported a different mounted volume")
	}
}

func TestVolumeRescanInvalidatesCacheWhenMountBoundariesChange(t *testing.T) {
	root := t.TempDir()
	mount := filepath.Join(root, "mounted")
	if err := os.Mkdir(mount, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "file.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	filesystem := &volumeBoundaryFilesystem{API: platform.Impl, root: root, mount: mount, ignoreMount: true}
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	t.Cleanup(func() { a.Shutdown(nil) })
	a.filesystem = filesystem
	a.scanCache.SetEnabled(false)
	if _, err := a.GetFullTree(root); err != nil {
		t.Fatal(err)
	}
	a.scanCache.SetEnabled(true)
	_, key := scanProfileCacheKey(a.GetProfile())
	a.scanCache.entries[scanMemoryCacheKey(root, key)] = &scanCacheEntry{
		rootPath: root, profileKey: key, root: a.store.root, nodes: a.store.nodes,
		fileCount: a.store.fileCount, dirCount: a.store.dirCount,
		directories: indexCachedDirectories(a.store.root), dirty: make(map[string]struct{}),
	}
	filesystem.ignoreMount = false
	info, err := a.GetFullTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.FileCount != 0 || a.store.root.SpaceUsage.ExcludedVolumes != 1 {
		t.Fatal("rescan reused a tree whose volume boundaries changed")
	}
}

func TestVolumeScanExcludesNestedVolumeButDirectScanKeepsIt(t *testing.T) {
	root := t.TempDir()
	mount := filepath.Join(root, "other-volume")
	if err := os.Mkdir(mount, 0700); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{root, mount} {
		if err := os.WriteFile(filepath.Join(folder, "file.txt"), []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	filesystem := volumeBoundaryFilesystem{API: platform.Impl, root: root, mount: mount}
	for _, path := range []string{root, mount} {
		scanner := NewScannerWithFilesystem(defaultProfile(), 2, filesystem)
		if note := configureScanVolume(scanner, path); note != "" {
			t.Fatal(note)
		}
		var files, dirs int64
		tree, err := scanner.buildTree(path, 0, -1, &files, &dirs)
		if err != nil {
			t.Fatal(err)
		}
		if files != 1 || dirs != 1 {
			t.Fatalf("%s included another volume: files=%d dirs=%d", path, files, dirs)
		}
		if tree.HasNestedVolumes != (path == root) {
			t.Fatal("volume boundary metadata is incorrect")
		}
	}
	filesystem.mountError = errors.New("unavailable mount table")
	scanner := NewScannerWithFilesystem(defaultProfile(), 1, filesystem)
	if note := configureScanVolume(scanner, root); note == "" {
		t.Fatal("mount enumeration failure was hidden")
	}
}
