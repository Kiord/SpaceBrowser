//go:build windows

package fileicon

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsAssociatedZipIcon(t *testing.T) {
	directory := t.TempDir()
	zipPath := filepath.Join(directory, "archive.zip")
	if err := os.WriteFile(zipPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	backend := windowsBackend{}
	zipIcon, err := backend.Lookup(zipPath, false)
	if err != nil {
		t.Fatalf("Lookup(zip): %v", err)
	}
	if zipIcon.MediaType != "image/png" {
		t.Fatalf("zip media type = %q", zipIcon.MediaType)
	}
	if _, err := png.Decode(bytes.NewReader(zipIcon.Data)); err != nil {
		t.Fatalf("associated zip icon is not PNG: %v", err)
	}

	folderIcon, err := backend.Lookup(directory, true)
	if err != nil {
		t.Fatalf("Lookup(folder): %v", err)
	}
	if bytes.Equal(zipIcon.Data, folderIcon.Data) {
		t.Fatal("zip association icon unexpectedly matches the folder icon")
	}
}
