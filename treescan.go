package main

import (
	"context"
	"errors"
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

	ModTime    int64  `json:"-"`
	LinkCount  uint64 `json:"-"`
	EntryFiles int    `json:"-"`
	EntryDirs  int    `json:"-"`
}

// ==============================
// Scanner with bounded concurrency + ID assignment
// ==============================

type Scanner struct {
	profile    *Profile
	filesystem platform.ScannerFilesystem
	sem        chan struct{} // worker tokens
	maxWorkers int

	// dense array: nodes[id] == *Node
	nodes               []*Node
	nodesMu             sync.Mutex
	idCounter           int64
	seen                map[platform.FileIdentity]*Node
	untrustedSeen       map[platform.FileIdentity]untrustedIdentityReference
	untrustedCollisions map[platform.FileIdentity]*untrustedIdentityBucket
	seenMu              sync.Mutex
	seenDirs            map[string]struct{}
	seenDirsMu          sync.Mutex

	workDiscovered int64
	workProcessed  int64
	fileCount      int64
	dirCount       int64

	ctx               context.Context
	onProgress        func(string)
	progressMu        sync.Mutex
	lastProgressAt    time.Time
	report            ScanReport
	cachedDirectories map[string]*Node
	dirtyCachePaths   []string
	reusedDirectories int64
}

type untrustedIdentityCandidate struct {
	path     string
	node     *Node
	usage    platform.FileUsage
	resolved bool
}

type untrustedIdentityReference struct {
	path string
	node *Node
}

type untrustedIdentityBucket struct {
	mu         sync.Mutex
	candidates []*untrustedIdentityCandidate
}

var errNetworkFilesystemRootSkipped = errors.New("network filesystem root is excluded by the scan profile")

// NewScanner(maxWorkers<=0 => sensible default)
func NewScanner(p *Profile, maxWorkers int) *Scanner {
	return NewScannerWithFilesystem(p, maxWorkers, platform.Impl)
}

