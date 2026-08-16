//go:build linux && cgo

package fileicon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxGIOAssociatedIcon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document.txt")
	if err := os.WriteFile(path, []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := newPlatformBackend()
	icon, err := backend.Lookup(path, false)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(icon.Data) == 0 || icon.MediaType == "" {
		t.Fatalf("empty icon: %+v", icon)
	}
}
