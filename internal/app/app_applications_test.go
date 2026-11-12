package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"spacebrowser/internal/platform"
)

type metadataDesktop struct {
	platform.Default
	name      string
	browser   platform.Application
	requested string
	fail      bool
}

func (d *metadataDesktop) DefaultApplicationName(string) (string, error) { return d.name, nil }
func (d *metadataDesktop) DefaultApplicationPath(path string) (string, error) {
	d.requested = path
	return d.browser.Path, nil
}
func (d *metadataDesktop) FileBrowserApplication() (platform.Application, error) {
	if d.fail {
		return platform.Application{}, fmt.Errorf("unavailable")
	}
	return d.browser, nil
}

func TestApplicationMetadataPreservesNamesWhenIconsAreUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	desktop := &metadataDesktop{name: "Editor", browser: platform.Application{Name: "Custom Browser", Path: filepath.Join(t.TempDir(), "missing.exe")}}
	a := &App{desktop: desktop}
	info, err := a.GetDefaultApplicationInfo(path)
	if err != nil || info.Name != "Editor" || info.IconURL != "" || desktop.requested != path {
		t.Fatalf("default application = %+v, err=%v, requested=%q", info, err, desktop.requested)
	}
	if got := a.GetFileBrowserInfo(); got.Name != "Custom Browser" || got.IconURL != "" {
		t.Fatalf("browser = %+v", got)
	}
	desktop.fail = true
	if got := a.GetFileBrowserInfo(); got != (ApplicationInfo{}) {
		t.Fatalf("unavailable browser = %+v", got)
	}
}

func TestApplicationMetadataRejectsMissingTarget(t *testing.T) {
	desktop := &metadataDesktop{name: "Editor"}
	a := &App{desktop: desktop}
	if _, err := a.GetDefaultApplicationInfo(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing target accepted")
	}
	if desktop.requested != "" {
		t.Fatal("lookup ran for missing target")
	}
}
