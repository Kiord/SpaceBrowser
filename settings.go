package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"spacebrowser/internal/platform"
)

const settingsFileVersion = 6

type persistedSettings struct {
	Version              int                `json:"version"`
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

type persistedSettingsLocation struct {
	Path string `json:"path"`
}

func defaultSettingsPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user configuration directory: %w", err)
	}
	return filepath.Join(configDir, "SpaceBrowser", "settings.json"), nil
}

func configuredSettingsPath(defaultPath string) string {
	if defaultPath == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(defaultPath), "settings-location.json"))
	if err != nil {
		return defaultPath
	}
	var location persistedSettingsLocation
	if json.Unmarshal(data, &location) != nil || location.Path == "" {
		return defaultPath
	}
	return filepath.Clean(location.Path)
}

func saveSettingsLocation(defaultPath, activePath string) error {
	data, err := json.MarshalIndent(persistedSettingsLocation{Path: activePath}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings location: %w", err)
	}
	data = append(data, '\n')
	if err := writeSettingsFile(filepath.Join(filepath.Dir(defaultPath), "settings-location.json"), data); err != nil {
		return fmt.Errorf("save settings location: %w", err)
	}
	return nil
}

func loadSettings(path string) (Profile, error) {
	return loadSettingsWithFilesystem(path, platform.Impl)
}

func loadSettingsWithFilesystem(path string, filesystem platform.ScannerFilesystem) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}

	var saved persistedSettings
	if err := json.Unmarshal(data, &saved); err != nil {
		return Profile{}, fmt.Errorf("decode settings: %w", err)
	}
	if saved.Version < 1 || saved.Version > settingsFileVersion {
		return Profile{}, fmt.Errorf("unsupported settings version %d", saved.Version)
	}
	appearance := saved.Appearance
	if saved.Version == 1 {
		appearance = defaultAppearanceSettings()
	}
	allowDelete := saved.AllowDelete
	allowPermanentDelete := saved.AllowPermanentDelete
	rescanOnDelete := saved.RescanOnDelete
	if saved.Version < 3 {
		defaults := defaultProfile()
		allowDelete = defaults.AllowDelete
		rescanOnDelete = defaults.RescanOnDelete
	}
	if saved.Version < 6 {
		allowPermanentDelete = defaultProfile().AllowPermanentDelete
	}
	keyBindings := saved.KeyBindings
	if saved.Version < 4 {
		keyBindings = defaultKeyBindings()
	} else if saved.Version < 5 {
		defaults := defaultKeyBindings()
		keyBindings.VisitSelected = defaults.VisitSelected
		keyBindings.Delete = defaults.Delete
	}

	return normalizeProfileWithFilesystem(Profile{
		ExcludedPaths:        saved.ExcludedPaths,
		SkipHidden:           saved.SkipHidden,
		MinFileSize:          saved.MinFileSize,
		FollowSymlinks:       saved.FollowSymlinks,
		SkipNetworkFS:        saved.SkipNetworkFS,
		AllowDelete:          allowDelete,
		AllowPermanentDelete: allowPermanentDelete,
		RescanOnDelete:       rescanOnDelete,
		Appearance:           appearance,
		KeyBindings:          keyBindings,
	}, filesystem)
}

func saveSettings(path string, profile Profile) error {
	saved := persistedSettings{
		Version:              settingsFileVersion,
		ExcludedPaths:        profile.ExcludedPaths,
		SkipHidden:           profile.SkipHidden,
		MinFileSize:          profile.MinFileSize,
		FollowSymlinks:       profile.FollowSymlinks,
		SkipNetworkFS:        profile.SkipNetworkFS,
		AllowDelete:          profile.AllowDelete,
		AllowPermanentDelete: profile.AllowPermanentDelete,
		RescanOnDelete:       profile.RescanOnDelete,
		Appearance:           profile.Appearance,
		KeyBindings:          profile.KeyBindings,
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')
	return writeSettingsFile(path, data)
}

func writeSettingsFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}

	temp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary settings file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("secure temporary settings file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary settings file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("flush temporary settings file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary settings file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace settings file: %w", err)
	}
	return nil
}
