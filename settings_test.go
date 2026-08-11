package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestSettingsPersistAcrossAppInstances(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "SpaceBrowser", "settings.json")
	excludedPath := filepath.Join(t.TempDir(), "excluded")

	first := newApp(settingsPath)
	want := Profile{
		ExcludedPaths:  []string{"  " + excludedPath + "  ", excludedPath},
		SkipHidden:     true,
		MinFileSize:    1024 * 1024,
		FollowSymlinks: true,
		SkipNetworkFS:  false,
		AllowDelete:    true,
		RescanOnDelete: false,
		Appearance: AppearanceSettings{
			Palette:        "ocean",
			ZoomFactor:     1.4,
			CornerRadius:   6,
			ReliefStrength: 0.18,
		},
		KeyBindings: KeyBindings{
			Back:          "Alt+Left",
			Forward:       "Alt+Right",
			Parent:        "P",
			Root:          "R",
			Open:          "Ctrl+O",
			OpenWith:      "Ctrl+Shift+O",
			VisitSelected: "V",
			Delete:        "Shift+Delete",
		},
	}
	if err := first.SetProfile(want); err != nil {
		t.Fatalf("SetProfile() error = %v", err)
	}
	want.MinFileSize = 2 * 1024 * 1024
	if err := first.SetProfile(want); err != nil {
		t.Fatalf("second SetProfile() error = %v", err)
	}

	second := newApp(settingsPath)
	got := second.GetProfile()
	if got.PlatformSystem != runtime.GOOS {
		t.Fatalf("PlatformSystem = %q, want %q", got.PlatformSystem, runtime.GOOS)
	}
	want.PlatformSystem = runtime.GOOS
	want.ExcludedPaths = first.GetProfile().ExcludedPaths
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted profile = %#v, want %#v", got, want)
	}
}

func TestVersionFourSettingsGainNewCommandBindings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{
  "version": 4,
  "excludedPaths": [],
  "skipHidden": false,
  "minFileSize": 1024,
  "followSymlinks": false,
  "skipNetworkFS": true,
  "allowDelete": false,
  "rescanOnDelete": true,
  "appearance": {
    "palette": "default",
    "zoomFactor": 1,
    "cornerRadius": 0,
    "reliefStrength": 0.1
  },
  "keyBindings": {
    "back": "B",
    "forward": "F",
    "parent": "P",
    "root": "R",
    "open": "Ctrl+O",
    "openWith": "Ctrl+Shift+O"
  }
}`)
	if err := os.WriteFile(settingsPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	got := newApp(settingsPath).GetProfile().KeyBindings
	if got.Back != "B" || got.Forward != "F" || got.Parent != "P" || got.Root != "R" {
		t.Fatalf("existing key bindings were not preserved: %#v", got)
	}
	defaults := defaultKeyBindings()
	if got.VisitSelected != defaults.VisitSelected || got.Delete != defaults.Delete {
		t.Fatalf("new key bindings = (%q, %q), want defaults (%q, %q)",
			got.VisitSelected, got.Delete, defaults.VisitSelected, defaults.Delete)
	}
}

func TestVersionThreeSettingsGainDefaultKeyBindings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{
  "version": 3,
  "excludedPaths": [],
  "skipHidden": false,
  "minFileSize": 1024,
  "followSymlinks": false,
  "skipNetworkFS": true,
  "allowDelete": false,
  "rescanOnDelete": true,
  "appearance": {
    "palette": "default",
    "zoomFactor": 1,
    "cornerRadius": 0,
    "reliefStrength": 0.1
  }
}`)
	if err := os.WriteFile(settingsPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	got := newApp(settingsPath).GetProfile()
	if got.KeyBindings != defaultKeyBindings() {
		t.Fatalf("key bindings = %#v, want defaults %#v", got.KeyBindings, defaultKeyBindings())
	}
}

func TestEmptyKeyBindingsRemainUnassigned(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	app := newApp(settingsPath)
	profile := app.GetProfile()
	profile.KeyBindings = KeyBindings{}
	if err := app.SetProfile(profile); err != nil {
		t.Fatalf("SetProfile() error = %v", err)
	}

	if got := newApp(settingsPath).GetProfile().KeyBindings; got != (KeyBindings{}) {
		t.Fatalf("key bindings = %#v, want all bindings unassigned", got)
	}
}

func TestVersionTwoSettingsGainDefaultDeletionSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{
  "version": 2,
  "excludedPaths": [],
  "skipHidden": false,
  "minFileSize": 1024,
  "followSymlinks": false,
  "skipNetworkFS": true,
  "appearance": {
    "palette": "default",
    "zoomFactor": 1,
    "cornerRadius": 0,
    "reliefStrength": 0.1
  }
}`)
	if err := os.WriteFile(settingsPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	got := newApp(settingsPath).GetProfile()
	defaults := defaultProfile()
	if got.AllowDelete != defaults.AllowDelete || got.RescanOnDelete != defaults.RescanOnDelete {
		t.Fatalf("deletion settings = (%v, %v), want defaults (%v, %v)",
			got.AllowDelete, got.RescanOnDelete, defaults.AllowDelete, defaults.RescanOnDelete)
	}
}

func TestVersionOneSettingsGainDefaultAppearance(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{
  "version": 1,
  "excludedPaths": [],
  "skipHidden": true,
  "minFileSize": 2048,
  "followSymlinks": false,
  "skipNetworkFS": true
}`)
	if err := os.WriteFile(settingsPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	got := newApp(settingsPath).GetProfile()
	if got.Appearance != defaultAppearanceSettings() {
		t.Fatalf("appearance = %#v, want defaults %#v", got.Appearance, defaultAppearanceSettings())
	}
}

func TestNewAppUsesDefaultsForInvalidSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := newApp(settingsPath).GetProfile()
	if !reflect.DeepEqual(got, *defaultProfile()) {
		t.Fatalf("profile = %#v, want defaults %#v", got, *defaultProfile())
	}
}

func TestFailedSaveDoesNotApplyProfile(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newApp(filepath.Join(parentFile, "settings.json"))
	want := app.GetProfile()

	changed := want
	changed.SkipHidden = !changed.SkipHidden
	if err := app.SetProfile(changed); err == nil {
		t.Fatal("SetProfile() error = nil, want save error")
	}
	if got := app.GetProfile(); !reflect.DeepEqual(got, want) {
		t.Fatalf("profile changed after failed save: got %#v, want %#v", got, want)
	}
}
