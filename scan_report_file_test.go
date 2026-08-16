package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestPersistScanReportSkipsCleanScans(t *testing.T) {
	defaultPath := filepath.Join(t.TempDir(), "settings.json")
	app := newAppWithPathsAndLogger(defaultPath, defaultPath, NewSeverityLogger(verbosityInfo, io.Discard))
	if info := app.persistScanReport("root", time.Now(), time.Second, *defaultProfile(), ScanReportSnapshot{}, 1, 1, 1); info != nil {
		t.Fatalf("clean scan returned report info: %+v", info)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(defaultPath), "logs")); !os.IsNotExist(err) {
		t.Fatalf("clean scan created a logs directory")
	}
}
