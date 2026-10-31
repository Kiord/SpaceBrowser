//go:build linux

package platform

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func linuxTrashRestoreFixture(t *testing.T, directory bool) (info TrashRestoreInfo, contentPath, metadataPath string, metadata []byte) {
	t.Helper()
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	root := filepath.Join(dataHome, "Trash")
	for _, dir := range []string{"files", "info"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	info = TrashRestoreInfo{
		TargetPath:   filepath.Join(root, "files", "item"),
		OriginalPath: filepath.Join(dataHome, "restored item"),
	}
	contentPath = info.TargetPath
	if directory {
		if err := os.Mkdir(info.TargetPath, 0o700); err != nil {
			t.Fatal(err)
		}
		contentPath = filepath.Join(info.TargetPath, "child.txt")
	}
	if err := os.WriteFile(contentPath, []byte("trashed content"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadataPath = filepath.Join(root, "info", "item.trashinfo")
	metadata = []byte("[Trash Info]\nPath=" + url.PathEscape(info.OriginalPath) + "\n")
	if err := os.WriteFile(metadataPath, metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	return info, contentPath, metadataPath, metadata
}

func TestLinuxRestorePreservesDestinationCreatedAtCommit(t *testing.T) {
	for _, kind := range []string{"file", "empty directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			info, contentPath, metadataPath, metadata := linuxTrashRestoreFixture(t, kind == "empty directory")
			var created os.FileInfo
			var linkTarget string
			calls := 0
			err := (Linux{}).restoreTrashItem(info.TargetPath, info, func(oldDir int, oldPath string, newDir int, newPath string, flags uint) error {
				calls++
				if oldPath != info.TargetPath || newPath != info.OriginalPath {
					t.Fatalf("rename paths = %q -> %q, want %+v", oldPath, newPath, info)
				}
				// Inject another application's destination creation after all
				// restore validation, immediately before the real atomic syscall.
				switch kind {
				case "file":
					if err := os.WriteFile(newPath, []byte("another application's content"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "empty directory":
					if err := os.Mkdir(newPath, 0o700); err != nil {
						t.Fatal(err)
					}
				default:
					linkTarget = filepath.Join(filepath.Dir(newPath), "link target")
					if kind == "symlink" {
						if err := os.WriteFile(linkTarget, []byte("link target content"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Symlink(linkTarget, newPath); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				created, err = os.Lstat(newPath)
				if err != nil {
					t.Fatal(err)
				}
				return unix.Renameat2(oldDir, oldPath, newDir, newPath, flags)
			})
			if !errors.Is(err, unix.EEXIST) || calls != 1 {
				t.Fatalf("restore = %v, rename calls = %d; want one attempt and EEXIST", err, calls)
			}
			remaining, err := os.Lstat(info.OriginalPath)
			if err != nil || !os.SameFile(created, remaining) {
				t.Fatalf("restore replaced the newly created destination: %v", err)
			}
			switch kind {
			case "file":
				if data, err := os.ReadFile(info.OriginalPath); err != nil || string(data) != "another application's content" {
					t.Fatalf("destination content changed: %q, %v", data, err)
				}
			case "empty directory":
				if entries, err := os.ReadDir(info.OriginalPath); err != nil || len(entries) != 0 {
					t.Fatalf("destination directory changed: %v, %v", entries, err)
				}
			default:
				if target, err := os.Readlink(info.OriginalPath); err != nil || target != linkTarget {
					t.Fatalf("destination symlink changed: %q, %v", target, err)
				}
			}
			for path, want := range map[string]string{contentPath: "trashed content", metadataPath: string(metadata)} {
				if data, err := os.ReadFile(path); err != nil || string(data) != want {
					t.Errorf("failed restore changed %q: %q, %v", path, data, err)
				}
			}
		})
	}
}

func TestLinuxRestoreFailsClosedWhenAtomicRenameFails(t *testing.T) {
	for _, failure := range []error{unix.ENOSYS, unix.EINVAL, unix.EOPNOTSUPP, unix.EXDEV} {
		t.Run(failure.Error(), func(t *testing.T) {
			info, contentPath, metadataPath, metadata := linuxTrashRestoreFixture(t, false)
			calls := 0
			err := (Linux{}).restoreTrashItem(info.TargetPath, info, func(int, string, int, string, uint) error {
				calls++
				return failure
			})
			if !errors.Is(err, failure) || calls != 1 {
				t.Fatalf("restore = %v, rename calls = %d; want one attempt and %v", err, calls, failure)
			}
			if _, err := os.Lstat(info.OriginalPath); !os.IsNotExist(err) {
				t.Fatalf("failed restore created a destination: %v", err)
			}
			for path, want := range map[string]string{contentPath: "trashed content", metadataPath: string(metadata)} {
				if data, err := os.ReadFile(path); err != nil || string(data) != want {
					t.Errorf("failed restore changed %q: %q, %v", path, data, err)
				}
			}
		})
	}
}

func TestLinuxRestoreDirectoryWithoutReplacingDestination(t *testing.T) {
	info, _, metadataPath, _ := linuxTrashRestoreFixture(t, true)
	if err := (Linux{}).RestoreTrashItem(info.TargetPath, info); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(info.OriginalPath, "child.txt")); err != nil || string(data) != "trashed content" {
		t.Fatalf("restored directory content = %q, %v", data, err)
	}
	for _, path := range []string{info.TargetPath, metadataPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("successful restore left %q: %v", path, err)
		}
	}
}
