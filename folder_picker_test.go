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
	originalPlatform := platform.Impl
	picker := &folderPickerPlatform{API: originalPlatform, path: t.TempDir()}
	platform.Impl = picker
	defer func() { platform.Impl = originalPlatform }()

	app := &App{ctx: context.Background()}
	path, err := app.PickFolder()
	if err != nil {
		t.Fatal(err)
	}
	if path != platform.Impl.Canonicalize(picker.path) || picker.calls != 1 {
		t.Fatalf("PickFolder returned %q after %d platform calls", path, picker.calls)
	}
}

func TestAppPickFolderTreatsPlatformCancellationNormally(t *testing.T) {
	originalPlatform := platform.Impl
	picker := &folderPickerPlatform{API: originalPlatform, err: platform.ErrOperationCancelled}
	platform.Impl = picker
	defer func() { platform.Impl = originalPlatform }()

	app := &App{ctx: context.Background()}
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
	originalPlatform := platform.Impl
	picker := &folderPickerPlatform{API: originalPlatform, path: file}
	platform.Impl = picker
	defer func() { platform.Impl = originalPlatform }()

	app := &App{ctx: context.Background()}
	if _, err := app.PickFolder(); err == nil {
		t.Fatal("PickFolder accepted a non-directory portal result")
	}
}
