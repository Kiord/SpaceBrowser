package main

import (
	"path/filepath"
	"testing"
)

func retainedJob(id uint64, path string) *scanJob {
	root := &Node{ID: 0, ParentID: -1, FullPath: path, IsFolder: true}
	return &scanJob{info: ScanJobInfo{ID: id, Path: path, State: "completed"}, snapshot: &treeStoreSnapshot{root: root, nodes: []*Node{root}}}
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

}

func TestSwitchingLocationsKeepsCompletedTreesWithoutCache(t *testing.T) {
	app := newApp("")
	defer app.scanCache.Close()
	profile := app.GetProfile()
	profile.UseCache = false
	if err := app.SetProfile(profile); err != nil {
		t.Fatal(err)
	}
	first, second := retainedJob(1, "C:/"), retainedJob(2, "D:/")
	q := app.jobQueue()
	q.jobs = []*scanJob{first, second}
	for _, id := range []uint64{1, 2, 1, 2, 1} {
		view, err := app.SelectScanJob(id)
		if err != nil || view.Tree == nil {
			t.Fatalf("location %d lost its tree: %+v, %v", id, view, err)
		}
		if first.snapshot == nil || second.snapshot == nil {
			t.Fatal("switching discarded another location")
		}
	}
	// Clearing the acceleration cache and saving settings cannot discard scans either.
	app.scanCache.Clear()
	if err := app.SetProfile(profile); err != nil {
		t.Fatal(err)
	}
	if len(q.jobs) != 2 || first.snapshot == nil || second.snapshot == nil {
		t.Fatal("completed locations were removed")
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
