package main

import (
	"testing"

	"spacebrowser/internal/platform"
)

type staticLocationProvider struct {
	locations []platform.ScanLocation
	err       error
}

func (p staticLocationProvider) ListScanLocations() ([]platform.ScanLocation, error) {
	return p.locations, p.err
}

func TestGetScanLocationsPreservesLocationsWhenIconsAreUnavailable(t *testing.T) {
	app := &App{locations: staticLocationProvider{locations: []platform.ScanLocation{
		{Name: "Test volume", Path: t.TempDir() + "-missing", Kind: "volume"},
	}}}

	locations, err := app.GetScanLocations()
	if err != nil {
		t.Fatalf("GetScanLocations returned an error: %v", err)
	}
	if len(locations) != 1 {
		t.Fatalf("got %d locations, want 1", len(locations))
	}
	if locations[0].Name != "Test volume" || locations[0].Kind != "volume" {
		t.Fatalf("location metadata was not preserved: %+v", locations[0])
	}
	if locations[0].IconURL != "" {
		t.Fatalf("unavailable icon URL = %q, want empty", locations[0].IconURL)
	}
}
