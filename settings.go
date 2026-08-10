package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const settingsFileVersion = 2

type persistedSettings struct {
	Version        int                `json:"version"`
	ExcludedPaths  []string           `json:"excludedPaths"`
	SkipHidden     bool               `json:"skipHidden"`
	MinFileSize    int64              `json:"minFileSize"`
	FollowSymlinks bool               `json:"followSymlinks"`
	SkipNetworkFS  bool               `json:"skipNetworkFS"`
	Appearance     AppearanceSettings `json:"appearance"`
}

func defaultSettingsPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user configuration directory: %w", err)
	}
	return filepath.Join(configDir, "SpaceBrowser", "settings.json"), nil
}

func loadSettings(path string) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}

	var saved persistedSettings
	if err := json.Unmarshal(data, &saved); err != nil {
		return Profile{}, fmt.Errorf("decode settings: %w", err)
	}
	if saved.Version != 1 && saved.Version != settingsFileVersion {
		return Profile{}, fmt.Errorf("unsupported settings version %d", saved.Version)
	}
	appearance := saved.Appearance
	if saved.Version == 1 {
		appearance = defaultAppearanceSettings()
	}

	return normalizeProfile(Profile{
		ExcludedPaths:  saved.ExcludedPaths,
		SkipHidden:     saved.SkipHidden,
		MinFileSize:    saved.MinFileSize,
		FollowSymlinks: saved.FollowSymlinks,
		SkipNetworkFS:  saved.SkipNetworkFS,
		Appearance:     appearance,
	})
}

func saveSettings(path string, profile Profile) error {
	saved := persistedSettings{
		Version:        settingsFileVersion,
		ExcludedPaths:  profile.ExcludedPaths,
		SkipHidden:     profile.SkipHidden,
		MinFileSize:    profile.MinFileSize,
		FollowSymlinks: profile.FollowSymlinks,
		SkipNetworkFS:  profile.SkipNetworkFS,
		Appearance:     profile.Appearance,
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')

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
