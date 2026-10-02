package main

import (
	"sort"
	"sync"
	"time"
)

// Preview publication replaces pointers only. Retaining the previous immutable
// allocation makes cancellation restore the original tree without copying it.
type treeStoreSnapshot struct {
	root                 *Node
	nodes                []*Node
	files, dirs          int
	shared, hasDiskUsage bool
	diskTotal, diskFree  int64
}

func (s *TreeStore) snapshotState() *treeStoreSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &treeStoreSnapshot{s.root, s.nodes, s.fileCount, s.dirCount, s.shared, s.hasDiskUsage, s.diskTotal, s.diskFree}
}

func (s *TreeStore) restoreState(previous *treeStoreSnapshot) {
	if previous == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.root, s.nodes = previous.root, previous.nodes
	s.fileCount, s.dirCount = previous.files, previous.dirs
	s.shared, s.hasDiskUsage = previous.shared, previous.hasDiskUsage
	s.diskTotal, s.diskFree = previous.diskTotal, previous.diskFree
}

// Preview records contain only immutable display metadata, never pointers into
// the working scanner tree. In particular, link metadata may still be updated
// by other workers and must not be read here.
type scanPreviewTree struct {
	mu                  sync.Mutex
	nodes               map[int]previewNode
	small               map[int]Node
	revision            uint64
	diskTotal, diskFree int64 // fixed before scanning starts
}

type previewNode struct {
	parent, depth   int
	name, path      string
	size, modTime   int64
	folder, pending bool
}

func newScanPreviewTree() *scanPreviewTree {
	return &scanPreviewTree{nodes: make(map[int]previewNode), small: make(map[int]Node)}
}

func (p *scanPreviewTree) add(node *Node) {
	if p == nil {
		return
	}
	copy := previewNode{parent: node.ParentID, name: node.Name, path: node.FullPath,
		size: node.Size, folder: node.IsFolder, depth: node.Depth, modTime: node.ModTime, pending: node.IsFolder}
	if copy.folder {
		copy.size = 0
	}
	p.mu.Lock()
	p.nodes[node.ID] = copy
	p.revision++
	p.mu.Unlock()
}

func (p *scanPreviewTree) smallFiles(parent int, size, count, limit int64, depth int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.small[parent] = Node{ID: -1, ParentID: parent, Name: "[Small Files]", IsSmallFiles: true,
		Size: size, SmallFileCount: count, SmallFileLimit: limit, Depth: depth}
	p.revision++
	p.mu.Unlock()
}

func (p *scanPreviewTree) complete(id int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	node := p.nodes[id]
	node.pending = false
	p.nodes[id] = node
	p.revision++
	p.mu.Unlock()
}

func (p *scanPreviewTree) snapshot() (*Node, []*Node, uint64) {
	p.mu.Lock()
	maxID := -1
	for id := range p.nodes {
		if id > maxID {
			maxID = id
		}
	}
	nodes := make([]*Node, maxID+1)
	for id, source := range p.nodes {
		node := Node{ID: id, ParentID: source.parent, Depth: source.depth, Name: source.name,
			FullPath: source.path, Size: source.size, ModTime: source.modTime, IsFolder: source.folder, ScanIncomplete: source.pending}
		nodes[id] = &node
	}
	small := make([]Node, 0, len(p.small))
	for _, node := range p.small {
		small = append(small, node)
	}
	revision := p.revision
	p.mu.Unlock()
	var root *Node
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.ParentID < 0 {
			root = node
		} else if node.ParentID < len(nodes) && nodes[node.ParentID] != nil {
			parent := nodes[node.ParentID]
			parent.Children = append(parent.Children, node)
		}
	}
	for i := range small {
		node := &small[i]
		if node.ParentID < len(nodes) && nodes[node.ParentID] != nil {
			parent := nodes[node.ParentID]
			parent.Children = append(parent.Children, node)
		}
	}
	var total func(*Node)
	total = func(node *Node) {
		if !node.IsFolder {
			return
		}
		for _, child := range node.Children {
			total(child)
			node.Size += child.Size
		}
		sort.Slice(node.Children, func(i, j int) bool {
			if node.Children[i].Size == node.Children[j].Size {
				return node.Children[i].Name < node.Children[j].Name
			}
			return node.Children[i].Size > node.Children[j].Size
		})
	}
	if root != nil {
		total(root)
		if p.diskTotal > 0 {
			// Unscanned allocation starts in the free-space region and is
			// displaced by discovered files, without inventing file sizes.
			remaining := max(p.diskFree, p.diskTotal-root.Size)
			root.DiskTotal, root.DiskFree = p.diskTotal, remaining
			root.Children = append(root.Children, &Node{ID: -1, ParentID: root.ID, Depth: 1,
				Name: "[Free / Unscanned Space]", IsFreeSpace: true, ScanIncomplete: true,
				Size: remaining, DiskTotal: p.diskTotal})
			sort.Slice(root.Children, func(i, j int) bool { return root.Children[i].Size > root.Children[j].Size })
		}
	}
	return root, nodes, revision
}

// GetScanPreview is throttled independently of progress polling. Snapshots are
// assembled without a store lock, then swapped in only for the same scan.
func (a *App) GetScanPreview(generation uint64) *TreeInfo {
	return a.publishScanPreview(generation, false)
}

func (a *App) publishScanPreview(generation uint64, force bool) *TreeInfo {
	a.previewMu.Lock()
	defer a.previewMu.Unlock()
	a.scanMu.RLock()
	if a.scanGeneration != generation {
		a.scanMu.RUnlock()
		return nil
	}
	scanner, previous := a.scanScanner, a.scanPreview
	if !a.scanActive || a.scanResultPublished || scanner == nil || scanner.preview == nil || (!force && time.Since(a.scanPreviewAt) < time.Second) {
		a.scanMu.RUnlock()
		return previous
	}
	a.scanMu.RUnlock()
	root, nodes, revision := scanner.preview.snapshot()
	if root == nil {
		return nil
	}
	files, dirs := scanner.LiveCounts()
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	if a.scanGeneration != generation || !a.scanActive || a.scanResultPublished {
		return nil
	}
	if previous != nil && previous.Revision == revision {
		a.scanPreviewAt = time.Now()
		return previous
	}
	a.store.Replace(root, nodes, int(files), int(dirs))
	a.scanPreview = &TreeInfo{RootID: root.ID, FileCount: int(files), DirCount: int(dirs), Revision: revision}
	a.scanPreviewAt = time.Now()
	return a.scanPreview
}
