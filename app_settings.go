package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"spacebrowser/internal/platform"
)

func (a *App) SetShowFreeSpace(show bool) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.showFreeSpace = show
}

func (a *App) GetProfile() Profile {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	profile := a.profile
	profile.ExcludedPaths = append([]string(nil), a.profile.ExcludedPaths...)
	return profile
}

func (a *App) GetDefaultProfile() Profile {
	return *defaultProfile()
}

func (a *App) SetProfile(profile Profile) error {
	profile, err := normalizeProfileWithFilesystem(profile, a.filesystem)
	if err != nil {
		return err
	}

	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if a.settingsPath != "" {
		if err := saveSettings(a.settingsPath, profile); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}
	a.profile = profile
	return nil
}

func (a *App) GetSettingsPath() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settingsPath
}

func (a *App) GetDefaultSettingsPath() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.defaultSettingsPath
}

func (a *App) SetSettingsPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("settings path cannot be empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve settings path: %w", err)
	}
	path = filepath.Clean(absPath)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("settings path points to a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect settings path: %w", err)
	}

	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if path == a.settingsPath {
		return nil
	}
	if a.defaultSettingsPath == "" {
		return fmt.Errorf("default settings location is unavailable")
	}
	if err := saveSettings(path, a.profile); err != nil {
		return fmt.Errorf("write settings at new location: %w", err)
	}
	if err := saveSettingsLocation(a.defaultSettingsPath, path); err != nil {
		return err
	}
	a.settingsPath = path
	return nil
}

func (a *App) PickSettingsPath() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("app not initialized")
	}
	currentPath := a.GetSettingsPath()
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "Choose configuration file location",
		DefaultDirectory:     filepath.Dir(currentPath),
		DefaultFilename:      filepath.Base(currentPath),
		CanCreateDirectories: true,
		Filters:              []runtime.FileFilter{{DisplayName: "JSON files (*.json)", Pattern: "*.json"}},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve settings path: %w", err)
	}
	return filepath.Clean(absPath), nil
}

func normalizeProfile(profile Profile) (Profile, error) {
	return normalizeProfileWithFilesystem(profile, platform.Impl)
}

func normalizeProfileWithFilesystem(profile Profile, filesystem platform.ScannerFilesystem) (Profile, error) {
	if profile.MinFileSize < 0 {
		return Profile{}, fmt.Errorf("minimum file size cannot be negative")
	}
	if profile.TooltipDelayMS < 0 || profile.TooltipDelayMS > 1000 {
		return Profile{}, fmt.Errorf("tooltip delay must be between 0 and 1000 milliseconds")
	}

	profile.PlatformSystem = defaultProfile().PlatformSystem
	cleaned := make([]string, 0, len(profile.ExcludedPaths))
	seen := make(map[string]struct{}, len(profile.ExcludedPaths))
	for _, path := range profile.ExcludedPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		path = filesystem.Canonicalize(path)
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		cleaned = append(cleaned, path)
	}
	profile.ExcludedPaths = cleaned

	appearance, err := normalizeAppearance(profile.Appearance)
	if err != nil {
		return Profile{}, err
	}
	profile.Appearance = appearance
	profile.Controls = normalizeControlSettings(profile.Controls)
	return profile, nil
}

func normalizeControlSettings(controls ControlSettings) ControlSettings {
	controls.Back = strings.TrimSpace(controls.Back)
	controls.Forward = strings.TrimSpace(controls.Forward)
	controls.Parent = strings.TrimSpace(controls.Parent)
	controls.Root = strings.TrimSpace(controls.Root)
	controls.Open = strings.TrimSpace(controls.Open)
	controls.OpenWith = strings.TrimSpace(controls.OpenWith)
	controls.VisitSelected = strings.TrimSpace(controls.VisitSelected)
	controls.Delete = strings.TrimSpace(controls.Delete)
	return controls
}

func normalizeAppearance(appearance AppearanceSettings) (AppearanceSettings, error) {
	if appearance == (AppearanceSettings{}) {
		return defaultAppearanceSettings(), nil
	}
	validPalettes := map[string]bool{
		"default": true, "legacy": true, "single": true, "duotone": true,
		"tricolor": true, "playful": true, "monochrome": true,
		"earth": true, "ocean": true, "retro": true,
	}
	if !validPalettes[appearance.Palette] {
		return AppearanceSettings{}, fmt.Errorf("unknown colour palette %q", appearance.Palette)
	}
	if math.IsNaN(appearance.ZoomFactor) || math.IsInf(appearance.ZoomFactor, 0) || appearance.ZoomFactor < 0.5 || appearance.ZoomFactor > 5 {
		return AppearanceSettings{}, fmt.Errorf("zoom factor must be between 0.5 and 5")
	}
	if appearance.CornerRadius < 0 || appearance.CornerRadius > 10 {
		return AppearanceSettings{}, fmt.Errorf("corner radius must be between 0 and 10")
	}
	if math.IsNaN(appearance.ReliefStrength) || math.IsInf(appearance.ReliefStrength, 0) || appearance.ReliefStrength < 0 || appearance.ReliefStrength > 0.5 {
		return AppearanceSettings{}, fmt.Errorf("relief strength must be between 0 and 0.5")
	}
	if math.IsNaN(appearance.HoverBrightness) || math.IsInf(appearance.HoverBrightness, 0) || appearance.HoverBrightness < 0 || appearance.HoverBrightness > 0.3 {
		return AppearanceSettings{}, fmt.Errorf("hover brightness must be between 0 and 0.3")
	}
	return appearance, nil
}
