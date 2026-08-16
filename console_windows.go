//go:build windows

package main

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

const attachParentProcess = ^uintptr(0)

var attachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

// GUI applications launched by Git Bash/mintty receive a usable stdout pipe
// but no Win32 console to attach to, and stderr may be discarded. Using stdout
// covers both that case and ordinary attached Windows consoles. Explorer gives
// the process no usable stdout, so this still cannot create a console window.
var terminalLogs io.Writer = os.Stdout

// attachParentConsole lets a Windows GUI-subsystem build write to the console
// that launched it without creating a console when started from Explorer.
func attachParentConsole() {
	attached, _, _ := attachConsole.Call(attachParentProcess)
	if attached == 0 {
		// Console-subsystem and development builds are already attached and
		// already have valid Go standard streams.
		return
	}

	os.Stdout = standardFile(windows.STD_OUTPUT_HANDLE, "stdout", os.Stdout)
	os.Stderr = standardFile(windows.STD_ERROR_HANDLE, "stderr", os.Stderr)

	// Stdout is also the reliable stream when a shell leaves a GUI application
	// with a stale stderr pipe after AttachConsole succeeds.
	terminalLogs = os.Stdout
}

func terminalLogOutput() io.Writer { return terminalLogs }

func standardFile(kind uint32, name string, fallback *os.File) *os.File {
	handle, err := windows.GetStdHandle(kind)
	if err != nil || handle == 0 || handle == windows.InvalidHandle {
		return fallback
	}
	return os.NewFile(uintptr(handle), name)
}
