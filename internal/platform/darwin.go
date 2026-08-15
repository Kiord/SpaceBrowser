//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
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
			HasIdentity: true,
			LinkCount:   uint64(st.Nlink),
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

func (Darwin) OpenWith(p string) error {
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
