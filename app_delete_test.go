package main

import (
	"errors"
	"os"
	"path/filepath"
	"spacebrowser/internal/platform"
	"testing"
)

type trashActionDesktop struct {
	platform.DesktopActions
	path    string
	emptied *bool
}

func (desktop trashActionDesktop) IsTrashRoot(path string) bool {
	return path == desktop.path
}

func (desktop trashActionDesktop) EmptyTrash(path string) error {
	if path != desktop.path {
		return errors.New("unexpected Trash path")
	}
	*desktop.emptied = true
	return nil
}

func TestTreeStoreDeleteNodeUpdatesTree(t *testing.T) {
	target := filepath.Join(t.TempDir(), "folder")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	root := &Node{ID: 0, ParentID: -1, Name: "root", Size: 100, IsFolder: true}
	folder := &Node{ID: 1, ParentID: 0, Name: "folder", FullPath: target, Size: 60, IsFolder: true}
	nested := &Node{ID: 2, ParentID: 1, Name: "nested", FullPath: filepath.Join(target, "nested"), Size: 60}
	kept := &Node{ID: 3, ParentID: 0, Name: "kept", FullPath: filepath.Join(filepath.Dir(target), "kept"), Size: 40}
	small := &Node{ID: -1, ParentID: 0, Name: "small", IsSmallFiles: true, SmallFileCount: 2}
	root.Children = []*Node{folder, kept, small}
	folder.Children = []*Node{nested}
	store := &TreeStore{root: root, nodes: []*Node{root, folder, nested, kept}, fileCount: 4, dirCount: 2}

	calledWith := ""
	result, err := store.DeleteNode(folder.ID, nil, func(path string) error {
		calledWith = path
		return nil
	})
	if err != nil {
		t.Fatalf("deleteNode() error = %v", err)
	}
	if calledWith != target {
		t.Fatalf("moveToTrash called with %q, want %q", calledWith, target)
	}
	if result.FileCount != 3 || result.DirCount != 1 {
		t.Fatalf("counts = (%d, %d), want (3, 1)", result.FileCount, result.DirCount)
	}
	if root.Size != 40 || len(root.Children) != 2 || root.Children[0] != kept {
		t.Fatalf("root was not updated after deletion: %#v", root)
	}
	if store.nodes[folder.ID] != nil || store.nodes[nested.ID] != nil {
		t.Fatal("deleted subtree remains addressable")
	}
}

func TestTreeStoreDeleteNodeLeavesTreeUntouchedWhenTrashFails(t *testing.T) {
	target := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := &Node{ID: 0, ParentID: -1, Size: 4, IsFolder: true}
	file := &Node{ID: 1, ParentID: 0, FullPath: target, Size: 4}
	root.Children = []*Node{file}
	store := &TreeStore{root: root, nodes: []*Node{root, file}, fileCount: 1, dirCount: 1}
	wantErr := errors.New("trash unavailable")

	if _, err := store.DeleteNode(file.ID, nil, func(string) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("deleteNode() error = %v, want %v", err, wantErr)
	}
	if root.Size != 4 || len(root.Children) != 1 || store.nodes[file.ID] != file {
		t.Fatal("tree changed after the OS trash operation failed")
	}
}

func TestTreeStoreDeleteNodeRejectsRoot(t *testing.T) {
	root := &Node{ID: 0, ParentID: -1, Name: "root", IsFolder: true}
	store := &TreeStore{root: root, nodes: []*Node{root}}
	called := false
	if _, err := store.DeleteNode(root.ID, nil, func(string) error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("deleteNode(root) error = nil")
	}
	if called {
		t.Fatal("moveToTrash was called for the root")
	}
}

