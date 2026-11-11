//go:build windows

package platform

import (
	"path/filepath"
	"testing"
)

func TestWindowsCanonicalizeResolvesScanInputs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	volume := filepath.VolumeName(dir)
	for input, want := range map[string]string{
		"data":                      filepath.Join(dir, "data"),
		`data/../other`:             filepath.Join(dir, "other"),
		volume + `data`:             filepath.Join(dir, "data"),
		`\data`:                     volume + `\data`,
		`d:`:                        `D:\`,
		`d`:                         `D:\`,
		`d:/folder/../data`:         `D:\data`,
		`\\?\D:\data`:               `D:\data`,
		`\\server\share\data`:       `\\server\share\data`,
		`\\?\UNC\server\share\data`: `\\server\share\data`,
	} {
		if got := (Windows{}).Canonicalize(input); got != want || !filepath.IsAbs(got) {
			t.Errorf("Canonicalize(%q) = %q, want %q", input, got, want)
		}
	}
	for _, path := range []string{`data`, `D:`, `D:data`, `\data`} {
		if err := RequireAbsolutePath(path); err == nil {
			t.Errorf("mutation guard accepted non-absolute path %q", path)
		}
	}
}
