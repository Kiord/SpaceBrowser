//go:build windows

package platform

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	shgfiIcon       = 0x000000100
	dibRGBColors    = 0
	biRGB           = 0
	swShowNormal    = 1
	sOK             = 0
	sFalse          = 1
	rpcEChangedMode = 0x80010106
)

var (
	shell32                = syscall.NewLazyDLL("shell32.dll")
	user32                 = syscall.NewLazyDLL("user32.dll")
	gdi32                  = syscall.NewLazyDLL("gdi32.dll")
	ole32                  = syscall.NewLazyDLL("ole32.dll")
	procSHGetFileInfoW     = shell32.NewProc("SHGetFileInfoW")
	procShellExecuteW      = shell32.NewProc("ShellExecuteW")
	procDestroyIcon        = user32.NewProc("DestroyIcon")
	procGetIconInfo        = user32.NewProc("GetIconInfo")
	procGetObjectW         = gdi32.NewProc("GetObjectW")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procGetDIBits          = gdi32.NewProc("GetDIBits")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procCoInitializeEx     = ole32.NewProc("CoInitializeEx")
	procCoUninitialize     = ole32.NewProc("CoUninitialize")
)

type shellFileInfo struct {
	Icon        syscall.Handle
	IconIndex   int32
	Attributes  uint32
	DisplayName [260]uint16
	TypeName    [80]uint16
}

type iconInfo struct {
	IsIcon   int32
	HotspotX uint32
	HotspotY uint32
	Mask     syscall.Handle
	Color    syscall.Handle
}

type bitmap struct {
	Type       int32
	Width      int32
	Height     int32
	WidthBytes int32
	Planes     uint16
	BitsPixel  uint16
	Bits       unsafe.Pointer
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

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

func (Windows) AssociatedIcon(path string, _ bool) ([]byte, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	pathUTF16, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	// Shell icon handlers may use COM. Keep initialization and the shell call
	// on the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hresult, _, _ := procCoInitializeEx.Call(0, 0)
	if hresult == sOK || hresult == sFalse {
		defer procCoUninitialize.Call()
	} else if hresult != rpcEChangedMode {
		return nil, fmt.Errorf("initialise COM for file icon: HRESULT 0x%x", hresult)
	}

	var fileInfo shellFileInfo
	result, _, callErr := procSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(pathUTF16)),
		0,
		uintptr(unsafe.Pointer(&fileInfo)),
		unsafe.Sizeof(fileInfo),
		shgfiIcon,
	)
	if result == 0 || fileInfo.Icon == 0 {
		return nil, fmt.Errorf("get associated icon: %v", callErr)
	}
	defer procDestroyIcon.Call(uintptr(fileInfo.Icon))

	return iconHandlePNG(fileInfo.Icon)
}

func iconHandlePNG(handle syscall.Handle) ([]byte, error) {
	var info iconInfo
	if result, _, err := procGetIconInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&info))); result == 0 {
		return nil, fmt.Errorf("get icon info: %w", err)
	}
	if info.Mask != 0 {
		defer procDeleteObject.Call(uintptr(info.Mask))
	}
	if info.Color != 0 {
		defer procDeleteObject.Call(uintptr(info.Color))
	} else {
		return nil, fmt.Errorf("associated icon has no colour bitmap")
	}

	var bmp bitmap
	if result, _, err := procGetObjectW.Call(uintptr(info.Color), unsafe.Sizeof(bmp), uintptr(unsafe.Pointer(&bmp))); result == 0 {
		return nil, fmt.Errorf("read icon bitmap: %w", err)
	}
	width, height := int(bmp.Width), int(bmp.Height)
	if width < 0 {
		width = -width
	}
	if height < 0 {
		height = -height
	}
	if width == 0 || height == 0 {
		return nil, fmt.Errorf("associated icon has invalid dimensions")
	}

	dc, _, err := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return nil, fmt.Errorf("create icon device context: %w", err)
	}
	defer procDeleteDC.Call(dc)

	colorInfo := bitmapInfo{Header: bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(width),
		Height:      int32(height),
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}}
	pixels := make([]byte, width*height*4)
	if result, _, err := procGetDIBits.Call(
		dc,
		uintptr(info.Color),
		0,
		uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&colorInfo)),
		dibRGBColors,
	); result == 0 {
		return nil, fmt.Errorf("read associated icon pixels: %w", err)
	}

	hasAlpha := false
	for i := 3; i < len(pixels); i += 4 {
		if pixels[i] != 0 {
			hasAlpha = true
			break
		}
	}
	mask := []byte(nil)
	maskRowBytes := 0
	if !hasAlpha && info.Mask != 0 {
		maskRowBytes = ((width + 31) / 32) * 4
		mask = make([]byte, maskRowBytes*height)
		maskInfo := bitmapInfo{Header: bitmapInfoHeader{
			Size:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			Width:    int32(width),
			Height:   int32(height),
			Planes:   1,
			BitCount: 1,
		}}
		if result, _, _ := procGetDIBits.Call(
			dc,
			uintptr(info.Mask),
			0,
			uintptr(height),
			uintptr(unsafe.Pointer(&mask[0])),
			uintptr(unsafe.Pointer(&maskInfo)),
			dibRGBColors,
		); result == 0 {
			mask = nil
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source := ((height-1-y)*width + x) * 4
			alpha := pixels[source+3]
			if !hasAlpha {
				alpha = 255
				if mask != nil {
					maskY := height - 1 - y
					if mask[maskY*maskRowBytes+x/8]&(0x80>>uint(x%8)) != 0 {
						alpha = 0
					}
				}
			}
			img.SetRGBA(x, y, color.RGBA{
				R: pixels[source+2],
				G: pixels[source+1],
				B: pixels[source],
				A: alpha,
			})
		}
	}

	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
