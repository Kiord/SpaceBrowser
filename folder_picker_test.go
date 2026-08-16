package main

import (
	"context"
	"os"
	"path/filepath"
	"spacebrowser/internal/platform"
	"testing"
)

type folderPickerPlatform struct {
	platform.API
	path  string
	err   error
	calls int
}

func (p *folderPickerPlatform) PickFolder(context.Context, string) (string, error) {
	p.calls++
	return p.path, p.err
}

func TestAppPickFolderUsesPlatformPickerFirst(t *testing.T) {
	picker := &folderPickerPlatform{API: platform.Impl, path: t.TempDir()}

	app := &App{ctx: context.Background(), filesystem: picker, desktop: picker}
	path, err := app.PickFolder()
	if err != nil {
		t.Fatal(err)
	}
	if path != picker.Canonicalize(picker.path) || picker.calls != 1 {
		t.Fatalf("PickFolder returned %q after %d platform calls", path, picker.calls)
	}
}

func TestAppPickFolderTreatsPlatformCancellationNormally(t *testing.T) {
	picker := &folderPickerPlatform{API: platform.Impl, err: platform.ErrOperationCancelled}

	app := &App{ctx: context.Background(), filesystem: picker, desktop: picker}
	path, err := app.PickFolder()
	if err != nil || path != "" {
		t.Fatalf("cancelled PickFolder returned %q, %v", path, err)
	}
}

func TestAppPickFolderRejectsNonDirectoryPortalResult(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	picker := &folderPickerPlatform{API: platform.Impl, path: file}

	app := &App{ctx: context.Background(), filesystem: picker, desktop: picker}
	if _, err := app.PickFolder(); err == nil {
		t.Fatal("PickFolder accepted a non-directory portal result")
	}
}
