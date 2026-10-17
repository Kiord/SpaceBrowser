//go:build !windows

package platform

import (
	"os"
	"testing"
)

func makeTrashTestLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
