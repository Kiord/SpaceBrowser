//go:build windows

package platform

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsDirectoryBufferSize = 64 * 1024
	windowsReparseTagDedup     = 0x80000013
	windowsReparseTagAFUnix    = 0x80000023
)

type windowsDirectoryRecordLayout struct {
	name             string
	class            uint32
	restart          uint32
	nameOffset       int
	reparseTagOffset int
	idOffset         int
	idSize           int
}

// These headers mirror the fixed portion of the corresponding Windows SDK
// structures. FileName is the first element of a variable-length UTF-16 name.
// Offsets used by the parser are derived from these declarations rather than
// repeated as unexplained byte constants.
type windowsFileIDExtdDirectoryHeader struct {
	NextEntryOffset uint32
	FileIndex       uint32
	CreationTime    int64
	LastAccessTime  int64
	LastWriteTime   int64
	ChangeTime      int64
	EndOfFile       int64
	AllocationSize  int64
	FileAttributes  uint32
	FileNameLength  uint32
	EaSize          uint32
	ReparsePointTag uint32
	FileID          [16]byte
	FileName        [1]uint16
}

type windowsFileIDBothDirectoryHeader struct {
	NextEntryOffset uint32
	FileIndex       uint32
	CreationTime    int64
	LastAccessTime  int64
	LastWriteTime   int64
	ChangeTime      int64
	EndOfFile       int64
	AllocationSize  int64
	FileAttributes  uint32
	FileNameLength  uint32
	EaSize          uint32
	ShortNameLength byte
	Reserved        byte
	ShortName       [12]uint16
	FileID          uint64
	FileName        [1]uint16
}

var windowsDirectoryLayoutByVolume sync.Map

var windowsDirectoryLayouts = [...]windowsDirectoryRecordLayout{
	{
		name:             "128-bit file ID layout",
		class:            windows.FileIdExtdDirectoryInfo,
		restart:          windows.FileIdExtdDirectoryRestartInfo,
		nameOffset:       int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.FileName)),
		reparseTagOffset: int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.ReparsePointTag)),
		idOffset:         int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.FileID)),
		idSize:           16,
	},
	{
		name:             "64-bit file ID layout",
		class:            windows.FileIdBothDirectoryInfo,
		restart:          windows.FileIdBothDirectoryRestartInfo,
		nameOffset:       int(unsafe.Offsetof(windowsFileIDBothDirectoryHeader{}.FileName)),
		reparseTagOffset: int(unsafe.Offsetof(windowsFileIDBothDirectoryHeader{}.EaSize)), // EaSize contains the tag for reparse points.
		idOffset:         int(unsafe.Offsetof(windowsFileIDBothDirectoryHeader{}.FileID)),
		idSize:           8,
	},
}

var windowsCommonDirectoryOffsets = struct {
	nextEntry      int
	creation       int
	lastAccess     int
	lastWrite      int
	endOfFile      int
	allocation     int
	attributes     int
	fileNameLength int
}{
	nextEntry:      int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.NextEntryOffset)),
	creation:       int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.CreationTime)),
	lastAccess:     int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.LastAccessTime)),
	lastWrite:      int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.LastWriteTime)),
	endOfFile:      int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.EndOfFile)),
	allocation:     int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.AllocationSize)),
	attributes:     int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.FileAttributes)),
	fileNameLength: int(unsafe.Offsetof(windowsFileIDExtdDirectoryHeader{}.FileNameLength)),
}

type windowsBatchFileInfo struct {
	name       string
	size       int64
	mode       fs.FileMode
	modTime    time.Time
	attributes uint32
	creation   syscall.Filetime
	access     syscall.Filetime
	write      syscall.Filetime
}

func (fi windowsBatchFileInfo) Name() string       { return fi.name }
func (fi windowsBatchFileInfo) Size() int64        { return fi.size }
func (fi windowsBatchFileInfo) Mode() fs.FileMode  { return fi.mode }
func (fi windowsBatchFileInfo) ModTime() time.Time { return fi.modTime }
func (fi windowsBatchFileInfo) IsDir() bool        { return fi.mode.IsDir() }
func (fi windowsBatchFileInfo) Sys() any {
	return &syscall.Win32FileAttributeData{
		FileAttributes: fi.attributes,
		CreationTime:   fi.creation,
		LastAccessTime: fi.access,
		LastWriteTime:  fi.write,
		FileSizeHigh:   uint32(uint64(fi.size) >> 32),
		FileSizeLow:    uint32(fi.size),
	}
}

type windowsBatchDirEntry struct{ info windowsBatchFileInfo }

func (de windowsBatchDirEntry) Name() string               { return de.info.Name() }
func (de windowsBatchDirEntry) IsDir() bool                { return de.info.IsDir() }
func (de windowsBatchDirEntry) Type() fs.FileMode          { return de.info.Mode().Type() }
func (de windowsBatchDirEntry) Info() (os.FileInfo, error) { return de.info, nil }

