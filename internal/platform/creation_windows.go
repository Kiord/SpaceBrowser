package platform

import (
	"os"
	"syscall"
)

func FileCreationTime(path string) int64 {
	info, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || data.CreationTime == (syscall.Filetime{}) {
		return 0
	}
	return data.CreationTime.Nanoseconds() / 1e9
}
