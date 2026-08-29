//go:build !windows

package platform

import "path/filepath"

func resolvePhysicalPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
