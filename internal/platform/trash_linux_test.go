//go:build linux

package platform

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLinuxTrashRootClassification(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	linuxPlatform := Linux{}
	if !linuxPlatform.IsTrashRoot(filepath.Join(dataHome, "Trash")) {
		t.Fatal("the FreeDesktop home Trash was not recognized")
	}
	uid := strconv.Itoa(os.Getuid())
	sharedTrash := filepath.Join(t.TempDir(), ".Trash")
	if err := os.MkdirAll(filepath.Join(sharedTrash, uid), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join("/media/volume", ".Trash-"+uid),
		filepath.Join("/media/volume", ".Trash", uid),
		sharedTrash,
	} {
		if !linuxPlatform.IsTrashRoot(path) {
			t.Fatalf("IsTrashRoot(%q) = false, want true", path)
		}
	}
	if linuxPlatform.IsTrashRoot(filepath.Join(dataHome, "not-trash")) {
		t.Fatal("an ordinary folder was recognized as Trash")
	}
}
