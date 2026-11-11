package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spacebrowser/internal/platform"
)

type fastScanTestFilesystem struct {
	platform.API
	index    platform.ScannerFilesystem
	err      error
	attempts int
	prepare  func(context.Context, func() error) error
	stats    *platform.FastScanStats
	options  platform.FastScanOptions
}

func (f *fastScanTestFilesystem) PrepareFastScan(ctx context.Context, _ string, check func() error, options platform.FastScanOptions) (platform.ScannerFilesystem, platform.FastScanStats, error) {
	f.attempts++
	f.options = options
	if f.prepare != nil {
		if err := f.prepare(ctx, check); err != nil {
			return nil, platform.FastScanStats{}, err
		}
	}
	if f.stats != nil {
		return f.index, *f.stats, f.err
	}
	return f.index, platform.FastScanStats{Records: 30, Batches: 2, Bytes: 8192}, f.err
}

func TestFastScanProfileAndReconciliationReport(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("timing=%t/failure=%t", enabled, failed), func(t *testing.T) {
				profile := defaultProfile()
				profile.ScanTiming = enabled
				stats := &platform.FastScanStats{Records: 30, Batches: 2, Bytes: 8192, DuplicateNames: 3, ReconciledDirectories: 1, ReconciledSubtrees: 1, Timing: &platform.FastScanTimings{
					Query: platform.FastScanMetric{Calls: 3, Duration: 4 * time.Second}, Decode: platform.FastScanMetric{Calls: 2, Duration: 3 * time.Second},
					Records: platform.FastScanMetric{Calls: 2, Duration: time.Second}, Hierarchy: platform.FastScanMetric{Calls: 1, Duration: 2 * time.Second},
					Wait: platform.FastScanMetric{Calls: 3, Duration: 2 * time.Second},
				}}
				provider := &fastScanTestFilesystem{API: platform.Impl, index: platform.Default{}, stats: stats}
				if failed {
					provider.err = os.ErrPermission
				}
				scanner := NewScannerWithFilesystem(profile, 1, provider)
				if err := scanner.prepareFastScan("root"); err != nil {
					t.Fatal(err)
				}
				if provider.options.Timing != enabled {
					t.Fatal("profiling preference was not passed to NTFS")
				}
				details := testScanReportDetails(time.Now())
				details.Report = scanner.Report()
				details.Report.Timing = scanner.timing.snapshot(10*time.Second, 1, 0)
				text := formatScanReport(details, time.Now())
				if failed {
					if !strings.Contains(text, "Abandoned NTFS attempt") || !strings.Contains(text, "NTFS file records: 30") {
						t.Fatalf("failed attempt statistics were discarded: %s", text)
					}
				} else {
					for _, expected := range []string{"Repeated NTFS names removed: 3", "NTFS directory conflicts requiring native reads: 1", "NTFS conflicting subtrees requiring native reads: 1", "path may still be denied", "two buffers, one query at a time"} {
						if !strings.Contains(text, expected) {
							t.Errorf("missing %q: %s", expected, text)
						}
					}
				}
				for _, expected := range []string{"NTFS file-table query API: 4s (3 calls", "NTFS file-table record decoding: 3s (2 calls", "NTFS file-record indexing: 1s (2 calls", "NTFS directory hierarchy assembly: 2s (1 calls", "NTFS waiting for next metadata batch: 2s (3 calls"} {
					if strings.Contains(text, expected) != enabled {
						t.Errorf("unexpected profiling for %q: %s", expected, text)
					}
				}
			})
		}
	}
}

func TestFastScanSelectionFallbackAndCancellation(t *testing.T) {
	for _, mode := range []string{"index", "denied", "disabled", "symlinks", "cancelled", "nil-index"} {
		t.Run(mode, func(t *testing.T) {
			profile := *defaultProfile()
			provider := &fastScanTestFilesystem{API: platform.Impl, index: platform.Default{}}
			switch mode {
			case "denied":
				provider.err = os.ErrPermission
			case "disabled":
				profile.UseMFT = false
			case "symlinks":
				profile.FollowSymlinks = true
			case "cancelled":
				provider.err = context.Canceled
			case "nil-index":
				provider.index = nil
			}
			scanner := NewScannerWithFilesystem(&profile, 1, provider)
			scanner.SetIncrementalCache(map[string]*Node{"old": {}}, []string{"dirty"})
			err := scanner.prepareFastScan("root")
			if mode == "cancelled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation was swallowed: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			method := scanner.Report().Method
			if mode == "index" {
				if method.Name != scanMethodNTFS || method.Records != 30 || scanner.filesystem != provider.index || len(scanner.cachedDirectories) > 0 {
					t.Fatalf("index was not selected: %+v", method)
				}
			} else if method.Name != scanMethodDirectory || method.FallbackReason == "" || scanner.filesystem != provider {
				t.Fatalf("bad fallback: %+v", method)
			}
			if (mode == "disabled" || mode == "symlinks") && provider.attempts != 0 {
				t.Fatal("ineligible scan imported the file table")
			}
		})
	}
}

