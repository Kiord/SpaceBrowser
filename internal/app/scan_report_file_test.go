package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

func testScanReportDetails(startedAt time.Time) scanReportDetails {
	var report ScanReportSnapshot
	report.Skipped[scanSkipHidden] = 3
	report.Errors[scanErrorReadDirectory] = 1
	report.Examples = []ScanReportExample{{
		Reason: scanErrorLabels[scanErrorReadDirectory],
		Path:   filepath.Join("root", "private"),
		Error:  "access denied",
	}}
	return scanReportDetails{
		RootPath:  filepath.Join("root", "scan"),
		StartedAt: startedAt,
		Duration:  1500 * time.Millisecond,
		Profile:   *defaultProfile(),
		Report:    report,
		Files:     12,
		Folders:   4,
		Bytes:     4096,
	}
}

func TestWriteScanReportCreatesDetailedLocalReport(t *testing.T) {
	configDirectory := t.TempDir()
	defaultPath := filepath.Join(configDirectory, "settings.json")
	details := testScanReportDetails(time.Date(2026, 8, 16, 14, 30, 52, 0, time.UTC))
	details.SpaceUsage = scanSpaceUsage(&Node{Size: details.Bytes}, details.Report, &disk.UsageStat{Total: 16384, Free: 8192}, "", details.StartedAt)

	reportPath, err := writeScanReport(defaultPath, details)
	if err != nil {
		t.Fatalf("writeScanReport: %v", err)
	}
	if filepath.Dir(reportPath) != filepath.Join(configDirectory, "logs") {
		t.Fatalf("report directory = %q", filepath.Dir(reportPath))
	}
	if filepath.Base(reportPath) != "scan-2026-08-16-143053.log" {
		t.Fatalf("report filename = %q", filepath.Base(reportPath))
	}
	contents, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	text := string(contents)
	for _, expected := range []string{
		"SpaceBrowser scan report",
		"Scan root: " + details.RootPath,
		"Filesystem or metadata errors: 1",
		"hidden: 3",
		"[unreadable directories]",
		"Path: " + filepath.Join("root", "private"),
		"Error: access denied",
		"Scanned file allocation: 4096 bytes",
		"Volume used space: 8192 bytes",
		"Unaccounted difference (volume used - scanned allocation): 4096 bytes",
		"Space measured at: 2026-08-16T14:30:52Z",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("report does not contain %q", expected)
		}
	}
}

func TestWriteScanReportRetainsLatestTwenty(t *testing.T) {
	configDirectory := t.TempDir()
	defaultPath := filepath.Join(configDirectory, "settings.json")
	logsDirectory := filepath.Join(configDirectory, "logs")
	baseTime := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	var paths []string
	for index := 0; index < retainedScanReports+2; index++ {
		details := testScanReportDetails(baseTime.Add(time.Duration(index) * time.Minute))
		path, err := writeScanReport(defaultPath, details)
		if err != nil {
			t.Fatalf("write report %d: %v", index, err)
		}
		paths = append(paths, path)
	}

	entries, err := os.ReadDir(logsDirectory)
	if err != nil {
		t.Fatalf("read logs directory: %v", err)
	}
	if len(entries) != retainedScanReports {
		t.Fatalf("retained report count = %d, want %d", len(entries), retainedScanReports)
	}
	for _, expired := range paths[:2] {
		if _, err := os.Stat(expired); !os.IsNotExist(err) {
			t.Errorf("expired report still exists: %s", expired)
		}
	}
	if _, err := os.Stat(paths[len(paths)-1]); err != nil {
		t.Errorf("latest report is unavailable: %v", err)
	}
}

func TestPersistScanReportIncludesCleanScans(t *testing.T) {
	defaultPath := filepath.Join(t.TempDir(), "settings.json")
	app := newAppWithPathsAndLogger(defaultPath, defaultPath, NewSeverityLogger(verbosityInfo, io.Discard))
	info := app.persistScanReport("root", time.Now(), time.Second, *defaultProfile(), ScanReportSnapshot{}, 1, 1, 1)
	if info == nil || info.ErrorCount != 0 || info.ReportPath == "" || info.SaveError != "" {
		t.Fatalf("clean scan did not save a report: %+v", info)
	}
	contents, err := os.ReadFile(info.ReportPath)
	if err != nil || !strings.Contains(string(contents), "Scanned file allocation: 1 bytes") {
		t.Fatalf("clean scan report is unavailable: %v", err)
	}
}

func TestScanReportDoesNotCompareChangedFilesWithOldVolumeUsage(t *testing.T) {
	details := testScanReportDetails(time.Now())
	details.SpaceUsage = &ScanSpaceUsage{VolumeAvailable: true, VolumeUsed: 8000, Difference: 3904, Stale: true}
	text := formatScanReport(details, time.Now())
	if strings.Contains(text, "Volume used space:") || strings.Contains(text, "Unaccounted difference") || !strings.Contains(text, "files changed since the recorded measurement") {
		t.Fatalf("report compared unmatched measurements: %s", text)
	}
}

func TestScanReportExplainsExcessAllocationWithoutInventingFreeSpace(t *testing.T) {
	details := testScanReportDetails(time.Now())
	details.SpaceUsage = &ScanSpaceUsage{VolumeAvailable: true, VolumeUsed: 3000, VolumeTotal: 5000, Difference: -1096}
	text := formatScanReport(details, time.Now())
	for _, expected := range []string{"-1096 bytes", "allocation exceeds volume usage", "may contribute", "Negative values are not free space", "GiB"} {
		if !strings.Contains(text, expected) {
			t.Errorf("report omitted %q", expected)
		}
	}
}
