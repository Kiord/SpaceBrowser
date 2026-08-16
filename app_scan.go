package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"

	"spacebrowser/internal/platform"
)

type TreeInfo struct {
	RootID     int             `json:"rootId"`
	FileCount  int             `json:"fileCount"`
	DirCount   int             `json:"dirCount"`
	ScanReport *ScanReportInfo `json:"scanReport,omitempty"`
}

type ScanProgress struct {
	Active                bool    `json:"active"`
	Path                  string  `json:"path"`
	Processed             int64   `json:"processed"`
	Discovered            int64   `json:"discovered"`
	Determinate           bool    `json:"determinate"`
	Fraction              float64 `json:"fraction"`
	ElapsedMilliseconds   int64   `json:"elapsedMilliseconds"`
	RemainingMilliseconds int64   `json:"remainingMilliseconds"`
}

const scanProgressSlowdown = 10.0

const networkFilesystemScanDisabledMessage = "network filesystem scanning is disabled; go to Settings and untick Skip network filesystems"

func validateScanPathWithFilesystem(path string, filesystem platform.ScannerFilesystem) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("missing path")
	}

	path = filesystem.Canonicalize(path)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("this path does not exist")
	}
	if err != nil {
		return "", fmt.Errorf("cannot access this path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("this path is not a folder")
	}
	return path, nil
}

func (a *App) ValidateScanPath(path string) (string, error) {
	return a.validateScanPath(path)
}

func (a *App) validateScanPath(path string) (string, error) {
	path, err := validateScanPathWithFilesystem(path, a.filesystem)
	if err != nil {
		return "", err
	}
	if a.GetProfile().SkipNetworkFS && a.filesystem.IsLikelyNetworkFS(path) {
		return "", errors.New(networkFilesystemScanDisabledMessage)
	}
	return path, nil
}

func (a *App) GetScanProgress() ScanProgress {
	a.scanMu.RLock()
	active := a.scanActive
	path := a.scanPath
	startedAt := a.scanStartedAt
	scanner := a.scanScanner
	totalBytes := a.scanTotalBytes
	a.scanMu.RUnlock()

	var processed, discovered int64
	var processedBytes int64
	if scanner != nil {
		processed, discovered = scanner.WorkProgress()
		processedBytes = scanner.BytesProcessed()
	}
	elapsed := time.Since(startedAt)
	if !active || startedAt.IsZero() {
		elapsed = 0
	}
	fraction := 0.0
	remainingMilliseconds := int64(-1)
	determinate := totalBytes > 0
	if determinate {
		rawFraction := float64(processedBytes) / float64(totalBytes)
		if rawFraction > 1 {
			rawFraction = 1
		}
		// Disk usage underestimates metadata-heavy work. This curve applies the
		// observed slowdown while preserving a true 100% endpoint.
		fraction = rawFraction / (scanProgressSlowdown - (scanProgressSlowdown-1)*rawFraction)
		if elapsed >= 6*time.Second && processedBytes > 0 && processedBytes < totalBytes {
			remaining := time.Duration(float64(elapsed) * float64(totalBytes-processedBytes) / float64(processedBytes))
			remainingMilliseconds = time.Duration(float64(remaining) * scanProgressSlowdown).Milliseconds()
		}
	}

	return ScanProgress{
		Active:                active,
		Path:                  path,
		Processed:             processed,
		Discovered:            discovered,
		Determinate:           determinate,
		Fraction:              fraction,
		ElapsedMilliseconds:   elapsed.Milliseconds(),
		RemainingMilliseconds: remainingMilliseconds,
	}
}

func (a *App) CancelScan() {
	a.scanMu.RLock()
	cancel := a.scanCancel
	a.scanMu.RUnlock()
	if cancel != nil {
		a.logger.Infof("cancelling active scan")
		cancel()
	}
}

func (a *App) beginScan(path string, totalBytes int64) (context.Context, uint64) {
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)

	a.scanMu.Lock()
	if a.scanCancel != nil {
		a.scanCancel()
	}
	a.scanGeneration++
	generation := a.scanGeneration
	a.scanActive = true
	a.scanPath = path
	a.scanCancel = cancel
	a.scanStartedAt = time.Now()
	a.scanScanner = nil
	a.scanTotalBytes = totalBytes
	a.scanMu.Unlock()
	return ctx, generation
}

func (a *App) attachScanner(generation uint64, scanner *Scanner) {
	a.scanMu.Lock()
	if a.scanGeneration == generation && a.scanActive {
		a.scanScanner = scanner
	}
	a.scanMu.Unlock()
}

func (a *App) updateScanPath(generation uint64, path string) {
	a.scanMu.Lock()
	if a.scanGeneration == generation && a.scanActive {
		a.scanPath = path
		a.logger.Tracef("scanning %s", path)
	}
	a.scanMu.Unlock()
}

