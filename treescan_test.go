package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"spacebrowser/internal/platform"
	"strings"
	"sync"
	"testing"
	"time"
)

type readDirFailurePlatform struct {
	platform.API
	failingPath string
}

type collidingIdentityPlatform struct {
	platform.API
	root                      string
	identityNeedsConfirmation bool
	usageCalls                *int
}

type fallbackDiagnosticPlatform struct {
	platform.API
	root string
}

type blockingReadDirPlatform struct {
	platform.API
	path    string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type metadataFailurePlatform struct {
	platform.API
	root     string
	fileName string
}

type networkRootPlatform struct {
	platform.API
	root string
}

func (p networkRootPlatform) IsLikelyNetworkFS(path string) bool {
	return filepath.Clean(path) == filepath.Clean(p.root)
}

type metadataFailureDirEntry struct {
	os.DirEntry
}

func (entry metadataFailureDirEntry) Info() (os.FileInfo, error) {
	return nil, os.ErrPermission
}

func (p *blockingReadDirPlatform) ReadDir(path string) ([]platform.DirectoryEntry, error) {
	if filepath.Clean(path) == filepath.Clean(p.path) {
		p.once.Do(func() { close(p.entered) })
		<-p.release
	}
	return p.API.ReadDir(path)
}

func (p metadataFailurePlatform) ReadDir(path string) ([]platform.DirectoryEntry, error) {
	entries, err := p.API.ReadDir(path)
	if err != nil || filepath.Clean(path) != filepath.Clean(p.root) {
		return entries, err
	}
	for index := range entries {
		if entries[index].Name() == p.fileName {
			entries[index].DirEntry = metadataFailureDirEntry{DirEntry: entries[index].DirEntry}
		}
	}
	return entries, nil
}

func (p fallbackDiagnosticPlatform) ReadDirWithDiagnostics(path string) ([]platform.DirectoryEntry, *platform.DirectoryReadDiagnostic, error) {
	entries, err := p.API.ReadDir(path)
	if err != nil || filepath.Clean(path) != filepath.Clean(p.root) {
		return entries, nil, err
	}
	return entries, &platform.DirectoryReadDiagnostic{
		PortableFallback: true,
		Cause:            errors.New("both native layouts are unsupported"),
	}, nil
}

func (p collidingIdentityPlatform) ReadDir(path string) ([]platform.DirectoryEntry, error) {
	entries, err := p.API.ReadDir(path)
	if err != nil || filepath.Clean(path) != filepath.Clean(p.root) {
		return entries, err
	}
	for index := range entries {
		if entries[index].IsDir() {
			continue
		}
		info, infoErr := entries[index].Info()
		if infoErr != nil {
			return nil, infoErr
		}
		entries[index].Usage = platform.FileUsage{
			AllocatedSize:             info.Size(),
			Identity:                  platform.FileIdentity{Volume: 1, Low: 1},
			HasIdentity:               true,
			IdentityNeedsConfirmation: p.identityNeedsConfirmation,
		}
		entries[index].HasUsage = true
	}
	return entries, nil
}

func (p collidingIdentityPlatform) UsageFor(path string, info os.FileInfo) platform.FileUsage {
	if p.usageCalls != nil {
		*p.usageCalls++
	}
	return p.API.UsageFor(path, info)
}

func (p readDirFailurePlatform) ReadDir(path string) ([]platform.DirectoryEntry, error) {
	if filepath.Clean(path) == filepath.Clean(p.failingPath) {
		return nil, os.ErrPermission
	}
	return p.API.ReadDir(path)
}

func TestScannerCollectsWideTreeConcurrently(t *testing.T) {
	const branchCount = 256
	dir := t.TempDir()
	for i := 0; i < branchCount; i++ {
		branch := filepath.Join(dir, fmt.Sprintf("branch-%03d", i))
		if err := os.Mkdir(branch, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(branch, "content.bin"), []byte{byte(i)}, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScanner(profile, 8)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(dir, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}

	if got := len(root.Children); got != branchCount {
		t.Fatalf("root child count = %d, want %d", got, branchCount)
	}
	if fileCount != branchCount || dirCount != branchCount+1 {
		t.Fatalf("scan counts = %d files and %d directories, want %d and %d", fileCount, dirCount, branchCount, branchCount+1)
	}

	seenBranches := make(map[string]struct{}, branchCount)
	for _, branch := range root.Children {
		if !branch.IsFolder || len(branch.Children) != 1 {
			t.Fatalf("invalid branch node: %+v", branch)
		}
		seenBranches[branch.Name] = struct{}{}
	}
	if len(seenBranches) != branchCount {
		t.Fatalf("unique branch count = %d, want %d", len(seenBranches), branchCount)
	}

	nodes := scanner.Nodes()
	wantNodes := 1 + branchCount*2
	if len(nodes) != wantNodes {
		t.Fatalf("node count = %d, want %d", len(nodes), wantNodes)
	}
	for id, node := range nodes {
		if node == nil || node.ID != id {
			t.Fatalf("node index %d contains %+v", id, node)
		}
	}
}

func TestAppRejectsNetworkRootWhenNetworkSkippingIsEnabled(t *testing.T) {
	root := t.TempDir()
	filesystem := networkRootPlatform{API: platform.Impl, root: root}
	profile := *defaultProfile()
	app := &App{filesystem: filesystem, profile: profile}

	if _, err := app.ValidateScanPath(root); err == nil || !strings.Contains(err.Error(), "go to Settings and untick Skip network filesystems") {
		t.Fatalf("ValidateScanPath() error = %v, want actionable network-filesystem message", err)
	}

	app.profile.SkipNetworkFS = false
	if path, err := app.ValidateScanPath(root); err != nil || path != filesystem.Canonicalize(root) {
		t.Fatalf("ValidateScanPath() with network skipping disabled = %q, %v", path, err)
	}
}

func TestScannerHonorsCancelledContext(t *testing.T) {
	profile := defaultProfile()
	scanner := NewScanner(profile, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scanner.SetContext(ctx, nil)

	var fileCount, dirCount int64
	_, err := scanner.buildTree(t.TempDir(), 0, -1, &fileCount, &dirCount)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("buildTree error = %v, want context.Canceled", err)
	}
}

func TestScannerCancelsActiveScan(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "content.bin"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	filesystem := &blockingReadDirPlatform{
		API:     platform.Impl,
		path:    rootPath,
		entered: entered,
		release: release,
	}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScannerWithFilesystem(profile, 1, filesystem)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner.SetContext(ctx, nil)

	result := make(chan error, 1)
	go func() {
		var fileCount, dirCount int64
		_, err := scanner.buildTree(rootPath, 0, -1, &fileCount, &dirCount)
		result <- err
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("scan did not enter directory enumeration")
	}
	cancel()
	close(release)

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("active scan error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active scan did not stop after cancellation")
	}
}

func TestScannerSkipsAndFollowsDirectorySymlinks(t *testing.T) {
	rootPath := t.TempDir()
	targetPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetPath, "content.bin"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(rootPath, "linked")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	profile.FollowSymlinks = false
	skippingScanner := NewScanner(profile, 1)
	var skippedFiles, skippedDirs int64
	skippedRoot, err := skippingScanner.buildTree(rootPath, 0, -1, &skippedFiles, &skippedDirs)
	if err != nil {
		t.Fatal(err)
	}
	if skippedFiles != 0 || len(skippedRoot.Children) != 0 {
		t.Fatalf("non-following scan found %d files and %d children, want none", skippedFiles, len(skippedRoot.Children))
	}
	if got := skippingScanner.Report().Skipped[scanSkipSymlink]; got != 1 {
		t.Fatalf("skipped symlink count = %d, want 1", got)
	}

	profile.FollowSymlinks = true
	followingScanner := NewScanner(profile, 1)
	var followedFiles, followedDirs int64
	followedRoot, err := followingScanner.buildTree(rootPath, 0, -1, &followedFiles, &followedDirs)
	if err != nil {
		t.Fatal(err)
	}
	if followedFiles != 1 || followedDirs != 2 {
		t.Fatalf("following scan found %d files and %d directories, want 1 and 2", followedFiles, followedDirs)
	}
	if len(followedRoot.Children) != 1 || followedRoot.Children[0].Name != "linked" || !followedRoot.Children[0].IsFolder {
		t.Fatalf("followed symlink node = %+v", followedRoot.Children)
	}
	if len(followedRoot.Children[0].Children) != 1 || followedRoot.Children[0].Children[0].Name != "content.bin" {
		t.Fatalf("followed symlink contents = %+v", followedRoot.Children[0].Children)
	}
}

func TestScannerReportsBrokenSymlink(t *testing.T) {
	rootPath := t.TempDir()
	linkPath := filepath.Join(rootPath, "broken-link")
	if err := os.Symlink(filepath.Join(rootPath, "missing-target"), linkPath); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	profile.FollowSymlinks = true
	scanner := NewScanner(profile, 1)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(rootPath, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}
	if fileCount != 0 || len(root.Children) != 0 {
		t.Fatalf("broken symlink produced %d files and %d children, want none", fileCount, len(root.Children))
	}
	if got := scanner.Report().Errors[scanErrorSymlinkTarget]; got != 1 {
		t.Fatalf("broken symlink errors = %d, want 1", got)
	}
}

func TestScannerExcludesExactPathsAndDescendants(t *testing.T) {
	rootPath := t.TempDir()
	excludedDir := filepath.Join(rootPath, "excluded")
	keptDir := filepath.Join(rootPath, "excluded-sibling")
	for _, path := range []string{excludedDir, keptDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string][]byte{
		filepath.Join(rootPath, "excluded.bin"):  []byte("excluded file"),
		filepath.Join(rootPath, "kept.bin"):      []byte("kept file"),
		filepath.Join(excludedDir, "nested.bin"): []byte("excluded descendant"),
		filepath.Join(keptDir, "nested.bin"):     []byte("kept descendant"),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	profile.ExcludedPaths = []string{excludedDir, filepath.Join(rootPath, "excluded.bin")}
	scanner := NewScanner(profile, 2)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(rootPath, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}
	if fileCount != 2 || dirCount != 2 {
		t.Fatalf("scan counts = %d files and %d directories, want 2 and 2", fileCount, dirCount)
	}
	if got := scanner.Report().Skipped[scanSkipExcluded]; got != 2 {
		t.Fatalf("excluded path count = %d, want 2", got)
	}
	for _, child := range root.Children {
		if child.Name == "excluded" || child.Name == "excluded.bin" {
			t.Fatalf("excluded node remained in tree: %+v", child)
		}
	}
}

func TestScannerReportsFileMetadataFailures(t *testing.T) {
	rootPath := t.TempDir()
	fileName := "unreadable-metadata.bin"
	filePath := filepath.Join(rootPath, fileName)
	if err := os.WriteFile(filePath, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}

	filesystem := metadataFailurePlatform{API: platform.Impl, root: rootPath, fileName: fileName}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScannerWithFilesystem(profile, 1, filesystem)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(rootPath, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}
	if fileCount != 0 || len(root.Children) != 0 {
		t.Fatalf("metadata failure produced %d files and %d children, want none", fileCount, len(root.Children))
	}
	report := scanner.Report()
	if report.Errors[scanErrorFileMetadata] != 1 || report.TotalErrors() != 1 {
		t.Fatalf("report errors = %+v, want one file metadata error", report.Errors)
	}
	if len(report.Examples) != 1 || report.Examples[0].Path != filePath {
		t.Fatalf("report examples = %+v, want %q", report.Examples, filePath)
	}
}

func TestScanProgressLifecycleAndCancellation(t *testing.T) {
	app := NewApp()
	ctx, generation := app.beginScan("first", 0)

	progress := app.GetScanProgress()
	if !progress.Active || progress.Path != "first" {
		t.Fatalf("initial progress = %+v", progress)
	}

	app.updateScanPath(generation, "second")
	if got := app.GetScanProgress().Path; got != "second" {
		t.Fatalf("updated path = %q, want second", got)
	}

	app.CancelScan()
	if err := ctx.Err(); err == nil {
		t.Fatal("scan context was not cancelled")
	}

	app.finishScan(generation)
	if app.GetScanProgress().Active {
		t.Fatal("scan remained active after finish")
	}
}

func TestPublishScanResultRejectsSupersededGeneration(t *testing.T) {
	app := NewApp()
	original := &Node{ID: 0, ParentID: -1, Name: "original", IsFolder: true}
	app.store.Replace(original, []*Node{original}, 0, 1)

	oldContext, oldGeneration := app.beginScan("old", 0)
	_, currentGeneration := app.beginScan("current", 0)
	defer app.finishScan(currentGeneration)

	replacement := &Node{ID: 0, ParentID: -1, Name: "replacement", IsFolder: true}
	persisted := false
	if _, err := app.publishScanResult(oldContext, oldGeneration, replacement, []*Node{replacement}, 0, 1, func() *ScanReportInfo {
		persisted = true
		return &ScanReportInfo{}
	}); !errors.Is(err, errScanSuperseded) {
		t.Fatalf("publishScanResult() error = %v, want %v", err, errScanSuperseded)
	}
	if persisted {
		t.Fatal("superseded scan report was persisted")
	}
	app.store.mu.RLock()
	storedRoot := app.store.root
	app.store.mu.RUnlock()
	if storedRoot != original {
		t.Fatal("superseded scan replaced the application tree")
	}
}

func TestPublishScanResultCommitsCurrentGeneration(t *testing.T) {
	app := NewApp()
	ctx, generation := app.beginScan("current", 0)
	defer app.finishScan(generation)

	root := &Node{ID: 0, ParentID: -1, Name: "current", IsFolder: true}
	wantReport := &ScanReportInfo{ErrorCount: 1}
	persisted := false
	report, err := app.publishScanResult(ctx, generation, root, []*Node{root}, 0, 1, func() *ScanReportInfo {
		persisted = true
		return wantReport
	})
	if err != nil {
		t.Fatal(err)
	}
	if !persisted || report != wantReport {
		t.Fatalf("report publication = (%t, %p), want (true, %p)", persisted, report, wantReport)
	}
	app.store.mu.RLock()
	storedRoot := app.store.root
	app.store.mu.RUnlock()
	if storedRoot != root {
		t.Fatal("current scan did not replace the application tree")
	}
}

func TestPublishScanResultRejectsCancelledContext(t *testing.T) {
	app := NewApp()
	ctx, generation := app.beginScan("cancelled", 0)
	defer app.finishScan(generation)
	app.CancelScan()

	root := &Node{ID: 0, ParentID: -1, Name: "cancelled", IsFolder: true}
	persisted := false
	if _, err := app.publishScanResult(ctx, generation, root, []*Node{root}, 0, 1, func() *ScanReportInfo {
		persisted = true
		return &ScanReportInfo{}
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("publishScanResult() error = %v, want context.Canceled", err)
	}
	if persisted {
		t.Fatal("cancelled scan report was persisted")
	}
	app.store.mu.RLock()
	storedRoot := app.store.root
	app.store.mu.RUnlock()
	if storedRoot != nil {
		t.Fatal("cancelled scan replaced the application tree")
	}
}

func TestScannerAggregatesSmallFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"empty.txt": nil,
		"tiny.txt":  make([]byte, 100),
		"large.bin": make([]byte, 2048),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	subdir := filepath.Join(dir, "nested")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "nested-tiny.txt"), make([]byte, 200), 0o600); err != nil {
		t.Fatal(err)
	}

	profile := defaultProfile()
	profile.MinFileSize = 1024
	profile.SkipNetworkFS = false
	scanner := NewScanner(profile, 1)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(dir, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}

	if fileCount != 4 {
		t.Fatalf("file count = %d, want 4", fileCount)
	}
	processed, discovered := scanner.WorkProgress()
	if discovered == 0 || processed != discovered {
		t.Fatalf("work progress = %d/%d, want a completed non-empty scan", processed, discovered)
	}

	directAggregate := func(folder *Node) *Node {
		for _, child := range folder.Children {
			if child.IsSmallFiles {
				return child
			}
		}
		return nil
	}

	rootAggregate := directAggregate(root)
	if rootAggregate == nil || rootAggregate.SmallFileCount != 2 {
		t.Fatalf("root aggregate = %+v, want 2 directly contained small files", rootAggregate)
	}

	var nested *Node
	for _, child := range root.Children {
		if child.IsFolder && child.Name == "nested" {
			nested = child
			break
		}
	}
	if nested == nil {
		t.Fatal("nested folder not found")
	}
	if nested.ModTime == 0 {
		t.Fatal("nested folder has no modification date")
	}
	nestedAggregate := directAggregate(nested)
	if nestedAggregate == nil || nestedAggregate.SmallFileCount != 1 {
		t.Fatalf("nested aggregate = %+v, want 1 directly contained small file", nestedAggregate)
	}

	for folder, aggregate := range map[*Node]*Node{root: rootAggregate, nested: nestedAggregate} {
		if aggregate.ParentID != folder.ID {
			t.Fatalf("aggregate parent = %d, want folder %d", aggregate.ParentID, folder.ID)
		}
		if aggregate.SmallFileLimit != profile.MinFileSize {
			t.Fatalf("small-files limit = %d, want %d", aggregate.SmallFileLimit, profile.MinFileSize)
		}
		if aggregate.ID != -1 || aggregate.IsFolder || aggregate.IsFreeSpace {
			t.Fatalf("aggregate node has unexpected identity or type: %+v", aggregate)
		}

		foundRect := false
		for _, rect := range ComputeTreemapRects(folder, 800, 600, 1) {
			if rect.IsSmallFiles && rect.ParentID != nil && *rect.ParentID == folder.ID {
				foundRect = true
				if rect.SmallFileCount != aggregate.SmallFileCount {
					t.Fatalf("rect small-files count = %d, want %d", rect.SmallFileCount, aggregate.SmallFileCount)
				}
			}
		}
		if !foundRect {
			t.Fatalf("small-files aggregate rectangle not found for %q", folder.Name)
		}
	}
}

func TestScannerReportsUnreadableDirectories(t *testing.T) {
	rootPath := t.TempDir()
	unreadablePath := filepath.Join(rootPath, "unreadable")
	if err := os.Mkdir(unreadablePath, 0o700); err != nil {
		t.Fatal(err)
	}

	filesystem := readDirFailurePlatform{API: platform.Impl, failingPath: unreadablePath}

	profile := defaultProfile()
	profile.SkipNetworkFS = false
	scanner := NewScannerWithFilesystem(profile, 1, filesystem)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(rootPath, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || len(root.Children) != 1 {
		t.Fatalf("partial tree = %+v, want unreadable folder node", root)
	}

	report := scanner.Report()
	if report.Errors[scanErrorReadDirectory] != 1 || report.TotalErrors() != 1 {
		t.Fatalf("report errors = %+v, want one unreadable directory", report.Errors)
	}
	if len(report.Examples) != 1 || report.Examples[0].Path != unreadablePath {
		t.Fatalf("report examples = %+v, want %q", report.Examples, unreadablePath)
	}
}

func TestScannerDoesNotDeduplicateUnconfirmedIdentityCollision(t *testing.T) {
	rootPath := t.TempDir()
	for _, name := range []string{"first.bin", "second.bin"} {
		if err := os.WriteFile(filepath.Join(rootPath, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	usageCalls := 0
	filesystem := collidingIdentityPlatform{
		API:                       platform.Impl,
		root:                      rootPath,
		identityNeedsConfirmation: true,
		usageCalls:                &usageCalls,
	}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScannerWithFilesystem(profile, 1, filesystem)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(rootPath, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}
	if fileCount != 2 || len(root.Children) != 2 {
		t.Fatalf("identity collision produced %d files and %d nodes, want 2 and 2", fileCount, len(root.Children))
	}
	if got := scanner.Report().Skipped[scanSkipDuplicateIdentity]; got != 0 {
		t.Fatalf("reported %d duplicate identities, want 0", got)
	}
	if usageCalls == 0 {
		t.Fatal("untrusted identity collision was not confirmed")
	}
}

func TestScannerReportsPortableDirectoryEnumerationFallback(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "content.bin"), []byte{1}, 0o600); err != nil {
		t.Fatal(err)
	}

	filesystem := fallbackDiagnosticPlatform{API: platform.Impl, root: rootPath}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScannerWithFilesystem(profile, 1, filesystem)
	var fileCount, dirCount int64
	if _, err := scanner.buildTree(rootPath, 0, -1, &fileCount, &dirCount); err != nil {
		t.Fatal(err)
	}

	report := scanner.Report()
	if got := report.Errors[scanErrorPortableDirectoryFallback]; got != 1 {
		t.Fatalf("portable fallback count = %d, want 1", got)
	}
	if len(report.Examples) != 1 || !strings.Contains(report.Examples[0].Error, "both native layouts") {
		t.Fatalf("portable fallback examples = %+v", report.Examples)
	}
}

func TestScannerTrustsStrongBatchedIdentityWithoutMetadataLookup(t *testing.T) {
	rootPath := t.TempDir()
	for _, name := range []string{"first.bin", "second.bin"} {
		if err := os.WriteFile(filepath.Join(rootPath, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	usageCalls := 0
	filesystem := collidingIdentityPlatform{API: platform.Impl, root: rootPath, usageCalls: &usageCalls}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScannerWithFilesystem(profile, 1, filesystem)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(rootPath, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}
	if fileCount != 1 || len(root.Children) != 1 {
		t.Fatalf("strong duplicate identity produced %d files and %d nodes, want 1 and 1", fileCount, len(root.Children))
	}
	if usageCalls != 0 {
		t.Fatalf("strong duplicate identity triggered %d metadata lookups, want 0", usageCalls)
	}
}

func TestScanReportLoggingIsCompactAndInformative(t *testing.T) {
	var output strings.Builder
	app := &App{logger: NewSeverityLogger(verbosityInfo, &output)}
	report := ScanReportSnapshot{}
	report.Skipped[scanSkipHidden] = 3
	report.Errors[scanErrorFileMetadata] = 2
	report.Examples = []ScanReportExample{{
		Reason: scanErrorLabels[scanErrorFileMetadata],
		Path:   "missing.dat",
		Error:  "access denied",
	}}

	app.logScanReport(report)
	logged := output.String()
	for _, expected := range []string{
		"scan report: 3 skipped paths, 2 filesystem or metadata errors",
		"hidden=3",
		"file metadata=2",
		"missing.dat: access denied",
	} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("scan report log %q does not contain %q", logged, expected)
		}
	}
}

func TestScanReportErrorRetentionFollowsVerbosityMode(t *testing.T) {
	compact := NewScanReport(maximumScanReportExamples)
	verbose := NewScanReport(-1)
	for index := 0; index < maximumScanReportExamples+3; index++ {
		err := fmt.Errorf("failure %d", index)
		compact.RecordError(scanErrorFileMetadata, fmt.Sprintf("compact-%d", index), err)
		verbose.RecordError(scanErrorFileMetadata, fmt.Sprintf("verbose-%d", index), err)
	}

	if got := len(compact.Snapshot().Examples); got != maximumScanReportExamples {
		t.Fatalf("compact report retained %d entries, want %d", got, maximumScanReportExamples)
	}
	if got := len(verbose.Snapshot().Examples); got != maximumScanReportExamples+3 {
		t.Fatalf("verbose report retained %d entries, want %d", got, maximumScanReportExamples+3)
	}
}

func TestCompactScanReportRetainsPriorityDiagnostic(t *testing.T) {
	report := NewScanReport(maximumScanReportExamples)
	for index := 0; index < maximumScanReportExamples; index++ {
		report.RecordError(scanErrorFileMetadata, fmt.Sprintf("file-%d", index), errors.New("metadata failure"))
	}
	report.RecordPriorityError(scanErrorPortableDirectoryFallback, "fallback-dir", errors.New("native layouts failed"))

	snapshot := report.Snapshot()
	if len(snapshot.Examples) != maximumScanReportExamples {
		t.Fatalf("compact report retained %d entries, want %d", len(snapshot.Examples), maximumScanReportExamples)
	}
	found := false
	for _, example := range snapshot.Examples {
		if example.Path == "fallback-dir" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("priority diagnostic missing from %+v", snapshot.Examples)
	}
}
