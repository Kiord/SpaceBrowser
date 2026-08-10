//go:build windows

package platform

import (
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	foDelete          = 3
	fofNoConfirmation = 0x0010
	fofAllowUndo      = 0x0040
	fofNoErrorUI      = 0x0400
)

type shFileOpStruct struct {
	wnd               uintptr
	function          uint32
	from              *uint16
	to                *uint16
	flags             uint16
	operationsAborted int32
	nameMappings      uintptr
	progressTitle     *uint16
}

var shFileOperationW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")

func (Windows) MoveToTrash(p string) error {
	// SHFileOperation expects a double-NUL-terminated path list.
	from := utf16.Encode(append([]rune(p), 0, 0))
	op := shFileOpStruct{
		function: foDelete,
		from:     &from[0],
		flags:    fofNoConfirmation | fofAllowUndo | fofNoErrorUI,
	}
	result, _, _ := shFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if result != 0 {
		return fmt.Errorf("move to Recycle Bin failed with code %d", result)
	}
	if op.operationsAborted != 0 {
		return fmt.Errorf("move to Recycle Bin was cancelled")
	}
	return nil
}
