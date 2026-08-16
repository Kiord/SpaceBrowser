package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	ErrFolderPickerUnavailable = errors.New("platform folder picker unavailable")
	ErrOperationCancelled      = errors.New("platform operation cancelled")
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
	// IdentityNeedsConfirmation is set for identifiers, such as the legacy
	// 64-bit Windows directory ID, that are not guaranteed collision-free.
	IdentityNeedsConfirmation bool
	LinkCount                 uint64
	HasLinkCount              bool
	MetadataError             error
}

// DirectoryEntry carries metadata that a platform can obtain while enumerating
// a directory. HasUsage and HasHidden allow the scanner to avoid repeating
// per-file system calls when the enumeration API already returned those values.
type DirectoryEntry struct {
	os.DirEntry
	Usage     FileUsage
	HasUsage  bool
	Hidden    bool
	HasHidden bool
}

type DirectoryReadDiagnostic struct {
	PortableFallback bool
	Cause            error
}

type diagnosticDirectoryReader interface {
	ReadDirWithDiagnostics(string) ([]DirectoryEntry, *DirectoryReadDiagnostic, error)
}

func ReadDirWithDiagnostics(api API, path string) ([]DirectoryEntry, *DirectoryReadDiagnostic, error) {
	if reader, ok := api.(diagnosticDirectoryReader); ok {
		return reader.ReadDirWithDiagnostics(path)
	}
	entries, err := api.ReadDir(path)
	return entries, nil, err
}

type API interface {
	ReadDir(string) ([]DirectoryEntry, error)
	UsageFor(string, os.FileInfo) FileUsage
	BaseName(string) string
	IsHidden(string) bool
	IsMountRoot(string) bool
	OpenInFileBrowser(string) error
	OpenPath(string) error
	DefaultApplicationName(string) (string, error)
	OpenWith(context.Context, string) error
	PickFolder(context.Context, string) (string, error)
	ShowProperties(string) error
	MoveToTrash(string) error
	Canonicalize(string) string
	DefaultStartPath() string
	IsLikelyNetworkFS(string) bool
}

// -------- defaults (POSIX-ish + xdg-open) --------

type Default struct{}

func (Default) ReadDir(path string) ([]DirectoryEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	result := make([]DirectoryEntry, len(entries))
	for i, entry := range entries {
		result[i].DirEntry = entry
	}
	return result, nil
}

func (Default) UsageFor(_ string, fi os.FileInfo) FileUsage {
	return FileUsage{AllocatedSize: fi.Size(), LinkCount: 1}
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

func (Default) OpenWith(context.Context, string) error {
	return fmt.Errorf("application selection is not supported on this platform")
}

func (Default) PickFolder(context.Context, string) (string, error) {
	return "", ErrFolderPickerUnavailable
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
