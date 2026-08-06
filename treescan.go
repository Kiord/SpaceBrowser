package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"spacebrowser/internal/platform"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// =====================
// Data Model (server-side)
// =====================

type Node struct {
	ID             int     `json:"id,omitempty"`
	ParentID       int     `json:"parent_id,omitempty"`
	Name           string  `json:"name"`
	Size           int64   `json:"size"`
	IsFolder       bool    `json:"is_folder"`
	IsFreeSpace    bool    `json:"is_free_space"`
	IsSmallFiles   bool    `json:"is_small_files"`
	SmallFileCount int64   `json:"small_file_count,omitempty"`
	SmallFileLimit int64   `json:"small_file_limit,omitempty"`
	Depth          int     `json:"depth"`
	FullPath       string  `json:"full_path,omitempty"`
	Children       []*Node `json:"children"`

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
	nodes      []*Node
	nodesMu    sync.Mutex
	idCounter  int64
	seen       map[platform.InodeKey]struct{}
	seenMu     sync.Mutex
	seenDirs   map[string]struct{}
	seenDirsMu sync.Mutex

	smallFilesSize int64
	smallFileCount int64
	workDiscovered int64
	workProcessed  int64
	bytesProcessed int64

	ctx            context.Context
	onProgress     func(string)
	progressMu     sync.Mutex
	lastProgressAt time.Time
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
		seenDirs:   make(map[string]struct{}),
		ctx:        context.Background(),
	}
}

func (s *Scanner) SetContext(ctx context.Context, onProgress func(string)) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.ctx = ctx
	s.onProgress = onProgress
}

func (s *Scanner) WorkProgress() (processed, discovered int64) {
	return atomic.LoadInt64(&s.workProcessed), atomic.LoadInt64(&s.workDiscovered)
}

func (s *Scanner) BytesProcessed() int64 {
	return atomic.LoadInt64(&s.bytesProcessed)
}

func (s *Scanner) reportProgress(path string) {
	if s.onProgress == nil {
		return
	}

	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	now := time.Now()
	if !s.lastProgressAt.IsZero() && now.Sub(s.lastProgressAt) < 50*time.Millisecond {
		return
	}
	s.lastProgressAt = now
	s.onProgress(path)
}

func (s *Scanner) seenDirectory(path string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	path = platform.Impl.Canonicalize(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}

	s.seenDirsMu.Lock()
	_, exists := s.seenDirs[path]
	if !exists {
		s.seenDirs[path] = struct{}{}
	}
	s.seenDirsMu.Unlock()
	return exists
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

func (s *Scanner) addSmallFilesAggregate(root *Node) {
	count := atomic.LoadInt64(&s.smallFileCount)
	if root == nil || count == 0 {
		return
	}

	size := atomic.LoadInt64(&s.smallFilesSize)
	root.Children = append(root.Children, &Node{
		ID:             -1,
		ParentID:       root.ID,
		Name:           "[Small Files]",
		Size:           size,
		IsSmallFiles:   true,
		SmallFileCount: count,
		SmallFileLimit: s.profile.MinFileSize,
		Depth:          root.Depth + 1,
	})
	root.Size += size
}

// buildTree scans 'path' and all descendants, assigning IDs.
// Concurrency: subdirectories of a folder are scanned in parallel, bounded by s.sem.
func (s *Scanner) buildTree(path string, depth int, parentID int, fileCount, dirCount *int64) (*Node, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if depth > 0 {
		defer atomic.AddInt64(&s.workProcessed, 1)
	}
	abs := platform.Impl.Canonicalize(path)
	if s.profile.FollowSymlinks && s.seenDirectory(abs) {
		return nil, nil
	}
	s.reportProgress(abs)

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
	atomic.AddInt64(&s.workDiscovered, int64(len(entries)))

	// First pass: files now, subdirs later
	type subdir struct{ full string }
	subdirs := make([]subdir, 0, 32)
	var processedBatch int64
	flushProcessed := func() {
		if processedBatch > 0 {
			atomic.AddInt64(&s.workProcessed, processedBatch)
			processedBatch = 0
		}
	}
	defer flushProcessed()

	for _, de := range entries {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		completeNow := func() bool {
			name := de.Name()
			full := filepath.Join(abs, name)

			if shouldExclude(s.profile, full) {
				return true
			}
			isSymlink := de.Type()&os.ModeSymlink != 0
			var info os.FileInfo
			isDir := de.IsDir()
			if isSymlink {
				if !s.profile.FollowSymlinks {
					return true
				}
				var err error
				info, err = os.Stat(full)
				if err != nil {
					return true
				}
				isDir = info.IsDir()
			}

			if s.profile.SkipHidden && platform.Impl.IsHidden(full) {
				return true
			}

			if isDir {
				if s.profile.SkipNetworkFS && platform.Impl.IsLikelyNetworkFS(full) {
					return true
				}
				subdirs = append(subdirs, subdir{full: full})
				return false
			}

			if info == nil {
				var err error
				info, err = de.Info()
				if err != nil {
					return true
				}
			}
			if !info.Mode().IsRegular() {
				return true
			}

			sz := platform.Impl.AllocatedSize(info)
			if k, ok := platform.Impl.InodeKeyOf(info); ok && s.seenOnce(k) {
				return true
			}
			atomic.AddInt64(&s.bytesProcessed, sz)

			if s.profile.MinFileSize > 0 && info.Size() < s.profile.MinFileSize {
				atomic.AddInt64(&s.smallFilesSize, sz)
				atomic.AddInt64(&s.smallFileCount, 1)
				atomic.AddInt64(fileCount, 1)
				return true
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
			return true
		}()
		if completeNow {
			processedBatch++
			if processedBatch >= 64 {
				flushProcessed()
			}
		}
	}

	// Second pass: scan subdirectories (bounded)
	if len(subdirs) > 0 {
		var wg sync.WaitGroup
		var mu sync.Mutex
		results := make([]*Node, 0, len(subdirs))

		for _, sd := range subdirs {
			if s.ctx.Err() != nil {
				break
			}
			select {
			case s.sem <- struct{}{}:
				wg.Add(1)
				go func(p string) {
					defer wg.Done()
					defer func() { <-s.sem }()
					n, _ := s.buildTree(p, depth+1, root.ID, fileCount, dirCount)
					if n != nil {
						mu.Lock()
						results = append(results, n)
						mu.Unlock()
					}
				}(sd.full)
			default:
				// inline to avoid deadlock
				n, _ := s.buildTree(sd.full, depth+1, root.ID, fileCount, dirCount)
				if n != nil {
					results = append(results, n)
				}
			}
		}

		wg.Wait()
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		for _, n := range results {
			root.Children = append(root.Children, n)
			root.Size += n.Size
		}
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}

	// Sort children by size desc (UI expects this)
	sort.Slice(root.Children, func(i, j int) bool { return root.Children[i].Size > root.Children[j].Size })
	return root, nil
}
