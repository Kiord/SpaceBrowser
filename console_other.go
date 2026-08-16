//go:build !windows

package main

import (
	"io"
	"os"
)

func attachParentConsole() {}

func terminalLogOutput() io.Writer { return os.Stderr }
