package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Profile struct {
	PlatformSystem string
	ExcludedPaths  []string
	SkipHidden     bool
	MinFileSize    int64
	FollowSymlinks bool
}

func isHidden(path string) bool {
	base := filepath.Base(path)
	if base == "" {
		return false
	}
	// Simple cross-platform heuristic: leading dot
	// (On Windows, we don't read FILE_ATTRIBUTE_HIDDEN to keep deps small.)
	return strings.HasPrefix(base, ".")
}

func shouldExclude(p *Profile, absPath string) bool {
	for _, ex := range p.ExcludedPaths {
		if absPath == ex || strings.HasPrefix(absPath, filepath.Clean(ex)+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func defaultProfile() *Profile {
	p := &Profile{
		PlatformSystem: runtime.GOOS, // "windows" | "darwin" | "linux"
		SkipHidden:     false,
		MinFileSize:    1024,
		FollowSymlinks: false,
	}
	return p
}
