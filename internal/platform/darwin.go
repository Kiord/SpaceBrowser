//go:build darwin

package platform

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type Darwin struct{ Default }

func (Darwin) AllocatedSize(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}
func (Darwin) InodeKeyOf(fi os.FileInfo) (InodeKey, bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return InodeKey{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, true
	}
	return InodeKey{}, false
}
func (Darwin) OpenInFileBrowser(p string) error {
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return exec.Command("open", "-R", p).Run() // reveal
	}
	return exec.Command("open", p).Run()
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
