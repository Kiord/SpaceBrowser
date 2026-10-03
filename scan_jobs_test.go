package main

import (
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

func TestQueuePauseAdvancesNextJobAndKeepsSnapshotsIsolated(t *testing.T) {
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
	if second.State != "queued" {
		t.Fatal("second job did not queue")
	}
	duplicate, _ := a.QueueScan(secondPath)
	if duplicate.ID != second.ID {
		t.Fatal("duplicate active path queued twice")
	}
	a.SetScanJobPaused(first.ID, true)
	waitJob(t, a, second.ID, "completed")
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

func TestQueueCancellationRestoresBeforeTreeAndSkipsCancelledQueueEntry(t *testing.T) {
	a, path, release := queuedFixture(t)
	baseline := &Node{ID: 0, FullPath: "previous", IsFolder: true}
	a.store.Replace(baseline, []*Node{baseline}, 0, 1)
	first, _ := a.QueueScan(path)
	select {
	case <-a.filesystem.(*blockingReadDirPlatform).entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	second, _ := a.QueueScan(t.TempDir())
	a.CancelScanJob(second.ID)
	waitJob(t, a, second.ID, "cancelled")
	third, _ := a.QueueScan(t.TempDir())
	a.SelectScanJob(first.ID)
	a.CancelScanJob(first.ID)
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

func TestContinuePausedJobWaitsForRunningJob(t *testing.T) {
	a, path, release := queuedFixture(t)
	first, _ := a.QueueScan(path)
	select {
	case <-a.filesystem.(*blockingReadDirPlatform).entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	second, _ := a.QueueScan(t.TempDir())
	a.SetScanJobPaused(second.ID, true)
	a.SetScanJobPaused(second.ID, false)
	if jobs := a.GetScanJobs(); jobs[1].State != "queued" || jobs[0].State != "running" {
		t.Fatalf("bad states: %+v", jobs)
	}
	release()
	waitJob(t, a, first.ID, "completed")
	waitJob(t, a, second.ID, "completed")
}
