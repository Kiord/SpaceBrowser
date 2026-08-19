package main

import (
	"encoding/json"
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
		ExcludedPaths:        []string{"  " + excludedPath + "  ", excludedPath},
		SkipHidden:           true,
		MinFileSize:          1024 * 1024,
		FollowSymlinks:       true,
		SkipNetworkFS:        false,
		ShowTooltips:         false,
		TooltipDelayMS:       350,
		AllowDelete:          true,
		AllowPermanentDelete: true,
		RescanOnDelete:       false,
		Appearance: AppearanceSettings{
			Palette:         "ocean",
			ZoomFactor:      1.4,
			CornerRadius:    6,
			ReliefStrength:  0.18,
			HoverBrightness: 0.12,
			RollOverBoxes:   true,
		},
		Controls: ControlSettings{
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
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if _, ok := persisted["controls"]; !ok {
		t.Fatal("saved settings do not contain the controls section")
	}
	if _, ok := persisted["input"]; ok {
		t.Fatal("saved settings still contain the old input section")
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

func TestVersionSixSettingsGainDefaultInput(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{
  "version": 6,
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

	profile := newApp(settingsPath).GetProfile()
	got := profile.Controls
	if got != defaultControlSettings() {
		t.Fatalf("controls = %#v, want defaults %#v", got, defaultControlSettings())
	}
	if profile.Appearance.HoverBrightness != defaultAppearanceSettings().HoverBrightness {
		t.Fatalf("hover brightness = %v, want default %v", profile.Appearance.HoverBrightness, defaultAppearanceSettings().HoverBrightness)
	}
	if profile.Appearance.RollOverBoxes != defaultAppearanceSettings().RollOverBoxes {
		t.Fatalf("roll over boxes = %v, want default %v", profile.Appearance.RollOverBoxes, defaultAppearanceSettings().RollOverBoxes)
	}
	if !profile.ShowTooltips || profile.TooltipDelayMS != 0 {
		t.Fatalf("tooltip settings = (%v, %d), want (true, 0)", profile.ShowTooltips, profile.TooltipDelayMS)
	}
}

func TestTooltipDelayOutsideRangeIsRejected(t *testing.T) {
	for _, delay := range []int{-1, 1001} {
		app := newApp(filepath.Join(t.TempDir(), "settings.json"))
		profile := app.GetProfile()
		profile.TooltipDelayMS = delay
		if err := app.SetProfile(profile); err == nil {
			t.Fatalf("SetProfile() accepted tooltip delay %d", delay)
		}
	}
}

func TestVersionThreeSettingsGainDefaultInput(t *testing.T) {
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
	if got.Controls != defaultControlSettings() {
		t.Fatalf("controls = %#v, want defaults %#v", got.Controls, defaultControlSettings())
	}
}

func TestEmptyControlBindingsRemainUnassigned(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	app := newApp(settingsPath)
	profile := app.GetProfile()
	profile.Controls = ControlSettings{}
	if err := app.SetProfile(profile); err != nil {
		t.Fatalf("SetProfile() error = %v", err)
	}

	if got := newApp(settingsPath).GetProfile().Controls; got != (ControlSettings{}) {
		t.Fatalf("controls = %#v, want all bindings unassigned", got)
	}
}

func TestHoverBrightnessOutsideSliderRangeIsRejected(t *testing.T) {
	app := newApp(filepath.Join(t.TempDir(), "settings.json"))
	profile := app.GetProfile()
	profile.Appearance.HoverBrightness = 0.31
	if err := app.SetProfile(profile); err == nil {
		t.Fatal("SetProfile() accepted hover brightness above 0.3")
	}
}

func TestSettingsPathCanBeRelocated(t *testing.T) {
	dir := t.TempDir()
	defaultPath := filepath.Join(dir, "default", "settings.json")
	customPath := filepath.Join(dir, "custom", "spacebrowser.json")
	app := newAppWithPaths(defaultPath, defaultPath)
	profile := app.GetProfile()
	profile.SkipHidden = true
	if err := app.SetProfile(profile); err != nil {
		t.Fatalf("SetProfile() error = %v", err)
	}
	if err := app.SetSettingsPath(customPath); err != nil {
		t.Fatalf("SetSettingsPath() error = %v", err)
	}

	if got := app.GetSettingsPath(); got != customPath {
		t.Fatalf("settings path = %q, want %q", got, customPath)
	}
	if got := configuredSettingsPath(defaultPath); got != customPath {
		t.Fatalf("configured settings path = %q, want %q", got, customPath)
	}
	loaded, err := loadSettings(customPath)
	if err != nil {
		t.Fatalf("loadSettings() error = %v", err)
	}
	if !loaded.SkipHidden {
		t.Fatal("relocated settings did not contain the current profile")
	}
	restarted := newAppWithPaths(configuredSettingsPath(defaultPath), defaultPath)
	if got := restarted.GetSettingsPath(); got != customPath {
		t.Fatalf("restarted settings path = %q, want %q", got, customPath)
	}
	if !restarted.GetProfile().SkipHidden {
		t.Fatal("restarted app did not load the relocated settings")
	}
	if _, err := os.Stat(defaultPath); err != nil {
		t.Fatalf("previous settings file should be retained: %v", err)
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
