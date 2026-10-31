package app

import (
	"fmt"
	"sort"
)

type DirectoryListing struct {
	Items    []Rect `json:"items"`
	Total    int    `json:"total"`
	Path     string `json:"path"`
	ParentID int    `json:"parentId"`
}

// ListDirectory includes entries too small to draw on the treemap. Bound each
// response so large directories remain usable by keyboard and assistive tools.
func (a *App) ListDirectory(nodeID int, order string, offset int) (DirectoryListing, error) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	if nodeID < 0 || nodeID >= len(a.store.nodes) || a.store.nodes[nodeID] == nil || !a.store.nodes[nodeID].IsFolder {
		return DirectoryListing{}, fmt.Errorf("folder is no longer available")
	}
	if offset < 0 || (order != "size" && order != "name") {
		return DirectoryListing{}, fmt.Errorf("invalid directory listing request")
	}
	node := a.store.nodes[nodeID]
	children := make([]*Node, 0, len(node.Children))
	for _, child := range node.Children {
		if !child.IsFreeSpace {
			children = append(children, child)
		}
	}
	sort.Slice(children, func(i, j int) bool {
		if order == "size" && children[i].Size != children[j].Size {
			return children[i].Size > children[j].Size
		}
		if children[i].Name != children[j].Name {
			return children[i].Name < children[j].Name
		}
		return children[i].ID < children[j].ID
	})
	result := DirectoryListing{Items: []Rect{}, Total: len(children), Path: node.FullPath, ParentID: node.ParentID}
	offset = min(offset, len(children))
	for _, child := range children[min(offset, len(children)):min(offset+200, len(children))] {
		emitRect(&result.Items, child, 0, 0, 0, 0)
		item := &result.Items[len(result.Items)-1]
		if a.desktop != nil && item.FullPath != "" {
			item.IsTrashRoot = item.IsFolder && a.desktop.IsTrashRoot(item.FullPath)
			item.IsInTrash = item.IsTrashRoot || a.desktop.IsInTrash(item.FullPath)
		}
	}
	return result, nil
}
