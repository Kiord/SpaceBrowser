package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// TreeStore owns the currently scanned tree and its dense node index.
// It also applies successful filesystem deletions to the in-memory model.
type TreeStore struct {
	mu        sync.RWMutex
	root      *Node
	nodes     []*Node // nodes[id] == *Node
	fileCount int
	dirCount  int
}

type DeleteResult struct {
	FileCount      int  `json:"fileCount"`
	DirCount       int  `json:"dirCount"`
	RescanRequired bool `json:"rescanRequired"`
}

func (s *TreeStore) Replace(root *Node, nodes []*Node, fileCount, dirCount int) {
	s.mu.Lock()
	s.root, s.nodes = root, nodes
	s.fileCount, s.dirCount = fileCount, dirCount
	s.mu.Unlock()
}

func (s *TreeStore) DiskUsageRootPath() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.root == nil || s.root.FullPath == "" {
		return "", false
	}
	for _, child := range s.root.Children {
		if child.IsFreeSpace {
			return s.root.FullPath, true
		}
	}
	return "", false
}

func (s *TreeStore) UpdateDiskUsage(total, free int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return false
	}
	for _, child := range s.root.Children {
		if !child.IsFreeSpace {
			continue
		}
		s.root.DiskTotal = total
		s.root.DiskFree = free
		child.Size = free
		child.DiskTotal = total
		sort.Slice(s.root.Children, func(i, j int) bool {
			return s.root.Children[i].Size > s.root.Children[j].Size
		})
		return true
	}
	return false
}

func (s *TreeStore) Layout(nodeID, width, height int, scale float64, showFreeSpace bool) ([]Rect, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if nodeID < 0 || nodeID >= len(s.nodes) {
		return nil, fmt.Errorf("invalid node_id")
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid width/height")
	}
	node := s.nodes[nodeID]
	if node == nil {
		return nil, fmt.Errorf("node not found")
	}

	viewRoot := *node
	if !showFreeSpace {
		viewRoot.Children = make([]*Node, 0, len(node.Children))
		for _, child := range node.Children {
			if !child.IsFreeSpace {
				viewRoot.Children = append(viewRoot.Children, child)
			}
		}
	}
	return ComputeTreemapRects(&viewRoot, float64(width), float64(height), scale), nil
}

func (s *TreeStore) DeleteNode(nodeID int, isTrashRoot func(string) bool, moveToTrash func(string) error) (DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if nodeID < 0 || nodeID >= len(s.nodes) || s.nodes[nodeID] == nil {
		return DeleteResult{}, fmt.Errorf("selected item is no longer available")
	}
	node := s.nodes[nodeID]
	if node.ParentID < 0 || node.FullPath == "" || node.IsFreeSpace || node.IsSmallFiles {
		return DeleteResult{}, fmt.Errorf("the scan root and virtual items cannot be deleted")
	}
	if isTrashRoot != nil && isTrashRoot(node.FullPath) {
		return DeleteResult{}, fmt.Errorf("the Trash root cannot be deleted; use Empty Trash instead")
	}
	if node.ParentID >= len(s.nodes) || s.nodes[node.ParentID] == nil {
		return DeleteResult{}, fmt.Errorf("selected item's parent is no longer available")
	}
	if _, err := os.Lstat(node.FullPath); err != nil {
		if os.IsNotExist(err) {
			return DeleteResult{}, fmt.Errorf("selected path no longer exists")
		}
		return DeleteResult{}, fmt.Errorf("inspect selected path: %w", err)
	}
	if err := moveToTrash(node.FullPath); err != nil {
		return DeleteResult{}, err
	}
	trash := displayedTrashNode(s.root, node)

	parent := s.nodes[node.ParentID]
	for index, child := range parent.Children {
		if child == node {
			parent.Children = append(parent.Children[:index], parent.Children[index+1:]...)
			break
		}
	}

	deletedSize := node.Size
	s.adjustAncestorSizes(parent, -deletedSize)
	rescanRequired := subtreeHasSharedAllocation(node)
	if trash != nil {
		node.ParentID = trash.ID
		prepareMovedSubtree(node, trash.Depth+1)
		trash.Children = append(trash.Children, node)
		s.adjustAncestorSizes(trash, deletedSize)
		return DeleteResult{FileCount: s.fileCount, DirCount: s.dirCount, RescanRequired: rescanRequired}, nil
	}

	deletedFiles, deletedDirs := s.detachSubtree(node)

	s.fileCount = max(0, s.fileCount-deletedFiles)
	s.dirCount = max(0, s.dirCount-deletedDirs)
	return DeleteResult{FileCount: s.fileCount, DirCount: s.dirCount, RescanRequired: rescanRequired}, nil
}

