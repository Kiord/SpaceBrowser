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
