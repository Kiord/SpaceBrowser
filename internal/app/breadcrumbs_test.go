package app

import (
	"path/filepath"
	"testing"
)

func TestBreadcrumbsFollowSnapshotParentsAndRetainActionMetadata(t *testing.T) {
	rootPath := t.TempDir()
	root := &Node{ID: 0, ParentID: -1, IsFolder: true, Name: "scan root", FullPath: rootPath, Size: 100}
	parent := &Node{ID: 2, ParentID: 0, IsFolder: true, Name: "parent", FullPath: filepath.Join(rootPath, "parent"), Size: 50}
	current := &Node{ID: 3, ParentID: 2, IsFolder: true, Name: "文件", FullPath: filepath.Join(parent.FullPath, "文件"), Size: 10, ScanIncomplete: true}
	a := &App{store: TreeStore{root: root, nodes: []*Node{root, nil, parent, current}}, desktop: trashActionDesktop{path: rootPath}}
	crumbs, err := a.GetBreadcrumbs(3, current.FullPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(crumbs) != 3 || crumbs[0].NodeID != 0 || crumbs[1].NodeID != 2 || crumbs[2].NodeID != 3 {
		t.Fatalf("ancestor order = %+v", crumbs)
	}
	if !crumbs[0].IsTrashRoot || !crumbs[1].IsInTrash || crumbs[1].Size != 50 || crumbs[1].FullPath != parent.FullPath || !crumbs[2].ScanIncomplete {
		t.Fatalf("missing action metadata: %+v", crumbs)
	}
	if rootOnly, err := a.GetBreadcrumbs(0, rootPath); err != nil || len(rootOnly) != 1 {
		t.Fatalf("root breadcrumbs = %+v, %v", rootOnly, err)
	}
	if _, err := a.GetBreadcrumbs(3, parent.FullPath); err == nil {
		t.Fatal("reused ID with stale path accepted")
	}
	parent.ParentID = 3
	if _, err := a.GetBreadcrumbs(3, current.FullPath); err == nil {
		t.Fatal("cyclic ancestors accepted")
	}
}

func TestBreadcrumbsRejectMissingAndNonFolderNodes(t *testing.T) {
	a := &App{store: TreeStore{nodes: []*Node{{ID: 0, ParentID: -1, Name: "file", FullPath: "file"}, nil}}}
	for _, id := range []int{-1, 0, 1, 99} {
		if _, err := a.GetBreadcrumbs(id, "file"); err == nil {
			t.Fatalf("invalid breadcrumb node %d accepted", id)
		}
	}
}
