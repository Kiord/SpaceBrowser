//go:build linux

package platform

import "testing"

func TestNormalizeLinuxLocationsAddsRootAndRemovesDuplicates(t *testing.T) {
	locations := normalizeLinuxLocations([]ScanLocation{
		{Name: "First", Path: "/", Kind: "volume"},
		{Name: "Duplicate", Path: "/", Kind: "network"},
	})
	if len(locations) != 1 {
		t.Fatalf("got %d normalized locations, want 1", len(locations))
	}
	if locations[0].Path != "/" || locations[0].Name != "File system" {
		t.Fatalf("unexpected root location: %+v", locations[0])
	}
}

func TestLinuxNetworkFilesystemTypes(t *testing.T) {
	if !isLinuxNetworkFilesystemType("nfs4") {
		t.Fatal("nfs4 should be classified as a network filesystem")
	}
	if isLinuxNetworkFilesystemType("ext4") {
		t.Fatal("ext4 should not be classified as a network filesystem")
	}
}
