package platform

import (
	"os"
	"os/exec"
	"path/filepath"
)

type InodeKey struct{ Dev, Ino uint64 }

type API interface {
	AllocatedSize(os.FileInfo) int64
	InodeKeyOf(os.FileInfo) (InodeKey, bool)
	BaseName(string) string
	IsMountRoot(string) bool
	OpenInFileBrowser(string) error
}

// -------- defaults (POSIX-ish + xdg-open) --------

type Default struct{}

func (Default) AllocatedSize(fi os.FileInfo) int64      { return fi.Size() }
func (Default) InodeKeyOf(os.FileInfo) (InodeKey, bool) { return InodeKey{}, false }

func (Default) BaseName(p string) string {
	b := filepath.Base(p)
	if b == "." || b == string(os.PathSeparator) || b == "" {
		return "/"
	}
	return b
}
func (Default) IsMountRoot(p string) bool {
	p, _ = filepath.Abs(p)
	return filepath.Clean(p) == "/"
}
func (Default) OpenInFileBrowser(p string) error {
	// Reasonable default for “other” platforms
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return exec.Command("xdg-open", p).Run()
	}
	return exec.Command("xdg-open", filepath.Dir(p)).Run()
}

// Global chosen implementation (overridden in per-OS files during init()).
var Impl API = Default{}
