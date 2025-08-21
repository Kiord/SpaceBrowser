//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

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
