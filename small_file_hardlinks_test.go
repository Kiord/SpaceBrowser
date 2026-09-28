package main

import (
	"os"
	"path/filepath"
	"testing"

	"spacebrowser/internal/platform"
)

// Synthetic allocation and identities make these tests independent of the
// host filesystem's support for hard links and resident small-file storage.
type smallHardLinkFilesystem struct {
	platform.API
	unknownLinkCount bool
	untrusted        bool
	distinct         bool
}

func (fs smallHardLinkFilesystem) ReadDir(path string) ([]platform.DirectoryEntry, error) {
	entries, err := fs.API.ReadDir(path)
	for i := range entries {
		if entries[i].IsDir() {
			continue
		}
		info, infoErr := entries[i].Info()
		if infoErr != nil {
			return nil, infoErr
		}
		usage := fs.UsageFor(filepath.Join(path, entries[i].Name()), info)
		if fs.unknownLinkCount || fs.untrusted {
			usage.LinkCount, usage.HasLinkCount = 0, false
		}
		if fs.untrusted {
			usage.Identity = platform.FileIdentity{Volume: 1, Low: 1}
			usage.IdentityNeedsConfirmation = true
		}
		entries[i].Usage, entries[i].HasUsage = usage, true
	}
	return entries, err
}

func (fs smallHardLinkFilesystem) UsageFor(path string, _ os.FileInfo) platform.FileUsage {
	id, links := uint64(1), uint64(2)
	if filepath.Base(path) == "ordinary.bin" {
		id, links = 2, 1
	} else if fs.distinct {
		links = 1
		if filepath.Base(filepath.Dir(path)) == "right" {
			id = 3
		}
	}
	return platform.FileUsage{
		AllocatedSize: 4096, Identity: platform.FileIdentity{Volume: 1, Low: id},
		HasIdentity: true, LinkCount: links, HasLinkCount: true,
	}
}

func smallHardLinkFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	for _, dir := range []string{left, right} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "link.bin"), []byte("small"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(left, "ordinary.bin"), []byte("small"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, left, right
}

func scanSmallHardLinks(t *testing.T, path string, fs smallHardLinkFilesystem) (*Scanner, *Node, Profile) {
	t.Helper()
	profile := *defaultProfile()
	profile.SkipNetworkFS = false
	scanner := NewScannerWithFilesystem(&profile, 4, fs)
	var files, dirs int64
	root, err := scanner.buildTree(path, 0, -1, &files, &dirs)
	if err != nil {
		t.Fatal(err)
	}
	return scanner, root, profile
}

func TestSmallHardLinksPreventUnsafeCacheReuse(t *testing.T) {
	path, left, right := smallHardLinkFixture(t)
	fs := smallHardLinkFilesystem{API: platform.Impl}
	scanner, root, profile := scanSmallHardLinks(t, left, fs)
	if root.Size != 8192 || root.EntryFiles != 2 || !subtreeHasSharedAllocation(root) {
		t.Fatalf("small-file accounting or sharing metadata lost: %+v", root)
	}
	_, key := scanProfileCacheKey(profile)
	entry := &scanCacheEntry{
		rootPath: left, profileKey: key, root: root, nodes: scanner.Nodes(),
		directories: indexCachedDirectories(root), sharedAllocation: subtreeHasSharedAllocation(root),
		report: scanner.Report(), dirty: make(map[string]struct{}),
	}
	manager := newScanCacheManager(nil)
	t.Cleanup(manager.Close)
	manager.entries[scanMemoryCacheKey(left, key)] = entry
	if plan := manager.Prepare(path, profile); len(plan.directories) != 0 {
		t.Fatal("broader scan can reuse a subtree whose small files share allocation")
	}
	// Unchanged complete-tree reuse remains valid, but partial reuse does not.
	if plan := manager.Prepare(left, profile); len(plan.directories) == 0 {
		t.Fatal("unchanged complete tree was not reusable")
	}
	entry.dirty[canonicalCachePath(filepath.Join(left, "ordinary.bin"))] = struct{}{}
	if plan := manager.Prepare(left, profile); len(plan.directories) != 0 {
		t.Fatal("dirty tree with aggregated hard links allowed partial reuse")
	}
	_, full, _ := scanSmallHardLinks(t, path, fs)
	if full.Size != 8192 || full.EntryFiles != 3 {
		t.Fatalf("full scan must deduplicate links in %s and %s: %+v", left, right, full)
	}
}

func TestSmallHardLinksRetainLateIdentityDiscoveries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fs     smallHardLinkFilesystem
		shared bool
		bytes  int64
	}{
		{"trusted-without-link-count", smallHardLinkFilesystem{unknownLinkCount: true}, true, 8192},
		{"confirmed-untrusted", smallHardLinkFilesystem{untrusted: true}, true, 8192},
		{"unconfirmed-collision", smallHardLinkFilesystem{untrusted: true, distinct: true}, false, 12288},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _, _ := smallHardLinkFixture(t)
			tc.fs.API = platform.Impl
			_, root, _ := scanSmallHardLinks(t, path, tc.fs)
			if root.Size != tc.bytes || root.EntryFiles != 3 {
				t.Fatalf("wrong allocation/count after identity resolution: %+v", root)
			}
			for _, dir := range root.Children {
				if got := subtreeHasSharedAllocation(dir); got != tc.shared {
					t.Errorf("%s shared = %t, want %t", dir.Name, got, tc.shared)
				}
			}
		})
	}
}

func TestSmallHardLinkSafetySurvivesTreeCopies(t *testing.T) {
	path, left, _ := smallHardLinkFixture(t)
	scanner, root, _ := scanSmallHardLinks(t, path, smallHardLinkFilesystem{API: platform.Impl})
	clone, nodes := cloneTreePreservingIDs(root, len(scanner.Nodes()))
	if !subtreeHasSharedAllocation(clone) {
		t.Fatal("tree copy lost aggregate sharing metadata")
	}
	store := &TreeStore{}
	store.ReplaceShared(clone, nodes, root.EntryFiles, root.EntryDirs)
	var target *Node
	for _, node := range nodes {
		if node.FullPath == left {
			target = node
		}
	}
	if target == nil {
		t.Fatal("scanned folder missing")
	}
	result, err := store.DeleteNode(target.ID, nil, nil, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !result.RescanRequired {
		t.Fatal("deleting a folder containing aggregated hard links must request a rescan")
	}
}
