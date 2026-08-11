package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Profile struct {
	PlatformSystem string             `json:"platformSystem"`
	ExcludedPaths  []string           `json:"excludedPaths"`
	SkipHidden     bool               `json:"skipHidden"`
	MinFileSize    int64              `json:"minFileSize"`
	FollowSymlinks bool               `json:"followSymlinks"`
	SkipNetworkFS  bool               `json:"skipNetworkFS"`
	AllowDelete    bool               `json:"allowDelete"`
	RescanOnDelete bool               `json:"rescanOnDelete"`
	Appearance     AppearanceSettings `json:"appearance"`
	KeyBindings    KeyBindings        `json:"keyBindings"`
}

type AppearanceSettings struct {
	Palette        string  `json:"palette"`
	ZoomFactor     float64 `json:"zoomFactor"`
	CornerRadius   int     `json:"cornerRadius"`
	ReliefStrength float64 `json:"reliefStrength"`
}

type KeyBindings struct {
	Back     string `json:"back"`
	Forward  string `json:"forward"`
	Parent   string `json:"parent"`
	Root     string `json:"root"`
	Open     string `json:"open"`
	OpenWith string `json:"openWith"`
}

func defaultKeyBindings() KeyBindings {
	return KeyBindings{
		Open:     "Ctrl+O",
		OpenWith: "Ctrl+Shift+O",
	}
}

func defaultAppearanceSettings() AppearanceSettings {
	return AppearanceSettings{
		Palette:        "default",
		ZoomFactor:     1,
		CornerRadius:   0,
		ReliefStrength: 0.10,
	}
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
		AllowDelete:    false,
		RescanOnDelete: true,
		Appearance:     defaultAppearanceSettings(),
		KeyBindings:    defaultKeyBindings(),
	}
	return p
}
