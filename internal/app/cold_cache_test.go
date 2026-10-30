package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func coldFixture(path string) *treeStoreSnapshot {
	root := &Node{ID: 0, ParentID: -1, FullPath: path, Name: filepath.Base(path), IsFolder: true, DiskTotal: 1000, DiskFree: 200}
	child := &Node{ID: 2, ParentID: 0, FullPath: filepath.Join(path, "file"), Name: "file", Size: 800, LinkCount: 3, ModTime: 123}
	small := &Node{ID: -1, ParentID: 0, Name: "[Small Files]", IsSmallFiles: true, LinkCount: 4, SmallFileCount: 12}
	free := &Node{ID: -1, ParentID: 0, Name: "[Free Disk Space]", IsFreeSpace: true, Size: 200}
	root.Children = []*Node{child, small, free}
	return &treeStoreSnapshot{root: root, nodes: []*Node{root, nil, child}, files: 13, dirs: 1, shared: true, hasDiskUsage: true, diskTotal: 1000, diskFree: 200}
}
func TestColdCacheRoundTripAndLatestPerPath(t *testing.T) {
	c := &coldCache{dir: t.TempDir()}
	path := t.TempDir()
	snapshot := coldFixture(path)
	info := ScanJobInfo{Path: path, State: "completed", ScannedAt: "2026-10-18T10:00:00Z"}
	if err := c.save(info, snapshot, c.epoch(path)); err != nil {
		t.Fatal(err)
	}
	info.ScannedAt = "2026-10-18T11:00:00Z"
	snapshot.root.Name = "latest"
	if err := c.save(info, snapshot, c.epoch(path)); err != nil {
		t.Fatal(err)
	}
	info.ScannedAt = "2026-10-18T09:00:00Z"
	snapshot.root.Name = "older"
	if err := c.save(info, snapshot, c.epoch(path)); err != nil {
		t.Fatal(err)
	}
	headers := c.headers()
	if len(headers) != 1 || headers[0].Info.ScannedAt != "2026-10-18T11:00:00Z" || headers[0].Info.DiskBytes == 0 {
		t.Fatalf("headers: %+v", headers)
	}
	loaded, err := c.load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.root.Name != "latest" || loaded.nodes[1] != nil || loaded.nodes[2].LinkCount != 3 || loaded.nodes[2].ModTime != 123 {
		t.Fatal("snapshot metadata was lost")
	}
	if len(loaded.root.Children) != 3 || loaded.root.Children[1].LinkCount != 4 || !loaded.root.Children[2].IsFreeSpace || !loaded.hasDiskUsage {
		t.Fatal("virtual nodes or disk metadata lost")
	}
}
func TestColdCacheRestartLazyLoadAndExplicitRelease(t *testing.T) {
	config := filepath.Join(t.TempDir(), "settings.json")
	a := newApp(config)
	path := t.TempDir()
	info := ScanJobInfo{Path: path, State: "completed", ScannedAt: scanTimestamp(), Progress: ScanProgress{ElapsedMilliseconds: 12345}}
	if err := a.coldCache.save(info, coldFixture(path), a.coldCache.epoch(path)); err != nil {
		t.Fatal(err)
	}
	restarted := newApp(config)
	defer restarted.Shutdown(nil)
	jobs := restarted.GetScanJobs()
	if len(jobs) != 1 || !jobs[0].Cached {
		t.Fatalf("jobs: %+v", jobs)
	}
	if jobs[0].Progress.ElapsedMilliseconds != info.Progress.ElapsedMilliseconds {
		t.Fatalf("saved duration was lost after restart: %+v", jobs[0].Progress)
	}
	if restarted.jobs.jobs[0].snapshot != nil {
		t.Fatal("loaded tree before selection")
	}
	scans := restarted.GetCachedScans()
	if len(scans) != 1 || scans[0].Loaded || scans[0].MemoryBytes != 0 {
		t.Fatalf("usage: %+v", scans)
	}
	view, err := restarted.SelectScanJob(jobs[0].ID)
	if err != nil || view.Tree == nil {
		t.Fatalf("view: %+v %v", view, err)
	}
	if view.Job.Progress.ElapsedMilliseconds != info.Progress.ElapsedMilliseconds {
		t.Fatalf("loading the snapshot changed its duration: %+v", view.Job.Progress)
	}
	if !restarted.GetCachedScans()[0].Loaded || restarted.scanActive {
		t.Fatal("opening historical snapshot started a scan or failed to load")
	}
	if err := restarted.ClearCachedScan(path); err != nil {
		t.Fatal(err)
	}
	if len(restarted.GetScanJobs()) != 0 || restarted.store.sharedSnapshot().root != nil || len(restarted.GetCachedScans()) != 0 {
		t.Fatal("clear retained scan references")
	}
	if len(newApp(config).GetScanJobs()) != 0 {
		t.Fatal("cleared scan returned after restart")
	}
}

