package app

import (
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"spacebrowser/internal/platform"
)

// ScanSpaceUsage is measured with the scan and retained in its saved snapshot.
// Difference is signed: positive means unaccounted; negative means excess allocation.
type ScanSpaceUsage struct {
	RootPath              string `json:"rootPath"`
	MeasuredAt            string `json:"measuredAt"`
	ScannedBytes          int64  `json:"scannedBytes"`
	VolumeAvailable       bool   `json:"volumeAvailable"`
	VolumeScan            bool   `json:"volumeScan"`
	VolumeUsed            int64  `json:"volumeUsed"`
	VolumeTotal           int64  `json:"volumeTotal"`
	Difference            int64  `json:"difference"`
	UnreadableDirectories int64  `json:"unreadableDirectories"`
	ErrorCount            int64  `json:"errorCount"`
	CoverageKnown         bool   `json:"coverageKnown"`
	FilteredPaths         int64  `json:"filteredPaths"`
	ExcludedVolumes       int64  `json:"excludedVolumes"`
	ScopeNote             string `json:"scopeNote,omitempty"`
	Stale                 bool   `json:"stale"`
	VolumeScope           string `json:"-"`
}

func scanVolumeScopeKey(scanner *Scanner, note string) string {
	paths := make([]string, 0, len(scanner.skippedMounts))
	for path := range scanner.skippedMounts {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return note + "\n" + strings.Join(paths, "\n")
}

func configureScanVolume(scanner *Scanner, root string) string {
	if !scanner.filesystem.IsMountRoot(root) {
		return ""
	}
	if provider, ok := scanner.filesystem.(platform.NestedVolumeProvider); ok {
		mounts, err := provider.NestedVolumeMounts(root)
		if err != nil {
			return "Mounted volumes could not be checked; this scan may include other volumes."
		}
		scanner.skippedMounts = make(map[string]struct{}, len(mounts))
		for _, mount := range mounts {
			scanner.skippedMounts[canonicalCachePath(mount)] = struct{}{}
		}
	}
	if scanner.profile.FollowSymlinks {
		return "Symbolic links were followed and may include files outside this volume. Disable Follow symlinks to compare volume space."
	}
	return ""
}

func scanSpaceUsage(root *Node, report ScanReportSnapshot, volume *disk.UsageStat, scopeNote string, measuredAt time.Time) *ScanSpaceUsage {
	usage := &ScanSpaceUsage{
		RootPath: root.FullPath, MeasuredAt: measuredAt.Format(time.RFC3339Nano), ScannedBytes: root.Size,
		UnreadableDirectories: report.Errors[scanErrorReadDirectory], ErrorCount: report.TotalErrors(),
		CoverageKnown: true, FilteredPaths: report.Skipped[scanSkipExcluded] + report.Skipped[scanSkipHidden] + report.Skipped[scanSkipNetwork],
		ExcludedVolumes: report.Skipped[scanSkipNestedVolume], ScopeNote: scopeNote,
	}
	if volume != nil && volume.Total > 0 && volume.Free <= volume.Total {
		usage.VolumeAvailable = true
		usage.VolumeScan = true
		usage.VolumeUsed, usage.VolumeTotal = int64(volume.Total-volume.Free), int64(volume.Total)
		usage.Difference = usage.VolumeUsed - usage.ScannedBytes
	}
	return usage
}

func (a *App) finishScanSpace(root *Node, report ScanReportSnapshot, scopeNote string) {
	var volume *disk.UsageStat
	volumeScan := a.filesystem.IsMountRoot(root.FullPath)
	if volumeScan {
		volume, _ = disk.Usage(root.FullPath)
	}
	root.SpaceUsage = scanSpaceUsage(root, report, volume, scopeNote, time.Now())
	root.SpaceUsage.VolumeScan = volumeScan
	children := make([]*Node, 0, len(root.Children)+1)
	for _, child := range root.Children {
		if !child.IsFreeSpace {
			children = append(children, child)
		}
	}
	root.Children = children
	root.DiskTotal, root.DiskFree = 0, 0
	if root.SpaceUsage.VolumeAvailable {
		root.DiskTotal, root.DiskFree = int64(volume.Total), int64(volume.Free)
		root.Children = append(root.Children, &Node{
			ID: -1, ParentID: root.ID, Name: "[Free Disk Space]", Size: root.DiskFree,
			DiskTotal: root.DiskTotal, IsFreeSpace: true, Depth: root.Depth + 1,
		})
	}
}

func (s *TreeStore) markSpaceUsageStaleLocked() {
	if s.root != nil && s.root.SpaceUsage != nil && !s.root.SpaceUsage.Stale {
		usage := *s.root.SpaceUsage
		usage.Stale = true
		s.root.SpaceUsage = &usage
	}
}
