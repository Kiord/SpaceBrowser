package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrashDeletionRejectsLinkedAncestorsButDeletesLinkItself(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "keep")
	if err := os.Mkdir(victim, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(victim, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(root, "files")
	if err := os.Mkdir(files, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(files, "link")
	makeTrashTestLink(t, outside, link)
	if err := removeTrashPaths(root, []string{filepath.Join(link, "keep")}, nil); err == nil {
		t.Fatal("deletion through intermediate link was allowed")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("external sentinel changed: %v", err)
	}
	if err := removeTrashPaths(root, []string{link}, nil); err != nil {
		t.Fatalf("delete link itself: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link still exists: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("external sentinel changed: %v", err)
	}
}

func TestTrashDeletionValidatesMetadataBeforeRemovingData(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	data := filepath.Join(root, "item")
	if err := os.WriteFile(data, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "info")
	makeTrashTestLink(t, outside, link)
	if err := removeTrashPaths(root, []string{data}, []string{filepath.Join(link, "metadata")}); err == nil {
		t.Fatal("linked metadata directory accepted")
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatalf("data removed before validation: %v", err)
	}
}

func TestTrashDeletionRejectsRootAndExternalPaths(t *testing.T) {
	root := t.TempDir()
	for _, target := range []string{root, filepath.Join(filepath.Dir(root), "outside")} {
		if err := removeTrashPaths(root, []string{target}, nil); err == nil {
			t.Fatalf("unsafe target accepted: %s", target)
		}
	}
}
