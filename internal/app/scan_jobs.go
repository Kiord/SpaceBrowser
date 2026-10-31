package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ScanJobInfo struct {
	ScannedAt  string          `json:"scannedAt,omitempty"`
	Cached     bool            `json:"cached"`
	Saving     bool            `json:"saving"`
	CacheError string          `json:"cacheError,omitempty"`
	ID         uint64          `json:"id"`
	Path       string          `json:"path"`
	State      string          `json:"state"`
	Progress   ScanProgress    `json:"progress"`
	Report     *ScanReportInfo `json:"report,omitempty"`
	Error      string          `json:"error,omitempty"`
	Partial    bool            `json:"partial"`
}

type ScanJobView struct {
	Job               ScanJobInfo `json:"job"`
	Tree              *TreeInfo   `json:"tree,omitempty"`
	Key               string      `json:"key"`
	NodeIDs           map[int]int `json:"nodeIds,omitempty"`
	MappingGeneration uint64      `json:"mappingGeneration"`
	Restored          bool        `json:"restored"`
}

type scanJob struct {
	coldEpoch         coldEpoch
	snapshot          *treeStoreSnapshot
	revision          uint64
	info              ScanJobInfo
	worker            *App
	pause             *scanPause
	cancel            context.CancelFunc
	started           bool
	startedAt         time.Time
	previous          *treeStoreSnapshot
	targets           []FolderRefreshTarget
	tracked           []int
	nodeIDs           map[int]int
	mappingGeneration uint64
	queueOrder        uint64
	autoResume        bool
}

type scanJobQueue struct {
	requested                 uint64
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
		a.loadColdJobs(a.jobs)
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
	worker := &App{ctx: ctx, logger: q.app.logger, filesystem: q.app.filesystem, desktop: q.app.desktop,
		locations: q.app.locations, scanCache: q.app.scanCache, profile: profile, initialPause: pause,
		defaultSettingsPath: q.app.GetDefaultSettingsPath()}
	previous := q.app.store.sharedSnapshot()
	if len(targets) > 0 {
		worker.store.restoreState(previous)
	}
	q.nextID++
	job := &scanJob{coldEpoch: q.app.coldCache.epoch(path), info: ScanJobInfo{ID: q.nextID, Path: path, State: "queued", Partial: len(targets) > 0}, worker: worker,
		pause: pause, cancel: cancel, previous: previous, targets: targets, tracked: tracked}
	q.jobs = append(q.jobs, job)
	q.requested = job.info.ID
	q.queueClock++
	job.queueOrder = q.queueClock
	q.activateLocked(job)
	info := q.infoLocked(job)
	return &info
}

