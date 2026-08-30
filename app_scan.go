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
	RootID                  int             `json:"rootId"`
	FileCount               int             `json:"fileCount"`
	DirCount                int             `json:"dirCount"`
	ScanReport              *ScanReportInfo `json:"scanReport,omitempty"`
	Cached                  bool            `json:"cached,omitempty"`
	SnapshotAgeMilliseconds int64           `json:"snapshotAgeMilliseconds,omitempty"`
}

type ScanProgress struct {
	Active              bool    `json:"active"`
	Path                string  `json:"path"`
	Processed           int64   `json:"processed"`
	Discovered          int64   `json:"discovered"`
	Fraction            float64 `json:"fraction"`
	FileCount           int64   `json:"fileCount"`
	DirCount            int64   `json:"dirCount"`
	ElapsedMilliseconds int64   `json:"elapsedMilliseconds"`
}

const networkFilesystemScanDisabledMessage = "network filesystem scanning is disabled; go to Settings and untick Skip network filesystems"

var errScanSuperseded = errors.New("scan was superseded by a newer scan")

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

func (a *App) startupScanCacheDisabled(path string, consume bool) bool {
	a.initialScanMu.Lock()
	defer a.initialScanMu.Unlock()
	if !a.initialScanNoCache || a.initialScanPath == "" {
		return false
	}
	initialPath := canonicalCachePath(a.filesystem.Canonicalize(a.initialScanPath))
	if initialPath != canonicalCachePath(path) {
		return false
	}
	if consume {
		a.initialScanNoCache = false
	}
	return true
}

