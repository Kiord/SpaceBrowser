package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// =====================
// Data Model (JSON API)
// =====================

type Node struct {
	Name        string  `json:"name"`
	Size        int64   `json:"size"`
	IsFolder    bool    `json:"is_folder"`
	IsFreeSpace bool    `json:"is_free_space"`
	Depth       int     `json:"depth"`
	FullPath    string  `json:"full_path,omitempty"`
	Children    []*Node `json:"children"`
	// Only populated on root for convenience (UI uses these if present)
	FileCount   int `json:"file_count,omitempty"`
	FolderCount int `json:"folder_count,omitempty"`
}

// ==============================
// Tree Building
// ==============================

type Scanner struct {
	profile    *Profile
	sem        chan struct{} // worker tokens
	maxWorkers int
}

// NewScanner(maxWorkers<=0 => sensible default)
func NewScanner(p *Profile, maxWorkers int) *Scanner {
	if maxWorkers <= 0 {
		maxWorkers = runtime.NumCPU() * 4 // good starting point for NVMe; tune for HDDs
	}
	return &Scanner{profile: p, sem: make(chan struct{}, maxWorkers), maxWorkers: maxWorkers}
}

// buildTree scans 'path' and all descendants.
// Concurrency: subdirectories of a folder are scanned in parallel, bounded by s.sem.
func (s *Scanner) buildTree(path string, depth int, fileCount, dirCount *int64) (*Node, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	// Count this directory
	atomic.AddInt64(dirCount, 1)

	root := &Node{
		Name:     baseName(abs),
		Size:     0,
		IsFolder: true,
		Depth:    depth,
		FullPath: abs,
		Children: make([]*Node, 0, 128),
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		// unreadable directory -> return empty folder
		return root, nil
	}

	// First pass: decide files vs subdirs; handle files immediately, queue subdirs.
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
			// Defer scanning to worker pool
			subdirs = append(subdirs, subdir{full: full})
			continue
		}

		// For files we need size -> call Info() once
		info, err := de.Info()
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if s.profile.MinFileSize > 0 && info.Size() < s.profile.MinFileSize {
			continue
		}

		sz := info.Size() // logical size; switch to on-disk if you add st_blocks
		child := &Node{Name: name, Size: sz, IsFolder: false, Depth: depth + 1}
		root.Children = append(root.Children, child)
		root.Size += sz
		atomic.AddInt64(fileCount, 1)
	}

	// Second pass: scan subdirectories in parallel (bounded).
	if len(subdirs) > 0 {

		var wg sync.WaitGroup
		var mu sync.Mutex
		results := make([]*Node, 0, len(subdirs))

		for _, sd := range subdirs {
			// Try to acquire a worker without blocking
			select {
			case s.sem <- struct{}{}:
				wg.Add(1)
				go func(p string) {
					defer wg.Done()
					defer func() { <-s.sem }()
					n, _ := s.buildTree(p, depth+1, fileCount, dirCount)
					mu.Lock()
					results = append(results, n)
					mu.Unlock()
				}(sd.full)
			default:
				// Pool is full — do it synchronously to avoid deadlock
				n, _ := s.buildTree(sd.full, depth+1, fileCount, dirCount)
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
