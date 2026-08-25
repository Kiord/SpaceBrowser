package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	trashRefreshes []trashRefreshTarget
}

type trashRefreshTarget struct {
	NodeID int
	Path   string
}

func (s *TreeStore) NodePath(nodeID int) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if nodeID < 0 || nodeID >= len(s.nodes) || s.nodes[nodeID] == nil || s.nodes[nodeID].FullPath == "" {
		return "", fmt.Errorf("selected item is no longer available")
	}
	return s.nodes[nodeID].FullPath, nil
}

func (s *TreeStore) Counts() (files, dirs int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fileCount, s.dirCount
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

func (s *TreeStore) DeleteNode(nodeID int, isTrashRoot, isInTrash func(string) bool, moveToTrash func(string) error) (DeleteResult, error) {
	return s.deleteNode(nodeID, isTrashRoot, isInTrash, moveToTrash, true)
}

func (s *TreeStore) DeleteNodePermanently(nodeID int, isTrashRoot, isInTrash func(string) bool, deletePermanently func(string) error) (DeleteResult, error) {
	return s.deleteNode(nodeID, isTrashRoot, isInTrash, deletePermanently, false)
}

func (s *TreeStore) deleteNode(nodeID int, isTrashRoot, isInTrash func(string) bool, deletePath func(string) error, refreshTrash bool) (DeleteResult, error) {
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
	if isInTrash != nil && isInTrash(node.FullPath) {
		return DeleteResult{}, fmt.Errorf("items inside Trash cannot be deleted; restore them using the system Trash")
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
	if err := deletePath(node.FullPath); err != nil {
		return DeleteResult{}, err
	}
	var trashRefreshes []trashRefreshTarget
	if refreshTrash {
		trashRefreshes = displayedTrashNodes(s.root, node, isTrashRoot)
	}

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
	deletedFiles, deletedDirs := s.detachSubtree(node)
	s.adjustAncestorEntryCounts(parent, -deletedFiles, -deletedDirs)

	s.fileCount = max(0, s.fileCount-deletedFiles)
	s.dirCount = max(0, s.dirCount-deletedDirs)
	return DeleteResult{
		FileCount:      s.fileCount,
		DirCount:       s.dirCount,
		RescanRequired: rescanRequired,
		trashRefreshes: trashRefreshes,
	}, nil
}

func (s *TreeStore) ReplaceSubtree(nodeID int, scanned *Node, scannedFiles, scannedDirs int) (DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if scanned == nil || nodeID < 0 || nodeID >= len(s.nodes) || s.nodes[nodeID] == nil {
		return DeleteResult{}, fmt.Errorf("the subtree to refresh is no longer available")
	}
	target := s.nodes[nodeID]
	if !target.IsFolder || target.FullPath == "" {
		return DeleteResult{}, fmt.Errorf("the subtree to refresh is not a filesystem folder")
	}
	if sPath, tPath := filepath.Clean(scanned.FullPath), filepath.Clean(target.FullPath); sPath != tPath {
		return DeleteResult{}, fmt.Errorf("refreshed subtree path changed from %s to %s", tPath, sPath)
	}

	oldSize := target.Size
	oldFiles, oldDirsIncludingRoot := subtreeEntryCounts(target)
	oldDirs := max(0, oldDirsIncludingRoot-1)
	for _, child := range target.Children {
		s.detachSubtreeIDs(child)
	}

	target.Name = scanned.Name
	target.Size = scanned.Size
	target.IsFolder = true
	target.IsFreeSpace = false
	target.IsSmallFiles = false
	target.SmallFileCount = 0
	target.SmallFileLimit = 0
	target.FullPath = scanned.FullPath
	target.ModTime = scanned.ModTime
	target.LinkCount = scanned.LinkCount
	target.EntryFiles = scannedFiles
	target.EntryDirs = scannedDirs
	target.Children = nil

	nextFreeID := 0
	allocateID := func(node *Node) int {
		for nextFreeID < len(s.nodes) && s.nodes[nextFreeID] != nil {
			nextFreeID++
		}
		if nextFreeID == len(s.nodes) {
			s.nodes = append(s.nodes, node)
			nextFreeID++
			return len(s.nodes) - 1
		}
		id := nextFreeID
		s.nodes[id] = node
		nextFreeID++
		return id
	}
	var adopt func(*Node, int, int) *Node
	adopt = func(source *Node, parentID, depth int) *Node {
		node := *source
		node.ParentID = parentID
		node.Depth = depth
		node.Children = make([]*Node, 0, len(source.Children))
		if source.IsFreeSpace || source.IsSmallFiles {
			node.ID = -1
		} else {
			node.ID = allocateID(&node)
		}
		for _, child := range source.Children {
			node.Children = append(node.Children, adopt(child, node.ID, depth+1))
		}
		return &node
	}
	for _, child := range scanned.Children {
		target.Children = append(target.Children, adopt(child, target.ID, target.Depth+1))
	}

	if target.ParentID >= 0 && target.ParentID < len(s.nodes) {
		s.adjustAncestorSizes(s.nodes[target.ParentID], target.Size-oldSize)
	}
	newDescendantDirs := max(0, scannedDirs-1)
	if target.ParentID >= 0 && target.ParentID < len(s.nodes) {
		s.adjustAncestorEntryCounts(s.nodes[target.ParentID], scannedFiles-oldFiles, newDescendantDirs-oldDirs)
	}
	s.fileCount = max(0, s.fileCount-oldFiles+scannedFiles)
	s.dirCount = max(0, s.dirCount-oldDirs+newDescendantDirs)
	return DeleteResult{
		FileCount:      s.fileCount,
		DirCount:       s.dirCount,
		RescanRequired: subtreeHasSharedAllocation(scanned),
	}, nil
}

func (s *TreeStore) NodePathMatches(nodeID int, predicate func(string) bool) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return nodeID >= 0 && nodeID < len(s.nodes) && s.nodes[nodeID] != nil && predicate != nil && predicate(s.nodes[nodeID].FullPath)
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
	trashRefreshes := displayedTrashNodes(s.root, nil, isTrashRoot)
	if err := emptyTrash(node.FullPath); err != nil {
		return DeleteResult{}, err
	}

	emptiedSize := node.Size
	rescanRequired := subtreeHasSharedAllocation(node)
	deletedFiles, deletedDirsIncludingRoot := subtreeEntryCounts(node)
	deletedDirs := max(0, deletedDirsIncludingRoot-1)
	for _, child := range node.Children {
		s.detachSubtreeIDs(child)
	}
	node.Children = nil
	node.Size = 0
	node.EntryFiles = 0
	node.EntryDirs = 1
	if node.ParentID >= 0 && node.ParentID < len(s.nodes) {
		s.adjustAncestorSizes(s.nodes[node.ParentID], -emptiedSize)
		s.adjustAncestorEntryCounts(s.nodes[node.ParentID], -deletedFiles, -deletedDirs)
	}
	s.fileCount = max(0, s.fileCount-deletedFiles)
	s.dirCount = max(0, s.dirCount-deletedDirs)
	return DeleteResult{
		FileCount:      s.fileCount,
		DirCount:       s.dirCount,
		RescanRequired: rescanRequired,
		trashRefreshes: trashRefreshes,
	}, nil
}

func (s *TreeStore) detachSubtree(current *Node) (files, dirs int) {
	if current == nil {
		return 0, 0
	}
	files, dirs = subtreeEntryCounts(current)
	s.detachSubtreeIDs(current)
	return files, dirs
}

func subtreeEntryCounts(current *Node) (files, dirs int) {
	if current == nil {
		return 0, 0
	}
	if current.EntryFiles > 0 || current.EntryDirs > 0 {
		return current.EntryFiles, current.EntryDirs
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
		childFiles, childDirs := subtreeEntryCounts(child)
		files += childFiles
		dirs += childDirs
	}
	return files, dirs
}

func (s *TreeStore) detachSubtreeIDs(current *Node) {
	if current == nil {
		return
	}
	for _, child := range current.Children {
		s.detachSubtreeIDs(child)
	}
	if current.ID >= 0 && current.ID < len(s.nodes) {
		s.nodes[current.ID] = nil
	}
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

func (s *TreeStore) adjustAncestorEntryCounts(node *Node, fileDelta, dirDelta int) {
	for current := node; current != nil; {
		if current.EntryFiles > 0 || current.EntryDirs > 0 {
			current.EntryFiles = max(0, current.EntryFiles+fileDelta)
			current.EntryDirs = max(1, current.EntryDirs+dirDelta)
		}
		if current.ParentID < 0 || current.ParentID >= len(s.nodes) {
			break
		}
		current = s.nodes[current.ParentID]
	}
}

func displayedTrashNodes(root, moving *Node, isTrashRoot func(string) bool) []trashRefreshTarget {
	if root == nil {
		return nil
	}
	var found []trashRefreshTarget
	var visit func(*Node)
	visit = func(current *Node) {
		if current == nil || current == moving {
			return
		}
		if current != root && current.IsFolder && current.FullPath != "" && isPotentialSystemTrashName(current.Name) && isTrashRoot != nil && isTrashRoot(current.FullPath) {
			if !subtreeContains(current, moving) && !subtreeContains(moving, current) {
				found = append(found, trashRefreshTarget{NodeID: current.ID, Path: current.FullPath})
				return
			}
		}
		for _, child := range current.Children {
			visit(child)
		}
	}
	visit(root)
	return found
}

func isPotentialSystemTrashName(name string) bool {
	trimmed := strings.TrimSpace(name)
	lower := strings.ToLower(trimmed)
	if lower == "$recycle.bin" || lower == "trash" || lower == ".trash" || lower == ".trashes" {
		return true
	}
	if strings.HasPrefix(lower, ".trash-") {
		_, err := strconv.ParseUint(strings.TrimPrefix(lower, ".trash-"), 10, 32)
		return err == nil
	}
	// Linux/macOS per-volume shared Trash containers place the current user's
	// Trash in a directory named with their numeric UID.
	_, err := strconv.ParseUint(trimmed, 10, 32)
	return err == nil
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
