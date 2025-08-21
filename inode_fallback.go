//go:build !linux && !darwin

package main

import "os"

func allocatedSize(fi os.FileInfo) int64        { return fi.Size() }
func inodeKey(fi os.FileInfo) (inodeKeyT, bool) { return inodeKeyT{}, false }