func (a *App) finishScan(generation uint64) {
	a.scanMu.Lock()
	if a.scanGeneration == generation {
		a.scanActive = false
		a.scanCancel = nil
		a.scanScanner = nil
		a.scanTotalBytes = 0
	}
	a.scanMu.Unlock()
}

func (a *App) GetFullTree(path string) (*TreeInfo, error) {
	path, err := a.validateScanPath(path)
	if err != nil {
		a.logger.Errorf("cannot start scan: %v", err)
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, err
	}
	startedAt := time.Now()
	a.logger.Infof("scan started: %s", path)

	var volumeUsage *disk.UsageStat
	var totalBytes int64
	if a.filesystem.IsMountRoot(path) {
		if fs, usageErr := disk.Usage(path); usageErr == nil {
			volumeUsage = fs
			totalBytes = int64(fs.Used)
		}
	}

	ctx, generation := a.beginScan(path, totalBytes)
	defer a.finishScan(generation)

	profile := a.GetProfile()
	a.logger.Debugf("scan settings: skipHidden=%t minFileSize=%d followSymlinks=%t skipNetworkFS=%t", profile.SkipHidden, profile.MinFileSize, profile.FollowSymlinks, profile.SkipNetworkFS)
	var files, dirs int64
	scanner := NewScannerWithFilesystem(&profile, 0, a.filesystem)
	// Persisted scan reports need the complete error list independently of the
	// terminal verbosity selected by the user.
	scanner.ReportAllErrors(true)
	a.attachScanner(generation, scanner)
	scanner.SetContext(ctx, func(path string) { a.updateScanPath(generation, path) })
	root, err := scanner.buildTree(path, 0, -1, &files, &dirs)
	if err != nil {
		if errors.Is(err, errNetworkFilesystemRootSkipped) {
			err = errors.New(networkFilesystemScanDisabledMessage)
		}
		a.logScanReport(scanner.Report())
		if ctx.Err() != nil {
			a.logger.Warningf("scan cancelled after %s: %s", time.Since(startedAt).Round(time.Millisecond), path)
			return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, fmt.Errorf("scan cancelled")
		}
		a.logger.Errorf("scan failed after %s: %v", time.Since(startedAt).Round(time.Millisecond), err)
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, err
	}

	if volumeUsage != nil {
		fs := volumeUsage
		free := &Node{
			ID:          -1,
			ParentID:    root.ID,
			Name:        "[Free Disk Space]",
			Size:        int64(fs.Free),
			DiskTotal:   int64(fs.Total),
			IsFolder:    false,
			IsFreeSpace: true,
			Depth:       1,
		}
		root.Children = append(root.Children, free)

		root.DiskTotal = int64(fs.Total)
		root.DiskFree = int64(fs.Free)
	}

	sort.Slice(root.Children, func(i, j int) bool {
		return root.Children[i].Size > root.Children[j].Size
	})

	a.store.Replace(root, scanner.Nodes(), int(files), int(dirs))
	report := scanner.Report()
	a.logScanReport(report)
	duration := time.Since(startedAt)
	a.logger.Infof("scan completed in %s: %s (%d files, %d folders, %d bytes)", duration.Round(time.Millisecond), path, files, dirs, root.Size)
	reportInfo := a.persistScanReport(path, startedAt, duration, profile, report, files, dirs, root.Size)
	return &TreeInfo{RootID: root.ID, FileCount: int(files), DirCount: int(dirs), ScanReport: reportInfo}, nil
}

func (a *App) logScanReport(report ScanReportSnapshot) {
	skipped := report.TotalSkipped()
	errors := report.TotalErrors()
	a.logger.Infof("scan report: %d skipped paths, %d filesystem or metadata errors", skipped, errors)
	if skipped > 0 {
		a.logger.Infof("scan report skipped: %s", formatNonzeroScanCounts(report.Skipped[:], scanSkipLabels[:]))
	}
	if errors > 0 {
		a.logger.Infof("scan report errors: %s", formatNonzeroScanCounts(report.Errors[:], scanErrorLabels[:]))
		entryLabel := "example"
		examples := report.Examples
		if a.logger.Enabled(verbosityDebug) {
			entryLabel = "error"
		} else if len(examples) > maximumScanReportExamples {
			examples = examples[:maximumScanReportExamples]
		}
		for _, example := range examples {
			a.logger.Infof("scan report %s [%s]: %s: %s", entryLabel, example.Reason, example.Path, example.Error)
		}
	}
}

func (a *App) Layout(nodeID, width, height int, scale float64) ([]Rect, error) {
	a.settingsMu.RLock()
	showFreeSpace := a.showFreeSpace
	a.settingsMu.RUnlock()
	return a.store.Layout(nodeID, width, height, scale, showFreeSpace)
}
