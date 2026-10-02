package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spacebrowser/internal/platform"
)

func refreshFixture(t *testing.T) (*App, *countingCacheFilesystem, string, map[string]int) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"a/child", "b", "c"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "file"), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs := &countingCacheFilesystem{API: platform.Impl, reads: make(map[string]int)}
	a := newAppWithDependencies("", "", NewSeverityLogger(0, os.Stderr), fs, platform.Impl, platform.Impl)
	a.profile.MinFileSize = 0
	a.profile.UseCache = false
	if _, err := a.GetFullTree(root); err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]int)
	for _, node := range a.store.nodes {
		if node != nil {
			name, _ := filepath.Rel(root, node.FullPath)
			ids[filepath.ToSlash(name)] = node.ID
		}
	}
	fs.reads = make(map[string]int)
	t.Cleanup(a.scanCache.Close)
	return a, fs, root, ids
}

func TestRefreshFoldersOnlyScansReducedSelection(t *testing.T) {
	a, fs, root, ids := refreshFixture(t)
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(root, name, "new"), []byte("new file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "a/child/file")); err != nil {
		t.Fatal(err)
	}
	targets := []FolderRefreshTarget{
		{ids["a/child"], filepath.Join(root, "a/child")}, {ids["a"], filepath.Join(root, "a")},
		{ids["b"], filepath.Join(root, "b")}, {ids["a"], filepath.Join(root, "a")},
	}
	tracked := []int{ids["."], ids["a/child"], ids["a/child/file"], ids["c/file"]}
	result, err := a.RefreshFolders(targets, tracked)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 4 || result.DirCount != 5 {
		t.Fatalf("counts: %+v", result)
	}
	for _, name := range []string{"a", "a/child", "b"} {
		if got := fs.readCount(filepath.Join(root, name)); got != 1 {
			t.Errorf("reads %s = %d", name, got)
		}
	}
	if fs.readCount(root) != 0 || fs.readCount(filepath.Join(root, "c")) != 0 {
		t.Fatal("scanned outside selection")
	}
	if result.NodeIDs[ids["a/child/file"]] != -1 {
		t.Fatal("removed file retained an ID")
	}
	for _, name := range []string{".", "a/child", "c/file"} {
		path, err := a.store.NodePath(result.NodeIDs[ids[name]])
		if err != nil || path != filepath.Join(root, name) {
			t.Fatalf("remap %s: %s, %v", name, path, err)
		}
	}
	var sum int64
	for _, child := range a.store.root.Children {
		sum += child.Size
	}
	if sum != a.store.root.Size {
		t.Fatal("ancestor size is inconsistent")
	}
}

