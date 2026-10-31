//go:build windows

package platform

import (
	"errors"
	"fmt"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	tsfDeleteRecycleIfPossible = 0x80
	recycleAbort               = 0x80004004 // E_ABORT
)

var (
	errRecycleUnavailable  = errors.New("the selected item cannot be recycled; permanent deletion was blocked")
	iidRecycleUnknown      = windows.GUID{Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidRecycleProgressSink = windows.GUID{Data1: 0x04b0f1a7, Data2: 0x9490, Data3: 0x44bc, Data4: [8]byte{0x96, 0xe1, 0x42, 0x96, 0xa3, 0x12, 0x52, 0xe2}}
)

// IFileOperationProgressSink callbacks run synchronously on the operation's
// STA. Go owns this object until the operation releases its references.
type recycleProgressSink struct {
	vtable   *[19]uintptr
	refs     atomic.Uint32
	err      error
	recycled bool
}

func newRecycleProgressSink() *recycleProgressSink {
	sink := &recycleProgressSink{vtable: &recycleSinkVTable}
	sink.refs.Store(1)
	return sink
}

func (sink *recycleProgressSink) preDelete(flags uint32) uintptr {
	if sink.err != nil {
		return recycleAbort
	}
	// The shell clears this flag when it switches to permanent deletion,
	// including targets without a bin and items that cannot fit in the bin.
	// Returning a failure cancels deletion before any content is removed.
	// https://learn.microsoft.com/windows/win32/api/shobjidl_core/nf-shobjidl_core-ifileoperationprogresssink-predeleteitem
	if flags&tsfDeleteRecycleIfPossible == 0 {
		sink.err = errRecycleUnavailable
		return recycleAbort
	}
	return 0
}

func (sink *recycleProgressSink) postDelete(hr, newItem uintptr) uintptr {
	if sink.err != nil {
		return recycleAbort
	}
	if err := recycleHRESULT("recycle selected item", hr); err != nil {
		sink.err = err
		return recycleAbort
	}
	if newItem == 0 {
		sink.err = fmt.Errorf("Windows did not return a recoverable Recycle Bin item")
		return recycleAbort
	}
	sink.recycled = true
	return 0
}

// Keep every SDK slot and its argument count, including unused operations,
// so the stdcall callbacks also work on 32-bit Windows.
var recycleSinkVTable = [19]uintptr{
	syscall.NewCallback(func(sink *recycleProgressSink, iid *windows.GUID, out **recycleProgressSink) uintptr {
		*out = nil
		if *iid != iidRecycleUnknown && *iid != iidRecycleProgressSink {
			return 0x80004002 // E_NOINTERFACE
		}
		*out = sink
		sink.refs.Add(1)
		return 0
	}),
	syscall.NewCallback(func(sink *recycleProgressSink) uintptr { return uintptr(sink.refs.Add(1)) }),
	syscall.NewCallback(func(sink *recycleProgressSink) uintptr {
		return uintptr(sink.refs.Add(^uint32(0)))
	}),
	syscall.NewCallback(func(this uintptr) uintptr { return 0 }),                                              // StartOperations
	syscall.NewCallback(func(this, hr uintptr) uintptr { return 0 }),                                          // FinishOperations
	syscall.NewCallback(func(this, flags, item, name uintptr) uintptr { return recycleAbort }),                // PreRenameItem
	syscall.NewCallback(func(this, flags, item, name, hr, newItem uintptr) uintptr { return 0 }),              // PostRenameItem
	syscall.NewCallback(func(this, flags, item, destination, name uintptr) uintptr { return recycleAbort }),   // PreMoveItem
	syscall.NewCallback(func(this, flags, item, destination, name, hr, newItem uintptr) uintptr { return 0 }), // PostMoveItem
	syscall.NewCallback(func(this, flags, item, destination, name uintptr) uintptr { return recycleAbort }),   // PreCopyItem
	syscall.NewCallback(func(this, flags, item, destination, name, hr, newItem uintptr) uintptr { return 0 }), // PostCopyItem
	syscall.NewCallback(func(sink *recycleProgressSink, flags, item uintptr) uintptr {
		return sink.preDelete(uint32(flags))
	}),
	syscall.NewCallback(func(sink *recycleProgressSink, flags, item, hr, newItem uintptr) uintptr {
		return sink.postDelete(hr, newItem)
	}),
	syscall.NewCallback(func(this, flags, destination, name uintptr) uintptr { return recycleAbort }),                         // PreNewItem
	syscall.NewCallback(func(this, flags, destination, name, template, attributes, hr, newItem uintptr) uintptr { return 0 }), // PostNewItem
	syscall.NewCallback(func(this, total, done uintptr) uintptr { return 0 }),                                                 // UpdateProgress
	syscall.NewCallback(func(this uintptr) uintptr { return 0 }),                                                              // ResetTimer
	syscall.NewCallback(func(this uintptr) uintptr { return 0 }),                                                              // PauseTimer
	syscall.NewCallback(func(this uintptr) uintptr { return 0 }),                                                              // ResumeTimer
}
