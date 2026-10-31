//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	shEmptyNoConfirm  = 0x0001
	shEmptyNoProgress = 0x0002
	shEmptyNoSound    = 0x0004
)

var shEmptyRecycleBinW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHEmptyRecycleBinW")

func (w Windows) IsTrashRoot(p string) bool {
	clean := w.Canonicalize(p)
	if !strings.EqualFold(filepath.Base(clean), "$Recycle.Bin") {
		return false
	}
	volume := filepath.VolumeName(clean)
	return len(volume) == 2 && volume[1] == ':' && strings.EqualFold(filepath.Clean(filepath.Dir(clean)), filepath.Clean(volume+`\`))
}

func (w Windows) IsInTrash(p string) bool {
	clean := w.Canonicalize(p)
	volume := filepath.VolumeName(clean)
	if len(volume) != 2 || volume[1] != ':' {
		return false
	}
	trashRoot := filepath.Clean(volume + `\$Recycle.Bin`)
	if strings.EqualFold(clean, trashRoot) {
		return true
	}
	return len(clean) > len(trashRoot) && strings.EqualFold(clean[:len(trashRoot)], trashRoot) && os.IsPathSeparator(clean[len(trashRoot)])
}

func (w Windows) EmptyTrash(p string) error {
	if !w.IsTrashRoot(p) {
		return fmt.Errorf("the selected folder is not a Recycle Bin root")
	}
	volumeRoot := filepath.VolumeName(w.Canonicalize(p)) + `\`
	root, err := windows.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return fmt.Errorf("prepare Recycle Bin volume: %w", err)
	}
	result, _, _ := shEmptyRecycleBinW.Call(
		0,
		uintptr(unsafe.Pointer(root)),
		shEmptyNoConfirm|shEmptyNoProgress|shEmptyNoSound,
	)
	if int32(result) < 0 {
		return fmt.Errorf("empty Recycle Bin failed with HRESULT 0x%08X", uint32(result))
	}
	return nil
}
