//go:build windows

package platform

import (
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fofSilent              = 0x0004
	fofNoConfirmation      = 0x0010
	fofNoErrorUI           = 0x0400
	fofNoConnectedElements = 0x2000
	fofxRecycleOnDelete    = 0x00080000
	fofxEarlyFailure       = 0x00100000
	fofxAddUndoRecord      = 0x20000000
)

var (
	recycleOleDLL      = windows.NewLazySystemDLL("ole32.dll")
	recycleInitialize  = recycleOleDLL.NewProc("CoInitializeEx")
	recycleUninit      = recycleOleDLL.NewProc("CoUninitialize")
	recycleCreate      = recycleOleDLL.NewProc("CoCreateInstance")
	recycleShellItem   = windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")
	clsidFileOperation = windows.GUID{Data1: 0x3ad05575, Data2: 0x8857, Data3: 0x4850, Data4: [8]byte{0x92, 0x77, 0x11, 0xb8, 0x5b, 0xdb, 0x8e, 0x09}}
	iidFileOperation   = windows.GUID{Data1: 0x947aab5f, Data2: 0x0a5c, Data3: 0x4c13, Data4: [8]byte{0xb4, 0xd6, 0x4b, 0xf7, 0x83, 0x6f, 0xc9, 0xf8}}
	iidShellItem       = windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
)

// IFileOperation requires an STA. Keep initialization, every interface call,
// and release on the same OS thread. If COM is unavailable, fail closed.
func (Windows) MoveToTrash(path string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := recycleInitialize.Call(0, 2) // COINIT_APARTMENTTHREADED
	if err := recycleHRESULT("initialize recycling", hr); err != nil {
		return err
	}
	defer recycleUninit.Call()

	var object *recycleCOMOperation
	hr, _, _ = recycleCreate.Call(
		uintptr(unsafe.Pointer(&clsidFileOperation)), 0, 1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidFileOperation)), uintptr(unsafe.Pointer(&object)),
	)
	if err := recycleHRESULT("create recycling operation", hr); err != nil {
		return err
	}
	operation := &nativeRecycleOperation{object: object, sink: newRecycleProgressSink()}
	defer operation.release()
	return recycleWindowsItem(path, operation)
}

type windowsRecycleOperation interface {
	setFlags(uint32) error
	deleteItem(string) error
	perform() error
	aborted() (bool, error)
}

func recycleWindowsItem(path string, operation windowsRecycleOperation) error {
	// Unlike SHFileOperation's best-effort FOF_ALLOWUNDO, RECYCLEONDELETE
	// requests recycling explicitly. The native progress sink also vetoes
	// permanent deletion before it starts: flags alone are not a guarantee.
	// EARLYFAILURE with NOERRORUI propagates failures instead of skipping them.
	// Never retry with permanent deletion or the legacy API.
	// https://learn.microsoft.com/windows/win32/api/shobjidl_core/nf-shobjidl_core-ifileoperation-setoperationflags
	flags := uint32(fofxRecycleOnDelete | fofxEarlyFailure | fofxAddUndoRecord |
		fofSilent | fofNoConfirmation | fofNoErrorUI | fofNoConnectedElements)
	if err := operation.setFlags(flags); err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("move to Recycle Bin requires an absolute path")
	}
	if err := operation.deleteItem(path); err != nil {
		return err
	}
	performErr := operation.perform()
	// The shell can return S_OK after cancellation or silently skipping an
	// item. Query this even when PerformOperations returns a failure HRESULT.
	aborted, statusErr := operation.aborted()
	if performErr != nil {
		return performErr
	}
	if statusErr != nil {
		return statusErr
	}
	if aborted {
		return fmt.Errorf("move to Recycle Bin: %w", ErrOperationCancelled)
	}
	return nil
}

// Slots follow IFileOperation's SDK declaration, including inherited IUnknown.
type recycleOperationVTable struct {
	queryInterface, addRef, release                                              uintptr
	advise, unadvise, setOperationFlags                                          uintptr
	setProgressMessage, setProgressDialog, setProperties, setOwnerWindow         uintptr
	applyPropertiesToItem, applyPropertiesToItems                                uintptr
	renameItem, renameItems, moveItem, moveItems, copyItem, copyItems            uintptr
	deleteItem, deleteItems, newItem, performOperations, getAnyOperationsAborted uintptr
}

type recycleCOMOperation struct {
	vtable *recycleOperationVTable
}

type nativeRecycleOperation struct {
	object *recycleCOMOperation
	sink   *recycleProgressSink
}

func (operation *nativeRecycleOperation) release() {
	syscall.SyscallN(operation.object.vtable.release, uintptr(unsafe.Pointer(operation.object)))
	runtime.KeepAlive(operation.sink)
}

func (operation *nativeRecycleOperation) setFlags(flags uint32) error {
	hr, _, _ := syscall.SyscallN(operation.object.vtable.setOperationFlags, uintptr(unsafe.Pointer(operation.object)), uintptr(flags))
	return recycleHRESULT("set recycle-only flags", hr)
}

func (operation *nativeRecycleOperation) deleteItem(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("prepare recycling path: %w", err)
	}
	var item *struct{ vtable *[3]uintptr } // IUnknown slots of IShellItem
	hr, _, _ := recycleShellItem.Call(uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
	if err := recycleHRESULT("locate recycling target", hr); err != nil {
		return err
	}
	defer syscall.SyscallN(item.vtable[2], uintptr(unsafe.Pointer(item)))
	hr, _, _ = syscall.SyscallN(operation.object.vtable.deleteItem, uintptr(unsafe.Pointer(operation.object)), uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(operation.sink)))
	return recycleHRESULT("queue recycling target", hr)
}

func (operation *nativeRecycleOperation) perform() error {
	hr, _, _ := syscall.SyscallN(operation.object.vtable.performOperations, uintptr(unsafe.Pointer(operation.object)))
	if operation.sink.err != nil {
		return operation.sink.err
	}
	if err := recycleHRESULT("move to Recycle Bin", hr); err != nil {
		return err
	}
	if !operation.sink.recycled {
		return fmt.Errorf("Windows did not confirm recycling the selected item")
	}
	return nil
}

func (operation *nativeRecycleOperation) aborted() (bool, error) {
	var aborted int32 // BOOL is 32 bits on both 32- and 64-bit Windows.
	hr, _, _ := syscall.SyscallN(operation.object.vtable.getAnyOperationsAborted, uintptr(unsafe.Pointer(operation.object)), uintptr(unsafe.Pointer(&aborted)))
	return aborted != 0, recycleHRESULT("check recycling result", hr)
}

func recycleHRESULT(action string, hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s failed (HRESULT 0x%08X)", action, uint32(hr))
	}
	return nil
}
