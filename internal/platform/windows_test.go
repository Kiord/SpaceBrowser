//go:build windows

package platform

import "testing"

func TestWindowsCanonicalizeDriveLetter(t *testing.T) {
	windows := Windows{}
	for input, want := range map[string]string{
		"C":  `C:\`,
		"d":  `d:\`,
		"E:": `E:\`,
	} {
		if got := windows.Canonicalize(input); got != want {
			t.Errorf("Canonicalize(%q) = %q, want %q", input, got, want)
		}
	}
}
