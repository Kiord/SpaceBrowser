package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"spacebrowser/internal/platform"
)

func waitJob(t *testing.T, a *App, id uint64, state string) ScanJobInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range a.GetScanJobs() {
			if job.ID == id {
				if job.State == state {
					return job
				}
				if job.State == "failed" {
					t.Fatalf("job failed: %s", job.Error)
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job %d never became %s: %+v", id, state, a.GetScanJobs())
	return ScanJobInfo{}
}

func queuedFixture(t *testing.T) (*App, string, func()) {
	a, root := previewFixture(t)
	block := &blockingReadDirPlatform{API: platform.Impl, path: filepath.Join(root, "pending"), entered: make(chan struct{}), release: make(chan struct{})}
	a.filesystem = block
	var once sync.Once
	release := func() { once.Do(func() { close(block.release) }) }
	t.Cleanup(func() { a.jobQueue().close(); release() })
	return a, root, release
}

func TestQueuedScanEstimatesFromPreviousCompletedScan(t *testing.T) {
	path := t.TempDir()
	completed := func(milliseconds int64) ScanJobInfo {
		return ScanJobInfo{Path: path, State: "completed", Progress: ScanProgress{ElapsedMilliseconds: milliseconds}}
	}
	failed := completed(1000)
	failed.State = "failed"
	cancelled := completed(2000)
	cancelled.State = "cancelled"
	partial := completed(3000)
	partial.Partial = true
	other := completed(4000)
	other.Path = filepath.Join(path, "other")
	canonical := completed(5000)
	canonical.Path += string(os.PathSeparator) + "."
	for _, tc := range []struct {
		name     string
		history  []ScanJobInfo
		partial  bool
		expected int64
	}{
		{name: "first scan"},
		{name: "previous full scan", history: []ScanJobInfo{completed(10000)}, expected: 10000},
		{name: "canonical location", history: []ScanJobInfo{canonical}, expected: 5000},
		{name: "another location", history: []ScanJobInfo{other}},
		{name: "unavailable legacy timing", history: []ScanJobInfo{completed(0)}},
		{name: "invalid timing", history: []ScanJobInfo{completed(-1)}},
		{name: "latest successful full scan", history: []ScanJobInfo{completed(20000), completed(10000), failed, cancelled, partial, other}, expected: 10000},
		{name: "partial history only", history: []ScanJobInfo{partial}},
		{name: "partial refresh uses work heuristic", history: []ScanJobInfo{completed(10000)}, partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(filepath.Join(t.TempDir(), "settings.json"))
			t.Cleanup(func() { a.Shutdown(nil) })
			// Hold the scheduler closed to inspect the queued job before any work.
			q := &scanJobQueue{app: a, closed: true}
			for _, info := range tc.history {
				q.nextID++
				info.ID = q.nextID
				q.jobs = append(q.jobs, &scanJob{info: info})
			}
			var targets []FolderRefreshTarget
			if tc.partial {
				targets = []FolderRefreshTarget{{NodeID: 0, Path: path}}
			}
			info := q.enqueue(path, targets, nil)
			t.Cleanup(q.jobs[len(q.jobs)-1].cancel)
			if info.EstimatedDurationMilliseconds != tc.expected {
				t.Fatalf("estimated duration = %d ms, want %d", info.EstimatedDurationMilliseconds, tc.expected)
			}
			if info.Progress.ElapsedMilliseconds != 0 {
				t.Fatal("queued scan inherited the previous scan's elapsed time")
			}
		})
	}
}

func TestRescanEstimateUsesSavedDurationAfterRestart(t *testing.T) {
	config := filepath.Join(t.TempDir(), "settings.json")
	path := t.TempDir()
	a := newApp(config)
	info := ScanJobInfo{Path: path, State: "completed", ScannedAt: scanTimestamp(), Progress: ScanProgress{ElapsedMilliseconds: 12000}}
	if err := a.coldCache.save(info, coldFixture(path), a.coldCache.epoch(path)); err != nil {
		t.Fatal(err)
	}
	a.Shutdown(nil)
	restarted := newApp(config)
	t.Cleanup(func() { restarted.Shutdown(nil) })
	q := restarted.jobQueue()
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	job, err := restarted.QueueScan(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.jobs[len(q.jobs)-1].cancel)
	if job.EstimatedDurationMilliseconds != 12000 {
		t.Fatalf("saved duration not used for rescan: %+v", job)
	}
	if q.jobs[0].snapshot != nil {
		t.Fatal("duration estimate eagerly loaded the saved tree")
	}
}

func TestRescanEstimateTracksActiveTimeAcrossPausesAndCompletion(t *testing.T) {
	a, path, release := queuedFixture(t)
	q := a.jobQueue()
	q.mu.Lock()
	q.nextID++
	q.jobs = append(q.jobs, &scanJob{info: ScanJobInfo{ID: q.nextID, Path: path, State: "completed",
		Progress: ScanProgress{ElapsedMilliseconds: 10000}}})
	q.mu.Unlock()
	info, err := a.QueueScan(path)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.filesystem.(*blockingReadDirPlatform).entered:
	case <-time.After(5 * time.Second):
		t.Fatal("rescan did not start")
	}
	q.mu.Lock()
	job := q.jobs[len(q.jobs)-1]
	start := time.Now().Add(-time.Minute)
	job.startedAt = start
	job.pause.setAt(true, start.Add(5*time.Second))
	job.info.State = "paused"
	q.mu.Unlock()
	assertProgress := func() {
		t.Helper()
		for _, progress := range a.GetScanJobs() {
			if progress.ID == info.ID {
				if progress.EstimatedDurationMilliseconds != 10000 || progress.Progress.ElapsedMilliseconds != 5000 {
					t.Fatalf("pause changed elapsed time or its estimate: %+v", progress)
				}
				return
			}
		}
		t.Fatal("rescan not found")
	}
	assertProgress()
	assertProgress()
	a.SetScanJobPaused(info.ID, false)
	release()
	done := waitJob(t, a, info.ID, "completed")
	if done.Progress.Fraction != 1 || done.Progress.ElapsedMilliseconds < 5000 || done.Progress.ElapsedMilliseconds >= 10000 {
		t.Fatalf("completion did not retain active time: %+v", done)
	}
}

