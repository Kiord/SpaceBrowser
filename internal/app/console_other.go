//go:build !windows

package app

import (
	"io"
	"os"
)

func attachParentConsole() {}

func terminalLogOutput() io.Writer { return os.Stderr }
