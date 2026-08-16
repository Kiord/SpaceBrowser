//go:build darwin

package platform

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDarwinTrashRootClassification(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	homeTrash := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(homeTrash, 0o700); err != nil {
		t.Fatal(err)
	}
	darwinPlatform := Darwin{}
	if !darwinPlatform.IsTrashRoot(homeTrash) {
		t.Fatal("the current user's Trash was not recognized")
	}
	if !darwinPlatform.IsInTrash(filepath.Join(homeTrash, "deleted.txt")) {
		t.Fatal("an item in the current user's Trash was not protected")
	}

	uid := strconv.Itoa(os.Getuid())
	mountRoot := t.TempDir()
	sharedTrash := filepath.Join(mountRoot, ".Trashes")
	userTrash := filepath.Join(sharedTrash, uid)
	if err := os.MkdirAll(userTrash, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sharedTrash, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	isMountRoot := func(path string) bool {
		return filepath.Clean(path) == filepath.Clean(mountRoot)
	}
	for _, path := range []string{sharedTrash, userTrash} {
		if !isDarwinTrashRoot(path, home, os.Getuid(), isMountRoot) {
			t.Fatalf("external-volume Trash %q was not recognized", path)
		}
		if !isDarwinPathInTrash(filepath.Join(path, "deleted.txt"), home, os.Getuid(), isMountRoot) {
			t.Fatalf("an item below %q was not recognized as Trash content", path)
		}
	}

	lookalike := filepath.Join(mountRoot, "ordinary", ".Trashes")
	if err := os.MkdirAll(filepath.Join(lookalike, uid), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lookalike, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if isDarwinTrashRoot(lookalike, home, os.Getuid(), isMountRoot) {
		t.Fatal("a .Trashes lookalike outside a mount root was recognized")
	}

	symlinkMount := t.TempDir()
	symlinkTarget := t.TempDir()
	if err := os.MkdirAll(filepath.Join(symlinkTarget, uid), 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkTrash := filepath.Join(symlinkMount, ".Trashes")
	if err := os.Symlink(symlinkTarget, symlinkTrash); err != nil {
		t.Fatal(err)
	}
	twoMountRoots := func(path string) bool {
		clean := filepath.Clean(path)
		return isMountRoot(clean) || clean == filepath.Clean(symlinkMount)
	}
	if isDarwinTrashRoot(symlinkTrash, home, os.Getuid(), twoMountRoots) {
		t.Fatal("a symbolic link was recognized as an external-volume Trash")
	}
}

func TestDarwinRootIsMountRoot(t *testing.T) {
	if !(Darwin{}).IsMountRoot("/") {
		t.Fatal("the root filesystem was not recognized as a mount root")
	}
}
