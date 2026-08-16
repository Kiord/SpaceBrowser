//go:build windows

package platform

import (
	"fmt"
	"syscall"
	"unsafe"
)

const swShowNormal = 1

var procShellExecuteW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")

func (Windows) OpenPath(path string) error {
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	result, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0,
		0,
		swShowNormal,
	)
	if result <= 32 {
		return fmt.Errorf("open path failed (ShellExecute code %d): %v", result, callErr)
	}
	return nil
}
