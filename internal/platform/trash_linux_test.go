//go:build linux

package platform

import (
	"net/url"
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
		if !linuxPlatform.IsInTrash(filepath.Join(path, "files", "deleted.txt")) {
			t.Fatalf("a descendant of %q was not classified as inside Trash", path)
		}
	}
	if linuxPlatform.IsTrashRoot(filepath.Join(dataHome, "not-trash")) {
		t.Fatal("an ordinary folder was recognized as Trash")
	}
}

func TestLinuxRestoreTrashItem(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	trashRoot := filepath.Join(dataHome, "Trash")
	filesDir := filepath.Join(trashRoot, "files")
	infoDir := filepath.Join(trashRoot, "info")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	originalDir := t.TempDir()
	originalPath := filepath.Join(originalDir, "restored file.txt")
	trashedPath := filepath.Join(filesDir, "restored file.txt")
	if err := os.WriteFile(trashedPath, []byte("restored"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := "[Trash Info]\nPath=" + url.PathEscape(originalPath) + "\nDeletionDate=2026-08-16T12:00:00\n"
	infoPath := filepath.Join(infoDir, "restored file.txt.trashinfo")
	if err := os.WriteFile(infoPath, []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}

	linuxPlatform := Linux{}
	details, err := linuxPlatform.TrashRestoreInfo(trashedPath)
	if err != nil {
		t.Fatal(err)
	}
	if details.OriginalPath != originalPath || details.TargetPath != trashedPath {
		t.Fatalf("restore info = %+v", details)
	}
	if err := linuxPlatform.RestoreTrashItem(trashedPath); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(originalPath); err != nil || string(data) != "restored" {
		t.Fatalf("restored data = %q, error = %v", data, err)
	}
	if _, err := os.Stat(infoPath); !os.IsNotExist(err) {
		t.Fatalf("Trash metadata still exists: %v", err)
	}
}

func TestLinuxPermanentlyDeletesTrashItemAndMetadata(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	trashRoot := filepath.Join(dataHome, "Trash")
	trashedPath := filepath.Join(trashRoot, "files", "deleted")
	infoPath := filepath.Join(trashRoot, "info", "deleted.trashinfo")
	if err := os.MkdirAll(trashedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(infoPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(infoPath, []byte("[Trash Info]\nPath=/tmp/deleted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (Linux{}).DeleteTrashItemPermanently(trashedPath); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{trashedPath, infoPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%q still exists: %v", path, err)
		}
	}
}
