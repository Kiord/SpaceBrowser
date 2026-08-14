//go:build windows

package main

import (
	"os"
	"path/filepath"
	"spacebrowser/internal/platform"
	"testing"

	winapi "golang.org/x/sys/windows"
)

func TestScannerSkipsWindowsHiddenAttribute(t *testing.T) {
	dir := t.TempDir()
	hiddenPath := filepath.Join(dir, "hidden.txt")
	dotPath := filepath.Join(dir, ".visible.txt")
	for _, path := range []string{hiddenPath, dotPath} {
		if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hiddenPtr, err := winapi.UTF16PtrFromString(hiddenPath)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := winapi.GetFileAttributes(hiddenPtr)
	if err != nil {
		t.Fatal(err)
	}
	if err := winapi.SetFileAttributes(hiddenPtr, attributes|winapi.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}

	profile := defaultProfile()
	profile.SkipHidden = true
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScanner(profile, 1)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(dir, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}

	if fileCount != 1 || len(root.Children) != 1 || root.Children[0].Name != ".visible.txt" {
		t.Fatalf("visible scan results = count %d, children %+v; want only .visible.txt", fileCount, root.Children)
	}
}

func TestScannerCountsHardLinkedFileOnce(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original.bin")
	link := filepath.Join(dir, "link.bin")
	if err := os.WriteFile(original, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, link); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}

	profile := defaultProfile()
	profile.MinFileSize = 0
	profile.SkipNetworkFS = false
	scanner := NewScanner(profile, 1)
	var fileCount, dirCount int64
	root, err := scanner.buildTree(dir, 0, -1, &fileCount, &dirCount)
	if err != nil {
		t.Fatal(err)
	}

	if fileCount != 1 || len(root.Children) != 1 {
		t.Fatalf("hard-linked data counted %d times with %d nodes, want once", fileCount, len(root.Children))
	}
	usage := platform.Impl.UsageFor(original, mustStat(t, original))
	if root.Size != usage.AllocatedSize {
		t.Fatalf("root size = %d, want one allocation of %d", root.Size, usage.AllocatedSize)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
