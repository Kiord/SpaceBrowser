//go:build darwin

package platform

import (
	"os"
	"os/exec"
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

func init() { Impl = Darwin{} }
