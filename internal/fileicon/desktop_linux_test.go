//go:build linux

package fileicon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopApplicationIconIgnoresActionIcons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.desktop")
	for _, text := range []string{
		"[Desktop Action New]\nIcon=wrong\n[Desktop Entry]\nName=Editor\nIcon=editor\n",
		"[Desktop Entry]\nIcon=editor\n[Desktop Action New]\nIcon=wrong\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := desktopApplicationIcon(path); err != nil || got != "editor" {
			t.Fatalf("icon=%q, err=%v", got, err)
		}
	}
	if err := os.WriteFile(path, []byte("[Desktop Entry]\nName=Editor\n[Desktop Action New]\nIcon=wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopApplicationIcon(path); err == nil {
		t.Fatal("action icon used as application icon")
	}
}