// An explicit scan request takes priority. Remember interrupted jobs so they
// resume in reverse interruption order, without resuming manually paused work.
func (q *scanJobQueue) activateLocked(target *scanJob) {
	for _, job := range q.jobs {
		if job != target && (job.info.State == "running" || job.info.State == "queued") {
			job.pause.set(true)
			job.info.State = "paused"
			job.autoResume = true
			q.queueClock++
			job.queueOrder = q.queueClock
		}
	}
	target.autoResume = false
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
		if next == nil {
			for _, job := range q.jobs {
				if job.info.State == "paused" && job.autoResume && (next == nil || job.queueOrder > next.queueOrder) {
					next = job
				}
			}
		}
		if job := next; job != nil {
			job.autoResume = false
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
	var included []*scanJob
	defer func() {
		type save struct {
			job      *scanJob
			info     ScanJobInfo
			snapshot *treeStoreSnapshot
			epoch    coldEpoch
			revision uint64
		}
		var saves []save
		if job.info.State == "completed" {
			included = append(included, job)
		}
		for _, target := range included {
			target.info.Saving = q.app.GetCachingEnabled()
			saves = append(saves, save{target, target.info, target.snapshot, target.coldEpoch, target.revision})
		}
		if len(saves) > 0 && q.app.coldCache != nil {
			q.app.coldCache.writes.Add(1)
		}
		q.mu.Unlock()
		if len(saves) > 0 {
			if q.app.coldCache != nil {
				defer q.app.coldCache.writes.Done()
			}
			for _, pending := range saves {
				err := q.app.coldCache.save(pending.info, pending.snapshot, pending.epoch)
				q.mu.Lock()
				if pending.job.revision == pending.revision {
					pending.job.info.Saving = false
					if err != nil {
						q.app.logger.Warningf("could not save scan snapshot: %v", err)
						pending.job.info.CacheError = err.Error()
					}
				}
				q.mu.Unlock()
			}
		}
	}()
	if q.closed {
		job.info.State = "cancelled"
	}
	job.info.Progress.ElapsedMilliseconds = job.pause.elapsed(job.startedAt, time.Now()).Milliseconds()
	if job.info.State != "cancelled" {
		if err != nil {
			job.info.State, job.info.Error = "failed", err.Error()
		} else {
			job.info.State = "completed"
			job.info.ScannedAt = scanTimestamp()
			if tree != nil {
				job.info.Report = tree.ScanReport
			}
			if refresh != nil {
				job.info.Report, job.nodeIDs = refresh.ScanReport, refresh.NodeIDs
				job.mappingGeneration++
			}
			snapshot := job.worker.store.snapshotState()
			job.info.Progress.FileCount, job.info.Progress.DirCount = int64(snapshot.files), int64(snapshot.dirs)
			job.info.Progress.Fraction = 1
		}
	}
	if q.running == job.info.ID {
		q.running = 0
	}
	// Terminal jobs retain only a snapshot and lightweight metadata, never a worker.
	if job.info.State == "completed" {
		job.snapshot = job.worker.store.sharedSnapshot()
		if job.worker.GetProfile().UpdateIncludedScans {
			if partial {
				included = q.updateRefreshedScansLocked(job, refresh.refreshedPaths)
			} else {
				included = q.updateIncludedScansLocked(job)
			}
		}
	} else {
		job.snapshot = job.previous
	}
	job.previous = nil
	job.worker = nil
	job.targets, job.tracked = nil, nil
	job.cancel()
	job.cancel = nil
	job.revision++
	q.scheduleLocked()
	q.pruneLocked()
}

func (q *scanJobQueue) infoLocked(job *scanJob) ScanJobInfo {
	info := job.info
	if job.worker != nil && job.started && pendingJob(info.State) {
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
		if paused {
			job.autoResume = false
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
		if !job.started {
			job.snapshot, job.previous = job.previous, nil
			job.worker, job.cancel = nil, nil
			job.targets, job.tracked = nil, nil
			job.revision++
		}
		// A cancelled worker may still be returning from native I/O. Its store
		// is isolated and its result is discarded, so the next job can proceed.
		if q.running == id {
			q.running = 0
		}
		q.scheduleLocked()
		q.pruneLocked()
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
				old.snapshot = a.store.sharedSnapshot()
				old.revision++
			}
		}
		q.selected, q.viewKey = id, ""
	}
	q.requested = 0
	view, err := q.viewLocked(id)
	q.pruneLocked()
	return view, err
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
		if job.info.Cached && job.snapshot == nil {
			snapshot, err := q.app.coldCache.load(job.info.Path)
			if err != nil {
				return nil, fmt.Errorf("load saved scan (clear it or rescan): %w", err)
			}
			job.snapshot = snapshot
		}
		info := q.infoLocked(job)
		if pendingJob(info.State) && info.Progress.LivePreview {
			job.worker.GetScanPreview(info.Progress.Generation)
		}
		generation := job.revision
		if job.worker != nil {
			job.worker.store.mu.RLock()
			generation = job.worker.store.generation
			job.worker.store.mu.RUnlock()
		}
		key := strconv.FormatUint(id, 10) + ":" + strconv.FormatUint(generation, 10) + ":" + info.State
		view := &ScanJobView{Job: info, Key: key, NodeIDs: job.nodeIDs, MappingGeneration: job.mappingGeneration}
		var snapshot *treeStoreSnapshot
		if info.State == "cancelled" || info.State == "failed" {
			snapshot, view.Restored = job.snapshot, true
			if job.worker != nil {
				snapshot = job.previous
			}
		} else if job.worker != nil {
			snapshot = job.worker.store.sharedSnapshot()
		} else {
			snapshot = job.snapshot
		}
		if snapshot != nil && snapshot.root != nil {
			view.Tree = &TreeInfo{RootID: snapshot.root.ID, FileCount: snapshot.files, DirCount: snapshot.dirs}
		}
		if key != q.viewKey {
			if snapshot == nil {
				snapshot = &treeStoreSnapshot{}
			}
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
		if job.cancel != nil {
			job.cancel()
		}
	}
}
