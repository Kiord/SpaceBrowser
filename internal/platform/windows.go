//go:build windows

package platform

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Windows struct{ Default }

var windowsNetworkRoots sync.Map

type windowsFileStandardInfo struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  byte
	Directory      byte
	_              [2]byte
}

type windowsFileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

func windowsPathPtr(path string) (*uint16, error) {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) && !strings.HasPrefix(path, `\\?\`) && !strings.HasPrefix(path, `\\.\`) {
		if strings.HasPrefix(path, `\\`) {
			path = `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
		} else {
			path = `\\?\` + path
		}
	}
	return windows.UTF16PtrFromString(path)
}

func windowsVolumePath(path string) (string, error) {
	pathPtr, err := windowsPathPtr(path)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, 32768)
	if err := windows.GetVolumePathName(pathPtr, &buffer[0], uint32(len(buffer))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer), nil
}

func windowsDriveType(root string) uint32 {
	rootPtr, err := windowsPathPtr(root)
	if err != nil {
		return windows.DRIVE_UNKNOWN
	}
	return windows.GetDriveType(rootPtr)
}

func isWindowsNetworkFS(
	path string,
	volumePath func(string) (string, error),
	driveType func(string) uint32,
	cache *sync.Map,
) bool {
	if strings.HasPrefix(path, `\\`) {
		return true
	}
	root, err := volumePath(path)
	if err != nil || root == "" {
		return false
	}
	cacheKey := strings.ToLower(filepath.Clean(root))
	if cached, ok := cache.Load(cacheKey); ok {
		return cached.(bool)
	}
	typeID := driveType(root)
	remote := typeID == windows.DRIVE_REMOTE
	if typeID != windows.DRIVE_UNKNOWN && typeID != windows.DRIVE_NO_ROOT_DIR {
		cache.Store(cacheKey, remote)
	}
	return remote
}

func (w Windows) UsageFor(path string, fi os.FileInfo) FileUsage {
	usage := w.Default.UsageFor(path, fi)
	pathPtr, err := windowsPathPtr(path)
	if err != nil {
		return usage
	}

	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return usage
	}
	defer windows.CloseHandle(handle)

	var standard windowsFileStandardInfo
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileStandardInfo,
		(*byte)(unsafe.Pointer(&standard)),
		uint32(unsafe.Sizeof(standard)),
	); err == nil && standard.AllocationSize >= 0 {
		usage.AllocatedSize = standard.AllocationSize
	}

	var id windowsFileIDInfo
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileIdInfo,
		(*byte)(unsafe.Pointer(&id)),
		uint32(unsafe.Sizeof(id)),
	); err == nil {
		usage.Identity = FileIdentity{
			Volume: id.VolumeSerialNumber,
			Low:    binary.LittleEndian.Uint64(id.FileID[:8]),
			High:   binary.LittleEndian.Uint64(id.FileID[8:]),
		}
		usage.HasIdentity = true
		return usage
	}

	var legacy windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &legacy); err == nil {
		usage.Identity = FileIdentity{
			Volume: uint64(legacy.VolumeSerialNumber),
			Low:    uint64(legacy.FileIndexHigh)<<32 | uint64(legacy.FileIndexLow),
		}
		usage.HasIdentity = true
	}
	return usage
}

func (w Windows) IsHidden(path string) bool {
	pathPtr, err := windowsPathPtr(path)
	if err != nil {
		return w.Default.IsHidden(path)
	}
	attributes, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		return w.Default.IsHidden(path)
	}
	return attributes&windows.FILE_ATTRIBUTE_HIDDEN != 0
}

func (Windows) BaseName(p string) string {
	b := filepath.Base(p)
	if b == "." || b == string(os.PathSeparator) || b == "" {
		if vol := filepath.VolumeName(p); vol != "" {
			return vol + `\`
		}
	}
	return b
}

// Canonicalize turns a user input path into a scanning-safe, OS-correct path.
// Key behavior: "D:" -> "D:\" (drive root), strip \\?\ for logic, normalize slashes.
func (Windows) Canonicalize(p string) string {
	p = strings.TrimSpace(p)

	// Strip extended prefix
	if strings.HasPrefix(strings.ToUpper(p), `\\?\UNC\`) {
		p = `\\` + p[len(`\\?\UNC\`):]
	} else {
		p = strings.TrimPrefix(p, `\\?\`)
	}

	// Normalize slashes
	p = strings.ReplaceAll(p, "/", `\`)

	// Bare drive letter or drive designator -> root.
	if len(p) == 1 &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		p += `:\`
	} else if len(p) == 2 && p[1] == ':' &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		p += `\`
	}

	return filepath.Clean(p)
}

func (w Windows) IsMountRoot(p string) bool {
	// Compare against canonicalized input (no Abs; Abs would pick the drive's current dir)
	clean := w.Canonicalize(p)

	// Drive root like "D:\"
	if vol := filepath.VolumeName(clean); vol != "" {
		root := vol + `\`
		return strings.EqualFold(clean, root)
	}

	// UNC share root: "\\server\share" (two components)
	if strings.HasPrefix(clean, `\\`) {
		parts := strings.Split(clean, `\`)
		// ["", "", "server", "share"] or with trailing slash
		if len(parts) == 4 || (len(parts) == 5 && parts[4] == "") {
			return true
		}
	}
	return false
}

func (Windows) OpenInFileBrowser(p string) error {
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return exec.Command("explorer", "/select,", p).Run()
	}
	return exec.Command("explorer", p).Run()
}

func (Windows) DefaultStartPath() string {
	drv := os.Getenv("SystemDrive") // typically "C:"
	if drv == "" {
		drv = "C:"
	}
	p := drv + `\` // ensure root, avoids "D:" current-dir semantics
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p
	}
	// Fallback to C:\ even if SystemDrive was odd/missing
	return `C:\`
}

func (w Windows) IsLikelyNetworkFS(p string) bool {
	clean := w.Canonicalize(p)
	return isWindowsNetworkFS(clean, windowsVolumePath, windowsDriveType, &windowsNetworkRoots)
}

func init() { Impl = Windows{} }
