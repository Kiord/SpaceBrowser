//go:build linux && cgo

package nativeinput

/*
#cgo pkg-config: gtk+-3.0
void spacebrowserStartMouseCapture(void);
*/
import "C"

import "sync"

var (
	auxiliaryMouseHandlerMu sync.RWMutex
	auxiliaryMouseHandler   func(int)
	startMouseCaptureOnce   sync.Once
)

func StartAuxiliaryMouseCapture(handler func(int)) {
	auxiliaryMouseHandlerMu.Lock()
	auxiliaryMouseHandler = handler
	auxiliaryMouseHandlerMu.Unlock()
	startMouseCaptureOnce.Do(func() { C.spacebrowserStartMouseCapture() })
}

//export spacebrowserAuxiliaryMouseButton
func spacebrowserAuxiliaryMouseButton(button C.uint) {
	auxiliaryMouseHandlerMu.RLock()
	handler := auxiliaryMouseHandler
	auxiliaryMouseHandlerMu.RUnlock()
	if handler != nil {
		go handler(int(button))
	}
}