func TestDirectMFTSelectionReportingAndCacheScopes(t *testing.T) {
	for _, mode := range []string{"direct", "bulk-fallback", "directory-fallback"} {
		t.Run(mode, func(t *testing.T) {
			profile := defaultProfile()
			profile.ScanTiming = true
			stats := &platform.FastScanStats{Method: platform.FastScanNTFSDirect, Records: 30, Batches: 2, Bytes: 32768, Timing: &platform.FastScanTimings{DirectRead: platform.FastScanMetric{Calls: 2, Duration: 3 * time.Second}, DirectMap: platform.FastScanMetric{Calls: 2, Duration: time.Millisecond}, DirectTableRead: platform.FastScanMetric{Calls: 1, Duration: 2 * time.Second}, DirectListRead: platform.FastScanMetric{Calls: 1, Duration: time.Second}}}
			provider := &fastScanTestFilesystem{API: platform.Impl, index: platform.Default{}, stats: stats}
			if mode != "direct" {
				stats.Method = platform.FastScanNTFSLayout
				stats.DirectFallbackReason = "raw volume access denied"
				stats.DirectRecords, stats.DirectBatches, stats.DirectBytes = 12, 1, 16384
			}
			if mode == "directory-fallback" {
				provider.err = errors.New("bulk metadata unavailable")
			}
			scanner := NewScannerWithFilesystem(profile, 1, provider)
			if err := scanner.prepareFastScan("root"); err != nil {
				t.Fatal(err)
			}
			report := scanner.Report()
			report.Timing = scanner.timing.snapshot(time.Second, 1, 0)
			details := testScanReportDetails(time.Now())
			details.Report = report
			text := formatScanReport(details, time.Now())
			expected := []string{"Direct MFT raw volume reads: 3s (2 calls", "Direct MFT extent mapping and validation: 1ms (2 calls", "Direct MFT table reads: 2s (1 calls", "Direct MFT attribute-list reads: 1s (1 calls", "those rows are not additional time"}
			if mode == "direct" {
				expected = append(expected, scanMethodNTFSDirect, "Raw MFT bytes read: 32768")
				if strings.Contains(text, "Direct MFT fallback:") || strings.Contains(text, "NTFS batch read-ahead:") {
					t.Fatalf("misreported direct scan: %s", text)
				}
			} else {
				expected = append(expected, "Direct MFT fallback: raw volume access denied", "Abandoned direct MFT attempt: 12 file records, 1 batches, 16384 bytes read")
				if mode == "bulk-fallback" {
					expected = append(expected, "Scan method: "+scanMethodNTFS)
				} else {
					expected = append(expected, "Scan method: "+scanMethodDirectory, "NTFS fast-path fallback: bulk metadata unavailable")
				}
			}
			for _, value := range expected {
				if !strings.Contains(text, value) {
					t.Errorf("missing %q: %s", value, text)
				}
			}
			if mode != "directory-fallback" {
				entry := &scanCacheEntry{report: report}
				if cacheEntryIncrementallySafe(entry, *profile) || cacheEntryCrossRootSafe(entry, *profile) {
					t.Fatal("NTFS reader scopes mixed with directory scan caches")
				}
			}
		})
	}
}

func TestFullScanUsesIndexAndReportsMethod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	t.Cleanup(func() { a.Shutdown(nil) })
	index := &countingCacheFilesystem{API: platform.Impl, reads: make(map[string]int)}
	provider := &fastScanTestFilesystem{API: platform.Impl, index: index}
	a.filesystem = provider
	a.profile.ScanTiming = true
	a.profile.MinFileSize = 0
	a.scanCache.SetEnabled(false)
	result, err := a.GetFullTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if provider.attempts != 1 || index.readCount(root) != 1 || result.FileCount != 1 {
		t.Fatalf("did not scan indexed metadata: %+v", result)
	}
	data, err := os.ReadFile(result.ScanReport.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{scanMethodNTFS, "NTFS file records: 30", "NTFS metadata batches: 2", "NTFS metadata acquisition and indexing"} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("report omitted %q: %s", expected, data)
		}
	}
	provider.err = os.ErrPermission
	result, err = a.GetFullTree(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(result.ScanReport.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "NTFS fast-path fallback:") || !strings.Contains(string(data), "Scan method: Directory enumeration") || result.FileCount != 1 {
		t.Fatalf("fallback did not finish normally: %s", data)
	}
}

func TestFastScanPauseCheckpointAndCancel(t *testing.T) {
	profile := defaultProfile()
	entered := make(chan struct{})
	provider := &fastScanTestFilesystem{API: platform.Impl, prepare: func(_ context.Context, check func() error) error { close(entered); return check() }}
	scanner := NewScannerWithFilesystem(profile, 1, provider)
	scanner.pause = &scanPause{}
	scanner.pause.paused.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner.SetContext(ctx, nil)
	done := make(chan error, 1)
	go func() { done <- scanner.prepareFastScan("root") }()
	<-entered
	select {
	case err := <-done:
		t.Fatalf("paused scan continued: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("bad cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt pause")
	}
}

func TestFastScanSettingsMigrationPersistenceAndCacheScope(t *testing.T) {
	if !defaultProfile().UseMFT {
		t.Fatal("fast scans should default on")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":21}`), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := loadSettings(path)
	if err != nil || !profile.UseMFT {
		t.Fatalf("migration: %+v %v", profile, err)
	}
	_, enabledKey := scanProfileCacheKey(profile)
	for _, enabled := range []bool{false, true} {
		profile.UseMFT = enabled
		if err := saveSettings(path, profile); err != nil {
			t.Fatal(err)
		}
		loaded, err := loadSettings(path)
		if err != nil || loaded.UseMFT != enabled {
			t.Fatalf("persistence: %+v %v", loaded, err)
		}
		_, key := scanProfileCacheKey(loaded)
		if (key == enabledKey) != enabled {
			t.Fatal("different scan methods shared a metadata cache key")
		}
	}
	entry := &scanCacheEntry{report: ScanReportSnapshot{Method: &ScanMethodInfo{Name: scanMethodNTFS}}}
	if cacheEntryIncrementallySafe(entry, profile) || cacheEntryCrossRootSafe(entry, profile) {
		t.Fatal("indexed permission scopes must not mix with directory scans")
	}
}
