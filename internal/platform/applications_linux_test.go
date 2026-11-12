//go:build linux

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxApplicationMetadataUsesConfiguredHandlers(t *testing.T) {
	root := t.TempDir()
	applications := filepath.Join(root, "applications")
	if err := os.MkdirAll(applications, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", root)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	for name, contents := range map[string]string{
		"editor.desktop":  "[Desktop Entry]\nName=Text Editor\nIcon=editor\n",
		"manager.desktop": "[Desktop Action Other]\nName=Wrong Name\n[Desktop Entry]\nName=Custom Manager\nIcon=manager\n",
	} {
		if err := os.WriteFile(filepath.Join(applications, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := filepath.Join(root, "xdg-mime")
	script := `#!/bin/sh
case "$2:$3" in
  filetype:*) echo text/plain ;;
  default:text/plain) echo editor.desktop ;;
  default:inode/directory) echo manager.desktop ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(command, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	platform := Linux{}
	if name, err := platform.DefaultApplicationName("/document.txt"); err != nil || name != "Text Editor" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
	if path, err := platform.DefaultApplicationPath("/document.txt"); err != nil || path != filepath.Join(applications, "editor.desktop") {
		t.Fatalf("path = %q, err = %v", path, err)
	}
	if browser, err := platform.FileBrowserApplication(); err != nil || browser.Name != "Custom Manager" || browser.Path != filepath.Join(applications, "manager.desktop") {
		t.Fatalf("browser = %+v, err = %v", browser, err)
	}
	if _, err := linuxDesktopApplicationPath("../manager.desktop"); err == nil {
		t.Fatal("desktop ID escaped applications directory")
	}
}

func TestLinuxFileBrowserOpensContainingFolderWithDefaultHandler(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "item.txt")
	output := filepath.Join(root, "opened")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "xdg-open"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$SB_TEST_OPENED\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("SB_TEST_OPENED", output)
	if err := (Linux{}).OpenInFileBrowser(file); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != root {
		t.Fatalf("opened = %q, err = %v", got, err)
	}
}
