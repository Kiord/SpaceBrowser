//go:build linux && !cgo

package platform

import "fmt"

func linuxDesktopScanLocations() ([]ScanLocation, error) {
	return nil, fmt.Errorf("GIO volume monitor requires cgo")
}
