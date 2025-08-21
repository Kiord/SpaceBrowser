//go:build linux

package main

import (
	"os"
	"syscall"
)

func allocatedSize(fi os.FileInfo) int64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fi.Size()
	}
	// st_blocks are 512-byte units on Linux
	return int64(st.Blocks) * 512
}

func inodeKey(fi os.FileInfo) (inodeKeyT, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return inodeKeyT{}, false
	}
	return inodeKeyT{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
}