func NewScannerWithFilesystem(p *Profile, maxWorkers int, filesystem platform.ScannerFilesystem) *Scanner {
	if filesystem == nil {
		filesystem = platform.Impl
	}
	if maxWorkers <= 0 {
		maxWorkers = runtime.NumCPU() * 4 // good starting point for NVMe; tune for HDDs
	}
	return &Scanner{
		profile:             p,
		filesystem:          filesystem,
		sem:                 make(chan struct{}, maxWorkers),
		maxWorkers:          maxWorkers,
		seen:                make(map[platform.FileIdentity]*Node),
		untrustedSeen:       make(map[platform.FileIdentity]untrustedIdentityReference),
		untrustedCollisions: make(map[platform.FileIdentity]*untrustedIdentityBucket),
		seenDirs:            make(map[string]struct{}),
		ctx:                 context.Background(),
		report:              NewScanReport(maximumScanReportExamples),
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

func (s *Scanner) LiveCounts() (files, dirs int64) {
	return atomic.LoadInt64(&s.fileCount), atomic.LoadInt64(&s.dirCount)
}

func (s *Scanner) Report() ScanReportSnapshot {
	return s.report.Snapshot()
}

func (s *Scanner) ReportAllErrors(reportAll bool) {
	if reportAll {
		s.report.SetExampleLimit(-1)
		return
	}
	s.report.SetExampleLimit(maximumScanReportExamples)
}

func (s *Scanner) SetIncrementalCache(directories map[string]*Node, dirtyPaths []string) {
	s.cachedDirectories = directories
	s.dirtyCachePaths = append([]string(nil), dirtyPaths...)
}

func (s *Scanner) ReusedDirectories() int64 {
	return atomic.LoadInt64(&s.reusedDirectories)
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
	} else {
		s.report.RecordError(scanErrorResolveSymlink, path, err)
	}
	path = s.filesystem.Canonicalize(path)
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

func updateNodeUsage(node *Node, usage platform.FileUsage) {
	if node != nil {
		node.Size = usage.AllocatedSize
		node.LinkCount = usage.LinkCount
	}
}

func (s *Scanner) registerFileIdentity(path string, info os.FileInfo, usage platform.FileUsage, node *Node) (platform.FileUsage, bool) {
	if !usage.HasIdentity {
		return usage, false
	}
	updateNodeUsage(node, usage)
	if usage.IdentityNeedsConfirmation {
		return s.registerUntrustedFileIdentity(path, info, usage, node)
	}

	s.seenMu.Lock()
	existing, ok := s.seen[usage.Identity]
	if !ok {
		s.seen[usage.Identity] = node
		s.seenMu.Unlock()
		return usage, false
	}
	linkCount := usage.LinkCount
	if !usage.HasLinkCount || linkCount < 2 {
		linkCount = 2
	}
	if existing != nil && existing.LinkCount < linkCount {
		existing.LinkCount = linkCount
	}
	s.seenMu.Unlock()
	return usage, true
}

func (s *Scanner) registerUntrustedFileIdentity(path string, info os.FileInfo, usage platform.FileUsage, node *Node) (platform.FileUsage, bool) {
	candidate := &untrustedIdentityCandidate{path: path, node: node}

	s.seenMu.Lock()
	first, exists := s.untrustedSeen[usage.Identity]
	if !exists {
		s.untrustedSeen[usage.Identity] = untrustedIdentityReference{path: path, node: node}
		s.seenMu.Unlock()
		return usage, false
	}
	bucket := s.untrustedCollisions[usage.Identity]
	if bucket == nil {
		bucket = &untrustedIdentityBucket{
			candidates: []*untrustedIdentityCandidate{{path: first.path, node: first.node}},
		}
		s.untrustedCollisions[usage.Identity] = bucket
	}
	s.seenMu.Unlock()

	// Legacy 64-bit Windows directory IDs are not guaranteed collision-free.
	// Resolve both sides of an apparent collision: resolving only the new path
	// cannot match it against the unresolved identifier stored for the first.
	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	candidate.usage = s.filesystem.UsageFor(path, info)
	candidate.resolved = true
	updateNodeUsage(node, candidate.usage)

	for _, previous := range bucket.candidates {
		previousUsage := s.resolveUntrustedIdentity(previous)
		linkCount, confirmed := confirmedHardLink(previousUsage, candidate.usage)
		if !confirmed {
			continue
		}
		if previous.node != nil && previous.node.LinkCount < linkCount {
			previous.node.LinkCount = linkCount
		}
		return candidate.usage, true
	}

	bucket.candidates = append(bucket.candidates, candidate)
	return candidate.usage, false
}

func (s *Scanner) resolveUntrustedIdentity(candidate *untrustedIdentityCandidate) platform.FileUsage {
	if candidate.resolved {
		return candidate.usage
	}
	info, err := os.Stat(candidate.path)
	if err != nil {
		s.report.RecordError(scanErrorFileMetadata, candidate.path, err)
		candidate.resolved = true
		return candidate.usage
	}
	candidate.usage = s.filesystem.UsageFor(candidate.path, info)
	candidate.resolved = true
	if candidate.usage.MetadataError != nil {
		s.report.RecordError(scanErrorUsageMetadata, candidate.path, candidate.usage.MetadataError)
	}
	// The candidate's size may already have contributed to an ancestor total.
	// Only link metadata is safe and relevant to update retroactively here.
	if candidate.node != nil && candidate.usage.HasLinkCount {
		candidate.node.LinkCount = candidate.usage.LinkCount
	}
	return candidate.usage
}

func confirmedHardLink(first, second platform.FileUsage) (uint64, bool) {
	if !first.HasIdentity || !second.HasIdentity || first.Identity != second.Identity {
		return 0, false
	}
	if (!first.HasLinkCount || first.LinkCount < 2) && (!second.HasLinkCount || second.LinkCount < 2) {
		return 0, false
	}
	linkCount := first.LinkCount
	if second.LinkCount > linkCount {
		linkCount = second.LinkCount
	}
	if linkCount < 2 {
		linkCount = 2
	}
	return linkCount, true
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
	return s.buildTreeWithModTime(path, depth, parentID, fileCount, dirCount, 0)
}

func (s *Scanner) buildTreeWithModTime(path string, depth int, parentID int, fileCount, dirCount *int64, modTime int64) (*Node, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if depth > 0 {
		defer atomic.AddInt64(&s.workProcessed, 1)
	}
	abs := s.filesystem.Canonicalize(path)
	if depth == 0 && s.profile.SkipNetworkFS {
		if s.filesystem.IsLikelyNetworkFS(abs) {
			return nil, errNetworkFilesystemRootSkipped
		}
	}
	if s.profile.FollowSymlinks && s.seenDirectory(abs) {
		s.report.RecordSkip(scanSkipRepeatedDirectory)
		return nil, nil
	}
	s.reportProgress(abs)
	if cached := s.reusableCachedDirectory(abs); cached != nil {
		return s.cloneCachedSubtree(cached, depth, parentID, fileCount, dirCount), nil
	}

	// directory node
	root := &Node{
		ParentID:  parentID,
		Name:      s.filesystem.BaseName(abs),
		Size:      0,
		IsFolder:  true,
		Depth:     depth,
		FullPath:  abs,
		Children:  make([]*Node, 0, 128),
		ModTime:   modTime,
		EntryDirs: 1,
	}
	s.assignID(root)
	atomic.AddInt64(dirCount, 1)
	atomic.AddInt64(&s.dirCount, 1)

	entries, diagnostic, err := platform.ReadDirWithDiagnostics(s.filesystem, abs)
	if err != nil {
		s.report.RecordError(scanErrorReadDirectory, abs, err)
		// Preserve the partial tree while making the omission visible in the report.
		return root, nil
	}
	if diagnostic != nil && diagnostic.PortableFallback {
		s.report.RecordPriorityError(scanErrorPortableDirectoryFallback, abs, diagnostic.Cause)
	}
	atomic.AddInt64(&s.workDiscovered, int64(len(entries)))

	// First pass: files now, subdirs later
	type subdir struct {
		full    string
		modTime int64
	}
	subdirs := make([]subdir, 0, 32)
	var smallFilesSize, smallFileCount int64
	var processedBatch int64
	flushProcessed := func() {
		if processedBatch > 0 {
			atomic.AddInt64(&s.workProcessed, processedBatch)
			processedBatch = 0
		}
	}
	defer flushProcessed()

	for _, entry := range entries {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		completeNow := func() bool {
			de := entry.DirEntry
			name := de.Name()
			full := filepath.Join(abs, name)

			if shouldExclude(s.profile, full) {
				s.report.RecordSkip(scanSkipExcluded)
				return true
			}
			isSymlink := de.Type()&os.ModeSymlink != 0
			var info os.FileInfo
			isDir := de.IsDir()
			if isSymlink {
				if !s.profile.FollowSymlinks {
					s.report.RecordSkip(scanSkipSymlink)
					return true
				}
				var err error
				info, err = os.Stat(full)
				if err != nil {
					s.report.RecordError(scanErrorSymlinkTarget, full, err)
					return true
				}
				isDir = info.IsDir()
			}

			if s.profile.SkipHidden {
				hidden := entry.Hidden
				if !entry.HasHidden {
					hidden = s.filesystem.IsHidden(full)
				}
				if hidden {
					s.report.RecordSkip(scanSkipHidden)
					return true
				}
			}

			if isDir {
				if s.profile.SkipNetworkFS {
					networkPath := full
					if isSymlink {
						if resolved, err := filepath.EvalSymlinks(full); err == nil {
							networkPath = resolved
						} else {
							s.report.RecordError(scanErrorResolveSymlink, full, err)
						}
					}
					if s.filesystem.IsLikelyNetworkFS(networkPath) {
						s.report.RecordSkip(scanSkipNetwork)
						return true
					}
				}
				if info == nil {
					var err error
					info, err = de.Info()
					if err != nil {
						s.report.RecordError(scanErrorDirectoryMetadata, full, err)
					}
				}
				var modTime int64
				if info != nil {
					modTime = info.ModTime().Unix()
				}
				subdirs = append(subdirs, subdir{full: full, modTime: modTime})
				return false
			}

			if info == nil {
				var err error
				info, err = de.Info()
				if err != nil {
					s.report.RecordError(scanErrorFileMetadata, full, err)
					return true
				}
			}
			if !info.Mode().IsRegular() {
				s.report.RecordSkip(scanSkipNonRegular)
				return true
			}

			usage := entry.Usage
			batchedUsage := entry.HasUsage && !isSymlink
			if !batchedUsage {
				usage = s.filesystem.UsageFor(full, info)
			}
			isSmall := s.profile.MinFileSize > 0 && info.Size() < s.profile.MinFileSize
			var child *Node
			if !isSmall {
				child = &Node{
					ParentID:  root.ID,
					Name:      name,
					FullPath:  full,
					Size:      usage.AllocatedSize,
					IsFolder:  false,
					Depth:     depth + 1,
					ModTime:   info.ModTime().Unix(),
					LinkCount: usage.LinkCount,
				}
			}
			var duplicate bool
			usage, duplicate = s.registerFileIdentity(full, info, usage, child)
			if usage.MetadataError != nil {
				s.report.RecordError(scanErrorUsageMetadata, full, usage.MetadataError)
			}
			sz := usage.AllocatedSize
			// FileCount describes directory entries, while allocation and treemap
			// nodes remain deduplicated for hard links.
			atomic.AddInt64(fileCount, 1)
			atomic.AddInt64(&s.fileCount, 1)
			root.EntryFiles++

			if isSmall {
				smallFileCount++
				if duplicate {
					s.report.RecordSkip(scanSkipDuplicateIdentity)
					return true
				}
				smallFilesSize += sz
				return true
			}

			if duplicate {
				s.report.RecordSkip(scanSkipDuplicateIdentity)
				return true
			}
			s.assignID(child)
			root.Children = append(root.Children, child)
			root.Size += sz
			return true
		}()
		if completeNow {
			processedBatch++
			if processedBatch >= 64 {
				flushProcessed()
			}
		}
	}

	if smallFileCount > 0 {
		root.Children = append(root.Children, &Node{
			ID:             -1,
			ParentID:       root.ID,
			Name:           "[Small Files]",
			Size:           smallFilesSize,
			IsSmallFiles:   true,
			SmallFileCount: smallFileCount,
			SmallFileLimit: s.profile.MinFileSize,
			Depth:          root.Depth + 1,
		})
		root.Size += smallFilesSize
	}

	// Second pass: scan subdirectories (bounded)
	if len(subdirs) > 0 {
		var wg sync.WaitGroup
		var mu sync.Mutex
		results := make([]*Node, 0, len(subdirs))
		appendResult := func(node *Node) {
			if node == nil {
				return
			}
			mu.Lock()
			results = append(results, node)
			mu.Unlock()
		}

		for _, sd := range subdirs {
			if s.ctx.Err() != nil {
				break
			}
			select {
			case s.sem <- struct{}{}:
				wg.Add(1)
				go func(sd subdir) {
					defer wg.Done()
					defer func() { <-s.sem }()
					n, err := s.buildTreeWithModTime(sd.full, depth+1, root.ID, fileCount, dirCount, sd.modTime)
					if err != nil && s.ctx.Err() == nil {
						s.report.RecordError(scanErrorSubdirectory, sd.full, err)
					}
					appendResult(n)
				}(sd)
			default:
				// inline to avoid deadlock
				n, err := s.buildTreeWithModTime(sd.full, depth+1, root.ID, fileCount, dirCount, sd.modTime)
				if err != nil && s.ctx.Err() == nil {
					s.report.RecordError(scanErrorSubdirectory, sd.full, err)
				}
				appendResult(n)
			}
		}

		wg.Wait()
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		for _, n := range results {
			root.Children = append(root.Children, n)
			root.Size += n.Size
			root.EntryFiles += n.EntryFiles
			root.EntryDirs += n.EntryDirs
		}
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}

	// Sort children by size desc (UI expects this)
	sort.Slice(root.Children, func(i, j int) bool { return root.Children[i].Size > root.Children[j].Size })
	return root, nil
}

func (s *Scanner) reusableCachedDirectory(path string) *Node {
	if len(s.cachedDirectories) == 0 {
		return nil
	}
	clean := canonicalCachePath(path)
	cached := s.cachedDirectories[clean]
	if cached == nil {
		return nil
	}
	for _, dirty := range s.dirtyCachePaths {
		if cachePathWithin(dirty, clean) || cachePathWithin(clean, dirty) {
			return nil
		}
	}
	return cached
}

func cachePathWithin(path, root string) bool {
	path = canonicalCachePath(path)
	root = canonicalCachePath(root)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func (s *Scanner) cloneCachedSubtree(source *Node, depth, parentID int, fileCount, dirCount *int64) *Node {
	var clone func(*Node, int, int) *Node
	clone = func(cached *Node, parent, currentDepth int) *Node {
		if cached == nil || cached.IsFreeSpace {
			return nil
		}
		node := *cached
		node.ParentID = parent
		node.Depth = currentDepth
		node.Children = make([]*Node, 0, len(cached.Children))
		if cached.IsSmallFiles {
			node.ID = -1
		} else {
			s.assignID(&node)
		}
		for _, child := range cached.Children {
			if cloned := clone(child, node.ID, currentDepth+1); cloned != nil {
				node.Children = append(node.Children, cloned)
			}
		}
		return &node
	}

	files := source.EntryFiles
	dirs := source.EntryDirs
	if files == 0 && dirs == 0 {
		files, dirs = subtreeEntryCounts(source)
	}
	atomic.AddInt64(fileCount, int64(files))
	atomic.AddInt64(dirCount, int64(dirs))
	atomic.AddInt64(&s.fileCount, int64(files))
	atomic.AddInt64(&s.dirCount, int64(dirs))
	atomic.AddInt64(&s.workDiscovered, int64(files+dirs))
	atomic.AddInt64(&s.workProcessed, int64(files+dirs))
	atomic.AddInt64(&s.reusedDirectories, int64(dirs))
	return clone(source, parentID, depth)
}
