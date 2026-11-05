package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"spacebrowser/internal/platform"
)

func TestScanTimingDisabledAndConcurrentCollection(t *testing.T) {
	var disabled *scanTiming
	if !disabled.start().IsZero() {
		t.Fatal("disabled timing read the clock")
	}
	disabled.finish(scanTimingMetadata, time.Time{})
	disabled.record(scanTimingMetadata, time.Second)
	if disabled.snapshotForScan(nil, time.Time{}, 0, 0, 0) != nil {
		t.Fatal("disabled timing produced a profile")
	}

	timing := newScanTiming(true)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				timing.record(scanTimingReadDirectory, time.Millisecond)
			}
		})
	}
	workers.Wait()
	snapshot := timing.snapshot(time.Second, 8, 3)
	if snapshot.Workers != 8 || snapshot.ReusedDirectories != 3 || len(snapshot.Operations) != 1 {
		t.Fatalf("timing metadata: %+v", snapshot)
	}
	metric := snapshot.Operations[0]
	if metric.Calls != 800 || metric.Duration != 800*time.Millisecond {
		t.Fatalf("concurrent timings were lost: %+v", metric)
	}
}

func TestScanTimingReportDistinguishesOverlappingTimeAndPauses(t *testing.T) {
	timing := newScanTiming(true)
	timing.record(scanTimingWalk, 3*time.Second)
	timing.record(scanTimingReadDirectory, 6*time.Second)
	start := time.Unix(100, 0)
	pause := &scanPause{}
	pause.setAt(true, start.Add(time.Second))
	pause.setAt(false, start.Add(2*time.Second))
	details := testScanReportDetails(start)
	details.Duration = 3 * time.Second
	details.Report.Timing = timing.snapshotForScan(pause, start, details.Duration, 4, 0)
	text := formatScanReport(details, start.Add(details.Duration))
	for _, expected := range []string{"Timing profile", "Active scan time: 2s", "Paused time: 1s", "Directory enumeration: 6s", "do not add them", "outside this profile"} {
		if !strings.Contains(text, expected) {
			t.Errorf("timing report missing %q", expected)
		}
	}
	details.Report.Timing = nil
	if strings.Contains(formatScanReport(details, start), "Timing profile") {
		t.Fatal("unprofiled report contains a timing section")
	}
}

func TestScanTimingIncludesDirectoryAPIBreakdownAndZeroFallbacks(t *testing.T) {
	timing := newScanTiming(true)
	read := &platform.DirectoryReadTimings{
		Open:             platform.DirectoryReadMetric{Calls: 1, Duration: time.Millisecond},
		Query:            platform.DirectoryReadMetric{Calls: 2, Duration: 20 * time.Millisecond},
		Decode:           platform.DirectoryReadMetric{Calls: 1, Duration: 2 * time.Millisecond},
		ExtendedAttempts: 1,
	}
	timing.recordDirectoryRead(read)
	timing.recordDirectoryRead(read)
	details := testScanReportDetails(time.Now())
	details.Report.Timing = timing.snapshot(time.Second, 16, 0)
	text := formatScanReport(details, time.Now())
	for _, expected := range []string{
		"Portable directory enumeration fallbacks: 0", "Native Windows 128-bit: 2", "Native Windows 64-bit: 0", "Portable fallback: 0",
		"Windows directory open API: 2ms (2 calls", "Windows directory enumeration API: 40ms (4 calls", "Windows directory record decoding (excludes allocation APIs): 4ms (2 calls",
		"do not isolate physical disk latency",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("missing directory detail %q: %s", expected, text)
		}
	}
}

