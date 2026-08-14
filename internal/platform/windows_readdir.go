//go:build windows

package platform

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"math"
	"os"
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
	class            uint32
	restart          uint32
	nameOffset       int
	reparseTagOffset int
	idOffset         int
	idSize           int
}

var windowsDirectoryLayouts = [...]windowsDirectoryRecordLayout{
	{
		class:            windows.FileIdExtdDirectoryInfo,
		restart:          windows.FileIdExtdDirectoryRestartInfo,
		nameOffset:       88,
		reparseTagOffset: 68,
		idOffset:         72,
		idSize:           16,
	},
	{
		class:            windows.FileIdBothDirectoryInfo,
		restart:          windows.FileIdBothDirectoryRestartInfo,
		nameOffset:       104,
		reparseTagOffset: 64, // EaSize contains the tag for reparse points.
		idOffset:         96,
		idSize:           8,
	},
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
		next := int(binary.LittleEndian.Uint32(record[0:4]))
		nameLength := int(binary.LittleEndian.Uint32(record[60:64]))
		name, ok := windowsUTF16String(record, layout.nameOffset, nameLength)
		if !ok {
			return nil, fmt.Errorf("invalid Windows directory record name length %d", nameLength)
		}
		if name != "." && name != ".." {
			attributes := binary.LittleEndian.Uint32(record[56:60])
			reparseTag := binary.LittleEndian.Uint32(record[layout.reparseTagOffset : layout.reparseTagOffset+4])
			logicalSize := binary.LittleEndian.Uint64(record[40:48])
			allocationSize := binary.LittleEndian.Uint64(record[48:56])
			creation := *(*syscall.Filetime)(unsafe.Pointer(&record[8]))
			access := *(*syscall.Filetime)(unsafe.Pointer(&record[16]))
			write := *(*syscall.Filetime)(unsafe.Pointer(&record[24]))
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
				HasIdentity:   hasVolume,
				LinkCount:     1,
			}
			if layout.idSize == 16 {
				usage.Identity.Low = binary.LittleEndian.Uint64(record[layout.idOffset : layout.idOffset+8])
				usage.Identity.High = binary.LittleEndian.Uint64(record[layout.idOffset+8 : layout.idOffset+16])
			} else {
				usage.Identity.Low = binary.LittleEndian.Uint64(record[layout.idOffset : layout.idOffset+8])
			}

			if attributes&(windows.FILE_ATTRIBUTE_COMPRESSED|windows.FILE_ATTRIBUTE_SPARSE_FILE) != 0 {
				if pathPtr, err := windowsPathPtr(parent + `\` + name); err == nil {
					if allocated, ok := windowsAllocatedSize(pathPtr); ok {
						usage.AllocatedSize = allocated
					}
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

	var volumeSerial uint32
	volumeErr := windows.GetVolumeInformationByHandle(handle, nil, 0, &volumeSerial, nil, nil, nil, 0)
	if volumeErr != nil {
		return nil, volumeErr
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
		batch, err := parseWindowsDirectoryBuffer(buffer, layout, uint64(volumeSerial), true, path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, batch...)
	}
}

// ReadDir uses the rich, batched Windows directory records so scanning ordinary
// files does not need to open a handle for every entry. Older or unusual file
// systems fall back to the portable implementation.
func (w Windows) ReadDir(path string) ([]DirectoryEntry, error) {
	for _, layout := range windowsDirectoryLayouts {
		if entries, err := readWindowsDirectory(path, layout); err == nil {
			return entries, nil
		}
	}
	return w.Default.ReadDir(path)
}