func TestRemoveScanLocationCancelsWorkAndCannotRestoreCache(t *testing.T) {
	a, path, release := queuedFixture(t)
	a.coldCache = &coldCache{dir: t.TempDir()}
	other := t.TempDir()
	for _, location := range []string{path, other} {
		info := ScanJobInfo{Path: location, State: "completed", ScannedAt: scanTimestamp()}
		if err := a.coldCache.save(info, coldFixture(location), a.coldCache.epoch(location)); err != nil {
			t.Fatal(err)
		}
	}
	job, err := a.QueueScan(path)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.filesystem.(*blockingReadDirPlatform).entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not start")
	}
	q := a.jobQueue()
	q.mu.Lock()
	var running *scanJob
	for _, candidate := range q.jobs {
		if candidate.info.ID == job.ID {
			running = candidate
		}
	}
	q.mu.Unlock()
	if _, err := a.SelectScanJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveScanLocation(path); err != nil {
		t.Fatal(err)
	}
	if a.store.sharedSnapshot().root != nil {
		t.Fatal("selected folder view was retained")
	}
	if a.scanActive {
		t.Fatal("removed scan still marks the app busy")
	}
	for _, info := range a.GetScanJobs() {
		if info.Path == path {
			t.Fatal("removed folder still has a job")
		}
	}
	release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		q.mu.Lock()
		done := running.worker == nil
		q.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled worker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	a.coldCache.writes.Wait()
	headers := a.coldCache.headers()
	if len(headers) != 1 || headers[0].Info.Path != other {
		t.Fatalf("wrong cache retained: %+v", headers)
	}
	for _, info := range a.GetScanJobs() {
		if info.Path == path {
			t.Fatal("worker restored removed folder")
		}
	}
}
func TestColdCacheClearRejectsStaleWritesWithoutAffectingOtherPaths(t *testing.T) {
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	defer a.Shutdown(nil)
	first, second := t.TempDir(), t.TempDir()
	one, two := a.coldCache.epoch(first), a.coldCache.epoch(second)
	if err := a.ClearCachedScan(first); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path  string
		epoch coldEpoch
	}{{first, one}, {second, two}} {
		if err := a.coldCache.save(ScanJobInfo{Path: item.path, ScannedAt: scanTimestamp()}, coldFixture(item.path), item.epoch); err != nil {
			t.Fatal(err)
		}
	}
	headers := a.coldCache.headers()
	if len(headers) != 1 || headers[0].Info.Path != second {
		t.Fatalf("unexpected saved scans: %+v", headers)
	}
	if err := a.ClearAllCachedScans(); err != nil {
		t.Fatal(err)
	}
	if len(a.coldCache.headers()) != 0 {
		t.Fatal("clear all failed")
	}
}
func TestCompletedQueuedScanPersistsButCancellationDoesNotReplaceIt(t *testing.T) {
	a, path, release := queuedFixture(t)
	a.coldCache = &coldCache{dir: t.TempDir()}
	old := ScanJobInfo{Path: path, State: "completed", ScannedAt: "2026-01-01T00:00:00Z"}
	if err := a.coldCache.save(old, coldFixture(path), a.coldCache.epoch(path)); err != nil {
		t.Fatal(err)
	}
	job, err := a.QueueScan(path)
	if err != nil {
		t.Fatal(err)
	}
	a.CancelScanJob(job.ID)
	release()
	waitJob(t, a, job.ID, "cancelled")
	if got := a.coldCache.headers(); len(got) != 1 || got[0].Info.ScannedAt != old.ScannedAt {
		t.Fatal("cancel replaced previous scan")
	}
	next, err := a.QueueScan(path)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, a, next.ID, "completed")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h := a.coldCache.headers()
		if len(h) == 1 && h[0].Info.ScannedAt != old.ScannedAt {
			if _, err := a.coldCache.load(path); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("completed scan was not persisted")
}
func TestColdCacheRejectsTruncatedData(t *testing.T) {
	c := &coldCache{dir: t.TempDir()}
	path := t.TempDir()
	if err := c.save(ScanJobInfo{Path: path, ScannedAt: scanTimestamp()}, coldFixture(path), c.epoch(path)); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(c.filename(path))
	if err := os.Truncate(c.filename(path), stat.Size()-5); err != nil {
		t.Fatal(err)
	}
	if _, err := c.load(path); err == nil {
		t.Fatal("accepted truncated snapshot")
	}
}

func TestCachingPolicyIsSessionOnlyAndPreservesLiveCaching(t *testing.T) {
	config := filepath.Join(t.TempDir(), "settings.json")
	a := newApp(config)
	defer a.Shutdown(nil)
	path := t.TempDir()
	info := ScanJobInfo{Path: path, State: "completed", ScannedAt: scanTimestamp()}
	if err := a.coldCache.save(info, coldFixture(path), a.coldCache.epoch(path)); err != nil {
		t.Fatal(err)
	}
	a.SetCachingEnabled(false)
	if a.GetCachingEnabled() || !a.scanCache.Enabled() {
		t.Fatal("wrong caching policy")
	}
	if len(a.GetScanJobs()) != 0 {
		t.Fatal("loaded saved metadata while disabled")
	}
	epoch := a.coldCache.epoch(path)
	if err := a.coldCache.save(ScanJobInfo{Path: path, ScannedAt: scanTimestamp()}, coldFixture(path), epoch); err != nil {
		t.Fatal(err)
	}
	if a.coldCache.headers()[0].Info.ScannedAt != info.ScannedAt {
		t.Fatal("saved while disabled")
	}
	a.SetCachingEnabled(true)
	jobs := a.GetScanJobs()
	if len(jobs) != 1 {
		t.Fatal("reenable did not expose snapshot")
	}
	if _, err := a.SelectScanJob(jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	a.SetCachingEnabled(false)
	if len(a.GetScanJobs()) != 1 || a.store.sharedSnapshot().root == nil {
		t.Fatal("disable discarded an opened tree")
	}
	if err := a.SetProfile(a.GetProfile()); err != nil {
		t.Fatal(err)
	}
	reopened := newApp(config)
	defer reopened.Shutdown(nil)
	if !reopened.GetCachingEnabled() || len(reopened.GetScanJobs()) != 1 {
		t.Fatal("session policy persisted or removed saved data")
	}
}
