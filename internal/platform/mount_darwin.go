//go:build darwin

package platform

import (
	"path/filepath"

	"github.com/shirou/gopsutil/v3/disk"
)

func (Darwin) IsMountRoot(path string) bool {
	partitions, err := disk.Partitions(true)
	if err != nil {
		return false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	clean := filepath.Clean(absolute)
	for _, partition := range partitions {
		if filepath.Clean(partition.Mountpoint) == clean {
			return true
		}
	}
	return false
}
