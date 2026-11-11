//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RestartElevated requests UAC consent using the shell; it never changes UAC policy.
// The caller decides when it is safe to close the original application.
func RestartElevated(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find working directory: %w", err)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = syscall.EscapeArg(arg)
	}
	parameters, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return err
	}
	cwd, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, parameters, cwd, swShowNormal); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return fmt.Errorf("administrator request cancelled; settings were saved and SpaceBrowser will stay open")
		}
		return fmt.Errorf("restart as administrator: %w", err)
	}
	return nil
}
