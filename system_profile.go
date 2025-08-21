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

func defaultProfile() *Profile {
	p := &Profile{
		PlatformSystem: runtime.GOOS, // "windows" | "darwin" | "linux"
		SkipHidden:     false,
		MinFileSize:    1024,
		FollowSymlinks: false,
	}

	switch runtime.GOOS {
	case "linux":
		p.ExcludedPaths = []string{"/proc", "/sys", "/dev", "/run", "/var/lib/docker", "/var/log/lastlog", "/snap"}
	case "darwin":
		p.ExcludedPaths = []string{"/System", "/private/var/vm", "/Volumes/MobileBackups", "/Library/Application Support/MobileSync/Backup"}
	case "windows":
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		p.ExcludedPaths = []string{
			`C:\$Recycle.Bin`,
			`C:\System Volume Information`,
			filepath.Join(windir, "WinSxS"),
			filepath.Join(windir, "Temp"),
		}
	}
	return p
}

func baseName(path string) string {
	b := filepath.Base(path)
	if b == "." || b == string(os.PathSeparator) || b == "" {
		if runtime.GOOS == "windows" {
			// best-effort root label (e.g., C:\)
			vol := filepath.VolumeName(path)
			if vol != "" {
				return vol + "\\"
			}
		}
		return "/"
	}
	return b
}

func shouldExclude(p *Profile, absPath string) bool {
	cmp := func(s string) string {
		if runtime.GOOS == "windows" {
			return strings.ToLower(s)
		}
		return s
	}
	ap := cmp(absPath)
	for _, ex := range p.ExcludedPaths {
		exAbs := cmp(ex)
		if ap == exAbs || strings.HasPrefix(ap, filepath.Clean(exAbs)+string(os.PathSeparator)) {
			return true
		}
	}
	return false
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

func isMountRoot(path string) bool {
	path, _ = filepath.Abs(path)
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(path)
		clean := filepath.Clean(path)
		return strings.EqualFold(clean, vol+"\\")
	}
	return filepath.Clean(path) == "/"
}
