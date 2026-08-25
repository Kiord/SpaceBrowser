package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"spacebrowser/internal/platform"
	"strings"
	"testing"
)

type trashActionDesktop struct {
	platform.DesktopActions
	path               string
	emptied            *bool
	permanentlyDeleted *string
	restored           *string
	originalPath       string
	moveDestination    string
}

type globalTrashDesktop struct {
	platform.DesktopActions
	roots []string
}

func (desktop globalTrashDesktop) IsTrashRoot(path string) bool {
	for _, root := range desktop.roots {
		if filepath.Clean(path) == filepath.Clean(root) {
			return true
		}
	}
	return false
}

func (desktop globalTrashDesktop) EmptyTrash(path string) error {
	if !desktop.IsTrashRoot(path) {
		return errors.New("unexpected Trash path")
	}
	for _, root := range desktop.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (desktop trashActionDesktop) MoveToTrash(path string) error {
	if desktop.moveDestination == "" {
		return errors.New("move to Trash was not expected")
	}
	if err := os.MkdirAll(filepath.Dir(desktop.moveDestination), 0o700); err != nil {
		return err
	}
	return os.Rename(path, desktop.moveDestination)
}

func (desktop trashActionDesktop) IsInTrash(path string) bool {
	return path == desktop.path || strings.HasPrefix(path, desktop.path+string(os.PathSeparator))
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

func (desktop trashActionDesktop) TrashRestoreInfo(path string) (platform.TrashRestoreInfo, error) {
	if !desktop.IsInTrash(path) || desktop.IsTrashRoot(path) {
		return platform.TrashRestoreInfo{}, errors.New("unexpected Trash item")
	}
	return platform.TrashRestoreInfo{TargetPath: path, OriginalPath: desktop.originalPath}, nil
}

func (desktop trashActionDesktop) RestoreTrashItem(path string) error {
	if desktop.restored == nil {
		return errors.New("restore was not expected")
	}
	*desktop.restored = path
	return nil
}

func (desktop trashActionDesktop) DeleteTrashItemPermanently(path string) error {
	if desktop.permanentlyDeleted == nil {
		return errors.New("permanent deletion was not expected")
	}
	*desktop.permanentlyDeleted = path
	return nil
}

func (desktop trashActionDesktop) DeletePermanently(path string) error {
	if desktop.permanentlyDeleted == nil {
		return errors.New("permanent deletion was not expected")
	}
	*desktop.permanentlyDeleted = path
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
	result, err := store.DeleteNode(folder.ID, nil, nil, func(path string) error {
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

	if _, err := store.DeleteNode(file.ID, nil, nil, func(string) error { return wantErr }); !errors.Is(err, wantErr) {
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
	if _, err := store.DeleteNode(root.ID, nil, nil, func(string) error {
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

	result, err := store.DeleteNode(file.ID, nil, nil, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !result.RescanRequired {
		t.Fatal("deleting a multiply linked file did not require a rescan")
	}
}

func TestTreeStoreRefreshesDisplayedRecycleBinAfterMove(t *testing.T) {
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

	isTrashRoot := func(path string) bool { return path == recycleBin.FullPath }
	result, err := store.DeleteNode(folder.ID, isTrashRoot, nil, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 2 || result.DirCount != 2 || len(result.trashRefreshes) != 1 {
		t.Fatalf("result before Trash refresh = %+v", result)
	}
	if root.Size != 40 || recycleBin.Size != 10 {
		t.Fatalf("sizes before Trash refresh = root %d, Recycle Bin %d; want 40 and 10", root.Size, recycleBin.Size)
	}
	if store.nodes[folder.ID] != nil || store.nodes[nested.ID] != nil {
		t.Fatal("moved subtree remained addressable at its old location")
	}

	trashedFolderPath := filepath.Join(recycleBin.FullPath, "owner", "$Rfolder")
	scannedTrash := &Node{Name: "$Recycle.Bin", FullPath: recycleBin.FullPath, Size: 70, IsFolder: true}
	scannedExisting := &Node{Name: "existing.bin", FullPath: filepath.Join(recycleBin.FullPath, "existing.bin"), Size: 10}
	scannedFolder := &Node{Name: "$Rfolder", FullPath: trashedFolderPath, Size: 60, IsFolder: true}
	scannedNested := &Node{Name: "nested.bin", FullPath: filepath.Join(trashedFolderPath, "nested.bin"), Size: 60}
	scannedFolder.Children = []*Node{scannedNested}
	scannedTrash.Children = []*Node{scannedFolder, scannedExisting}
	result, err = store.ReplaceSubtree(recycleBin.ID, scannedTrash, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 3 || result.DirCount != 3 || root.Size != 100 || recycleBin.Size != 70 {
		t.Fatalf("result after Trash refresh = %+v, root size %d, Trash size %d", result, root.Size, recycleBin.Size)
	}
	if len(recycleBin.Children) != 2 || recycleBin.Children[0].FullPath != trashedFolderPath {
		t.Fatalf("refreshed Recycle Bin children = %+v", recycleBin.Children)
	}
	if recycleBin.Children[0].ID < 0 || store.nodes[recycleBin.Children[0].ID] != recycleBin.Children[0] {
		t.Fatal("refreshed Trash item is not addressable by its new node ID")
	}
}

func TestTreeStoreProtectsAndEmptiesTrashRoot(t *testing.T) {
	trashPath := filepath.Join(t.TempDir(), "$Recycle.Bin")
	if err := os.Mkdir(trashPath, 0o700); err != nil {
		t.Fatal(err)
	}

	root := &Node{ID: 0, ParentID: -1, Name: "root", Size: 75, IsFolder: true}
	trash := &Node{ID: 1, ParentID: 0, Name: "$Recycle.Bin", FullPath: trashPath, Size: 60, IsFolder: true}
	nested := &Node{ID: 2, ParentID: 1, Name: "owner", FullPath: filepath.Join(trashPath, "owner"), Size: 60, IsFolder: true}
	file := &Node{ID: 3, ParentID: 2, Name: "deleted.bin", FullPath: filepath.Join(trashPath, "owner", "deleted.bin"), Size: 60}
	kept := &Node{ID: 4, ParentID: 0, Name: "kept.bin", Size: 15}
	root.Children = []*Node{trash, kept}
	trash.Children = []*Node{nested}
	nested.Children = []*Node{file}
	store := &TreeStore{root: root, nodes: []*Node{root, trash, nested, file, kept}, fileCount: 2, dirCount: 3}
	isTrashRoot := func(path string) bool { return path == trashPath }
	isInTrash := func(path string) bool {
		return path == trashPath || strings.HasPrefix(path, trashPath+string(os.PathSeparator))
	}

	moved := false
	if _, err := store.DeleteNode(trash.ID, isTrashRoot, isInTrash, func(string) error {
		moved = true
		return nil
	}); err == nil {
		t.Fatal("DeleteNode() allowed a Trash root to be moved")
	}
	if moved {
		t.Fatal("MoveToTrash was called for a Trash root")
	}
	if _, err := store.DeleteNode(file.ID, isTrashRoot, isInTrash, func(string) error {
		moved = true
		return nil
	}); err == nil {
		t.Fatal("DeleteNode() allowed an item inside Trash to be moved")
	}
	if moved {
		t.Fatal("MoveToTrash was called for an item inside Trash")
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

func TestAppEmptyGlobalTrashRefreshesEveryDisplayedTrashRoot(t *testing.T) {
	rootPath := t.TempDir()
	firstPath := filepath.Join(rootPath, ".Trash-1000")
	secondPath := filepath.Join(rootPath, ".Trash-1001")
	for _, trashPath := range []string{firstPath, secondPath} {
		if err := os.Mkdir(trashPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(trashPath, "deleted.bin"), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	root := &Node{ID: 0, ParentID: -1, Name: "root", FullPath: rootPath, Size: 8, IsFolder: true, EntryFiles: 2, EntryDirs: 3}
	first := &Node{ID: 1, ParentID: 0, Name: ".Trash-1000", FullPath: firstPath, Size: 4, IsFolder: true, Depth: 1, EntryFiles: 1, EntryDirs: 1}
	firstFile := &Node{ID: 2, ParentID: 1, Name: "deleted.bin", FullPath: filepath.Join(firstPath, "deleted.bin"), Size: 4, Depth: 2}
	second := &Node{ID: 3, ParentID: 0, Name: ".Trash-1001", FullPath: secondPath, Size: 4, IsFolder: true, Depth: 1, EntryFiles: 1, EntryDirs: 1}
	secondFile := &Node{ID: 4, ParentID: 3, Name: "deleted.bin", FullPath: filepath.Join(secondPath, "deleted.bin"), Size: 4, Depth: 2}
	root.Children = []*Node{first, second}
	first.Children = []*Node{firstFile}
	second.Children = []*Node{secondFile}
	app := &App{
		ctx:        context.Background(),
		profile:    Profile{AllowDelete: true},
		desktop:    globalTrashDesktop{roots: []string{firstPath, secondPath}},
		filesystem: platform.Impl,
		store: TreeStore{
			root: root, nodes: []*Node{root, first, firstFile, second, secondFile},
			fileCount: 2, dirCount: 3,
		},
	}

	result, err := app.DeleteNode(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.RescanRequired {
		t.Fatalf("global Trash refresh unexpectedly requires a full rescan: %+v", result)
	}
	if result.FileCount != 0 || result.DirCount != 3 {
		t.Fatalf("counts after refreshing every Trash root = (%d, %d), want (0, 3)", result.FileCount, result.DirCount)
	}
	if len(first.Children) != 0 || len(second.Children) != 0 || first.Size != 0 || second.Size != 0 || root.Size != 0 {
		t.Fatalf("displayed Trash roots remained stale: first=%+v second=%+v root size=%d", first, second, root.Size)
	}
}

func TestAppTargetedTrashRefreshMakesMovedItemActionable(t *testing.T) {
	rootPath := t.TempDir()
	trashPath := filepath.Join(rootPath, "$Recycle.Bin")
	if err := os.Mkdir(trashPath, 0o700); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(rootPath, "deleted.bin")
	if err := os.WriteFile(targetPath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	trashDestination := filepath.Join(trashPath, "$Rdeleted.bin")
	root := &Node{ID: 0, ParentID: -1, Name: "root", FullPath: rootPath, Size: 4, IsFolder: true, EntryFiles: 1, EntryDirs: 2}
	target := &Node{ID: 1, ParentID: 0, Name: "deleted.bin", FullPath: targetPath, Size: 4, Depth: 1}
	trash := &Node{ID: 2, ParentID: 0, Name: "$Recycle.Bin", FullPath: trashPath, IsFolder: true, Depth: 1, EntryDirs: 1}
	root.Children = []*Node{target, trash}
	desktop := trashActionDesktop{path: trashPath, moveDestination: trashDestination}
	app := &App{
		ctx:        context.Background(),
		profile:    Profile{AllowDelete: true},
		desktop:    desktop,
		filesystem: platform.Impl,
		store:      TreeStore{root: root, nodes: []*Node{root, target, trash}, fileCount: 1, dirCount: 2},
	}

	result, err := app.DeleteNode(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.RescanRequired || result.FileCount != 1 || result.DirCount != 2 {
		t.Fatalf("targeted refresh result = %+v", result)
	}
	if len(trash.Children) != 1 || trash.Children[0].FullPath != trashDestination {
		t.Fatalf("refreshed Trash children = %+v", trash.Children)
	}
	if root.EntryFiles != 1 || root.EntryDirs != 2 || trash.EntryFiles != 1 || trash.EntryDirs != 1 {
		t.Fatalf("entry counts after targeted refresh = root(%d, %d), Trash(%d, %d)", root.EntryFiles, root.EntryDirs, trash.EntryFiles, trash.EntryDirs)
	}
	moved := trash.Children[0]
	if !app.store.NodePathMatches(moved.ID, desktop.IsInTrash) {
		t.Fatal("refreshed Trash item is not actionable as Trash content")
	}
}

func TestAppSkipsTargetedTrashRefreshWhenDeleteWillRescan(t *testing.T) {
	rootPath := t.TempDir()
	trashPath := filepath.Join(rootPath, "$Recycle.Bin")
	if err := os.Mkdir(trashPath, 0o755); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(rootPath, "target.txt")
	if err := os.WriteFile(targetPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	trashDestination := filepath.Join(trashPath, "target.txt")

	root := &Node{ID: 0, ParentID: -1, Name: "root", FullPath: rootPath, Size: 4, IsFolder: true, EntryFiles: 1, EntryDirs: 2}
	target := &Node{ID: 1, ParentID: 0, Name: "target.txt", FullPath: targetPath, Size: 4, Depth: 1}
	trash := &Node{ID: 2, ParentID: 0, Name: "$Recycle.Bin", FullPath: trashPath, IsFolder: true, Depth: 1, EntryDirs: 1}
	root.Children = []*Node{target, trash}
	app := &App{
		profile:    Profile{AllowDelete: true, RescanOnDelete: true},
		desktop:    trashActionDesktop{path: trashPath, moveDestination: trashDestination},
		filesystem: platform.Impl,
		store:      TreeStore{root: root, nodes: []*Node{root, target, trash}, fileCount: 1, dirCount: 2},
	}

	result, err := app.DeleteNode(target.ID)
	if err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if _, err := os.Stat(trashDestination); err != nil {
		t.Fatalf("trashed file was not moved: %v", err)
	}
	if len(result.trashRefreshes) != 0 {
		t.Fatalf("trash refreshes = %d, want 0 before a full rescan", len(result.trashRefreshes))
	}
	if len(trash.Children) != 0 {
		t.Fatalf("trash children = %d, want no targeted refresh before a full rescan", len(trash.Children))
	}
}

func TestAppTrashItemPermanentDeletionOnlyRequiresDeletePermission(t *testing.T) {
	trashPath := filepath.Join(t.TempDir(), "$Recycle.Bin")
	itemPath := filepath.Join(trashPath, "deleted.bin")
	root := &Node{ID: 0, ParentID: -1, IsFolder: true}
	trash := &Node{ID: 1, ParentID: 0, FullPath: trashPath, IsFolder: true}
	item := &Node{ID: 2, ParentID: 1, FullPath: itemPath, Size: 12}
	root.Children = []*Node{trash}
	trash.Children = []*Node{item}
	deleted := ""
	app := &App{
		profile: Profile{AllowDelete: true},
		desktop: trashActionDesktop{path: trashPath, permanentlyDeleted: &deleted},
		store:   TreeStore{root: root, nodes: []*Node{root, trash, item}, fileCount: 1, dirCount: 2},
	}

	result, err := app.DeleteNode(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != itemPath || !result.RescanRequired {
		t.Fatalf("permanent deletion path = %q, result = %+v", deleted, result)
	}
}

func TestAppPermanentDeleteModeBypassesTrash(t *testing.T) {
	base := t.TempDir()
	targetPath := filepath.Join(base, "delete-me.bin")
	if err := os.WriteFile(targetPath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	trashPath := filepath.Join(base, "$Recycle.Bin")
	root := &Node{ID: 0, ParentID: -1, IsFolder: true}
	target := &Node{ID: 1, ParentID: 0, FullPath: targetPath, Size: 4}
	trash := &Node{ID: 2, ParentID: 0, FullPath: trashPath, IsFolder: true}
	root.Children = []*Node{target, trash}
	deleted := ""
	app := &App{
		profile: Profile{AllowDelete: true, AllowPermanentDelete: true},
		desktop: trashActionDesktop{path: trashPath, permanentlyDeleted: &deleted},
		store:   TreeStore{root: root, nodes: []*Node{root, target, trash}, fileCount: 1, dirCount: 2},
	}

	result, err := app.DeleteNode(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != targetPath {
		t.Fatalf("permanently deleted path = %q, want %q", deleted, targetPath)
	}
	if len(result.trashRefreshes) != 0 {
		t.Fatalf("permanent deletion requested %d Trash refreshes", len(result.trashRefreshes))
	}
}

func TestAppRestoresTrashItemToReportedOriginalLocation(t *testing.T) {
	trashPath := filepath.Join(t.TempDir(), "$Recycle.Bin")
	itemPath := filepath.Join(trashPath, "deleted.bin")
	originalPath := filepath.Join(t.TempDir(), "original.bin")
	root := &Node{ID: 0, ParentID: -1, IsFolder: true}
	trash := &Node{ID: 1, ParentID: 0, FullPath: trashPath, IsFolder: true}
	item := &Node{ID: 2, ParentID: 1, FullPath: itemPath, Size: 12}
	root.Children = []*Node{trash}
	trash.Children = []*Node{item}
	restored := ""
	app := &App{
		desktop: trashActionDesktop{path: trashPath, restored: &restored, originalPath: originalPath},
		store:   TreeStore{root: root, nodes: []*Node{root, trash, item}, fileCount: 1, dirCount: 2},
	}

	details, err := app.GetTrashRestoreInfo(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if details.OriginalPath != originalPath {
		t.Fatalf("original path = %q, want %q", details.OriginalPath, originalPath)
	}
	result, err := app.RestoreNode(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored != itemPath || !result.RescanRequired {
		t.Fatalf("restored path = %q, result = %+v", restored, result)
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
