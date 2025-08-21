//go:build !linux && !darwin && !windows

package main

import (
	"os"
	"path/filepath"
)

type inodeKeyT struct{ dev, ino uint64 }

func allocatedSize(fi os.FileInfo) int64        { return fi.Size() }
func inodeKey(fi os.FileInfo) (inodeKeyT, bool) { return inodeKeyT{}, false }

func baseName(path string) string {
	b := filepath.Base(path)
	if b == "." || b == string(os.PathSeparator) || b == "" {
		return "/"
	}
	return b
}

func isMountRoot(path string) bool {
	path, _ = filepath.Abs(path)
	return filepath.Clean(path) == "/"
}
