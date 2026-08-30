//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework CoreServices
#include <stdlib.h>
#include "tree_watcher_darwin.h"
*/
import "C"

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	fseventMustScanSubDirs = uint32(0x00000001)
	fseventUserDropped     = uint32(0x00000002)
	fseventKernelDropped   = uint32(0x00000004)
	fseventIDsWrapped      = uint32(0x00000008)
	fseventRootChanged     = uint32(0x00000020)
)

var (
	darwinWatcherSequence atomic.Uint64
	darwinWatchers        sync.Map
)

type darwinTreeWatcher struct {
	handle    uint64
	native    *C.SBTreeWatcher
	onChange  func(string)
	onSubtree func(string)
	onFailure func(error)
	once      sync.Once
	closed    atomic.Bool
	failed    atomic.Bool
}

func startTreeWatcher(root string, _ []string, onChange func(string), onSubtree func(string), onFailure func(error)) (treeWatcher, error) {
	watcher := &darwinTreeWatcher{
		handle: darwinWatcherSequence.Add(1), onChange: onChange, onSubtree: onSubtree, onFailure: onFailure,
	}
	darwinWatchers.Store(watcher.handle, watcher)
	cRoot := C.CString(filepath.Clean(root))
	defer C.free(unsafe.Pointer(cRoot))
	watcher.native = C.SBTreeWatcherStart(cRoot, C.uintptr_t(watcher.handle))
	if watcher.native == nil {
		darwinWatchers.Delete(watcher.handle)
		return nil, errors.New("start recursive FSEvents stream")
	}
	return watcher, nil
}

func (watcher *darwinTreeWatcher) AddDirectory(string) error {
	if watcher.closed.Load() {
		return errors.New("filesystem watcher is closed")
	}
	// FSEvents observes the complete hierarchy from the scan root.
	return nil
}

func (watcher *darwinTreeWatcher) Close() error {
	watcher.once.Do(func() {
		watcher.closed.Store(true)
		darwinWatchers.Delete(watcher.handle)
		C.SBTreeWatcherStop(watcher.native)
		watcher.native = nil
	})
	return nil
}

//export goTreeWatcherEvent
func goTreeWatcherEvent(handle C.uintptr_t, path *C.char, flags C.uint32_t) {
	value, ok := darwinWatchers.Load(uint64(handle))
	if !ok {
		return
	}
	watcher := value.(*darwinTreeWatcher)
	if watcher.closed.Load() || watcher.failed.Load() {
		return
	}
	eventFlags := uint32(flags)
	if eventFlags&(fseventUserDropped|fseventKernelDropped|fseventIDsWrapped) != 0 {
		if watcher.failed.CompareAndSwap(false, true) {
			watcher.onFailure(errors.New("FSEvents dropped filesystem changes"))
		}
		return
	}
	if eventFlags&fseventRootChanged != 0 {
		if watcher.failed.CompareAndSwap(false, true) {
			watcher.onFailure(errors.New("FSEvents scan root changed"))
		}
		return
	}
	if path == nil {
		if watcher.failed.CompareAndSwap(false, true) {
			watcher.onFailure(errors.New("FSEvents returned an empty path"))
		}
		return
	}
	changed := filepath.Clean(C.GoString(path))
	if changed == "" || changed == "." {
		if watcher.failed.CompareAndSwap(false, true) {
			watcher.onFailure(errors.New("FSEvents returned an invalid path"))
		}
		return
	}
	if eventFlags&fseventMustScanSubDirs != 0 {
		watcher.onSubtree(changed)
		return
	}
	watcher.onChange(changed)
}
