//go:build windows

package platform

import (
	"slices"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSplitWindowsDriveStrings(t *testing.T) {
	buffer := []uint16{'C', ':', '\\', 0, 'Z', ':', '\\', 0, 0}
	want := []string{`C:\`, `Z:\`}
	if got := splitWindowsDriveStrings(buffer); !slices.Equal(got, want) {
		t.Fatalf("splitWindowsDriveStrings() = %q, want %q", got, want)
	}
}

func TestWindowsLocationKind(t *testing.T) {
	tests := []struct {
		driveType uint32
		kind      string
		supported bool
	}{
		{windows.DRIVE_FIXED, "fixed", true},
		{windows.DRIVE_REMOVABLE, "removable", true},
		{windows.DRIVE_REMOTE, "network", true},
		{windows.DRIVE_CDROM, "optical", true},
		{windows.DRIVE_RAMDISK, "ramdisk", true},
		{windows.DRIVE_UNKNOWN, "", false},
	}
	for _, test := range tests {
		kind, _, supported := windowsLocationKind(test.driveType)
		if kind != test.kind || supported != test.supported {
			t.Errorf("windowsLocationKind(%d) = (%q, %v), want (%q, %v)", test.driveType, kind, supported, test.kind, test.supported)
		}
	}
}

func TestWindowsListsAtLeastOneAccessibleDrive(t *testing.T) {
	locations, err := (Windows{}).ListScanLocations()
	if err != nil {
		t.Fatalf("ListScanLocations returned an error: %v", err)
	}
	if len(locations) == 0 {
		t.Fatal("ListScanLocations returned no accessible drives")
	}
	for _, location := range locations {
		if location.Name == "" || location.Path == "" || location.Kind == "" {
			t.Fatalf("incomplete scan location: %+v", location)
		}
	}
}
