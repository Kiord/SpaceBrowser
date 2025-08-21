//go:build linux

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

type Linux struct{ Default }

func (Linux) AllocatedSize(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}
func (Linux) InodeKeyOf(fi os.FileInfo) (InodeKey, bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return InodeKey{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, true
	}
	return InodeKey{}, false
}
func (Linux) OpenInFileBrowser(p string) error {
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		uri := "file://" + filepath.ToSlash(p)
		if err := exec.Command("dbus-send", "--session",
			"--dest=org.freedesktop.FileManager1", "--type=method_call", "--print-reply",
			"/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItems",
			"array:string:"+uri, "string:").Run(); err == nil {
			return nil
		}
		return exec.Command("xdg-open", filepath.Dir(p)).Run()
	}
	return exec.Command("xdg-open", p).Run()
}

func init() { Impl = Linux{} }
