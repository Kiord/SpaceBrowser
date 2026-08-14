package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type FileIdentity struct {
	Volume uint64
	Low    uint64
	High   uint64
}

type FileUsage struct {
	AllocatedSize int64
	Identity      FileIdentity
	HasIdentity   bool
}

type API interface {
	UsageFor(string, os.FileInfo) FileUsage
	BaseName(string) string
	IsHidden(string) bool
	IsMountRoot(string) bool
	OpenInFileBrowser(string) error
	OpenPath(string) error
	DefaultApplicationName(string) (string, error)
	OpenWith(string) error
	ShowProperties(string) error
	MoveToTrash(string) error
	AssociatedIcon(string, bool) ([]byte, error)
	Canonicalize(string) string
	DefaultStartPath() string
	IsLikelyNetworkFS(string) bool
}

// -------- defaults (POSIX-ish + xdg-open) --------

type Default struct{}

func (Default) UsageFor(_ string, fi os.FileInfo) FileUsage {
	return FileUsage{AllocatedSize: fi.Size()}
}

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

func (Default) OpenPath(p string) error {
	return exec.Command("xdg-open", p).Run()
}

func (Default) DefaultApplicationName(string) (string, error) {
	return "", fmt.Errorf("default application lookup is not supported on this platform")
}

func (Default) OpenWith(string) error {
	return fmt.Errorf("application selection is not supported on this platform")
}

func (Default) ShowProperties(string) error {
	return fmt.Errorf("filesystem properties are not supported on this platform")
}

func (Default) MoveToTrash(p string) error {
	if _, err := exec.LookPath("gio"); err != nil {
		return fmt.Errorf("no desktop Trash service is available")
	}
	if err := exec.Command("gio", "trash", "--", p).Run(); err != nil {
		return fmt.Errorf("move to Trash: %w", err)
	}
	return nil
}

func (Default) AssociatedIcon(string, bool) ([]byte, error) { return nil, nil }

func (Default) Canonicalize(p string) string {
	abs, _ := filepath.Abs(p)
	return filepath.Clean(abs)
}

func (Default) DefaultStartPath() string {
	if h, err := os.UserHomeDir(); err == nil {
		if fi, err := os.Stat(h); err == nil && fi.IsDir() {
			return h
		}
	}
	return string(os.PathSeparator)
}

func (Default) IsHidden(path string) bool {
	base := filepath.Base(path)
	if base == "" {
		return false
	}
	return strings.HasPrefix(base, ".")
}

func (Default) IsLikelyNetworkFS(string) bool { return false }

// Global chosen implementation (overridden in per-OS files during init()).
var Impl API = Default{}
