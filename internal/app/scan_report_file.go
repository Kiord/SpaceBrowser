package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const retainedScanReports = 20

var scanReportFilesMu sync.Mutex

type ScanReportInfo struct {
	ErrorCount int64  `json:"errorCount"`
	Details    string `json:"details"`
	ReportPath string `json:"reportPath,omitempty"`
	SaveError  string `json:"saveError,omitempty"`
}

type scanReportDetails struct {
	RootPath   string
	StartedAt  time.Time
	Duration   time.Duration
	Profile    Profile
	Report     ScanReportSnapshot
	Files      int64
	Folders    int64
	Bytes      int64
	SpaceUsage *ScanSpaceUsage
}

func (a *App) persistScanReport(rootPath string, startedAt time.Time, duration time.Duration, profile Profile, report ScanReportSnapshot, files, folders, bytes int64, space ...*ScanSpaceUsage) *ScanReportInfo {
	info := &ScanReportInfo{
		ErrorCount: report.TotalErrors(),
		Details:    formatNonzeroScanCounts(report.Errors[:], scanErrorLabels[:]),
	}
	var usage *ScanSpaceUsage
	if len(space) > 0 {
		usage = space[0]
	}
	path, err := writeScanReport(a.GetDefaultSettingsPath(), scanReportDetails{
		RootPath:   rootPath,
		StartedAt:  startedAt,
		Duration:   duration,
		Profile:    profile,
		Report:     report,
		Files:      files,
		Folders:    folders,
		Bytes:      bytes,
		SpaceUsage: usage,
	})
	info.ReportPath = path
	if err != nil {
		info.SaveError = err.Error()
		a.logger.Warningf("could not save scan report: %v", err)
	} else {
		a.logger.Infof("scan report saved: %s", path)
	}
	return info
}

func writeScanReport(defaultSettingsPath string, details scanReportDetails) (string, error) {
	if defaultSettingsPath == "" {
		return "", fmt.Errorf("default configuration location is unavailable")
	}

	scanReportFilesMu.Lock()
	defer scanReportFilesMu.Unlock()

	directory := filepath.Join(filepath.Dir(defaultSettingsPath), "logs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create scan report directory: %w", err)
	}

	completedAt := details.StartedAt.Add(details.Duration)
	file, path, err := createScanReportFile(directory, completedAt)
	if err != nil {
		return "", err
	}
	removeIncomplete := true
	defer func() {
		file.Close()
		if removeIncomplete {
			os.Remove(path)
		}
	}()

	if _, err := file.WriteString(formatScanReport(details, completedAt)); err != nil {
		return "", fmt.Errorf("write scan report: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("flush scan report: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close scan report: %w", err)
	}
	removeIncomplete = false
	if err := os.Chtimes(path, completedAt, completedAt); err != nil {
		return path, fmt.Errorf("timestamp scan report: %w", err)
	}
	if err := pruneScanReports(directory, retainedScanReports); err != nil {
		return path, fmt.Errorf("prune old scan reports: %w", err)
	}
	return path, nil
}

func createScanReportFile(directory string, completedAt time.Time) (*os.File, string, error) {
	base := "scan-" + completedAt.Format("2006-01-02-150405")
	for suffix := 0; suffix < 1000; suffix++ {
		name := base + ".log"
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d.log", base, suffix+1)
		}
		path := filepath.Join(directory, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, path, nil
		}
		if !os.IsExist(err) {
			return nil, "", fmt.Errorf("create scan report: %w", err)
		}
	}
	return nil, "", fmt.Errorf("create scan report: too many reports share the same timestamp")
}

