//go:build darwin && cgo

package fileicon

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinAssociatedFileAndFolderIcons(t *testing.T) {
	directory := t.TempDir()
	filePath := filepath.Join(directory, "document.txt")
	if err := os.WriteFile(filePath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := darwinBackend{}
	for name, path := range map[string]string{"file": filePath, "folder": directory} {
		t.Run(name, func(t *testing.T) {
			icon, err := backend.Lookup(path, name == "folder")
			if err != nil {
				t.Fatalf("Lookup: %v", err)
			}
			if icon.MediaType != "image/png" {
				t.Fatalf("media type = %q", icon.MediaType)
			}
			if _, err := png.Decode(bytes.NewReader(icon.Data)); err != nil {
				t.Fatalf("icon is not PNG: %v", err)
			}
		})
	}
}
