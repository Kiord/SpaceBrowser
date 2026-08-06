package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Profile struct {
	PlatformSystem string   `json:"platformSystem"`
	ExcludedPaths  []string `json:"excludedPaths"`
	SkipHidden     bool     `json:"skipHidden"`
	MinFileSize    int64    `json:"minFileSize"`
	FollowSymlinks bool     `json:"followSymlinks"`
	SkipNetworkFS  bool     `json:"skipNetworkFS"`
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
		SkipNetworkFS:  true,
	}
	return p
}
