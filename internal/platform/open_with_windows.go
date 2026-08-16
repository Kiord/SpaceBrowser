//go:build windows

package platform

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	assocStrExecutable      = 2
	assocStrFriendlyAppName = 4
	oaifAllowRegistration   = 0x00000001
	oaifExecute             = 0x00000004
)

type openAsInfo struct {
	File  *uint16
	Class *uint16
	Flags uint32
}

var (
	assocQueryStringW = windows.NewLazySystemDLL("shlwapi.dll").NewProc("AssocQueryStringW")
	shOpenWithDialog  = windows.NewLazySystemDLL("shell32.dll").NewProc("SHOpenWithDialog")
)

func queryAssociation(extension string, valueType uintptr) (string, error) {
	association, err := windows.UTF16PtrFromString(extension)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, 1024)
	length := uint32(len(buffer))
	result, _, _ := assocQueryStringW.Call(
		0,
		valueType,
		uintptr(unsafe.Pointer(association)),
		0,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&length)),
	)
	if result != 0 {
		return "", fmt.Errorf("association lookup failed (HRESULT 0x%08x)", uint32(result))
	}
	return windows.UTF16ToString(buffer), nil
}

func (Windows) DefaultApplicationName(path string) (string, error) {
	extension := filepath.Ext(path)
	if extension == "" {
		return "", fmt.Errorf("this file has no extension association")
	}
	if name, err := queryAssociation(extension, assocStrFriendlyAppName); err == nil && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name), nil
	}
	executable, err := queryAssociation(extension, assocStrExecutable)
	if err != nil {
		return "", err
	}
	name := strings.TrimSuffix(filepath.Base(executable), filepath.Ext(executable))
	if name == "" {
		return "", fmt.Errorf("no default application is registered for %s files", extension)
	}
	return name, nil
}

func (Windows) OpenWith(_ context.Context, path string) error {
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	info := openAsInfo{
		File:  file,
		Flags: oaifAllowRegistration | oaifExecute,
	}
	result, _, _ := shOpenWithDialog.Call(0, uintptr(unsafe.Pointer(&info)))
	if result != 0 {
		return fmt.Errorf("open application selector failed (HRESULT 0x%08x)", uint32(result))
	}
	return nil
}
