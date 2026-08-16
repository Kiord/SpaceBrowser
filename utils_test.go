package main

import (
	"path/filepath"
	"testing"
)

func TestShouldExcludeUsesWindowsCaseInsensitiveSemantics(t *testing.T) {
	base := t.TempDir()
	excluded := filepath.Join(base, "Data")
	profile := defaultProfile()
	profile.PlatformSystem = "windows"
	profile.ExcludedPaths = []string{excluded}

	for _, path := range []string{
		filepath.Join(base, "data"),
		filepath.Join(base, "DATA", "nested", "file.bin"),
	} {
		if !shouldExclude(profile, path) {
			t.Fatalf("shouldExclude(%q) = false, want true for Windows exclusion %q", path, excluded)
		}
	}
	if shouldExclude(profile, filepath.Join(base, "database")) {
		t.Fatal("similarly prefixed sibling was excluded")
	}
}

func TestShouldExcludePreservesCaseSensitivePlatformSemantics(t *testing.T) {
	base := t.TempDir()
	profile := defaultProfile()
	profile.PlatformSystem = "linux"
	profile.ExcludedPaths = []string{filepath.Join(base, "Data")}

	if shouldExclude(profile, filepath.Join(base, "data")) {
		t.Fatal("case-distinct path was excluded on a case-sensitive platform")
	}
}