func TestScanTimingSettingsDefaultMigrationAndPersistence(t *testing.T) {
	if defaultProfile().ScanTiming {
		t.Fatal("timing should be opt-in")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":20}`), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := loadSettings(path)
	if err != nil || profile.ScanTiming {
		t.Fatalf("legacy settings enabled timing: %+v, %v", profile, err)
	}
	_, previousKey := scanProfileCacheKey(profile)
	for _, enabled := range []bool{true, false} {
		profile.ScanTiming = enabled
		if err := saveSettings(path, profile); err != nil {
			t.Fatal(err)
		}
		loaded, err := loadSettings(path)
		if err != nil || loaded.ScanTiming != enabled {
			t.Fatalf("timing setting did not round trip: %+v, %v", loaded, err)
		}
		if _, key := scanProfileCacheKey(loaded); key != previousKey {
			t.Fatal("timing setting invalidated reusable file metadata")
		}
	}
}

func TestFullScanTimingReportAndCacheReuse(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			path := t.TempDir()
			if err := os.WriteFile(filepath.Join(path, "file"), []byte("hello"), 0600); err != nil {
				t.Fatal(err)
			}
			a := newApp(filepath.Join(t.TempDir(), "settings.json"))
			t.Cleanup(func() { a.Shutdown(nil) })
			a.profile.ScanTiming = enabled
			a.profile.MinFileSize = 0
			a.scanCache.SetEnabled(false)
			result, err := a.GetFullTree(path)
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(result.ScanReport.ReportPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(contents), "Timing profile") != enabled {
				t.Fatalf("unexpected profiling output: %s", contents)
			}
			if enabled && !strings.Contains(string(contents), "Directory enumeration:") {
				t.Fatalf("missing measured filesystem work: %s", contents)
			}
			// Install a deterministic clean cache without relying on OS watcher
			// delivery, and ensure historical measurements are never reused.
			snapshot := a.store.snapshotState()
			a.scanCache.SetEnabled(true)
			observation := &scanCacheObservation{manager: a.scanCache, generation: a.scanCache.generation, rootPath: path, dirty: make(map[string]struct{})}
			oldReport := ScanReportSnapshot{Timing: &ScanTimingSnapshot{Operations: []ScanTimingMetric{{Name: "Old scan timing", Calls: 1, Duration: time.Hour}}}}
			a.scanCache.Install(path, a.GetProfile(), snapshot.root, snapshot.nodes, snapshot.files, snapshot.dirs, oldReport, scanReusePlan{}, observation)
			cached, err := a.GetFullTree(path)
			if err != nil {
				t.Fatal(err)
			}
			contents, err = os.ReadFile(cached.ScanReport.ReportPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(contents), "Old scan timing") || strings.Contains(string(contents), "Directory enumeration:") {
				t.Fatalf("cache reused old timings: %s", contents)
			}
			if strings.Contains(string(contents), "Complete-tree cache reuse:") != enabled {
				t.Fatalf("missing cache timing or incorrect opt-in: %s", contents)
			}
		})
	}
}

func TestFolderRefreshTimingReport(t *testing.T) {
	a, _, root, ids := refreshFixture(t)
	a.profile.ScanTiming = true
	a.defaultSettingsPath = filepath.Join(t.TempDir(), "settings.json")
	result, err := a.RefreshFolders([]FolderRefreshTarget{
		{NodeID: ids["a"], Path: filepath.Join(root, "a")},
		{NodeID: ids["b"], Path: filepath.Join(root, "b")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(result.ScanReport.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Timing profile", "Directory walk and tree assembly:", "Directory enumeration:", "Extra allocation / identity lookups:"} {
		if !strings.Contains(string(contents), expected) {
			t.Errorf("refresh report missing %q: %s", expected, contents)
		}
	}
}

func BenchmarkScanTimingInstrumentation(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		b.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(b *testing.B) {
			timing := newScanTiming(enabled)
			b.ReportAllocs()
			for b.Loop() {
				started := timing.start()
				timing.finish(scanTimingReadDirectory, started)
			}
		})
	}
}

// Keep operation timing observable even when a filesystem call blocks.
func TestScanTimingPreviewCollection(t *testing.T) {
	a, root := previewFixture(t)
	a.profile.ScanTiming = true
	result, failure, release := startBlockedPreviewScan(t, a, root)
	defer release()
	a.scanMu.RLock()
	generation := a.scanGeneration
	scanner := a.scanScanner
	a.scanMu.RUnlock()
	a.GetScanPreview(generation)
	timing := scanner.timing.snapshot(0, 1, 0)
	foundPreview := false
	for _, operation := range timing.Operations {
		foundPreview = foundPreview || operation.Name == "Live preview snapshots"
	}
	if !foundPreview {
		t.Fatal("preview scan did not time its snapshots")
	}
	release()
	select {
	case <-result:
		if err := <-failure; err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("profiled scan did not complete")
	}
}
