//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
)

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