func (a *App) GetScanProgress() ScanProgress {
	a.scanMu.RLock()
	active := a.scanActive
	path := a.scanPath
	startedAt := a.scanStartedAt
	scanner := a.scanScanner
	a.scanMu.RUnlock()

	var processed, discovered int64
	var files, dirs int64
	if scanner != nil {
		processed, discovered = scanner.WorkProgress()
		files, dirs = scanner.LiveCounts()
	}
	elapsed := time.Since(startedAt)
	if !active || startedAt.IsZero() {
		elapsed = 0
	}
	fraction := 0.0
	if discovered > 0 {
		fraction = min(1, float64(processed)/float64(discovered))
	}

	return ScanProgress{
		Active:              active,
		Path:                path,
		Processed:           processed,
		Discovered:          discovered,
		Fraction:            fraction,
		FileCount:           files,
		DirCount:            dirs,
		ElapsedMilliseconds: elapsed.Milliseconds(),
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

func (a *App) beginScan(path string) (context.Context, uint64) {
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
	}
	a.scanMu.Unlock()
}

func (a *App) publishScanResult(
	ctx context.Context,
	generation uint64,
	root *Node,
	nodes []*Node,
	files, dirs int,
	shared bool,
	persistReport func() *ScanReportInfo,
) (*ScanReportInfo, error) {
	a.scanMu.Lock()
	if a.scanGeneration != generation || !a.scanActive {
		a.scanMu.Unlock()
		return nil, errScanSuperseded
	}
	if err := ctx.Err(); err != nil {
		a.scanMu.Unlock()
		return nil, err
	}

	if shared {
		// The cache and snapshot worker retain this allocation. TreeStore
		// detaches lazily before any later structural mutation.
		a.store.ReplaceShared(root, nodes, files, dirs)
	} else {
		a.store.Replace(root, nodes, files, dirs)
	}
	a.scanMu.Unlock()
	return persistReport(), nil
}

func (a *App) publishCachedScanResult(ctx context.Context, generation uint64, root *Node, nodes []*Node, files, dirs int, persistReport func() *ScanReportInfo) (*ScanReportInfo, error) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	if a.scanGeneration != generation || !a.scanActive {
		return nil, errScanSuperseded
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.store.ReplaceShared(root, nodes, files, dirs)
	return persistReport(), nil
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
	if a.filesystem.IsMountRoot(path) {
		if fs, usageErr := disk.Usage(path); usageErr == nil {
			volumeUsage = fs
		}
	}

	ctx, generation := a.beginScan(path)
	defer a.finishScan(generation)

	profile := a.GetProfile()
	startupCacheDisabled := a.startupScanCacheDisabled(path, true)
	useCache := profile.UseCache && !startupCacheDisabled
	cachePlan := scanReusePlan{}
	if useCache {
		cachePlan = a.scanCache.Prepare(path, profile)
	}
	a.logger.Debugf("scan settings: skipHidden=%t minFileSize=%d followSymlinks=%t skipNetworkFS=%t useCache=%t", profile.SkipHidden, profile.MinFileSize, profile.FollowSymlinks, profile.SkipNetworkFS, useCache)
	if useCache && cachePlan.source != nil && len(cachePlan.directories) > 0 && len(cachePlan.dirty) == 0 && a.scanCache.StillClean(cachePlan.source, cachePlan.eventCount) {
		cachedRoot, cachedNodes := cachePlan.source.root, cachePlan.source.nodes
		duration := time.Since(startedAt)
		report := cachePlan.source.report
		reportInfo, publishErr := a.publishCachedScanResult(ctx, generation, cachedRoot, cachedNodes, cachePlan.source.fileCount, cachePlan.source.dirCount, func() *ScanReportInfo {
			return a.persistScanReport(path, startedAt, duration, profile, report, int64(cachePlan.source.fileCount), int64(cachePlan.source.dirCount), cachePlan.source.root.Size)
		})
		if publishErr != nil {
			return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, publishErr
		}
		if volumeUsage != nil {
			a.store.UpdateDiskUsage(int64(volumeUsage.Total), int64(volumeUsage.Free))
		}
		a.logScanReport(report)
		a.logger.Infof("scan cache reused the complete tree for %s", path)
		a.logger.Infof("scan completed in %s: %s (%d files, %d folders, %d bytes)", duration.Round(time.Millisecond), path, cachePlan.source.fileCount, cachePlan.source.dirCount, cachePlan.source.root.Size)
		return &TreeInfo{RootID: cachedRoot.ID, FileCount: cachePlan.source.fileCount, DirCount: cachePlan.source.dirCount, ScanReport: reportInfo}, nil
	}
	if useCache && cachePlan.source != nil && len(cachePlan.dirty) == 0 {
		// A watcher event arrived after Prepare. Refresh the plan so the scanner
		// cannot reuse a tree using the now-stale empty dirty set.
		cachePlan = a.scanCache.Prepare(path, profile)
	}
	var observation *scanCacheObservation
	if useCache {
		observation = a.scanCache.BeginObservation(path)
	}
	defer observation.Close()
	var files, dirs int64
	scanner := NewScannerWithFilesystem(&profile, 0, a.filesystem)
	// Persisted scan reports need the complete error list independently of the
	// terminal verbosity selected by the user.
	scanner.ReportAllErrors(true)
	if observation != nil {
		scanner.SetDirectoryObserver(observation.WatchDirectory)
	}
	if len(cachePlan.directories) > 0 {
		scanner.SetIncrementalCache(cachePlan.directories, cachePlan.dirty)
		scanner.SetIncrementalCacheReports(cachePlan.reports)
	}
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

	report := scanner.Report()
	duration := time.Since(startedAt)
	// Settings can be saved through another backend caller while a scan is in
	// progress. Do not publish or persist a new cache after caching was disabled.
	if useCache && !a.GetProfile().UseCache {
		useCache = false
	}
	reportInfo, err := a.publishScanResult(ctx, generation, root, scanner.Nodes(), int(files), int(dirs), useCache, func() *ScanReportInfo {
		return a.persistScanReport(path, startedAt, duration, profile, report, files, dirs, root.Size)
	})
	if err != nil {
		if errors.Is(err, errScanSuperseded) {
			a.logger.Warningf("discarding superseded scan result: %s", path)
			return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, err
		}
		a.logger.Warningf("discarding cancelled scan result: %s", path)
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, fmt.Errorf("scan cancelled")
	}
	a.logScanReport(report)
	if reused := scanner.ReusedDirectories(); reused > 0 {
		a.logger.Infof("scan cache reused %d directories", reused)
	}
	if useCache {
		a.scanCache.Install(path, profile, root, scanner.Nodes(), int(files), int(dirs), report, cachePlan, observation)
		a.scanCache.QueueSnapshot(path, profile, root, int(files), int(dirs), report)
	}
	a.logger.Infof("scan completed in %s: %s (%d files, %d folders, %d bytes)", duration.Round(time.Millisecond), path, files, dirs, root.Size)
	return &TreeInfo{RootID: root.ID, FileCount: int(files), DirCount: int(dirs), ScanReport: reportInfo}, nil
}

func (a *App) LoadScanSnapshot(path string) (*TreeInfo, error) {
	path, err := a.validateScanPath(path)
	if err != nil {
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, err
	}
	profile := a.GetProfile()
	if !profile.UseCache || a.startupScanCacheDisabled(path, false) {
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, nil
	}
	loaded, err := a.scanCache.LoadSnapshot(path, profile)
	if err != nil {
		if !os.IsNotExist(err) {
			a.logger.Warningf("scan snapshot unavailable for %s: %v", path, err)
		}
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, nil
	}
	var currentUsage *disk.UsageStat
	if a.filesystem.IsMountRoot(path) {
		currentUsage, _ = disk.Usage(path)
	}
	a.scanMu.Lock()
	if a.scanActive {
		a.scanMu.Unlock()
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, nil
	}
	if loaded.shared {
		a.store.ReplaceShared(loaded.root, loaded.nodes, loaded.files, loaded.dirs)
	} else {
		a.store.Replace(loaded.root, loaded.nodes, loaded.files, loaded.dirs)
	}
	if currentUsage != nil {
		a.store.UpdateDiskUsage(int64(currentUsage.Total), int64(currentUsage.Free))
	}
	a.scanMu.Unlock()
	age := time.Since(loaded.savedAt)
	if age < 0 {
		age = 0
	}
	a.logger.Infof("loaded persisted scan snapshot for %s (%s old); verifying with a live scan", path, age.Round(time.Second))
	return &TreeInfo{
		RootID: loaded.root.ID, FileCount: loaded.files, DirCount: loaded.dirs,
		Cached: true, SnapshotAgeMilliseconds: age.Milliseconds(),
	}, nil
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
	rects, err := a.store.Layout(nodeID, width, height, scale, showFreeSpace)
	if err != nil {
		return nil, err
	}
	inTrashByNodeID := make(map[int]bool, len(rects))
	for index := range rects {
		rect := &rects[index]
		if a.desktop == nil || rect.FullPath == "" {
			continue
		}
		if rect.IsFolder {
			rect.IsTrashRoot = a.desktop.IsTrashRoot(rect.FullPath)
		}
		parentInTrash, parentIsVisible := false, false
		if rect.ParentID != nil {
			parentInTrash, parentIsVisible = inTrashByNodeID[*rect.ParentID]
		}
		rect.IsInTrash = rect.IsTrashRoot || parentInTrash
		if !parentIsVisible && !rect.IsTrashRoot {
			rect.IsInTrash = a.desktop.IsInTrash(rect.FullPath)
		}
		inTrashByNodeID[rect.NodeID] = rect.IsInTrash
	}
	return rects, nil
}
