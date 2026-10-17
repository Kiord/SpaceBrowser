//go:build windows

package platform

import (
	"encoding/binary"
	"golang.org/x/sys/windows"
	"strings"
)

// Unsupported drivers and unavailable devices omit their hardware details.
func windowsStorageDetails(root string) (media, bus string) {
	kind := windowsDriveType(root)
	if kind != windows.DRIVE_FIXED && kind != windows.DRIVE_REMOVABLE {
		return
	}
	name, err := windows.UTF16PtrFromString(`\\.\` + strings.TrimRight(root, `\`))
	if err != nil {
		return
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	query := func(property uint32) []byte {
		input := make([]byte, 12)
		binary.LittleEndian.PutUint32(input, property)
		output := make([]byte, 1024)
		var count uint32
		if windows.DeviceIoControl(handle, 0x002d1400, &input[0], uint32(len(input)), &output[0], uint32(len(output)), &count, nil) != nil {
			return nil
		}
		return output[:count]
	}
	descriptor := query(0) // StorageDeviceProperty
	if len(descriptor) >= 32 {
		bus = storageBusName(binary.LittleEndian.Uint32(descriptor[28:32]))
	}
	// Avoid classifying virtual/RAID/network storage as a physical SSD or HDD.
	if bus == "SATA" || bus == "ATA" || bus == "SAS" || bus == "NVMe" {
		penalty := query(7) // StorageDeviceSeekPenaltyProperty
		if len(penalty) >= 9 {
			if penalty[8] != 0 {
				media = "HDD"
			} else {
				media = "SSD"
			}
		}
	}
	return
}

func storageBusName(bus uint32) string {
	return map[uint32]string{1: "SCSI", 2: "ATAPI", 3: "ATA", 4: "IEEE 1394", 6: "Fibre Channel", 7: "USB", 8: "RAID", 9: "iSCSI", 10: "SAS", 11: "SATA", 12: "SD", 13: "MMC", 14: "Virtual", 15: "Virtual", 16: "Storage Spaces", 17: "NVMe"}[bus]
}
