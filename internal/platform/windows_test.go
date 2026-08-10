//go:build windows

package platform

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsCanonicalizeDriveLetter(t *testing.T) {
	windows := Windows{}
	for input, want := range map[string]string{
		"C":  `C:\`,
		"d":  `d:\`,
		"E:": `E:\`,
	} {
		if got := windows.Canonicalize(input); got != want {
			t.Errorf("Canonicalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWindowsAssociatedZipIcon(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "archive.zip")
	if err := os.WriteFile(zipPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	windows := Windows{}
	zipIcon, err := windows.AssociatedIcon(zipPath, false)
	if err != nil {
		t.Fatalf("AssociatedIcon(zip) error = %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(zipIcon)); err != nil {
		t.Fatalf("associated zip icon is not PNG: %v", err)
	}

	folderIcon, err := windows.AssociatedIcon(dir, true)
	if err != nil {
		t.Fatalf("AssociatedIcon(folder) error = %v", err)
	}
	if bytes.Equal(zipIcon, folderIcon) {
		t.Fatal("zip association icon unexpectedly matches the folder icon")
	}
}
