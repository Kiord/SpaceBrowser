//go:build darwin

package platform

import (
	"bytes"
	"context"
	"encoding/xml"
	"os/exec"
	"time"
)

// diskutil exposes optional hardware properties even for many APFS volumes.
// A missing property is unknown, not evidence of a rotational disk.
func darwinStorageDetails(path string) (media, bus string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/usr/sbin/diskutil", "info", "-plist", path).Output()
	if err != nil {
		return
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	key := ""
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			if decoder.DecodeElement(&key, &start) != nil {
				return "", ""
			}
		case "true", "false":
			if key == "SolidState" {
				if start.Name.Local == "true" {
					media = "SSD"
				} else {
					media = "HDD"
				}
			}
			key = ""
		case "string":
			if key == "BusProtocol" {
				if decoder.DecodeElement(&bus, &start) != nil {
					return "", ""
				}
			}
			key = ""
		}
	}
	return
}
