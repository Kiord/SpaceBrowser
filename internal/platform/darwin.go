//go:build darwin

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type Darwin struct{ Default }

func (Darwin) UsageFor(_ string, fi os.FileInfo) FileUsage {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return FileUsage{
			AllocatedSize: int64(st.Blocks) * 512,
			Identity: FileIdentity{
				Volume: uint64(st.Dev),
				Low:    uint64(st.Ino),
			},
			HasIdentity:  true,
			LinkCount:    uint64(st.Nlink),
			HasLinkCount: true,
		}
	}
	return FileUsage{
		AllocatedSize: fi.Size(),
		LinkCount:     1,
		MetadataError: fmt.Errorf("native macOS file metadata is unavailable"),
	}
}
func (Darwin) OpenInFileBrowser(p string) error {
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return exec.Command("open", "-R", p).Run() // reveal
	}
	return exec.Command("open", p).Run()
}

func (Darwin) OpenPath(p string) error {
	return exec.Command("open", p).Run()
}

func (Darwin) DefaultApplicationName(p string) (string, error) {
	const script = `on run argv
set applicationAlias to default application of (info for POSIX file (item 1 of argv))
return name of (info for applicationAlias)
end run`
	output, err := exec.Command("osascript", "-e", script, p).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("find default application: %s", strings.TrimSpace(string(output)))
	}
	name := strings.TrimSpace(string(output))
	if name == "" {
		return "", fmt.Errorf("the default application has no display name")
	}
	return strings.TrimSuffix(name, ".app"), nil
}

func (Darwin) OpenWith(_ context.Context, p string) error {
	const script = `on run argv
set chosenApplication to choose application with title "Open With" with prompt "Choose an application:"
tell application "Finder" to open (POSIX file (item 1 of argv) as alias) using chosenApplication
end run`
	output, err := exec.Command("osascript", "-e", script, p).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if strings.Contains(message, "(-128)") {
			return nil
		}
		return fmt.Errorf("open application selector: %s", message)
	}
	return nil
}

func (Darwin) ShowProperties(p string) error {
	const script = `on run argv
tell application "Finder"
activate
open information window of (POSIX file (item 1 of argv) as alias)
end tell
end run`
	if err := exec.Command("osascript", "-e", script, p).Run(); err != nil {
		return fmt.Errorf("show filesystem properties: %w", err)
	}
	return nil
}

func (Darwin) MoveToTrash(p string) error {
	const script = `on run argv
tell application "Finder" to delete POSIX file (item 1 of argv)
end run`
	if err := exec.Command("osascript", "-e", script, p).Run(); err != nil {
		return fmt.Errorf("move to Trash: %w", err)
	}
	return nil
}

func (Darwin) IsTrashRoot(p string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return isDarwinTrashRoot(p, home, os.Getuid(), (Darwin{}).IsMountRoot)
}

func (d Darwin) IsInTrash(p string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return isDarwinPathInTrash(p, home, os.Getuid(), d.IsMountRoot)
}

func isDarwinTrashRoot(path, home string, uid int, isMountRoot func(string) bool) bool {
	clean, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	clean = filepath.Clean(clean)
	if clean == filepath.Clean(filepath.Join(home, ".Trash")) {
		return darwinOwnedDirectory(clean, uid)
	}

	uidText := strconv.Itoa(uid)
	base := filepath.Base(clean)
	switch {
	case base == uidText && filepath.Base(filepath.Dir(clean)) == ".Trashes":
		sharedTrash := filepath.Dir(clean)
		return isMountRoot(filepath.Dir(sharedTrash)) && darwinSafeSharedTrash(sharedTrash) && darwinOwnedDirectory(clean, uid)
	case base == ".Trashes":
		return isMountRoot(filepath.Dir(clean)) && darwinSafeSharedTrash(clean) && darwinOwnedDirectory(filepath.Join(clean, uidText), uid)
	default:
		return false
	}
}

func isDarwinPathInTrash(path, home string, uid int, isMountRoot func(string) bool) bool {
	clean, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for current := filepath.Clean(clean); ; current = filepath.Dir(current) {
		if isDarwinTrashRoot(current, home, uid, isMountRoot) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
	}
}

func darwinOwnedDirectory(path string, uid int) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid
}

func darwinSafeSharedTrash(path string) bool {
	info, err := os.Lstat(path)
	// Unlike the FreeDesktop .Trash convention, external macOS volumes may
	// use filesystems that do not expose a Unix sticky bit. The verified mount
	// location, directory type, and no-symlink rule are authoritative here.
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func (d Darwin) EmptyTrash(p string) error {
	if !d.IsTrashRoot(p) {
		return fmt.Errorf("the selected folder is not a supported Trash root")
	}
	if err := exec.Command("osascript", "-e", `tell application "Finder" to empty trash`).Run(); err != nil {
		return fmt.Errorf("empty Trash: %w", err)
	}
	return nil
}

func (d Darwin) TrashRestoreInfo(p string) (TrashRestoreInfo, error) {
	if !d.IsInTrash(p) || d.IsTrashRoot(p) {
		return TrashRestoreInfo{}, fmt.Errorf("the selected item is not inside a supported Trash folder")
	}
	return TrashRestoreInfo{}, fmt.Errorf("restoring Trash items is not available on macOS because Finder does not expose the original location")
}

func (d Darwin) RestoreTrashItem(p string) error {
	_, err := d.TrashRestoreInfo(p)
	return err
}

func (d Darwin) DeleteTrashItemPermanently(p string) error {
	if !d.IsInTrash(p) || d.IsTrashRoot(p) {
		return fmt.Errorf("the selected item is not inside a supported Trash folder")
	}
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("permanently delete Trash item: %w", err)
	}
	return nil
}

func (Darwin) DefaultStartPath() string {
	if fi, err := os.Stat("/Users"); err == nil && fi.IsDir() {
		return "/Users"
	}
	if h, err := os.UserHomeDir(); err == nil {
		if fi, err := os.Stat(h); err == nil && fi.IsDir() {
			return h
		}
	}
	return "/"
}

func (Darwin) IsLikelyNetworkFS(p string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return false
	}

	fsTypeName := func(st *syscall.Statfs_t) string {
		b := make([]byte, 0, len(st.Fstypename))
		for _, c := range st.Fstypename {
			if c == 0 {
				break
			}
			b = append(b, byte(c))
		}
		return string(b)
	}

	typ := fsTypeName(&st)
	if strings.HasPrefix(typ, "smbfs") || strings.HasPrefix(typ, "webdav") ||
		strings.HasPrefix(typ, "nfs") || strings.HasPrefix(typ, "afpfs") ||
		strings.Contains(typ, "fuse") {
		return true
	}
	return false
}

func init() { Impl = Darwin{} }