func (s *TreeStore) IsTrashNode(nodeID int, isTrashRoot func(string) bool) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return nodeID >= 0 && nodeID < len(s.nodes) && s.nodes[nodeID] != nil && isTrashRoot != nil && isTrashRoot(s.nodes[nodeID].FullPath)
}

func (s *TreeStore) EmptyTrashNode(nodeID int, isTrashRoot func(string) bool, emptyTrash func(string) error) (DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if nodeID < 0 || nodeID >= len(s.nodes) || s.nodes[nodeID] == nil {
		return DeleteResult{}, fmt.Errorf("selected item is no longer available")
	}
	node := s.nodes[nodeID]
	if !node.IsFolder || node.FullPath == "" || isTrashRoot == nil || !isTrashRoot(node.FullPath) {
		return DeleteResult{}, fmt.Errorf("the selected item is not a supported Trash root")
	}
	if _, err := os.Lstat(node.FullPath); err != nil {
		if os.IsNotExist(err) {
			return DeleteResult{}, fmt.Errorf("selected Trash no longer exists")
		}
		return DeleteResult{}, fmt.Errorf("inspect selected Trash: %w", err)
	}
	if emptyTrash == nil {
		return DeleteResult{}, fmt.Errorf("empty Trash command is unavailable")
	}
	if err := emptyTrash(node.FullPath); err != nil {
		return DeleteResult{}, err
	}

	emptiedSize := node.Size
	rescanRequired := subtreeHasSharedAllocation(node)
	var deletedFiles, deletedDirs int
	for _, child := range node.Children {
		files, dirs := s.detachSubtree(child)
		deletedFiles += files
		deletedDirs += dirs
	}
	node.Children = nil
	node.Size = 0
	if node.ParentID >= 0 && node.ParentID < len(s.nodes) {
		s.adjustAncestorSizes(s.nodes[node.ParentID], -emptiedSize)
	}
	s.fileCount = max(0, s.fileCount-deletedFiles)
	s.dirCount = max(0, s.dirCount-deletedDirs)
	return DeleteResult{FileCount: s.fileCount, DirCount: s.dirCount, RescanRequired: rescanRequired}, nil
}

func (s *TreeStore) detachSubtree(current *Node) (files, dirs int) {
	if current == nil {
		return 0, 0
	}
	if current.IsSmallFiles {
		files += int(current.SmallFileCount)
	} else if !current.IsFreeSpace {
		if current.IsFolder {
			dirs++
		} else {
			files++
		}
	}
	for _, child := range current.Children {
		childFiles, childDirs := s.detachSubtree(child)
		files += childFiles
		dirs += childDirs
	}
	if current.ID >= 0 && current.ID < len(s.nodes) {
		s.nodes[current.ID] = nil
	}
	return files, dirs
}

func (s *TreeStore) adjustAncestorSizes(node *Node, delta int64) {
	for current := node; current != nil; {
		current.Size = max(0, current.Size+delta)
		sort.Slice(current.Children, func(i, j int) bool {
			return current.Children[i].Size > current.Children[j].Size
		})
		if current.ParentID < 0 || current.ParentID >= len(s.nodes) {
			break
		}
		current = s.nodes[current.ParentID]
	}
}

func displayedTrashNode(root, moving *Node) *Node {
	if root == nil {
		return nil
	}
	var visit func(*Node) *Node
	visit = func(current *Node) *Node {
		if current == nil || current == moving {
			return nil
		}
		if current != root && current.IsFolder && current.Size > 0 && isSystemTrashName(current.Name) {
			if !subtreeContains(current, moving) && !subtreeContains(moving, current) {
				return current
			}
		}
		for _, child := range current.Children {
			if found := visit(child); found != nil {
				return found
			}
		}
		return nil
	}
	return visit(root)
}

func isSystemTrashName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "$recycle.bin" || lower == ".trash" || lower == ".trashes" || strings.HasPrefix(lower, ".trash-")
}

func subtreeContains(root, target *Node) bool {
	if root == nil || target == nil {
		return false
	}
	if root == target {
		return true
	}
	for _, child := range root.Children {
		if subtreeContains(child, target) {
			return true
		}
	}
	return false
}

func subtreeHasSharedAllocation(root *Node) bool {
	if root == nil {
		return false
	}
	if !root.IsFolder && !root.IsFreeSpace && !root.IsSmallFiles && root.LinkCount > 1 {
		return true
	}
	for _, child := range root.Children {
		if subtreeHasSharedAllocation(child) {
			return true
		}
	}
	return false
}

func prepareMovedSubtree(root *Node, depth int) {
	if root == nil {
		return
	}
	root.Depth = depth
	root.FullPath = ""
	for _, child := range root.Children {
		child.ParentID = root.ID
		prepareMovedSubtree(child, depth+1)
	}
}
