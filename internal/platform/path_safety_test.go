package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemMutationBoundariesRejectRelativePaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("payload", []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	operations := map[string]func(string) error{
		"default validation":         (Default{}).ValidateDeletion,
		"native validation":          Impl.(DeletionValidator).ValidateDeletion,
		"default permanent deletion": (Default{}).DeletePermanently,
		"native permanent deletion":  Impl.DeletePermanently,
		"default recycling":          (Default{}).MoveToTrash,
		"native recycling":           Impl.MoveToTrash,
		"empty Trash":                Impl.EmptyTrash,
		"delete Trash item":          Impl.DeleteTrashItemPermanently,
		"restore Trash item":         func(path string) error { return Impl.RestoreTrashItem(path, TrashRestoreInfo{}) },
		"remove Trash paths":         func(path string) error { return removeTrashPaths(path, nil, nil) },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"", "payload", filepath.Join(".", "payload")} {
				if err := operation(path); err == nil || !strings.Contains(err.Error(), "absolute path") {
					t.Fatalf("operation(%q) = %v; want absolute-path rejection", path, err)
				}
			}
		})
	}
	if data, err := os.ReadFile("payload"); err != nil || string(data) != "keep" {
		t.Fatalf("relative target changed: %q, %v", data, err)
	}
}

func TestPermanentDeletionStillAcceptsAbsolutePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("delete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (Default{}).DeletePermanently(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("absolute target was not deleted: %v", err)
	}
}
