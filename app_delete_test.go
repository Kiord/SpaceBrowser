package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
	result, err := store.DeleteNode(folder.ID, func(path string) error {
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

	if _, err := store.DeleteNode(file.ID, func(string) error { return wantErr }); !errors.Is(err, wantErr) {
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
	if _, err := store.DeleteNode(root.ID, func(string) error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("deleteNode(root) error = nil")
	}
	if called {
		t.Fatal("moveToTrash was called for the root")
	}
}
