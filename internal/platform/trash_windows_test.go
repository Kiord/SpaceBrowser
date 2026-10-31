//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func windowsTrashDeletionFixture(t *testing.T, directory bool, prefix string) (root, dataPath, contentPath, infoPath string) {
	t.Helper()
	root = t.TempDir()
	owner := filepath.Join(root, "S-1-5-21-test")
	if err := os.Mkdir(owner, 0o700); err != nil {
		t.Fatal(err)
	}
	dataPath = filepath.Join(owner, prefix+"item")
	infoPath = filepath.Join(owner, "$Iitem")
	contentPath = dataPath
	if directory {
		if err := os.Mkdir(dataPath, 0o700); err != nil {
			t.Fatal(err)
		}
		contentPath = filepath.Join(dataPath, "$Iordinary-child.txt")
	}
	for path, content := range map[string]string{contentPath: "payload", infoPath: "metadata"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, dataPath, contentPath, infoPath
}

func deleteWindowsTrashFixturePath(t *testing.T, root, path string) error {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return deleteWindowsTrashItem(path, root, strings.Split(relative, string(os.PathSeparator)))
}

func TestWindowsTrashDeletionRejectsMetadataAndPreservesPayload(t *testing.T) {
	for _, kind := range []string{"file", "directory"} {
		for _, name := range []string{"$Iitem", "$iITEM"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				root, _, contentPath, infoPath := windowsTrashDeletionFixture(t, kind == "directory", "$R")
				selected := filepath.Join(filepath.Dir(infoPath), name)
				if err := deleteWindowsTrashFixturePath(t, root, selected); err == nil {
					t.Error("direct Recycle Bin metadata deletion was accepted")
				}
				for path, want := range map[string]string{contentPath: "payload", infoPath: "metadata"} {
					if data, err := os.ReadFile(path); err != nil || string(data) != want {
						t.Errorf("metadata selection changed %q: %q, %v", path, data, err)
					}
				}
			})
		}
	}
}

func TestWindowsTrashDeletionRemovesSelectedPayloadAndMetadata(t *testing.T) {
	for _, prefix := range []string{"$R", "$r"} {
		for _, kind := range []string{"file", "directory"} {
			t.Run(prefix+"/"+kind, func(t *testing.T) {
				root, dataPath, _, infoPath := windowsTrashDeletionFixture(t, kind == "directory", prefix)
				neighbor := filepath.Join(filepath.Dir(dataPath), "$Rother")
				if err := os.WriteFile(neighbor, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := deleteWindowsTrashFixturePath(t, root, dataPath); err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{dataPath, infoPath} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Errorf("selected payload or its metadata still exists at %q: %v", path, err)
					}
				}
				if data, err := os.ReadFile(neighbor); err != nil || string(data) != "keep" {
					t.Fatalf("deletion changed an unrelated payload: %q, %v", data, err)
				}
			})
		}
	}
}

func TestWindowsTrashDeletionOfDescendantKeepsPair(t *testing.T) {
	root, dataPath, child, infoPath := windowsTrashDeletionFixture(t, true, "$R")
	if err := deleteWindowsTrashFixturePath(t, root, child); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(child); !os.IsNotExist(err) {
		t.Fatalf("selected child still exists: %v", err)
	}
	if _, err := os.Stat(dataPath); err != nil {
		t.Fatalf("deleting a child removed its parent payload: %v", err)
	}
	if data, err := os.ReadFile(infoPath); err != nil || string(data) != "metadata" {
		t.Fatalf("deleting a child changed the parent metadata: %q, %v", data, err)
	}
}
