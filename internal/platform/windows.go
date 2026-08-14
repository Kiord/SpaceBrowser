//go:build windows

package platform

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Windows struct{ Default }

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

func (w Windows) UsageFor(path string, fi os.FileInfo) FileUsage {
	usage := w.Default.UsageFor(path, fi)
	pathPtr, err := windows.UTF16PtrFromString(path)
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
	p = strings.TrimPrefix(p, `\\?\`)

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
	return strings.HasPrefix(clean, `\\`)
}

func init() { Impl = Windows{} }
