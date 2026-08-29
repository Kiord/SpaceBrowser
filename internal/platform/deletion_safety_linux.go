//go:build linux

package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

func (Linux) ValidateDeletion(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect deletion target: %w", err)
	}
	var mounts []string
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		mounts, err = linuxDeletionMountPoints()
		if err != nil {
			return fmt.Errorf("SpaceBrowser could not verify mounted filesystems, so deletion was blocked: %w", err)
		}
	}
	protectedTrees := []string{
		"/boot", "/dev", "/proc", "/sys", "/run", "/etc", "/usr",
		"/bin", "/sbin", "/lib", "/lib64", "/var", "/root", "/snap",
		"/nix", "/ostree",
	}
	protectedExact := []string{"/", "/home", "/mnt", "/media", "/tmp"}
	return validateDeletionTarget(path, info, protectedTrees, protectedExact, mounts, false)
}

func linuxDeletionMountPoints() ([]string, error) {
	file, err := os.Open(linuxMountInfoPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	mounts := linuxMountPoints(file)
	result := make([]string, 0, len(mounts))
	for mount := range mounts {
		result = append(result, filepath.Clean(mount))
	}
	return result, nil
}
