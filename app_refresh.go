package main

import (
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

type FolderRefreshTarget struct {
	NodeID int    `json:"nodeId"`
	Path   string `json:"path"`
}

type FolderRefreshResult struct {
	FileCount  int             `json:"fileCount"`
	DirCount   int             `json:"dirCount"`
	NodeIDs    map[int]int     `json:"nodeIds"`
	ScanReport *ScanReportInfo `json:"scanReport,omitempty"`
}

// RefreshFolders scans into detached trees and publishes the batch atomically.
// The frontend supplies the IDs whose paths it needs to follow across refresh.
func (a *App) RefreshFolders(targets []FolderRefreshTarget, trackedIDs []int) (*FolderRefreshResult, error) {
	a.filesystemMu.Lock()
	defer a.filesystemMu.Unlock()
	a.scanMu.RLock()
	active := a.scanActive
	a.scanMu.RUnlock()
	if active {
		return nil, fmt.Errorf("a scan is already active")
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("select at least one folder to refresh")
	}

	a.store.mu.RLock()
	shared := false
	for _, target := range targets {
		if target.NodeID < 0 || target.NodeID >= len(a.store.nodes) || a.store.nodes[target.NodeID] == nil {
			a.store.mu.RUnlock()
			return nil, fmt.Errorf("selected folder is no longer available")
		}
		node := a.store.nodes[target.NodeID]
		if !node.IsFolder || node.FullPath == "" || node.FullPath != target.Path {
			a.store.mu.RUnlock()
			return nil, fmt.Errorf("selected item is not the expected filesystem folder")
		}
		shared = shared || subtreeHasSharedAllocation(node)
	}
	rootTarget := FolderRefreshTarget{NodeID: a.store.root.ID, Path: a.store.root.FullPath}
	storeGeneration := a.store.generation
	hasDiskUsage := a.store.hasDiskUsage
	paths := make(map[int]string, len(trackedIDs))
	for _, id := range trackedIDs {
		if id >= 0 && id < len(a.store.nodes) && a.store.nodes[id] != nil {
			paths[id] = a.store.nodes[id].FullPath
		} else {
			paths[id] = ""
		}
	}
	a.store.mu.RUnlock()
	var reduced []FolderRefreshTarget
	for i, target := range targets {
		covered := false
		for j, parent := range targets {
			if i != j && cachePathWithin(target.Path, parent.Path) && (canonicalCachePath(target.Path) != canonicalCachePath(parent.Path) || j < i) {
				covered = true
				break
			}
		}
		if !covered {
			reduced = append(reduced, target)
		}
	}
	for _, target := range reduced {
		if _, err := a.validateScanPath(target.Path); err != nil {
			return nil, err
		}
		a.scanCache.InvalidatePath(target.Path)
	}
	// Shared allocations may cross the selected boundaries. Recompute their
	// identities from the scan root rather than publishing incorrect totals.
	if shared {
		a.logger.Infof("refresh requires a full scan to account for shared file allocations")
		reduced = []FolderRefreshTarget{rootTarget}
	}
	ctx, generation := a.beginScan(reduced[0].Path)
	defer a.finishScan(generation)
	started := time.Now()
	profile := a.GetProfile()
	type scannedFolder struct {
		target      FolderRefreshTarget
		root        *Node
		files, dirs int
	}
	var pending []scannedFolder
	var report ScanReportSnapshot
	for i := 0; i < len(reduced); i++ {
		target := reduced[i]
		var files, dirs int64
		scanner := NewScannerWithFilesystem(&profile, 0, a.filesystem)
		scanner.requireLinkCounts = target.NodeID != rootTarget.NodeID
		scanner.ReportAllErrors(true)
		a.attachScanner(generation, scanner)
		scanner.SetContext(ctx, func(current string) { a.updateScanPath(generation, current) })
		root, err := scanner.buildTree(target.Path, 0, -1, &files, &dirs)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("scan cancelled")
		}
		if err != nil {
			return nil, err
		}
		if target.NodeID != rootTarget.NodeID && subtreeHasSharedAllocation(root) {
			a.logger.Infof("refresh found shared or unknown file allocations; scanning the root for accurate totals")
			reduced = []FolderRefreshTarget{rootTarget}
			pending = nil
			report = ScanReportSnapshot{}
			i = -1
			continue
		}
		part := scanner.Report()
		for j, count := range part.Errors {
			report.Errors[j] += count
		}
		for j, count := range part.Skipped {
			report.Skipped[j] += count
		}
		report.Examples = append(report.Examples, part.Examples...)
		pending = append(pending, scannedFolder{target, root, int(files), int(dirs)})
	}
	for _, target := range reduced {
		a.scanCache.InvalidatePath(target.Path)
	}
	var volumeUsage *disk.UsageStat
	if hasDiskUsage {
		var err error
		volumeUsage, err = disk.Usage(rootTarget.Path)
		if err != nil {
			a.logger.Warningf("could not refresh disk usage: %v", err)
		}
	}
	a.scanMu.Lock()
	if a.scanGeneration != generation || !a.scanActive {
		a.scanMu.Unlock()
		return nil, errScanSuperseded
	}
	if ctx.Err() != nil {
		a.scanMu.Unlock()
		return nil, fmt.Errorf("scan cancelled")
	}
	a.store.mu.Lock()
	if a.store.generation != storeGeneration {
		a.store.mu.Unlock()
		a.scanMu.Unlock()
		return nil, fmt.Errorf("tree changed during refresh; please retry")
	}
	var result DeleteResult
	for _, folder := range pending {
		var err error
		result, err = a.store.replaceSubtreeLocked(folder.target.NodeID, folder.root, folder.files, folder.dirs)
		if err != nil {
			a.store.mu.Unlock()
			a.scanMu.Unlock()
			return nil, err
		}
	}
	remapped := make(map[int]int, len(paths))
	wanted := make(map[string][]int, len(paths))
	for id, oldPath := range paths {
		remapped[id] = -1
		if oldPath != "" {
			wanted[oldPath] = append(wanted[oldPath], id)
		}
	}
	for _, node := range a.store.nodes {
		if node != nil {
			for _, id := range wanted[node.FullPath] {
				remapped[id] = node.ID
			}
		}
	}
	totalSize := a.store.root.Size
	if volumeUsage != nil {
		a.store.diskTotal, a.store.diskFree = int64(volumeUsage.Total), int64(volumeUsage.Free)
	}
	a.store.mu.Unlock()
	a.scanMu.Unlock()
	a.logScanReport(report)
	reportInfo := a.persistScanReport(rootTarget.Path, started, time.Since(started), profile, report, int64(result.FileCount), int64(result.DirCount), totalSize)
	a.logger.Infof("folder refresh completed in %s: %d folders", time.Since(started).Round(time.Millisecond), len(reduced))
	return &FolderRefreshResult{FileCount: result.FileCount, DirCount: result.DirCount, NodeIDs: remapped, ScanReport: reportInfo}, nil
}
