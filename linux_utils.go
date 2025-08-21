//go:build linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

type inodeKeyT struct{ dev, ino uint64 }

func allocatedSize(fi os.FileInfo) int64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fi.Size()
	}
	// st_blocks is 512-byte units on Linux
	return int64(st.Blocks) * 512
}

func inodeKey(fi os.FileInfo) (inodeKeyT, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return inodeKeyT{}, false
	}
	return inodeKeyT{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
}

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
