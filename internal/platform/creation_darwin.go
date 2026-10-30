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
	data, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return data.Birthtimespec.Sec
}
