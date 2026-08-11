//go:build windows

package platform

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const seeMaskInvokeIDList = 0x0000000C

type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Window     uintptr
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   uintptr
	IDList     uintptr
	Class      *uint16
	ClassKey   uintptr
	HotKey     uint32
	Icon       uintptr
	Process    windows.Handle
}

var shellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func (Windows) ShowProperties(path string) error {
	verb, err := windows.UTF16PtrFromString("properties")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	info := shellExecuteInfo{
		Mask: seeMaskInvokeIDList,
		Verb: verb,
		File: file,
		Show: swShowNormal,
	}
	info.Size = uint32(unsafe.Sizeof(info))
	result, _, callErr := shellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return fmt.Errorf("show filesystem properties: %w", callErr)
	}
	return nil
}
