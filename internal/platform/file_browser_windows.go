//go:build windows

package platform

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shellBrowserDLL          = windows.NewLazySystemDLL("shell32.dll")
	parseDisplayName         = shellBrowserDLL.NewProc("SHParseDisplayName")
	openFolderAndSelectItems = shellBrowserDLL.NewProc("SHOpenFolderAndSelectItems")
	browserOleDLL            = windows.NewLazySystemDLL("ole32.dll")
	browserCoInitialize      = browserOleDLL.NewProc("CoInitializeEx")
	browserCoUninitialize    = browserOleDLL.NewProc("CoUninitialize")
	browserCoTaskMemFree     = browserOleDLL.NewProc("CoTaskMemFree")
)

func (w Windows) OpenInFileBrowser(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("open in file browser: %w", err)
	}
	// Explorer's process exit status does not describe whether it opened a window.
	// Ask the shell directly and retain real errors from the request instead.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := browserCoInitialize.Call(0, 2)     // COINIT_APARTMENTTHREADED
	if int32(hr) < 0 && uint32(hr) != 0x80010106 { // RPC_E_CHANGED_MODE: COM already initialized
		return fmt.Errorf("initialize file browser: HRESULT 0x%08x", uint32(hr))
	}
	if int32(hr) >= 0 {
		defer browserCoUninitialize.Call()
	}
	if info.IsDir() {
		return w.OpenPath(path)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var item uintptr
	hr, _, _ = parseDisplayName.Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&item)), 0, 0)
	if int32(hr) < 0 {
		return fmt.Errorf("locate file in browser: HRESULT 0x%08x", uint32(hr))
	}
	defer browserCoTaskMemFree.Call(item)
	// With zero child items, the shell opens the parent and selects this file.
	hr, _, _ = openFolderAndSelectItems.Call(item, 0, 0, 0)
	if int32(hr) < 0 {
		return fmt.Errorf("select file in browser: HRESULT 0x%08x", uint32(hr))
	}
	return nil
}
