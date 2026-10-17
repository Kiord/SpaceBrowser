//go:build windows

package platform

import (
	"os/exec"
	"testing"
)

func makeTrashTestLink(t *testing.T, target, link string) {
	t.Helper()
	// Junctions need no developer mode or symlink privilege.
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, output)
	}
}
