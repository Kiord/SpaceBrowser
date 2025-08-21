//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
)

type inodeKeyT struct{ dev, ino uint64 }

// NOTE: os.FileInfo on Windows doesn’t expose allocation size or a stable inode via Stat_t.
// Keep it simple & safe here; you can enhance later with x/sys/windows if needed.
func allocatedSize(fi os.FileInfo) int64        { return fi.Size() }
func inodeKey(fi os.FileInfo) (inodeKeyT, bool) { return inodeKeyT{}, false }

func baseName(path string) string {
	b := filepath.Base(path)
	if b == "." || b == string(os.PathSeparator) || b == "" {
		vol := filepath.VolumeName(path)
		if vol != "" {
			return vol + "\\"
		}
	}
	return b
}

func isMountRoot(path string) bool {
	path, _ = filepath.Abs(path)
	vol := filepath.VolumeName(path)
	clean := filepath.Clean(path)
	return strings.EqualFold(clean, vol+"\\")
}
