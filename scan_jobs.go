package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ScanJobInfo struct {
	ID       uint64          `json:"id"`
	Path     string          `json:"path"`
	State    string          `json:"state"`
	Progress ScanProgress    `json:"progress"`
	Report   *ScanReportInfo `json:"report,omitempty"`
	Error    string          `json:"error,omitempty"`
	Partial  bool            `json:"partial"`
}

type ScanJobView struct {
	Job      ScanJobInfo `json:"job"`
	Tree     *TreeInfo   `json:"tree,omitempty"`
	Key      string      `json:"key"`
	NodeIDs  map[int]int `json:"nodeIds,omitempty"`
	Restored bool        `json:"restored"`
}

type scanJob struct {
	info       ScanJobInfo
	worker     *App
	pause      *scanPause
	cancel     context.CancelFunc
	started    bool
	startedAt  time.Time
	previous   *treeStoreSnapshot
	targets    []FolderRefreshTarget
	tracked    []int
	nodeIDs    map[int]int
	queueOrder uint64
}

type scanJobQueue struct {
	mu                        sync.Mutex
	app                       *App
	jobs                      []*scanJob
	nextID, running, selected uint64
	viewKey                   string
	closed                    bool
	queueClock                uint64
}

func (a *App) jobQueue() *scanJobQueue {
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	if a.jobs == nil {
		a.jobs = &scanJobQueue{app: a}
	}
	return a.jobs
}

func pendingJob(state string) bool {
	return state == "queued" || state == "running" || state == "paused"
}

// Freeze the allocation before handing it to another store. Both stores detach
// lazily on mutation, so navigating between jobs never aliases mutable trees.
func (s *TreeStore) sharedSnapshot() *treeStoreSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shared = true
	return &treeStoreSnapshot{s.root, s.nodes, s.fileCount, s.dirCount, true, s.hasDiskUsage, s.diskTotal, s.diskFree}
}

func (a *App) QueueScan(path string) (*ScanJobInfo, error) {
	path, err := a.validateScanPath(path)
	if err != nil {
		return nil, err
	}
	return a.jobQueue().enqueue(path, nil, nil), nil
}

func (a *App) QueueFolderRefresh(targets []FolderRefreshTarget, tracked []int) (*ScanJobInfo, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("select folders to refresh")
	}
	snapshot := a.store.snapshotState()
	if snapshot.root == nil {
		return nil, fmt.Errorf("no scanned tree")
	}
	return a.jobQueue().enqueue(snapshot.root.FullPath, targets, tracked), nil
}

func (q *scanJobQueue) enqueue(path string, targets []FolderRefreshTarget, tracked []int) *ScanJobInfo {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, job := range q.jobs {
		if canonicalCachePath(job.info.Path) == canonicalCachePath(path) && pendingJob(job.info.State) {
			q.activateLocked(job)
			info := q.infoLocked(job)
			return &info
		}
	}
	base := q.app.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	pause := &scanPause{}
	profile := q.app.GetProfile()
	if q.app.startupScanCacheDisabled(path) {
		profile.UseCache = false
	}
	worker := &App{ctx: ctx, logger: q.app.logger, filesystem: q.app.filesystem, desktop: q.app.desktop,
		locations: q.app.locations, scanCache: q.app.scanCache, profile: profile, initialPause: pause}
	previous := q.app.store.sharedSnapshot()
	if len(targets) > 0 {
		worker.store.restoreState(previous)
	}
	q.nextID++
	job := &scanJob{info: ScanJobInfo{ID: q.nextID, Path: path, State: "queued", Partial: len(targets) > 0}, worker: worker,
		pause: pause, cancel: cancel, previous: previous, targets: targets, tracked: tracked}
	q.jobs = append(q.jobs, job)
	q.queueClock++
	job.queueOrder = q.queueClock
	q.activateLocked(job)
	info := q.infoLocked(job)
	return &info
}

// An explicit scan request takes priority. Interrupted jobs remain paused
// until the user chooses to continue them.
func (q *scanJobQueue) activateLocked(target *scanJob) {
	for _, job := range q.jobs {
		if job != target && (job.info.State == "running" || job.info.State == "queued") {
			job.pause.set(true)
			job.info.State = "paused"
		}
	}
	if target.info.State != "running" {
		target.info.State = "queued"
		q.running = 0
	}
	q.scheduleLocked()
}

func (q *scanJobQueue) scheduleLocked() {
	if q.closed {
		return
	}
	if q.running == 0 {
		var next *scanJob
		for _, job := range q.jobs {
			if job.info.State == "queued" && (next == nil || job.queueOrder < next.queueOrder) {
				next = job
			}
		}
		if job := next; job != nil {
			q.running = job.info.ID
			job.info.State = "running"
			if !job.started {
				job.pause = &scanPause{}
				job.worker.initialPause = job.pause
				job.started = true
				job.startedAt = time.Now()
				go q.run(job)
			} else {
				job.pause.set(false)
			}
		}
	}
	active := false
	for _, job := range q.jobs {
		active = active || pendingJob(job.info.State)
	}
	q.app.scanMu.Lock()
	q.app.scanActive = active // filesystem mutations remain guarded during queued work
	q.app.scanMu.Unlock()
}