func TestQueuedWorkerCanSaveReportsInDefaultConfigLocation(t *testing.T) {
	defaultPath := filepath.Join(t.TempDir(), "settings.json")
	a := newAppWithPaths(filepath.Join(t.TempDir(), "custom.json"), defaultPath)
	t.Cleanup(func() { a.Shutdown(nil) })
	path := t.TempDir()
	pending := filepath.Join(path, "pending")
	if err := os.Mkdir(pending, 0700); err != nil {
		t.Fatal(err)
	}
	block := &blockingReadDirPlatform{API: platform.Impl, path: pending, entered: make(chan struct{}), release: make(chan struct{})}
	a.filesystem = block
	var once sync.Once
	release := func() { once.Do(func() { close(block.release) }) }
	t.Cleanup(release)
	job, err := a.QueueScan(path)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-block.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not start")
	}
	q := a.jobQueue()
	q.mu.Lock()
	worker := q.jobs[0].worker
	q.mu.Unlock()
	release()
	waitJob(t, a, job.ID, "completed")
	details := testScanReportDetails(time.Now())
	info := worker.persistScanReport(path, details.StartedAt, details.Duration, details.Profile,
		details.Report, details.Files, details.Folders, details.Bytes)
	if info == nil || info.SaveError != "" {
		t.Fatalf("queued worker could not save report: %+v", info)
	}
	if filepath.Dir(info.ReportPath) != filepath.Join(filepath.Dir(defaultPath), "logs") {
		t.Fatalf("report saved outside default config location: %s", info.ReportPath)
	}
	if _, err := os.Stat(info.ReportPath); err != nil {
		t.Fatal(err)
	}
}

