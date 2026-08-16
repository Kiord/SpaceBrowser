//go:build darwin

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"
)

func (Darwin) ListScanLocations() ([]ScanLocation, error) {
	partitions, err := disk.Partitions(true)
	if err != nil {
		return nil, fmt.Errorf("enumerate macOS volumes: %w", err)
	}

	seen := make(map[string]struct{})
	locations := make([]ScanLocation, 0, len(partitions))
	for _, partition := range partitions {
		mountPoint := filepath.Clean(partition.Mountpoint)
		if mountPoint != "/" && !strings.HasPrefix(mountPoint, "/Volumes/") {
			continue
		}
		if mountPoint != "/" && containsMountOption(partition.Opts, "nobrowse") {
			continue
		}
		if _, found := seen[mountPoint]; found {
			continue
		}
		if info, statErr := os.Stat(mountPoint); statErr != nil || !info.IsDir() {
			continue
		}

		name := filepath.Base(mountPoint)
		if mountPoint == "/" {
			name = "Startup disk"
		}
		kind := "volume"
		if isNetworkFilesystemType(partition.Fstype) {
			kind = "network"
		}
		locations = append(locations, ScanLocation{Name: name, Path: mountPoint, Kind: kind})
		seen[mountPoint] = struct{}{}
	}

	if _, found := seen["/"]; !found {
		locations = append(locations, ScanLocation{Name: "Startup disk", Path: "/", Kind: "volume"})
	}
	sort.SliceStable(locations, func(i, j int) bool {
		if locations[i].Path == "/" {
			return true
		}
		if locations[j].Path == "/" {
			return false
		}
		return strings.ToLower(locations[i].Name) < strings.ToLower(locations[j].Name)
	})
	return locations, nil
}

func containsMountOption(options []string, wanted string) bool {
	for _, option := range options {
		if option == wanted {
			return true
		}
	}
	return false
}

func isNetworkFilesystemType(filesystemType string) bool {
	switch strings.ToLower(filesystemType) {
	case "afpfs", "cifs", "nfs", "nfs4", "smbfs", "webdav":
		return true
	default:
		return false
	}
}