func windowsMode(attributes, reparseTag uint32) fs.FileMode {
	var mode fs.FileMode
	if attributes&windows.FILE_ATTRIBUTE_READONLY != 0 {
		mode = 0o444
	} else {
		mode = 0o666
	}

	nameSurrogate := attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && reparseTag&0x20000000 != 0
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 && !nameSurrogate {
		mode |= fs.ModeDir | 0o111
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		switch reparseTag {
		case windows.IO_REPARSE_TAG_SYMLINK:
			mode |= fs.ModeSymlink
		case windowsReparseTagAFUnix:
			mode |= fs.ModeSocket
		case windowsReparseTagDedup:
			// Deduplicated files still behave as regular files.
		default:
			mode |= fs.ModeIrregular
		}
	}
	return mode
}

func windowsUTF16String(buffer []byte, offset, byteLength int) (string, bool) {
	if byteLength < 0 || byteLength%2 != 0 || offset < 0 || offset > len(buffer)-byteLength {
		return "", false
	}
	if byteLength == 0 {
		return "", true
	}
	name := unsafe.Slice((*uint16)(unsafe.Pointer(&buffer[offset])), byteLength/2)
	return windows.UTF16ToString(name), true
}

func parseWindowsDirectoryBuffer(buffer []byte, layout windowsDirectoryRecordLayout, volume uint64, hasVolume bool, parent string) ([]DirectoryEntry, error) {
	entries := make([]DirectoryEntry, 0, 128)
	for offset := 0; ; {
		if offset < 0 || offset > len(buffer)-layout.nameOffset {
			return nil, fmt.Errorf("invalid Windows directory record offset %d", offset)
		}
		record := buffer[offset:]
		nextOffset := windowsCommonDirectoryOffsets.nextEntry
		nameLengthOffset := windowsCommonDirectoryOffsets.fileNameLength
		next := int(binary.LittleEndian.Uint32(record[nextOffset : nextOffset+4]))
		nameLength := int(binary.LittleEndian.Uint32(record[nameLengthOffset : nameLengthOffset+4]))
		name, ok := windowsUTF16String(record, layout.nameOffset, nameLength)
		if !ok {
			return nil, fmt.Errorf("invalid Windows directory record name length %d", nameLength)
		}
		if name != "." && name != ".." {
			attributesOffset := windowsCommonDirectoryOffsets.attributes
			endOfFileOffset := windowsCommonDirectoryOffsets.endOfFile
			allocationOffset := windowsCommonDirectoryOffsets.allocation
			attributes := binary.LittleEndian.Uint32(record[attributesOffset : attributesOffset+4])
			reparseTag := binary.LittleEndian.Uint32(record[layout.reparseTagOffset : layout.reparseTagOffset+4])
			logicalSize := binary.LittleEndian.Uint64(record[endOfFileOffset : endOfFileOffset+8])
			allocationSize := binary.LittleEndian.Uint64(record[allocationOffset : allocationOffset+8])
			creation := *(*syscall.Filetime)(unsafe.Pointer(&record[windowsCommonDirectoryOffsets.creation]))
			access := *(*syscall.Filetime)(unsafe.Pointer(&record[windowsCommonDirectoryOffsets.lastAccess]))
			write := *(*syscall.Filetime)(unsafe.Pointer(&record[windowsCommonDirectoryOffsets.lastWrite]))
			if logicalSize > math.MaxInt64 || allocationSize > math.MaxInt64 {
				return nil, fmt.Errorf("Windows directory record size overflows int64")
			}

			info := windowsBatchFileInfo{
				name:       name,
				size:       int64(logicalSize),
				mode:       windowsMode(attributes, reparseTag),
				modTime:    time.Unix(0, write.Nanoseconds()),
				attributes: attributes,
				creation:   creation,
				access:     access,
				write:      write,
			}
			usage := FileUsage{
				AllocatedSize: int64(allocationSize),
				Identity:      FileIdentity{Volume: volume},
			}
			if layout.idSize == 16 {
				usage.Identity.Low = binary.LittleEndian.Uint64(record[layout.idOffset : layout.idOffset+8])
				usage.Identity.High = binary.LittleEndian.Uint64(record[layout.idOffset+8 : layout.idOffset+16])
			} else {
				usage.Identity.Low = binary.LittleEndian.Uint64(record[layout.idOffset : layout.idOffset+8])
			}
			// Extended IDs may legally be all zero when the filesystem does not
			// support them. Such an ID must not participate in hard-link deduplication.
			usage.HasIdentity = hasVolume && (usage.Identity.Low != 0 || usage.Identity.High != 0)
			usage.IdentityNeedsConfirmation = usage.HasIdentity && layout.idSize == 8

			if attributes&(windows.FILE_ATTRIBUTE_COMPRESSED|windows.FILE_ATTRIBUTE_SPARSE_FILE) != 0 {
				if pathPtr, err := windowsPathPtr(parent + `\` + name); err == nil {
					if allocated, ok := windowsAllocatedSize(pathPtr); ok {
						usage.AllocatedSize = allocated
					} else {
						usage.MetadataError = fmt.Errorf("read compressed or sparse allocation size")
					}
				} else {
					usage.MetadataError = fmt.Errorf("prepare Windows metadata path: %w", err)
				}
			}

			entries = append(entries, DirectoryEntry{
				DirEntry:  windowsBatchDirEntry{info: info},
				Usage:     usage,
				HasUsage:  true,
				Hidden:    attributes&windows.FILE_ATTRIBUTE_HIDDEN != 0,
				HasHidden: true,
			})
		}

		if next == 0 {
			break
		}
		if next < layout.nameOffset || next > len(record)-layout.nameOffset {
			return nil, fmt.Errorf("invalid Windows directory record length %d", next)
		}
		offset += next
	}
	return entries, nil
}

func readWindowsDirectory(path string, layout windowsDirectoryRecordLayout) ([]DirectoryEntry, error) {
	pathPtr, err := windowsPathPtr(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)

	var volumeSerial uint64
	var directoryID windowsFileIDInfo
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileIdInfo,
		(*byte)(unsafe.Pointer(&directoryID)),
		uint32(unsafe.Sizeof(directoryID)),
	); err == nil {
		volumeSerial = directoryID.VolumeSerialNumber
	} else {
		var legacyVolumeSerial uint32
		if err := windows.GetVolumeInformationByHandle(handle, nil, 0, &legacyVolumeSerial, nil, nil, nil, 0); err != nil {
			return nil, err
		}
		volumeSerial = uint64(legacyVolumeSerial)
	}

	buffer := make([]byte, windowsDirectoryBufferSize)
	entries := make([]DirectoryEntry, 0, 128)
	class := layout.restart
	for {
		err := windows.GetFileInformationByHandleEx(handle, class, &buffer[0], uint32(len(buffer)))
		if err != nil {
			if err == windows.ERROR_NO_MORE_FILES || err == windows.ERROR_FILE_NOT_FOUND {
				return entries, nil
			}
			return nil, err
		}
		class = layout.class
		batch, err := parseWindowsDirectoryBuffer(buffer, layout, volumeSerial, true, path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, batch...)
	}
}

func windowsDirectoryVolumeKey(path string) string {
	if volume := filepath.VolumeName(path); volume != "" {
		return strings.ToLower(filepath.Clean(volume))
	}
	return strings.ToLower(filepath.Clean(path))
}

func windowsLayoutOrder(volumeKey string) []int {
	order := make([]int, 0, len(windowsDirectoryLayouts))
	if cached, ok := windowsDirectoryLayoutByVolume.Load(volumeKey); ok {
		index := cached.(int)
		if index >= 0 && index < len(windowsDirectoryLayouts) {
			order = append(order, index)
		}
	}
	for index := range windowsDirectoryLayouts {
		if len(order) == 0 || order[0] != index {
			order = append(order, index)
		}
	}
	return order
}

func allWindowsDirectoryIdentitiesMissing(entries []DirectoryEntry) bool {
	if len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		if entry.Usage.HasIdentity {
			return false
		}
	}
	return true
}

// ReadDir uses the rich, batched Windows directory records so scanning ordinary
// files does not need to open a handle for every entry. Older or unusual file
// systems fall back to the portable implementation.
func (w Windows) ReadDir(path string) ([]DirectoryEntry, error) {
	entries, _, err := w.ReadDirWithDiagnostics(path)
	return entries, err
}

func (w Windows) ReadDirWithDiagnostics(path string) ([]DirectoryEntry, *DirectoryReadDiagnostic, error) {
	volumeKey := windowsDirectoryVolumeKey(path)
	var usableEntries []DirectoryEntry
	var nativeErrors []error
	for _, index := range windowsLayoutOrder(volumeKey) {
		layout := windowsDirectoryLayouts[index]
		entries, readErr := readWindowsDirectory(path, layout)
		if readErr != nil {
			nativeErrors = append(nativeErrors, fmt.Errorf("%s: %w", layout.name, readErr))
			continue
		}
		usableEntries = entries
		// A zero extended ID means that the filesystem does not support the
		// 128-bit form. Retry with the 64-bit format instead of treating every
		// such entry as the same file.
		if layout.idSize == 16 && allWindowsDirectoryIdentitiesMissing(entries) {
			continue
		}
		windowsDirectoryLayoutByVolume.Store(volumeKey, index)
		return entries, nil, nil
	}
	if usableEntries != nil {
		return usableEntries, nil, nil
	}
	entries, err := w.Default.ReadDir(path)
	if err != nil {
		return nil, nil, err
	}
	nativeCause := errors.Join(nativeErrors...)
	if nativeCause != nil {
		nativeCause = fmt.Errorf("native enumeration failed: %s", strings.ReplaceAll(nativeCause.Error(), "\n", "; "))
	}
	return entries, &DirectoryReadDiagnostic{
		PortableFallback: true,
		Cause:            nativeCause,
	}, nil
}