func formatScanReport(details scanReportDetails, completedAt time.Time) string {
	var output strings.Builder
	fmt.Fprintln(&output, "SpaceBrowser scan report")
	fmt.Fprintf(&output, "Version: %s\n", applicationVersion())
	fmt.Fprintf(&output, "Scan root: %s\n", details.RootPath)
	fmt.Fprintf(&output, "Started: %s\n", details.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&output, "Completed: %s\n", completedAt.Format(time.RFC3339))
	fmt.Fprintf(&output, "Duration: %s\n", details.Duration.Round(time.Millisecond))
	fmt.Fprintf(&output, "Files: %d\nFolders: %d\n", details.Files, details.Folders)
	fmt.Fprintf(&output, "Scanned file allocation: %d bytes (%.2f GiB)\n", details.Bytes, float64(details.Bytes)/(1<<30))
	if usage := details.SpaceUsage; usage != nil {
		fmt.Fprintf(&output, "Space measured at: %s\n", usage.MeasuredAt)
		if usage.VolumeAvailable && !usage.Stale {
			fmt.Fprintf(&output, "Volume used space: %d bytes (%.2f GiB)\nVolume capacity: %d bytes (%.2f GiB)\n", usage.VolumeUsed, float64(usage.VolumeUsed)/(1<<30), usage.VolumeTotal, float64(usage.VolumeTotal)/(1<<30))
			fmt.Fprintf(&output, "Volume free space: %d bytes (%.2f GiB)\n", usage.VolumeTotal-usage.VolumeUsed, float64(usage.VolumeTotal-usage.VolumeUsed)/(1<<30))
			if usage.ScopeNote == "" {
				fmt.Fprintf(&output, "Unaccounted difference (volume used - scanned allocation): %d bytes (%.2f GiB)\n", usage.Difference, float64(usage.Difference)/(1<<30))
				switch {
				case usage.Difference > 0:
					fmt.Fprintln(&output, "Used space not attributed to files. May include inaccessible or excluded files, snapshots, or filesystem data.")
				case usage.Difference < 0:
					fmt.Fprintln(&output, "Scanned allocation exceeds volume usage; shared blocks or overlapping accounting may contribute. Negative values are not free space.")
				default:
					fmt.Fprintln(&output, "Scanned file allocation matches the volume's reported used space.")
				}
			}
		}
		if usage.ScopeNote != "" {
			fmt.Fprintf(&output, "Space comparison unavailable: %s\n", usage.ScopeNote)
		}
		if usage.Stale {
			fmt.Fprintln(&output, "Space comparison unavailable: files changed since the recorded measurement; rescan to update it.")
		}
		if !usage.VolumeAvailable && usage.ScopeNote == "" && !usage.Stale {
			if usage.VolumeScan {
				fmt.Fprintln(&output, "Space comparison unavailable: volume usage could not be measured; rescan to retry.")
			} else {
				fmt.Fprintln(&output, "Space comparison unavailable: a full-volume scan is required to compare file allocation with volume usage.")
			}
		}
		fmt.Fprintln(&output, "Hard links count once; shared blocks may overlap. File and volume totals can change during scanning.")
		fmt.Fprintf(&output, "Unreadable directories: %d\nPaths excluded by scan settings: %d\nNested volumes excluded: %d (scan these separately)\n", usage.UnreadableDirectories, usage.FilteredPaths, usage.ExcludedVolumes)
	}
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "Scan settings")
	fmt.Fprintf(&output, "Skip hidden: %t\n", details.Profile.SkipHidden)
	fmt.Fprintf(&output, "Minimum file size: %d bytes\n", details.Profile.MinFileSize)
	fmt.Fprintf(&output, "Follow symlinks: %t\n", details.Profile.FollowSymlinks)
	fmt.Fprintf(&output, "Skip network filesystems: %t\n", details.Profile.SkipNetworkFS)
	if len(details.Profile.ExcludedPaths) == 0 {
		fmt.Fprintln(&output, "Excluded paths: none")
	} else {
		fmt.Fprintln(&output, "Excluded paths:")
		for _, path := range details.Profile.ExcludedPaths {
			fmt.Fprintf(&output, "  - %s\n", path)
		}
	}
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "Summary")
	fmt.Fprintf(&output, "Skipped paths: %d\n", details.Report.TotalSkipped())
	fmt.Fprintf(&output, "Filesystem or metadata errors: %d\n", details.Report.TotalErrors())
	writeScanReportCounts(&output, "Skipped", details.Report.Skipped[:], scanSkipLabels[:])
	writeScanReportCounts(&output, "Errors", details.Report.Errors[:], scanErrorLabels[:])
	if len(details.Report.Examples) > 0 {
		fmt.Fprintln(&output)
		fmt.Fprintln(&output, "Error entries")
		for _, entry := range details.Report.Examples {
			fmt.Fprintf(&output, "[%s]\nPath: %s\n", entry.Reason, entry.Path)
			if entry.Error != "" {
				fmt.Fprintf(&output, "Error: %s\n", entry.Error)
			}
			fmt.Fprintln(&output)
		}
	}
	return output.String()
}

func writeScanReportCounts(output *strings.Builder, heading string, counts []int64, labels []string) {
	fmt.Fprintf(output, "%s by reason:\n", heading)
	wroteAny := false
	for index, count := range counts {
		if count == 0 {
			continue
		}
		fmt.Fprintf(output, "  - %s: %d\n", labels[index], count)
		wroteAny = true
	}
	if !wroteAny {
		fmt.Fprintln(output, "  - none")
	}
}

func pruneScanReports(directory string, keep int) error {
	if keep < 0 {
		keep = 0
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	type reportFile struct {
		name    string
		modTime time.Time
	}
	files := make([]reportFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "scan-") || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, reportFile{name: entry.Name(), modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].name > files[j].name
		}
		return files[i].modTime.After(files[j].modTime)
	})
	if len(files) <= keep {
		return nil
	}
	for _, file := range files[keep:] {
		if err := os.Remove(filepath.Join(directory, file.name)); err != nil {
			return err
		}
	}
	return nil
}