func TestNewScanPausesCurrentJobAndKeepsSnapshotsIsolated(t *testing.T) {
	a, firstPath, release := queuedFixture(t)
	first, err := a.QueueScan(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	block := a.filesystem.(*blockingReadDirPlatform)
	select {
	case <-block.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first scan did not start")
	}
	secondPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(secondPath, "second"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := a.QueueScan(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != "running" {
		t.Fatal("second job did not take priority")
	}
	waitJob(t, a, second.ID, "completed")
	waitJob(t, a, first.ID, "running")
	a.SetScanJobPaused(first.ID, true)
	release()
	waitJob(t, a, first.ID, "paused")
	a.SelectScanJob(first.ID)
	if jobs := a.GetScanJobs(); jobs[0].State != "paused" {
		t.Fatal("opening a paused tile resumed it")
	}
	view, err := a.SelectScanJob(second.ID)
	if err != nil || view.Tree == nil || a.store.root.FullPath != secondPath {
		t.Fatalf("second snapshot: %+v %v", view, err)
	}
	if stale, _ := a.GetScanJobView(first.ID); stale != nil {
		t.Fatal("unselected preview was published")
	}
	a.SetScanJobPaused(first.ID, false)
	waitJob(t, a, first.ID, "completed")
	if a.store.root.FullPath != secondPath {
		t.Fatal("background completion replaced foreground tree")
	}
	view, err = a.SelectScanJob(first.ID)
	if err != nil || view.Tree == nil || a.store.root.FullPath != firstPath {
		t.Fatalf("first snapshot: %+v %v", view, err)
	}
}

func TestQueueCancellationRestoresBeforeTreeAndAllowsNextScan(t *testing.T) {
	a, path, release := queuedFixture(t)
	baseline := &Node{ID: 0, FullPath: "previous", IsFolder: true}
	a.store.Replace(baseline, []*Node{baseline}, 0, 1)
	first, _ := a.QueueScan(path)
	select {
	case <-a.filesystem.(*blockingReadDirPlatform).entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	second, _ := a.QueueScan(path)
	if second.ID != first.ID {
		t.Fatal("duplicate active path created another scan")
	}
	a.CancelScanJob(second.ID)
	waitJob(t, a, second.ID, "cancelled")
	third, _ := a.QueueScan(t.TempDir())
	a.SelectScanJob(first.ID)
	view, err := a.GetScanJobView(first.ID)
	if err != nil || !view.Restored || a.store.root != baseline {
		t.Fatalf("cancel did not restore baseline: %+v %v", view, err)
	}
	waitJob(t, a, third.ID, "completed")
	if a.store.root != baseline {
		t.Fatal("background completion changed restored tree")
	}
	release()
}

func TestQueuedFolderRefreshPublishesMappingGeneration(t *testing.T) {
	a, _, root, ids := refreshFixture(t)
	t.Cleanup(func() { a.Shutdown(nil) })
	job, err := a.QueueFolderRefresh([]FolderRefreshTarget{{NodeID: ids["a"], Path: filepath.Join(root, "a")}}, []int{ids["a"]})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, a, job.ID, "completed")
	view, err := a.SelectScanJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.MappingGeneration == 0 || view.NodeIDs == nil {
		t.Fatalf("completed refresh has no mapping generation: %+v", view)
	}
}

func TestContinuePausedJobPreemptsRunningJob(t *testing.T) {
	a := &App{}
	q := a.jobQueue()
	first := &scanJob{info: ScanJobInfo{ID: 1, State: "running"}, pause: &scanPause{}, started: true}
	second := &scanJob{info: ScanJobInfo{ID: 2, State: "paused"}, pause: &scanPause{}, started: true}
	second.pause.set(true)
	q.jobs, q.running = []*scanJob{first, second}, 1
	a.SetScanJobPaused(2, false)
	if first.info.State != "paused" || second.info.State != "running" || q.running != 2 {
		t.Fatalf("resume did not take priority: %s, %s", first.info.State, second.info.State)
	}
	if !first.pause.paused.Load() || second.pause.paused.Load() {
		t.Fatal("worker pause gates did not follow the priority switch")
	}
	a.SetScanJobPaused(1, false)
	if second.info.State != "paused" || first.info.State != "running" || q.running != 1 {
		t.Fatal("switching back did not pause the other scan")
	}
}

func TestInterruptedScansResumeInReverseOrder(t *testing.T) {
	for _, terminal := range []string{"completed", "cancelled", "failed"} {
		t.Run(terminal, func(t *testing.T) {
			a := &App{}
			q := a.jobQueue()
			makeJob := func(id uint64) *scanJob {
				job := &scanJob{info: ScanJobInfo{ID: id, State: "paused"}, pause: &scanPause{}, started: true}
				job.pause.set(true)
				return job
			}
			first, second, third, manual := makeJob(1), makeJob(2), makeJob(3), makeJob(4)
			q.jobs = []*scanJob{first, second, third, manual}
			q.activateLocked(first)
			q.activateLocked(second)
			q.activateLocked(third)
			third.info.State, q.running = terminal, 0
			q.scheduleLocked()
			if q.running != second.info.ID || second.pause.paused.Load() || first.info.State != "paused" {
				t.Fatal("most recently interrupted scan did not resume first")
			}
			second.info.State, q.running = "completed", 0
			q.scheduleLocked()
			if q.running != first.info.ID || first.pause.paused.Load() {
				t.Fatal("original scan did not resume")
			}
			first.info.State, q.running = "completed", 0
			q.scheduleLocked()
			if q.running != 0 || manual.info.State != "paused" || !manual.pause.paused.Load() {
				t.Fatal("manually paused scan resumed automatically")
			}
		})
	}
}

func TestManualPauseClearsAutomaticResume(t *testing.T) {
	a := &App{}
	q := a.jobQueue()
	first := &scanJob{info: ScanJobInfo{ID: 1, State: "running"}, pause: &scanPause{}, started: true}
	second := &scanJob{info: ScanJobInfo{ID: 2, State: "paused"}, pause: &scanPause{}, started: true}
	q.jobs, q.running = []*scanJob{first, second}, 1
	q.activateLocked(second)
	a.SetScanJobPaused(first.info.ID, true)
	second.info.State, q.running = "completed", 0
	q.scheduleLocked()
	if q.running != 0 || first.autoResume || first.info.State != "paused" {
		t.Fatal("explicit pause did not override automatic resume")
	}
}

func TestQueuedWorkerCannotUndoCacheDisable(t *testing.T) {
	for _, reenable := range []bool{false, true} {
		t.Run(fmt.Sprint(reenable), func(t *testing.T) {
			a, path, release := queuedFixture(t)
			profile := a.GetProfile()
			a.scanCache.SetEnabled(true)
			if err := a.SetProfile(profile); err != nil {
				t.Fatal(err)
			}
			job, err := a.QueueScan(path)
			if err != nil {
				t.Fatal(err)
			}
			block := a.filesystem.(*blockingReadDirPlatform)
			select {
			case <-block.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("scan did not start")
			}
			a.scanCache.SetEnabled(false)
			if err := a.SetProfile(profile); err != nil {
				t.Fatal(err)
			}
			if reenable {
				a.scanCache.SetEnabled(true)
				if err := a.SetProfile(profile); err != nil {
					t.Fatal(err)
				}
			}
			release()
			waitJob(t, a, job.ID, "completed")
			a.scanCache.mu.Lock()
			count := len(a.scanCache.entries)
			a.scanCache.mu.Unlock()
			if count != 0 {
				t.Fatalf("worker restored %d cache entries", count)
			}
		})
	}
}
