package app

import (
	"fmt"
	"testing"
)

func TestDirectoryListIncludesTinyEntriesAndPaginates(t *testing.T) {
	root := &Node{ID: 0, ParentID: -1, IsFolder: true, FullPath: "/root"}
	nodes := []*Node{root}
	for id := 1; id <= 205; id++ {
		node := &Node{ID: id, ParentID: 0, Name: fmt.Sprintf("file-%03d", 206-id), FullPath: fmt.Sprintf("/root/%d", id), Size: int64(id)}
		root.Children = append(root.Children, node)
		nodes = append(nodes, node)
	}
	root.Children = append(root.Children, &Node{ID: -1, IsFreeSpace: true})
	a := &App{store: TreeStore{root: root, nodes: nodes}}
	page, err := a.ListDirectory(0, "size", 0)
	if err != nil || page.Total != 205 || len(page.Items) != 200 || page.Items[0].NodeID != 205 {
		t.Fatalf("size page: %+v, %v", page, err)
	}
	last, err := a.ListDirectory(0, "size", 200)
	if err != nil || len(last.Items) != 5 || last.Items[4].Size != 1 {
		t.Fatalf("tiny entries missing: %+v, %v", last, err)
	}
	byName, err := a.ListDirectory(0, "name", 0)
	if err != nil || byName.Items[0].Name != "file-001" {
		t.Fatalf("name order: %+v, %v", byName, err)
	}
	if _, err := a.ListDirectory(1, "size", 0); err == nil {
		t.Fatal("non-folder accepted")
	}
}
