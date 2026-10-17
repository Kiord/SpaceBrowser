//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileBrowserRejectsMissingPath(t *testing.T) {
	err := (Windows{}).OpenInFileBrowser(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing-path error, got %v", err)
	}
}
