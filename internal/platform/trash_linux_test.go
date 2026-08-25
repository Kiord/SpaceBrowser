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
	homeTrash := filepath.Join(dataHome, "Trash")
	if err := os.MkdirAll(homeTrash, 0o700); err != nil {
		t.Fatal(err)
	}
	if !linuxPlatform.IsTrashRoot(homeTrash) {
		t.Fatal("the FreeDesktop home Trash was not recognized")
	}
	if !linuxPlatform.IsInTrash(filepath.Join(homeTrash, "files", "deleted.txt")) {
		t.Fatal("an item in the FreeDesktop home Trash was not recognized")
	}

	uid := strconv.Itoa(os.Getuid())
	mountRoot := t.TempDir()
	isMountRoot := func(path string) bool {
		return filepath.Clean(path) == filepath.Clean(mountRoot)
	}
	privateTrash := filepath.Join(mountRoot, ".Trash-"+uid)
	if err := os.MkdirAll(privateTrash, 0o700); err != nil {
		t.Fatal(err)
	}
	sharedTrash := filepath.Join(mountRoot, ".Trash")
	if err := os.MkdirAll(filepath.Join(sharedTrash, uid), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sharedTrash, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		privateTrash,
		filepath.Join(sharedTrash, uid),
		sharedTrash,
	} {
		if !isLinuxTrashRoot(path, dataHome, os.Getuid(), isMountRoot) {
			t.Fatalf("IsTrashRoot(%q) = false, want true", path)
		}
		if !isLinuxPathInTrash(filepath.Join(path, "files", "deleted.txt"), dataHome, os.Getuid(), isMountRoot) {
			t.Fatalf("a descendant of %q was not classified as inside Trash", path)
		}
	}

	lookalikeParent := filepath.Join(mountRoot, "ordinary")
	lookalike := filepath.Join(lookalikeParent, ".Trash-"+uid)
	if err := os.MkdirAll(lookalike, 0o700); err != nil {
		t.Fatal(err)
	}
	if isLinuxTrashRoot(lookalike, dataHome, os.Getuid(), isMountRoot) {
		t.Fatal("a correctly named directory below an ordinary folder was recognized as Trash")
	}

	nonStickyMount := t.TempDir()
	nonStickyShared := filepath.Join(nonStickyMount, ".Trash")
	if err := os.MkdirAll(filepath.Join(nonStickyShared, uid), 0o700); err != nil {
		t.Fatal(err)
	}
	twoMountRoots := func(path string) bool {
		clean := filepath.Clean(path)
		return clean == filepath.Clean(mountRoot) || clean == filepath.Clean(nonStickyMount)
	}
	if isLinuxTrashRoot(nonStickyShared, dataHome, os.Getuid(), twoMountRoots) {
		t.Fatal("a shared Trash without the sticky bit was recognized")
	}

	symlinkMount := t.TempDir()
	symlinkTarget := t.TempDir()
	symlinkTrash := filepath.Join(symlinkMount, ".Trash-"+uid)
	if err := os.Symlink(symlinkTarget, symlinkTrash); err != nil {
		t.Fatal(err)
	}
	threeMountRoots := func(path string) bool {
		clean := filepath.Clean(path)
		return twoMountRoots(clean) || clean == filepath.Clean(symlinkMount)
	}
	if isLinuxTrashRoot(symlinkTrash, dataHome, os.Getuid(), threeMountRoots) {
		t.Fatal("a symbolic link was recognized as a private Trash directory")
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

func TestLinuxEmptyTrashContinuesAfterSuccessfulNoOp(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(filesDir, "deleted.bin"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(infoDir, "deleted.bin.trashinfo"), []byte("[Trash Info]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var calls []string
	commands := [][]string{{"gio", "trash", "--empty"}, {"ktrash6", "--empty"}}
	err := emptyLinuxTrashLocations([]string{trashRoot}, commands, func(command []string) (bool, error) {
		calls = append(calls, command[0])
		if command[0] == "ktrash6" {
			return true, emptyLinuxTrashRoots([]string{trashRoot})
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "gio" || calls[1] != "ktrash6" {
		t.Fatalf("Trash helper calls = %v, want [gio ktrash6]", calls)
	}
	if containsItems, err := linuxTrashRootsContainItems([]string{trashRoot}); err != nil || containsItems {
		t.Fatalf("Trash still contains items: contains=%v error=%v", containsItems, err)
	}
}

func TestLinuxEmptyTrashFallsBackToVerifiedDirectories(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(filesDir, "deleted.bin"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(infoDir, "deleted.bin.trashinfo"), []byte("[Trash Info]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := emptyLinuxTrashLocations([]string{trashRoot}, nil, func([]string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if containsItems, err := linuxTrashRootsContainItems([]string{trashRoot}); err != nil || containsItems {
		t.Fatalf("Trash still contains items after direct fallback: contains=%v error=%v", containsItems, err)
	}
}
