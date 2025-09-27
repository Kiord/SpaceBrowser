//go:build linux

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func (Linux) DefaultStartPath() string {
	if fi, err := os.Stat("/home"); err == nil && fi.IsDir() {
		return "/home"
	}
	if h, err := os.UserHomeDir(); err == nil {
		if fi, err := os.Stat(h); err == nil && fi.IsDir() {
			return h
		}
	}
	return "/"
}

const (
	NFS_SUPER_MAGIC    = 0x6969
	CIFS_SUPER_MAGIC   = 0xFF534D42
	SMB2_SUPER_MAGIC   = 0xFE534D42
	FUSE_SUPER_MAGIC   = 0x65735546
	AUTOFS_SUPER_MAGIC = 0x0187
)

func (Linux) IsLikelyNetworkFS(p string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err == nil {
		switch uint64(st.Type) {
		case NFS_SUPER_MAGIC, CIFS_SUPER_MAGIC, SMB2_SUPER_MAGIC, FUSE_SUPER_MAGIC, AUTOFS_SUPER_MAGIC:
			return true
		}
	}
	// user mounts
	if strings.HasPrefix(p, "/run/user/") && strings.Contains(p, "/gvfs/") {
		return true
	}
	return false
}

func init() { Impl = Linux{} }
