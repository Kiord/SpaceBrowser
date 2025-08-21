//go:build windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Windows struct{ Default }

func (Windows) BaseName(p string) string {
	b := filepath.Base(p)
	if b == "." || b == string(os.PathSeparator) || b == "" {
		if vol := filepath.VolumeName(p); vol != "" {
			return vol + `\`
		}
	}
	return b
}
func (Windows) IsMountRoot(p string) bool {
	p, _ = filepath.Abs(p)
	vol := filepath.VolumeName(p)
	return strings.EqualFold(filepath.Clean(p), vol+`\\`)
}
func (Windows) OpenInFileBrowser(p string) error {
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return exec.Command("explorer", "/select,", p).Run()
	}
	return exec.Command("explorer", p).Run()
}

func init() { Impl = Windows{} }
