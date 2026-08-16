//go:build linux

package platform

import (
	"strings"
	"testing"
)

func TestLinuxMountPointsParsesEscapedAndBindMounts(t *testing.T) {
	contents := "29 23 8:1 / / rw,relatime - ext4 /dev/root rw\n" +
		"36 29 8:2 / /media/My\\040Disk rw,nosuid - ext4 /dev/sdb1 rw\n" +
		"41 29 8:1 /source /mnt/bind rw,relatime - ext4 /dev/root rw\n"
	mounts := linuxMountPoints(strings.NewReader(contents))
	for _, path := range []string{"/", "/media/My Disk", "/mnt/bind"} {
		if _, found := mounts[path]; !found {
			t.Errorf("mount point %q was not parsed", path)
		}
	}
}

func TestLinuxRootIsMountRoot(t *testing.T) {
	if !(Linux{}).IsMountRoot("/") {
		t.Fatal("the root filesystem was not recognized as a mount root")
	}
}
