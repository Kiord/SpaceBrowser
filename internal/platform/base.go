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

type TrashRestoreInfo struct {
	TargetPath   string
	OriginalPath string
}

// ScanLocation is a user-visible filesystem root that can be selected as a
// scan target, such as a Windows drive, a macOS volume, or a Linux mount.
type ScanLocation struct {
	Name string
	Path string
	Kind string
}

type diagnosticDirectoryReader interface {
	ReadDirWithDiagnostics(string) ([]DirectoryEntry, *DirectoryReadDiagnostic, error)
}

func ReadDirWithDiagnostics(filesystem ScannerFilesystem, path string) ([]DirectoryEntry, *DirectoryReadDiagnostic, error) {
	if reader, ok := filesystem.(diagnosticDirectoryReader); ok {
		return reader.ReadDirWithDiagnostics(path)
	}
	entries, err := filesystem.ReadDir(path)
	return entries, nil, err
}

// ScannerFilesystem is the filesystem surface required to discover and
// account for a scan tree. It deliberately excludes user-facing desktop
// operations so scanners can later receive only the dependency they need.
type ScannerFilesystem interface {
	ReadDir(string) ([]DirectoryEntry, error)
	UsageFor(string, os.FileInfo) FileUsage
	BaseName(string) string
	IsHidden(string) bool
	IsMountRoot(string) bool
	Canonicalize(string) string
	IsLikelyNetworkFS(string) bool
}

// DesktopActions contains operations initiated by the application UI. These
// methods may open native dialogs, launch applications, or modify filesystem
// state and therefore do not belong in the scanner contract.
type DesktopActions interface {
	OpenInFileBrowser(string) error
	OpenPath(string) error
	DefaultApplicationName(string) (string, error)
	OpenWith(context.Context, string) error
	PickFolder(context.Context, string) (string, error)
	ShowProperties(string) error
	MoveToTrash(string) error
	DeletePermanently(string) error
	IsTrashRoot(string) bool
	IsInTrash(string) bool
	EmptyTrash(string) error
	TrashRestoreInfo(string) (TrashRestoreInfo, error)
	RestoreTrashItem(string) error
	DeleteTrashItemPermanently(string) error
	DefaultStartPath() string
}

// LocationProvider discovers user-visible filesystem roots. It is separate
// from DesktopActions because enumeration is read-only and does not open or
// control desktop UI.
type LocationProvider interface {
	ListScanLocations() ([]ScanLocation, error)
}

// API is retained as the combined native platform implementation used by the
// application today. Dependency injection can provide its embedded interfaces
// independently.
type API interface {
	ScannerFilesystem
	DesktopActions
	LocationProvider
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

func (Default) DeletePermanently(p string) error {
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("delete permanently: %w", err)
	}
	return nil
}

func (Default) IsTrashRoot(string) bool {
	return false
}

func (Default) IsInTrash(string) bool {
	return false
}

func (Default) EmptyTrash(string) error {
	return fmt.Errorf("emptying Trash is not supported on this platform")
}

func (Default) TrashRestoreInfo(string) (TrashRestoreInfo, error) {
	return TrashRestoreInfo{}, fmt.Errorf("restoring Trash items is not supported on this platform")
}

func (Default) RestoreTrashItem(string) error {
	return fmt.Errorf("restoring Trash items is not supported on this platform")
}

func (Default) DeleteTrashItemPermanently(string) error {
	return fmt.Errorf("permanent deletion from Trash is not supported on this platform")
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

func (d Default) ListScanLocations() ([]ScanLocation, error) {
	path := d.DefaultStartPath()
	return []ScanLocation{{Name: "File system", Path: path, Kind: "volume"}}, nil
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
