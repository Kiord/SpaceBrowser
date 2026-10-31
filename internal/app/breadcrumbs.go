package app

import (
	"fmt"
	"slices"
)

// GetBreadcrumbs resolves ancestors from the displayed snapshot, including
// folders outside the currently laid-out subtree. Bind the ID to its path so
// an asynchronous request cannot silently target a reused node ID.
func (a *App) GetBreadcrumbs(nodeID int, path string) ([]Rect, error) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	if nodeID < 0 || nodeID >= len(a.store.nodes) || a.store.nodes[nodeID] == nil || path == "" || a.store.nodes[nodeID].FullPath != path {
		return nil, fmt.Errorf("the viewed folder is no longer available")
	}
	crumbs := []Rect{}
	seen := make(map[int]bool)
	for id := nodeID; id >= 0; {
		if id >= len(a.store.nodes) || a.store.nodes[id] == nil || seen[id] {
			return nil, fmt.Errorf("the viewed folder's ancestors are no longer available")
		}
		node := a.store.nodes[id]
		if !node.IsFolder || node.IsFreeSpace || node.IsSmallFiles || node.FullPath == "" {
			return nil, fmt.Errorf("breadcrumbs require scanned folders")
		}
		seen[id] = true
		emitRect(&crumbs, node, 0, 0, 0, 0)
		crumb := &crumbs[len(crumbs)-1]
		if a.desktop != nil {
			crumb.IsTrashRoot = a.desktop.IsTrashRoot(crumb.FullPath)
			crumb.IsInTrash = crumb.IsTrashRoot || a.desktop.IsInTrash(crumb.FullPath)
		}
		id = node.ParentID
	}
	slices.Reverse(crumbs)
	return crumbs, nil
}
