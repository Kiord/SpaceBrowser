package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"spacebrowser/internal/platform"
	"strings"
	"testing"
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

	originalPlatform := platform.Impl
	platform.Impl = readDirFailurePlatform{API: originalPlatform, failingPath: unreadablePath}
	defer func() { platform.Impl = originalPlatform }()

	profile := defaultProfile()
	profile.SkipNetworkFS = false
	scanner := NewScanner(profile, 1)
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

	originalPlatform := platform.Impl
	usageCalls := 0
	platform.Impl = collidingIdentityPlatform{
		API:                       originalPlatform,
		root:                      rootPath,
		identityNeedsConfirmation: true,
		usageCalls:                &usageCalls,
	}
	defer func() { platform.Impl = originalPlatform }()

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScanner(profile, 1)
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

func TestScannerTrustsStrongBatchedIdentityWithoutMetadataLookup(t *testing.T) {
	rootPath := t.TempDir()
	for _, name := range []string{"first.bin", "second.bin"} {
		if err := os.WriteFile(filepath.Join(rootPath, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	originalPlatform := platform.Impl
	usageCalls := 0
	platform.Impl = collidingIdentityPlatform{API: originalPlatform, root: rootPath, usageCalls: &usageCalls}
	defer func() { platform.Impl = originalPlatform }()

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScanner(profile, 1)
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
