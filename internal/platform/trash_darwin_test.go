//go:build darwin

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinTrashRootClassification(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	darwinPlatform := Darwin{}
	if !darwinPlatform.IsTrashRoot(filepath.Join(home, ".Trash")) {
		t.Fatal("the current user's Trash was not recognized")
	}
	if !darwinPlatform.IsInTrash(filepath.Join(home, ".Trash", "deleted.txt")) {
		t.Fatal("an item in the current user's Trash was not protected")
	}
	if !darwinPlatform.IsTrashRoot(filepath.Join("/Volumes", "External", ".Trashes")) {
		t.Fatal("an external-volume Trash container was not recognized")
	}
	if darwinPlatform.IsTrashRoot(filepath.Join(home, "Documents", ".Trash")) {
		t.Fatal("an ordinary .Trash folder was recognized as the system Trash")
	}
}