func TestTreeStoreDeleteNodeRequiresRescanForSharedAllocation(t *testing.T) {
	target := filepath.Join(t.TempDir(), "hard-link.bin")
	if err := os.WriteFile(target, []byte("shared"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := &Node{ID: 0, ParentID: -1, Size: 6, IsFolder: true}
	file := &Node{ID: 1, ParentID: 0, FullPath: target, Size: 6, LinkCount: 2}
	root.Children = []*Node{file}
	store := &TreeStore{root: root, nodes: []*Node{root, file}, fileCount: 1, dirCount: 1}

	result, err := store.DeleteNode(file.ID, nil, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !result.RescanRequired {
		t.Fatal("deleting a multiply linked file did not require a rescan")
	}
}

func TestTreeStoreMovesDeletedSubtreeIntoDisplayedRecycleBin(t *testing.T) {
	target := filepath.Join(t.TempDir(), "folder")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	root := &Node{ID: 0, ParentID: -1, Name: "root", Size: 100, IsFolder: true}
	folder := &Node{ID: 1, ParentID: 0, Name: "folder", FullPath: target, Size: 60, IsFolder: true, Depth: 1}
	nested := &Node{ID: 2, ParentID: 1, Name: "nested.bin", FullPath: filepath.Join(target, "nested.bin"), Size: 60, Depth: 2}
	recycleBin := &Node{ID: 3, ParentID: 0, Name: "$Recycle.Bin", FullPath: filepath.Join(filepath.Dir(target), "$Recycle.Bin"), Size: 10, IsFolder: true, Depth: 1}
	existingTrash := &Node{ID: 4, ParentID: 3, Name: "existing.bin", Size: 10, Depth: 2}
	kept := &Node{ID: 5, ParentID: 0, Name: "kept.bin", Size: 30, Depth: 1}
	root.Children = []*Node{folder, kept, recycleBin}
	folder.Children = []*Node{nested}
	recycleBin.Children = []*Node{existingTrash}
	store := &TreeStore{
		root: root, nodes: []*Node{root, folder, nested, recycleBin, existingTrash, kept},
		fileCount: 3, dirCount: 3,
	}

	result, err := store.DeleteNode(folder.ID, nil, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 3 || result.DirCount != 3 {
		t.Fatalf("counts after virtual move = (%d, %d), want unchanged (3, 3)", result.FileCount, result.DirCount)
	}
	if root.Size != 100 || recycleBin.Size != 70 {
		t.Fatalf("sizes after virtual move = root %d, Recycle Bin %d; want 100 and 70", root.Size, recycleBin.Size)
	}
	if folder.ParentID != recycleBin.ID || len(recycleBin.Children) != 2 || recycleBin.Children[0] != folder {
		t.Fatalf("Recycle Bin children after virtual move = %+v", recycleBin.Children)
	}
	if folder.FullPath != "" || nested.FullPath != "" || folder.Depth != 2 || nested.Depth != 3 {
		t.Fatalf("moved subtree metadata = folder %+v, nested %+v", folder, nested)
	}
	if store.nodes[folder.ID] != folder || store.nodes[nested.ID] != nested {
		t.Fatal("virtually moved subtree is no longer visitable")
	}
}

func TestTreeStoreProtectsAndEmptiesTrashRoot(t *testing.T) {
	trashPath := filepath.Join(t.TempDir(), "$Recycle.Bin")
	if err := os.Mkdir(trashPath, 0o700); err != nil {
		t.Fatal(err)
	}

	root := &Node{ID: 0, ParentID: -1, Name: "root", Size: 75, IsFolder: true}
	trash := &Node{ID: 1, ParentID: 0, Name: "$Recycle.Bin", FullPath: trashPath, Size: 60, IsFolder: true}
	nested := &Node{ID: 2, ParentID: 1, Name: "owner", Size: 60, IsFolder: true}
	file := &Node{ID: 3, ParentID: 2, Name: "deleted.bin", Size: 60}
	kept := &Node{ID: 4, ParentID: 0, Name: "kept.bin", Size: 15}
	root.Children = []*Node{trash, kept}
	trash.Children = []*Node{nested}
	nested.Children = []*Node{file}
	store := &TreeStore{root: root, nodes: []*Node{root, trash, nested, file, kept}, fileCount: 2, dirCount: 3}
	isTrashRoot := func(path string) bool { return path == trashPath }

	moved := false
	if _, err := store.DeleteNode(trash.ID, isTrashRoot, func(string) error {
		moved = true
		return nil
	}); err == nil {
		t.Fatal("DeleteNode() allowed a Trash root to be moved")
	}
	if moved {
		t.Fatal("MoveToTrash was called for a Trash root")
	}

	emptyCalledWith := ""
	result, err := store.EmptyTrashNode(trash.ID, isTrashRoot, func(path string) error {
		emptyCalledWith = path
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if emptyCalledWith != trashPath {
		t.Fatalf("EmptyTrash called with %q, want %q", emptyCalledWith, trashPath)
	}
	if trash.Size != 0 || len(trash.Children) != 0 || root.Size != 15 {
		t.Fatalf("tree after emptying Trash = root size %d, Trash size %d, children %d", root.Size, trash.Size, len(trash.Children))
	}
	if result.FileCount != 1 || result.DirCount != 2 {
		t.Fatalf("counts after emptying Trash = (%d, %d), want (1, 2)", result.FileCount, result.DirCount)
	}
	if store.nodes[nested.ID] != nil || store.nodes[file.ID] != nil || store.nodes[trash.ID] != trash {
		t.Fatal("emptying Trash detached the wrong nodes")
	}
}

func TestAppDeleteCommandRoutesTrashRootToEmptyTrash(t *testing.T) {
	trashPath := filepath.Join(t.TempDir(), "$Recycle.Bin")
	if err := os.Mkdir(trashPath, 0o700); err != nil {
		t.Fatal(err)
	}
	root := &Node{ID: 0, ParentID: -1, Size: 10, IsFolder: true}
	trash := &Node{ID: 1, ParentID: 0, FullPath: trashPath, Size: 10, IsFolder: true}
	file := &Node{ID: 2, ParentID: 1, Size: 10}
	root.Children = []*Node{trash}
	trash.Children = []*Node{file}
	emptied := false
	app := &App{
		profile: Profile{AllowDelete: true},
		desktop: trashActionDesktop{path: trashPath, emptied: &emptied},
		store:   TreeStore{root: root, nodes: []*Node{root, trash, file}, fileCount: 1, dirCount: 2},
	}

	result, err := app.DeleteNode(trash.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !emptied {
		t.Fatal("DeleteNode did not route the Trash root to EmptyTrash")
	}
	if result.FileCount != 0 || result.DirCount != 2 || trash.Size != 0 {
		t.Fatalf("result after routed EmptyTrash = %+v, Trash size %d", result, trash.Size)
	}
}

func TestTreeStoreUpdatesFreeSpaceAccounting(t *testing.T) {
	root := &Node{ID: 0, ParentID: -1, FullPath: t.TempDir(), Size: 600, IsFolder: true, DiskTotal: 1000, DiskFree: 400}
	used := &Node{ID: 1, ParentID: 0, Name: "used", Size: 600}
	free := &Node{ID: -1, ParentID: 0, Name: "free", Size: 400, IsFreeSpace: true, DiskTotal: 1000}
	root.Children = []*Node{used, free}
	store := &TreeStore{root: root, nodes: []*Node{root, used}}

	path, ok := store.DiskUsageRootPath()
	if !ok || path != root.FullPath {
		t.Fatalf("DiskUsageRootPath() = %q, %t; want %q, true", path, ok, root.FullPath)
	}
	if !store.UpdateDiskUsage(1400, 800) {
		t.Fatal("UpdateDiskUsage() did not find the free-space node")
	}
	if root.DiskTotal != 1400 || root.DiskFree != 800 || free.DiskTotal != 1400 || free.Size != 800 {
		t.Fatalf("updated disk accounting = root(%d total, %d free), node(%d total, %d free)", root.DiskTotal, root.DiskFree, free.DiskTotal, free.Size)
	}
	if root.Children[0] != free || root.Children[1] != used {
		t.Fatal("disk usage update did not retain size ordering")
	}
}

func TestAppsOwnIndependentTreeStores(t *testing.T) {
	root := &Node{ID: 0, ParentID: -1, Name: "first", IsFolder: true}
	first := &App{}
	second := &App{}
	first.store.Replace(root, []*Node{root}, 0, 1)

	if _, err := first.Layout(root.ID, 100, 100, 1); err != nil {
		t.Fatalf("first app Layout() error = %v", err)
	}
	if _, err := second.Layout(root.ID, 100, 100, 1); err == nil {
		t.Fatal("second app unexpectedly accessed the first app's tree")
	}
}
