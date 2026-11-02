package platform

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"
)

// NestedVolumeProvider supplies mount boundaries for a full-volume scan.
type NestedVolumeProvider interface {
	NestedVolumeMounts(string) ([]string, error)
}

func (Default) NestedVolumeMounts(root string) ([]string, error) {
	partitions, err := disk.Partitions(true)
	if err != nil {
		return nil, err
	}
	return nestedVolumeMounts(root, partitions, runtime.GOOS == "darwin"), nil
}

func nestedVolumeMounts(root string, partitions []disk.PartitionStat, macOS bool) []string {
	root = filepath.Clean(root)
	var mounts []string
	for _, partition := range partitions {
		mount := filepath.Clean(partition.Mountpoint)
		if macOS && root == filepath.Clean("/") && mount == filepath.FromSlash("/System/Volumes/Data") {
			// The startup disk's user-visible hierarchy spans System and Data.
			// Firmlink handling removes the duplicate directory paths separately.
			continue
		}
		rel, err := filepath.Rel(root, mount)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			mounts = append(mounts, mount)
		}
	}
	return mounts
}
