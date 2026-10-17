//go:build linux

package platform

import (
	"os"
	"path/filepath"
	"strings"
)

func linuxStorageDetails(device string) (media, bus string) {
	device, err := filepath.EvalSymlinks(device)
	if err != nil || !strings.HasPrefix(device, "/dev/") {
		return
	}
	sys, err := filepath.EvalSymlinks(filepath.Join("/sys/class/block", filepath.Base(device)))
	if err != nil || strings.Contains(sys, "/virtual/") {
		return
	}
	if _, err := os.Stat(filepath.Join(sys, "partition")); err == nil {
		sys = filepath.Dir(sys)
	}
	switch {
	case strings.Contains(sys, "/nvme/"):
		bus = "NVMe"
	case strings.Contains(sys, "/usb"):
		bus = "USB"
	case strings.Contains(sys, "/ata"):
		bus = "SATA"
	case strings.HasPrefix(filepath.Base(sys), "mmcblk"):
		bus = "MMC"
	}
	if bus != "MMC" {
		value, err := os.ReadFile(filepath.Join(sys, "queue/rotational"))
		if err == nil {
			switch strings.TrimSpace(string(value)) {
			case "1":
				media = "HDD"
			case "0":
				media = "SSD"
			}
		}
	}
	return
}
