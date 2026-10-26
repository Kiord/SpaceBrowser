package app

import (
	"bufio"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Cold snapshots are historical views, never evidence that a live cache is clean.
type CachedScanInfo struct {
	Path        string `json:"path"`
	ScannedAt   string `json:"scannedAt"`
	DiskBytes   int64  `json:"diskBytes"`
	MemoryBytes int64  `json:"memoryBytes"`
	Loaded      bool   `json:"loaded"`
}
type coldHeader struct {
	Version                                     int
	Info                                        CachedScanInfo
	Job                                         ScanJobInfo
	RootID, NodeCount, RecordCount, Files, Dirs int
	HasDiskUsage                                bool
	DiskTotal, DiskFree                         int64
}
type coldNode struct {
	Node       Node
	ChildCount int
}
type coldCache struct {
	mu         sync.Mutex
	dir        string
	disabled   bool
	generation uint64
	paths      map[string]uint64
	writes     sync.WaitGroup
}

func (c *coldCache) filename(path string) string {
	return filepath.Join(c.dir, fmt.Sprintf("%x.scan", sha256.Sum256([]byte(canonicalCachePath(path)))))
}

type coldEpoch struct{ global, path uint64 }

func (c *coldCache) epoch(path string) coldEpoch {
	if c == nil {
		return coldEpoch{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return coldEpoch{c.generation, c.paths[canonicalCachePath(path)]}
}
func (c *coldCache) headers() []coldHeader {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, _ := os.ReadDir(c.dir)
	var result []coldHeader
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".scan" {
			continue
		}
		f, err := os.Open(filepath.Join(c.dir, entry.Name()))
		if err != nil {
			continue
		}
		var h coldHeader
		err = gob.NewDecoder(bufio.NewReader(f)).Decode(&h)
		stat, statErr := f.Stat()
		f.Close()
		if err != nil || statErr != nil || h.Version != 1 || filepath.Base(c.filename(h.Info.Path)) != entry.Name() {
			continue
		}
		h.Info.DiskBytes = stat.Size()
		result = append(result, h)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Info.Path < result[j].Info.Path })
	return result
}
func (c *coldCache) save(info ScanJobInfo, snapshot *treeStoreSnapshot, epoch coldEpoch) error {
	if c == nil || snapshot == nil || snapshot.root == nil {
		return nil
	}
	c.mu.Lock()
	allowed := !c.disabled && epoch == (coldEpoch{c.generation, c.paths[canonicalCachePath(info.Path)]})
	c.mu.Unlock()
	if !allowed {
		return nil
	}
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(c.dir, ".snapshot-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	writer := bufio.NewWriter(f)
	encoder := gob.NewEncoder(writer)
	h := coldHeader{Version: 1, Info: CachedScanInfo{Path: info.Path, ScannedAt: info.ScannedAt, MemoryBytes: estimateSnapshotBytes(snapshot)}, Job: info,
		RootID: snapshot.root.ID, NodeCount: len(snapshot.nodes), Files: snapshot.files, Dirs: snapshot.dirs,
		HasDiskUsage: snapshot.hasDiskUsage, DiskTotal: snapshot.diskTotal, DiskFree: snapshot.diskFree}
	stack := []*Node{snapshot.root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		h.RecordCount++
		stack = append(stack, node.Children...)
	}
	if err = encoder.Encode(h); err != nil {
		return err
	}
	stack = append(stack, snapshot.root)
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		record := coldNode{Node: *node, ChildCount: len(node.Children)}
		record.Node.Children = nil
		if err = encoder.Encode(record); err != nil {
			return err
		}
		for i := len(node.Children) - 1; i >= 0; i-- {
			stack = append(stack, node.Children[i])
		}
	}
	if err = writer.Flush(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled || epoch != (coldEpoch{c.generation, c.paths[canonicalCachePath(info.Path)]}) {
		return nil
	}
	// An older writer must not replace a newer completed scan of this path.
	if previous, err := os.Open(c.filename(info.Path)); err == nil {
		var h coldHeader
		decodeErr := gob.NewDecoder(bufio.NewReader(previous)).Decode(&h)
		previous.Close()
		oldTime, _ := time.Parse(time.RFC3339Nano, h.Info.ScannedAt)
		newTime, _ := time.Parse(time.RFC3339Nano, info.ScannedAt)
		if decodeErr == nil && oldTime.After(newTime) {
			return nil
		}
	}
	return os.Rename(name, c.filename(info.Path))
}
func (c *coldCache) load(path string) (*treeStoreSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := os.Open(c.filename(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := gob.NewDecoder(bufio.NewReader(f))
	var h coldHeader
	if err = decoder.Decode(&h); err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if h.Version != 1 || canonicalCachePath(h.Info.Path) != canonicalCachePath(path) || h.NodeCount <= 0 || int64(h.NodeCount) > stat.Size() || h.RootID < 0 || h.RootID >= h.NodeCount {
		return nil, fmt.Errorf("invalid saved scan")
	}
	s := &treeStoreSnapshot{nodes: make([]*Node, h.NodeCount), files: h.Files, dirs: h.Dirs, shared: true, hasDiskUsage: h.HasDiskUsage, diskTotal: h.DiskTotal, diskFree: h.DiskFree}
	if h.RecordCount <= 0 || int64(h.RecordCount) > stat.Size() {
		return nil, fmt.Errorf("invalid saved node count")
	}
	type frame struct {
		node      *Node
		remaining int
	}
	var parents []frame
	for i := 0; i < h.RecordCount; i++ {
		var record coldNode
		if err = decoder.Decode(&record); err != nil {
			return nil, err
		}
		node := &record.Node
		if len(node.Children) != 0 || record.ChildCount < 0 || record.ChildCount > h.RecordCount-i-1 {
			return nil, fmt.Errorf("invalid saved node")
		}
		if node.ID >= 0 {
			if node.ID >= len(s.nodes) || s.nodes[node.ID] != nil {
				return nil, fmt.Errorf("invalid saved node ID")
			}
			s.nodes[node.ID] = node
		} else if !node.IsFreeSpace && !node.IsSmallFiles {
			return nil, fmt.Errorf("invalid virtual node")
		}
		if i == 0 {
			if node.ID != h.RootID || canonicalCachePath(node.FullPath) != canonicalCachePath(path) {
				return nil, fmt.Errorf("invalid saved root")
			}
			s.root = node
		} else {
			if len(parents) == 0 {
				return nil, fmt.Errorf("disconnected saved tree")
			}
			parent := &parents[len(parents)-1]
			if node.ParentID != parent.node.ID {
				return nil, fmt.Errorf("invalid saved parent")
			}
			parent.node.Children = append(parent.node.Children, node)
			parent.remaining--
		}
		if record.ChildCount > 0 {
			parents = append(parents, frame{node, record.ChildCount})
		}
		for len(parents) > 0 && parents[len(parents)-1].remaining == 0 {
			parents = parents[:len(parents)-1]
		}
	}
	if len(parents) != 0 {
		return nil, fmt.Errorf("incomplete saved tree")
	}
	var extra coldNode
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("invalid saved scan trailer")
	}
	return s, nil
}

func (a *App) GetCachedScans() []CachedScanInfo {
	headers := a.coldCache.headers()
	result := make([]CachedScanInfo, 0, len(headers))
	q := a.jobQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, h := range headers {
		info := h.Info
		info.MemoryBytes = 0
		for _, job := range q.jobs {
			if canonicalCachePath(job.info.Path) == canonicalCachePath(info.Path) && job.snapshot != nil && job.info.State == "completed" {
				info.Loaded = true
				info.MemoryBytes = h.Info.MemoryBytes
				break
			}
		}
		result = append(result, info)
	}
	return result
}
func (a *App) ClearCachedScan(path string) error { return a.clearCachedScans(path, false) }
func (a *App) ClearAllCachedScans() error        { return a.clearCachedScans("", true) }
func (a *App) clearCachedScans(path string, all bool) error {
	return a.clearScanLocations(path, all, false)
}

// RemoveScanLocation also cancels and detaches pending work so a removed sidebar
// tile cannot return when its worker finishes or writes its snapshot.
func (a *App) RemoveScanLocation(path string) error {
	return a.clearScanLocations(path, false, true)
}

func (a *App) clearScanLocations(path string, all, removeActive bool) error {
	// Same order as navigation: filesystem, queue, then cache. Save never holds queue.
	a.filesystemMu.Lock()
	defer a.filesystemMu.Unlock()
	q := a.jobQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	c := a.coldCache
	if c != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if all {
			c.generation++
		} else {
			if c.paths == nil {
				c.paths = make(map[string]uint64)
			}
			c.paths[canonicalCachePath(path)]++
		}
		if all {
			entries, err := os.ReadDir(c.dir)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, entry := range entries {
				if !entry.IsDir() && filepath.Ext(entry.Name()) == ".scan" {
					if err := os.Remove(filepath.Join(c.dir, entry.Name())); err != nil {
						return err
					}
				}
			}
		} else if err := os.Remove(c.filename(path)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	matches := func(p string) bool { return all || canonicalCachePath(p) == canonicalCachePath(path) }
	kept := q.jobs[:0]
	for _, job := range q.jobs {
		if job.previous != nil && job.previous.root != nil && matches(job.previous.root.FullPath) {
			job.previous = nil
		}
		if matches(job.info.Path) && (removeActive || (!pendingJob(job.info.State) && job.worker == nil)) {
			if removeActive && job.cancel != nil {
				job.info.State = "cancelled"
				job.cancel()
			}
			if q.running == job.info.ID {
				q.running = 0
			}
			if q.requested == job.info.ID {
				q.requested = 0
			}
			if q.selected == job.info.ID {
				q.selected = 0
				q.viewKey = ""
				a.store.restoreState(&treeStoreSnapshot{})
			}
			continue
		}
		kept = append(kept, job)
	}
	clear(q.jobs[len(kept):])
	q.jobs = kept
	if removeActive {
		q.scheduleLocked()
	}
	// Drop acceleration references too, without invalidating other locations' views.
	if all {
		a.scanCache.Clear()
	} else {
		a.scanCache.Forget(path)
	}
	return nil
}
func (a *App) loadColdJobs(q *scanJobQueue) {
	if !a.GetCachingEnabled() {
		return
	}
	existing := map[string]bool{}
	for _, job := range q.jobs {
		existing[canonicalCachePath(job.info.Path)] = true
	}
	for _, h := range a.coldCache.headers() {
		if existing[canonicalCachePath(h.Info.Path)] {
			continue
		}
		q.nextID++
		info := h.Job
		info.Path = h.Info.Path
		info.Saving = false
		info.ID = q.nextID
		info.State = "completed"
		info.Cached = true
		info.ScannedAt = h.Info.ScannedAt
		q.jobs = append(q.jobs, &scanJob{info: info, revision: 1})
	}
}
func scanTimestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// Caching policy is session-only. It never changes live filesystem watching.
func (a *App) GetCachingEnabled() bool {
	if a.coldCache == nil {
		return false
	}
	a.coldCache.mu.Lock()
	defer a.coldCache.mu.Unlock()
	return !a.coldCache.disabled
}
func (a *App) SetCachingEnabled(enabled bool) {
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	q := a.jobs
	if q != nil {
		q.mu.Lock()
		defer q.mu.Unlock()
	}
	c := a.coldCache
	if c != nil {
		c.mu.Lock()
		if c.disabled != !enabled {
			c.disabled = !enabled
			c.generation++
		}
		c.mu.Unlock()
	}
	if q == nil {
		return
	}
	if enabled {
		a.loadColdJobs(q)
		return
	}
	// Preserve loaded trees and active jobs; hide only unopened saved snapshots.
	kept := q.jobs[:0]
	for _, job := range q.jobs {
		if job.info.Cached && job.snapshot == nil && !pendingJob(job.info.State) {
			continue
		}
		kept = append(kept, job)
	}
	clear(q.jobs[len(kept):])
	q.jobs = kept
}
