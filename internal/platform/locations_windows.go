//go:build windows

package platform

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func (Windows) ListScanLocations() ([]ScanLocation, error) {
	required, err := windows.GetLogicalDriveStrings(0, nil)
	if err != nil {
		return nil, fmt.Errorf("enumerate Windows drives: %w", err)
	}
	if required == 0 {
		return nil, fmt.Errorf("enumerate Windows drives: no drive strings returned")
	}

	buffer := make([]uint16, required+1)
	written, err := windows.GetLogicalDriveStrings(uint32(len(buffer)), &buffer[0])
	if err != nil {
		return nil, fmt.Errorf("read Windows drive roots: %w", err)
	}
	if written > uint32(len(buffer)) {
		return nil, fmt.Errorf("read Windows drive roots: result exceeded allocated buffer")
	}

	var locations []ScanLocation
	for _, root := range splitWindowsDriveStrings(buffer[:written]) {
		driveType := windowsDriveType(root)
		kind, fallbackName, supported := windowsLocationKind(driveType)
		if !supported {
			continue
		}
		// Do not offer empty optical/removable drives or disconnected mappings.
		if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
			continue
		}

		name := windowsVolumeLabel(root)
		if name == "" {
			name = fallbackName
		}
		locations = append(locations, ScanLocation{Name: name, Path: root, Kind: kind})
	}
	return locations, nil
}

func splitWindowsDriveStrings(buffer []uint16) []string {
	var roots []string
	for start := 0; start < len(buffer); {
		end := start
		for end < len(buffer) && buffer[end] != 0 {
			end++
		}
		if end == start {
			break
		}
		roots = append(roots, windows.UTF16ToString(buffer[start:end]))
		start = end + 1
	}
	return roots
}

func windowsLocationKind(driveType uint32) (kind, name string, supported bool) {
	switch driveType {
	case windows.DRIVE_FIXED:
		return "fixed", "Local disk", true
	case windows.DRIVE_REMOVABLE:
		return "removable", "Removable drive", true
	case windows.DRIVE_REMOTE:
		return "network", "Network drive", true
	case windows.DRIVE_CDROM:
		return "optical", "Optical drive", true
	case windows.DRIVE_RAMDISK:
		return "ramdisk", "RAM disk", true
	default:
		return "", "", false
	}
}

func windowsVolumeLabel(root string) string {
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return ""
	}
	name := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeInformation(rootPtr, &name[0], uint32(len(name)), nil, nil, nil, nil, 0); err != nil {
		return ""
	}
	return strings.TrimSpace(windows.UTF16ToString(name))
}
