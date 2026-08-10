package main

import (
	"fmt"
	"os"
	"sort"
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
	FileCount int `json:"fileCount"`
	DirCount  int `json:"dirCount"`
}

func (s *TreeStore) Replace(root *Node, nodes []*Node, fileCount, dirCount int) {
	s.mu.Lock()
	s.root, s.nodes = root, nodes
	s.fileCount, s.dirCount = fileCount, dirCount
	s.mu.Unlock()
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

func (s *TreeStore) DeleteNode(nodeID int, moveToTrash func(string) error) (DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if nodeID < 0 || nodeID >= len(s.nodes) || s.nodes[nodeID] == nil {
		return DeleteResult{}, fmt.Errorf("selected item is no longer available")
	}
	node := s.nodes[nodeID]
	if node.ParentID < 0 || node.FullPath == "" || node.IsFreeSpace || node.IsSmallFiles {
		return DeleteResult{}, fmt.Errorf("the scan root and virtual items cannot be deleted")
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

	parent := s.nodes[node.ParentID]
	for index, child := range parent.Children {
		if child == node {
			parent.Children = append(parent.Children[:index], parent.Children[index+1:]...)
			break
		}
	}

	deletedSize := node.Size
	for current := parent; current != nil; {
		current.Size = max(0, current.Size-deletedSize)
		sort.Slice(current.Children, func(i, j int) bool {
			return current.Children[i].Size > current.Children[j].Size
		})
		if current.ParentID < 0 || current.ParentID >= len(s.nodes) {
			break
		}
		current = s.nodes[current.ParentID]
	}

	var deletedFiles, deletedDirs int
	var detach func(*Node)
	detach = func(current *Node) {
		if current == nil {
			return
		}
		if current.IsSmallFiles {
			deletedFiles += int(current.SmallFileCount)
		} else if !current.IsFreeSpace {
			if current.IsFolder {
				deletedDirs++
			} else {
				deletedFiles++
			}
		}
		for _, child := range current.Children {
			detach(child)
		}
		if current.ID >= 0 && current.ID < len(s.nodes) {
			s.nodes[current.ID] = nil
		}
	}
	detach(node)

	s.fileCount = max(0, s.fileCount-deletedFiles)
	s.dirCount = max(0, s.dirCount-deletedDirs)
	return DeleteResult{FileCount: s.fileCount, DirCount: s.dirCount}, nil
}
