//go:build !windows

package platform

import "fmt"

func IsElevated() bool { return false }

func RestartElevated([]string) error {
	return fmt.Errorf("administrator restart is only available on Windows")
}
