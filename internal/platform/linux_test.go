//go:build linux

package platform

import "testing"

func TestLinuxFileURIEscapesPathCharacters(t *testing.T) {
	const path = "/tmp/Space Browser/#100%/café.txt"
	const want = "file:///tmp/Space%20Browser/%23100%25/caf%C3%A9.txt"

	if got := linuxFileURI(path); got != want {
		t.Fatalf("linuxFileURI(%q) = %q, want %q", path, got, want)
	}
}
