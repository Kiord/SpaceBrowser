package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Profile struct {
	PlatformSystem       string             `json:"platformSystem"`
	ExcludedPaths        []string           `json:"excludedPaths"`
	SkipHidden           bool               `json:"skipHidden"`
	MinFileSize          int64              `json:"minFileSize"`
	FollowSymlinks       bool               `json:"followSymlinks"`
	SkipNetworkFS        bool               `json:"skipNetworkFS"`
	AllowDelete          bool               `json:"allowDelete"`
	AllowPermanentDelete bool               `json:"allowPermanentDelete"`
	RescanOnDelete       bool               `json:"rescanOnDelete"`
	Appearance           AppearanceSettings `json:"appearance"`
	KeyBindings          KeyBindings        `json:"keyBindings"`
}

type AppearanceSettings struct {
	Palette        string  `json:"palette"`
	ZoomFactor     float64 `json:"zoomFactor"`
	CornerRadius   int     `json:"cornerRadius"`
	ReliefStrength float64 `json:"reliefStrength"`
}

type KeyBindings struct {
	Back          string `json:"back"`
	Forward       string `json:"forward"`
	Parent        string `json:"parent"`
	Root          string `json:"root"`
	Open          string `json:"open"`
	OpenWith      string `json:"openWith"`
	VisitSelected string `json:"visitSelected"`
	Delete        string `json:"delete"`
}

func defaultKeyBindings() KeyBindings {
	return KeyBindings{
		Open:          "Ctrl+O",
		OpenWith:      "Ctrl+Shift+O",
		VisitSelected: "Enter",
		Delete:        "Delete",
	}
}

func defaultAppearanceSettings() AppearanceSettings {
	return AppearanceSettings{
		Palette:        "default",
		ZoomFactor:     1,
		CornerRadius:   0,
		ReliefStrength: 0.30,
	}
}

func shouldExclude(p *Profile, absPath string) bool {
	candidate := filepath.Clean(absPath)
	caseInsensitive := p.PlatformSystem == "windows"
	for _, ex := range p.ExcludedPaths {
		if strings.TrimSpace(ex) == "" {
			continue
		}
		excluded := filepath.Clean(ex)
		if pathsEqual(candidate, excluded, caseInsensitive) {
			return true
		}
		prefix := strings.TrimRight(excluded, `/\`) + string(os.PathSeparator)
		if pathHasPrefix(candidate, prefix, caseInsensitive) {
			return true
		}
	}
	return false
}

func pathsEqual(left, right string, caseInsensitive bool) bool {
	if caseInsensitive {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func pathHasPrefix(path, prefix string, caseInsensitive bool) bool {
	if len(path) < len(prefix) {
		return false
	}
	return pathsEqual(path[:len(prefix)], prefix, caseInsensitive)
}

func defaultProfile() *Profile {
	p := &Profile{
		PlatformSystem:       runtime.GOOS, // "windows" | "darwin" | "linux"
		SkipHidden:           false,
		MinFileSize:          1024,
		FollowSymlinks:       false,
		SkipNetworkFS:        true,
		AllowDelete:          false,
		AllowPermanentDelete: false,
		RescanOnDelete:       true,
		Appearance:           defaultAppearanceSettings(),
		KeyBindings:          defaultKeyBindings(),
	}
	return p
}