func (q *scanJobQueue) run(job *scanJob) {
	q.mu.Lock()
	partial, path := job.info.Partial, job.info.Path
	q.mu.Unlock()
	var tree *TreeInfo
	var refresh *FolderRefreshResult
	var err error
	if partial {
		refresh, err = job.worker.RefreshFolders(job.targets, job.tracked)
	} else {
		tree, err = job.worker.GetFullTree(path)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	job.info.Progress.ElapsedMilliseconds = job.pause.elapsed(job.startedAt, time.Now()).Milliseconds()
	if job.info.State != "cancelled" {
		if err != nil {
			job.info.State, job.info.Error = "failed", err.Error()
		} else {
			job.info.State = "completed"
			if tree != nil {
				job.info.Report = tree.ScanReport
			}
			if refresh != nil {
				job.info.Report, job.nodeIDs = refresh.ScanReport, refresh.NodeIDs
			}
			snapshot := job.worker.store.snapshotState()
			job.info.Progress.FileCount, job.info.Progress.DirCount = int64(snapshot.files), int64(snapshot.dirs)
			job.info.Progress.Fraction = 1
		}
	}
	if q.running == job.info.ID {
		q.running = 0
	}
	q.scheduleLocked()
}

func (q *scanJobQueue) infoLocked(job *scanJob) ScanJobInfo {
	info := job.info
	if job.started && pendingJob(info.State) {
		info.Progress = job.worker.GetScanProgress()
		info.Progress.ElapsedMilliseconds = job.pause.elapsed(job.startedAt, time.Now()).Milliseconds()
	}
	info.Progress.Paused = info.State == "paused"
	return info
}

func (a *App) GetScanJobs() []ScanJobInfo {
	q := a.jobQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]ScanJobInfo, 0, len(q.jobs))
	for _, job := range q.jobs {
		result = append(result, q.infoLocked(job))
	}
	return result
}

func (a *App) SetScanJobPaused(id uint64, paused bool) {
	q := a.jobQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, job := range q.jobs {
		if job.info.ID != id || !pendingJob(job.info.State) {
			continue
		}
		if paused && job.info.State != "paused" {
			job.pause.set(true)
			job.info.State = "paused"
			if q.running == id {
				q.running = 0
			}
		} else if !paused {
			q.activateLocked(job)
			return
		}
		q.scheduleLocked()
		return
	}
}

func (a *App) CancelScanJob(id uint64) {
	q := a.jobQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, job := range q.jobs {
		if job.info.ID != id || !pendingJob(job.info.State) {
			continue
		}
		job.info = q.infoLocked(job)
		job.info.State = "cancelled"
		job.cancel()
		// A cancelled worker may still be returning from native I/O. Its store
		// is isolated and its result is discarded, so the next job can proceed.
		if q.running == id {
			q.running = 0
		}
		q.scheduleLocked()
		return
	}
}

func (a *App) SelectScanJob(id uint64) (*ScanJobView, error) {
	a.filesystemMu.Lock()
	defer a.filesystemMu.Unlock()
	q := a.jobQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.selected != id {
		for _, old := range q.jobs {
			if old.info.ID == q.selected && old.info.State == "completed" && strings.HasSuffix(q.viewKey, ":completed") {
				old.worker.store.restoreState(a.store.sharedSnapshot())
			}
		}
		q.selected, q.viewKey = id, ""
	}
	return q.viewLocked(id)
}

func (a *App) GetScanJobView(id uint64) (*ScanJobView, error) {
	a.filesystemMu.Lock()
	defer a.filesystemMu.Unlock()
	q := a.jobQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.selected != id {
		return nil, nil
	}
	return q.viewLocked(id)
}

func (q *scanJobQueue) viewLocked(id uint64) (*ScanJobView, error) {
	for _, job := range q.jobs {
		if job.info.ID != id {
			continue
		}
		info := q.infoLocked(job)
		if pendingJob(info.State) && info.Progress.LivePreview {
			job.worker.GetScanPreview(info.Progress.Generation)
		}
		job.worker.store.mu.RLock()
		generation := job.worker.store.generation
		job.worker.store.mu.RUnlock()
		key := strconv.FormatUint(id, 10) + ":" + strconv.FormatUint(generation, 10) + ":" + info.State
		view := &ScanJobView{Job: info, Key: key, NodeIDs: job.nodeIDs}
		var snapshot *treeStoreSnapshot
		if info.State == "cancelled" || info.State == "failed" {
			snapshot, view.Restored = job.previous, true
		} else {
			snapshot = job.worker.store.sharedSnapshot()
		}
		if snapshot != nil && snapshot.root != nil {
			view.Tree = &TreeInfo{RootID: snapshot.root.ID, FileCount: snapshot.files, DirCount: snapshot.dirs}
		}
		if key != q.viewKey {
			q.app.store.restoreState(snapshot)
			q.viewKey = key
		}
		return view, nil
	}
	return nil, fmt.Errorf("scan job not found")
}

func (q *scanJobQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	for _, job := range q.jobs {
		job.cancel()
	}
}
