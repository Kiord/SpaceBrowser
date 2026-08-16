//go:build darwin && cgo

package fileicon

/*
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>
#include "backend_darwin.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type darwinBackend struct{}

func newPlatformBackend() backend { return darwinBackend{} }

func (darwinBackend) Lookup(path string, _ bool) (Icon, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var length C.size_t
	var errorMessage *C.char
	data := C.sb_file_icon_png(cPath, &length, &errorMessage)
	if errorMessage != nil {
		defer C.sb_icon_free(unsafe.Pointer(errorMessage))
	}
	if data == nil {
		if errorMessage != nil {
			return Icon{}, fmt.Errorf("get macOS file icon: %s", C.GoString(errorMessage))
		}
		return Icon{}, ErrUnavailable
	}
	defer C.sb_icon_free(unsafe.Pointer(data))
	if length == 0 {
		return Icon{}, ErrUnavailable
	}
	return Icon{Data: C.GoBytes(unsafe.Pointer(data), C.int(length)), MediaType: "image/png"}, nil
}
