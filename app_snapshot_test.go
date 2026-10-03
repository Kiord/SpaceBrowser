package main

import "testing"

func TestOpenScanSnapshotReusesDirtyTreeWithoutScanning(t *testing.T) {
	a := &App{scanCache: newScanCacheManager(nil)}
	path := t.TempDir()
	_, key := scanProfileCacheKey(a.GetProfile())
	root := &Node{ID: 0, FullPath: path, IsFolder: true}
	entry := &scanCacheEntry{root: root, nodes: []*Node{root}, fileCount: 3, dirCount: 1,
		rootPath: path, profileKey: key, dirty: map[string]struct{}{path: {}}}
	a.scanCache.entries[scanMemoryCacheKey(path, key)] = entry
	result := a.OpenScanSnapshot(path)
	if result == nil || result.FileCount != 3 || a.store.root != root || !a.store.shared {
		t.Fatalf("snapshot was not reopened: %+v", result)
	}
	if a.scanActive || entry.lastUsed == 0 {
		t.Fatal("opening a snapshot must only touch cache recency, not start a scan")
	}
	if a.OpenScanSnapshot(path+"-missing") != nil {
		t.Fatal("missing snapshot should return nil")
	}
	a.scanActive = true
	if a.OpenScanSnapshot(path) != nil {
		t.Fatal("snapshot must not replace an active scan")
	}
	a.scanActive = false
	a.profile.SkipHidden = !a.profile.SkipHidden
	if a.OpenScanSnapshot(path) != nil {
		t.Fatal("snapshot must match the scan settings")
	}
}