func TestRefreshFoldersRejectsInvalidBatchBeforeScanning(t *testing.T) {
	a, fs, root, ids := refreshFixture(t)
	for _, bad := range []FolderRefreshTarget{{-1, root}, {ids["a"], "wrong"}, {ids["c/file"], filepath.Join(root, "c/file")}} {
		_, err := a.RefreshFolders([]FolderRefreshTarget{{ids["a"], filepath.Join(root, "a")}, bad}, nil)
		if err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	if fs.readCount(filepath.Join(root, "a")) != 0 {
		t.Fatal("invalid batch started scanning")
	}
}

func TestRefreshCancellationKeepsWholeBatchAndDoesNotLockReaders(t *testing.T) {
	a, _, root, ids := refreshFixture(t)
	block := &blockingReadDirPlatform{API: platform.Impl, path: filepath.Join(root, "b"), entered: make(chan struct{}), release: make(chan struct{})}
	a.filesystem = block
	oldGeneration := a.store.generation
	if err := os.WriteFile(filepath.Join(root, "a/new"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.RefreshFolders([]FolderRefreshTarget{{ids["a"], filepath.Join(root, "a")}, {ids["b"], filepath.Join(root, "b")}}, nil)
		done <- err
	}()
	select {
	case <-block.entered:
	case <-time.After(5 * time.Second):
		close(block.release)
		t.Fatal("scan did not start")
	}
	read := make(chan error, 1)
	go func() { _, err := a.store.Layout(ids["."], 800, 600, 1, true); read <- err }()
	select {
	case err := <-read:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("filesystem scan blocks tree readers")
	}
	a.CancelScan()
	close(block.release)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancel result: %v", err)
	}
	if a.store.generation != oldGeneration {
		t.Fatal("cancelled batch published a subtree")
	}
	if a.GetScanProgress().Active {
		t.Fatal("scan left active")
	}
}

func TestRefreshNewHardLinkFallsBackToRoot(t *testing.T) {
	a, fs, root, ids := refreshFixture(t)
	if err := os.Link(filepath.Join(root, "a/child/file"), filepath.Join(root, "c/link")); err != nil {
		t.Skip(err)
	}
	if _, err := a.RefreshFolders([]FolderRefreshTarget{{ids["a"], filepath.Join(root, "a")}}, nil); err != nil {
		t.Fatal(err)
	}
	if fs.readCount(root) == 0 {
		t.Fatal("shared allocation did not trigger full scan")
	}
	want, _, _, _ := scanTestTree(t, root, platform.Impl)
	if a.store.root.Size != want.Size {
		t.Fatalf("size = %d, want %d", a.store.root.Size, want.Size)
	}
}

func TestRefreshRootKeepsFreeSpace(t *testing.T) {
	a, _, root, ids := refreshFixture(t)
	a.store.hasDiskUsage = true
	a.store.diskTotal, a.store.diskFree = 1000, 100
	if _, err := a.RefreshFolders([]FolderRefreshTarget{{ids["."], root}}, nil); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, node := range a.store.root.Children {
		if node.IsFreeSpace {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("free-space nodes = %d", count)
	}
}

func TestRefreshBindingMigrationPreservesOtherControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":13,"controls":{"open":"Alt+O","visitSelected":"Space"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := loadSettingsWithFilesystem(path, platform.Impl)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Controls.Refresh != "Ctrl+R" || profile.Controls.Open != "Alt+O" || profile.Controls.VisitSelected != "Space" {
		t.Fatalf("controls = %+v", profile.Controls)
	}
	profile.Controls.Refresh = ""
	if err := saveSettings(path, profile); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadSettingsWithFilesystem(path, platform.Impl)
	if err != nil || reloaded.Controls.Refresh != "" {
		t.Fatalf("cleared binding not preserved: %+v, %v", reloaded.Controls, err)
	}
}

func TestRefreshInvalidatesLiveCacheWithoutMutatingItsSnapshot(t *testing.T) {
	a, _, root, ids := refreshFixture(t)
	a.profile.UseCache = true
	if _, err := a.GetFullTree(root); err != nil {
		t.Fatal(err)
	}
	for _, node := range a.store.nodes {
		if node != nil && node.FullPath == filepath.Join(root, "a") {
			ids["a"] = node.ID
		}
	}
	oldRoot := a.store.root
	oldSize := oldRoot.Size
	if err := os.WriteFile(filepath.Join(root, "a/new"), make([]byte, 16384), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RefreshFolders([]FolderRefreshTarget{{ids["a"], filepath.Join(root, "a")}}, nil); err != nil {
		t.Fatal(err)
	}
	if oldRoot.Size != oldSize || oldRoot == a.store.root {
		t.Fatal("refresh mutated the shared cache tree")
	}
	a.scanCache.mu.Lock()
	defer a.scanCache.mu.Unlock()
	for _, entry := range a.scanCache.entries {
		if _, dirty := entry.dirty[canonicalCachePath(filepath.Join(root, "a"))]; !dirty {
			t.Fatal("old cached folder was not invalidated")
		}
	}
}

func TestRefreshCannotPublishOverANewerScan(t *testing.T) {
	a, _, root, ids := refreshFixture(t)
	block := &blockingReadDirPlatform{API: platform.Impl, path: filepath.Join(root, "a"), entered: make(chan struct{}), release: make(chan struct{})}
	a.filesystem = block
	done := make(chan error, 1)
	go func() {
		_, err := a.RefreshFolders([]FolderRefreshTarget{{ids["a"], filepath.Join(root, "a")}}, nil)
		done <- err
	}()
	select {
	case <-block.entered:
	case <-time.After(5 * time.Second):
		close(block.release)
		t.Fatal("scan did not start")
	}
	newer := t.TempDir()
	if _, err := a.GetFullTree(newer); err != nil {
		close(block.release)
		t.Fatal(err)
	}
	close(block.release)
	if err := <-done; err == nil {
		t.Fatal("superseded refresh succeeded")
	}
	if a.store.root.FullPath != newer {
		t.Fatal("refresh overwrote the newer tree")
	}
}
