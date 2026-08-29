//go:build darwin

package platform

import (
	"fmt"
	"os"

	"github.com/shirou/gopsutil/v3/disk"
)

func (Darwin) ValidateDeletion(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect deletion target: %w", err)
	}
	var mounts []string
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		partitions, partitionErr := disk.Partitions(true)
		if partitionErr != nil {
			return fmt.Errorf("SpaceBrowser could not verify mounted filesystems, so deletion was blocked: %w", partitionErr)
		}
		mounts = make([]string, 0, len(partitions))
		for _, partition := range partitions {
			mounts = append(mounts, partition.Mountpoint)
		}
	}
	protectedTrees := []string{
		"/System", "/Library", "/private", "/usr", "/bin", "/sbin",
		"/dev", "/Applications", "/cores",
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		protectedTrees = append(protectedTrees, home+"/Library")
	}
	protectedExact := []string{"/", "/Users", "/Volumes", "/tmp"}
	return validateDeletionTarget(path, info, protectedTrees, protectedExact, mounts, false)
}
