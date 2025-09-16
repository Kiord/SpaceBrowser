package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"spacebrowser/internal/platform"
	"sync"
	"sync/atomic"
)

// =====================
// Data Model (server-side)
// =====================

type Node struct {
	ID          int     `json:"id,omitempty"`
	ParentID    int     `json:"parent_id,omitempty"`
	Name        string  `json:"name"`
	Size        int64   `json:"size"`
	IsFolder    bool    `json:"is_folder"`
	IsFreeSpace bool    `json:"is_free_space"`
	Depth       int     `json:"depth"`
	FullPath    string  `json:"full_path,omitempty"`
	Children    []*Node `json:"children"`
	// Only populated on root for convenience (not sent to the new front)
	FileCount   int `json:"file_count,omitempty"`
	FolderCount int `json:"folder_count,omitempty"`

	// Only set on mount roots
	DiskTotal int64 `json:"disk_total,omitempty"`
	DiskFree  int64 `json:"disk_free,omitempty"`

	ModTime int64 `json:"-"`
}

// ==============================
// Scanner with bounded concurrency + ID assignment
// ==============================

type Scanner struct {
	profile    *Profile
	sem        chan struct{} // worker tokens
	maxWorkers int

	// dense array: nodes[id] == *Node
	nodes     []*Node
	nodesMu   sync.Mutex
	idCounter int64
	seen      map[platform.InodeKey]struct{}
	seenMu    sync.Mutex
}

// NewScanner(maxWorkers<=0 => sensible default)
func NewScanner(p *Profile, maxWorkers int) *Scanner {
	if maxWorkers <= 0 {
		maxWorkers = runtime.NumCPU() * 4 // good starting point for NVMe; tune for HDDs
	}
	return &Scanner{
		profile:    p,
		sem:        make(chan struct{}, maxWorkers),
		maxWorkers: maxWorkers,
		seen:       make(map[platform.InodeKey]struct{}),
	}
}

func (s *Scanner) seenOnce(k platform.InodeKey) bool {
	s.seenMu.Lock()
	_, ok := s.seen[k]
	if !ok {
		s.seen[k] = struct{}{}
	}
	s.seenMu.Unlock()
	return ok
}

func (s *Scanner) assignID(n *Node) int {
	id := int(atomic.AddInt64(&s.idCounter, 1) - 1)
	n.ID = id
	s.nodesMu.Lock()
	if id >= len(s.nodes) {
		s.nodes = append(s.nodes, make([]*Node, id+1-len(s.nodes))...)
	}
	s.nodes[id] = n
	s.nodesMu.Unlock()
	return id
}

func (s *Scanner) Nodes() []*Node {
	s.nodesMu.Lock()
	out := s.nodes
	s.nodesMu.Unlock()
	return out
}

// buildTree scans 'path' and all descendants, assigning IDs.
// Concurrency: subdirectories of a folder are scanned in parallel, bounded by s.sem.
func (s *Scanner) buildTree(path string, depth int, parentID int, fileCount, dirCount *int64) (*Node, error) {
	abs := platform.Impl.Canonicalize(path)

	// directory node
	root := &Node{
		ParentID: parentID,
		Name:     platform.Impl.BaseName(abs),
		Size:     0,
		IsFolder: true,
		Depth:    depth,
		FullPath: abs,
		Children: make([]*Node, 0, 128),
	}
	s.assignID(root)
	atomic.AddInt64(dirCount, 1)

	entries, err := os.ReadDir(abs)
	if err != nil {
		// unreadable directory -> return empty folder
		return root, nil
	}

	// First pass: files now, subdirs later
	type subdir struct{ full string }
	subdirs := make([]subdir, 0, 32)

	for _, de := range entries {
		name := de.Name()
		full := filepath.Join(abs, name)

		if shouldExclude(s.profile, full) {
			continue
		}
		// Skip symlinks early (no Info() needed)
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}
		// Hidden policy
		if s.profile.SkipHidden && isHidden(full) {
			continue
		}

		if de.IsDir() {
			subdirs = append(subdirs, subdir{full: full})
			continue
		}

		// files: need size
		info, err := de.Info()
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}

		sz := platform.Impl.AllocatedSize(info)

		if s.profile.MinFileSize > 0 && sz < s.profile.MinFileSize {
			continue
		}

		if k, ok := platform.Impl.InodeKeyOf(info); ok && s.seenOnce(k) {
			continue
		}

		child := &Node{
			ParentID: root.ID,
			Name:     name,
			FullPath: full,
			Size:     sz,
			IsFolder: false,
			Depth:    depth + 1,
			ModTime:  info.ModTime().Unix(),
		}
		s.assignID(child)

		root.Children = append(root.Children, child)
		root.Size += sz
		atomic.AddInt64(fileCount, 1)
	}

	// Second pass: scan subdirectories (bounded)
	if len(subdirs) > 0 {
		var wg sync.WaitGroup
		var mu sync.Mutex
		results := make([]*Node, 0, len(subdirs))

		for _, sd := range subdirs {
			select {
			case s.sem <- struct{}{}:
				wg.Add(1)
				go func(p string) {
					defer wg.Done()
					defer func() { <-s.sem }()
					n, _ := s.buildTree(p, depth+1, root.ID, fileCount, dirCount)
					mu.Lock()
					results = append(results, n)
					mu.Unlock()
				}(sd.full)
			default:
				// inline to avoid deadlock
				n, _ := s.buildTree(sd.full, depth+1, root.ID, fileCount, dirCount)
				results = append(results, n)
			}
		}

		wg.Wait()
		for _, n := range results {
			root.Children = append(root.Children, n)
			root.Size += n.Size
		}
	}

	// Sort children by size desc (UI expects this)
	sort.Slice(root.Children, func(i, j int) bool { return root.Children[i].Size > root.Children[j].Size })
	return root, nil
}
