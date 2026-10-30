package platform

import "golang.org/x/sys/unix"

func FileCreationTime(path string) int64 {
	var stat unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &stat); err != nil || stat.Mask&unix.STATX_BTIME == 0 {
		return 0
	}
	return stat.Btime.Sec
}
