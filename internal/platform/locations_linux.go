//go:build linux

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"
)

func (Linux) ListScanLocations() ([]ScanLocation, error) {
	locations, nativeErr := linuxDesktopScanLocations()
	fallback, fallbackErr := linuxPartitionScanLocations()
	if nativeErr != nil && fallbackErr != nil {
		return nil, fmt.Errorf("enumerate Linux locations: %v; fallback: %w", nativeErr, fallbackErr)
	}
	if nativeErr != nil {
		locations = fallback
	} else if fallbackErr == nil {
		// GIO provides the user-facing names and removable-drive classification.
		// Mount information supplements it with locally mounted network volumes.
		locations = append(locations, fallback...)
	} else if len(locations) == 0 {
		return nil, fallbackErr
	}
	if len(locations) == 0 {
		if fallbackErr != nil {
			return nil, fallbackErr
		}
	}

	locations = normalizeLinuxLocations(locations)
	sort.SliceStable(locations, func(i, j int) bool {
		if locations[i].Path == locations[j].Path {
			return false
		}
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

func normalizeLinuxLocations(locations []ScanLocation) []ScanLocation {
	seen := make(map[string]int, len(locations)+1)
	result := make([]ScanLocation, 0, len(locations)+1)
	for _, location := range locations {
		path := filepath.Clean(location.Path)
		if !filepath.IsAbs(path) {
			continue
		}
		if index, found := seen[path]; found {
			if location.Kind == "network" {
				result[index].Kind = "network"
			}
			continue
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}
		name := strings.TrimSpace(location.Name)
		if name == "" {
			name = filepath.Base(path)
		}
		if path == "/" {
			name = "File system"
		}
		kind := location.Kind
		if kind == "" {
			kind = "volume"
		}
		result = append(result, ScanLocation{Name: name, Path: path, Kind: kind})
		seen[path] = len(result) - 1
	}
	if _, found := seen["/"]; !found {
		result = append(result, ScanLocation{Name: "File system", Path: "/", Kind: "volume"})
	}
	return result
}

func linuxPartitionScanLocations() ([]ScanLocation, error) {
	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil, fmt.Errorf("enumerate Linux mounts: %w", err)
	}
	uidPrefix := filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "gvfs")
	locations := make([]ScanLocation, 0, len(partitions))
	for _, partition := range partitions {
		mountPoint := filepath.Clean(partition.Mountpoint)
		visiblePath := mountPoint == "/" ||
			strings.HasPrefix(mountPoint, "/media/") ||
			strings.HasPrefix(mountPoint, "/run/media/") ||
			strings.HasPrefix(mountPoint, "/mnt/") ||
			mountPoint == "/mnt" ||
			strings.HasPrefix(mountPoint, uidPrefix+string(os.PathSeparator))
		if !visiblePath && !isLinuxNetworkFilesystemType(partition.Fstype) {
			continue
		}
		name := filepath.Base(mountPoint)
		kind := "volume"
		if isLinuxNetworkFilesystemType(partition.Fstype) {
			kind = "network"
		}
		locations = append(locations, ScanLocation{Name: name, Path: mountPoint, Kind: kind})
	}
	return locations, nil
}

func isLinuxNetworkFilesystemType(filesystemType string) bool {
	switch strings.ToLower(filesystemType) {
	case "9p", "afp", "ceph", "cifs", "davfs", "fuse.sshfs", "nfs", "nfs4", "smb3", "sshfs":
		return true
	default:
		return false
	}
}
