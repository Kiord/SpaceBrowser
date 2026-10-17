package main

import (
	"path/filepath"
	"testing"
)

func retainedJob(id uint64, path string) *scanJob {
	root := &Node{ID: 0, ParentID: -1, FullPath: path, IsFolder: true}
	return &scanJob{info: ScanJobInfo{ID: id, Path: path, State: "completed"}, snapshot: &treeStoreSnapshot{root: root, nodes: []*Node{root}}, lastUsed: id}
}

func TestRepeatedRescansReleaseWorkersAndOldTreesWithoutCache(t *testing.T) {
	app, path := previewFixture(t)
	for i := 0; i < 12; i++ {
		job, err := app.QueueScan(path)
		if err != nil {
			t.Fatal(err)
		}
		waitJob(t, app, job.ID, "completed")
		if _, err := app.SelectScanJob(job.ID); err != nil {
			t.Fatal(err)
		}
	}
	q := app.jobQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) != 1 {
		t.Fatalf("retained %d jobs for repeated rescans", len(q.jobs))
	}
	job := q.jobs[0]
	if job.worker != nil || job.previous != nil || job.snapshot == nil {
		t.Fatal("terminal job still retains worker or rollback state")
	}
	if len(q.retainedSizes) != 1 {
		t.Fatalf("accounting retained %d obsolete roots", len(q.retainedSizes))
	}
}

func TestRetentionEvictsLeastRecentlyViewedAndSharedCacheReference(t *testing.T) {
	app := &App{profile: Profile{ScanMemoryBudgetMiB: 1}, scanCache: newScanCacheManager(nil)}
	oldest, recent, selected := retainedJob(1, "old"), retainedJob(2, "recent"), retainedJob(3, "selected")
	q := &scanJobQueue{app: app, selected: 3, jobs: []*scanJob{oldest, recent, selected}, retainedSizes: map[*Node]int64{
		oldest.snapshot.root: 600 << 10, recent.snapshot.root: 600 << 10, selected.snapshot.root: 100 << 10,
	}}
	// Simulate large allocations without allocating hundreds of MiB in the test.
	app.scanCache.entries["old"] = &scanCacheEntry{root: oldest.snapshot.root, nodes: oldest.snapshot.nodes}
	q.pruneLocked()
	if !oldest.info.Evicted || oldest.snapshot != nil {
		t.Fatal("oldest snapshot was not evicted")
	}
	if recent.info.Evicted || selected.info.Evicted {
		t.Fatal("recent or displayed snapshot was evicted")
	}
	if len(app.scanCache.entries) != 0 {
		t.Fatal("cache kept the evicted tree alive")
	}
	if len(q.jobs) != 3 {
		t.Fatal("eviction removed location history")
	}
	app.jobs = q
	view, err := app.SelectScanJob(1)
	if err != nil || !view.Job.Evicted || view.Tree != nil {
		t.Fatalf("evicted view = %+v, %v", view, err)
	}
}

func TestRetentionCountsSharedRootsOnceAndProtectsPausedRollback(t *testing.T) {
	app := &App{profile: Profile{ScanMemoryBudgetMiB: 1}, scanCache: newScanCacheManager(nil)}
	completed := retainedJob(1, "completed")
	paused := &scanJob{info: ScanJobInfo{ID: 2, Path: "paused", State: "paused"}, worker: &App{}, previous: completed.snapshot}
	q := &scanJobQueue{app: app, jobs: []*scanJob{completed, paused}, retainedSizes: map[*Node]int64{completed.snapshot.root: 900 << 10}}
	app.scanCache.entries["same"] = &scanCacheEntry{root: completed.snapshot.root, nodes: completed.snapshot.nodes}
	q.pruneLocked()
	if completed.info.Evicted || len(app.scanCache.entries) != 1 {
		t.Fatal("shared allocation charged twice")
	}
	q.retainedSizes[completed.snapshot.root] = 2 << 20
	q.pruneLocked()
	if completed.info.Evicted || paused.previous == nil {
		t.Fatal("paused rollback tree was evicted")
	}
}

func TestScanBudgetChangePrunesImmediately(t *testing.T) {
	app := newApp(filepath.Join(t.TempDir(), "settings.json"))
	t.Cleanup(app.scanCache.Close)
	q := app.jobQueue()
	oldest, selected := retainedJob(1, "old"), retainedJob(2, "selected")
	q.jobs, q.selected = []*scanJob{oldest, selected}, 2
	q.retainedSizes = map[*Node]int64{oldest.snapshot.root: 50 << 20, selected.snapshot.root: 50 << 20}
	profile := app.GetProfile()
	profile.ScanMemoryBudgetMiB = 64
	if err := app.SetProfile(profile); err != nil {
		t.Fatal(err)
	}
	if !oldest.info.Evicted || selected.info.Evicted {
		t.Fatal("lowered budget was not applied immediately")
	}
}

func TestRepeatedPartialRefreshesReleaseRollbackTrees(t *testing.T) {
	app, path := previewFixture(t)
	job, err := app.QueueScan(path)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, app, job.ID, "completed")
	if _, err := app.SelectScanJob(job.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		folder := filepath.Join(path, "pending")
		id := -1
		for _, node := range app.store.snapshotState().nodes {
			if node != nil && node.FullPath == folder {
				id = node.ID
			}
		}
		if id < 0 {
			t.Fatal("refresh folder missing")
		}
		next, err := app.QueueFolderRefresh([]FolderRefreshTarget{{NodeID: id, Path: folder}}, []int{id})
		if err != nil {
			t.Fatal(err)
		}
		waitJob(t, app, next.ID, "completed")
		if _, err := app.SelectScanJob(next.ID); err != nil {
			t.Fatal(err)
		}
	}
	q := app.jobQueue()
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) != 1 || q.jobs[0].previous != nil || q.jobs[0].worker != nil {
		t.Fatal("partial refresh retained obsolete trees")
	}
}
