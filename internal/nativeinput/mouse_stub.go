//go:build !linux || !cgo

package nativeinput

func StartAuxiliaryMouseCapture(func(int)) {}
