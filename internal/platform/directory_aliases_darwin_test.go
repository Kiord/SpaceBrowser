//go:build darwin

package platform

import (
	"os"
	"testing"
)

func TestDarwinDirectoryAliasesMatchNativeDirectories(t *testing.T) {
	if _, err := os.Stat("/usr/share/firmlinks"); err != nil {
		t.Skipf("system has no firmlink table: %v", err)
	}
	aliases := (Darwin{}).DirectoryAliases()
	foundUsers := false
	for _, alias := range aliases {
		preferred, err := os.Stat(alias.Preferred)
		if err != nil {
			t.Fatal(err)
		}
		backing, err := os.Stat(alias.Backing)
		if err != nil {
			t.Fatal(err)
		}
		if !preferred.IsDir() || !backing.IsDir() || !os.SameFile(preferred, backing) {
			t.Fatalf("alias does not identify the same directory: %+v", alias)
		}
		foundUsers = foundUsers || alias == (DirectoryAlias{Preferred: "/Users", Backing: "/System/Volumes/Data/Users"})
	}
	preferred, preferredErr := os.Stat("/Users")
	backing, backingErr := os.Stat("/System/Volumes/Data/Users")
	if preferredErr == nil && backingErr == nil && os.SameFile(preferred, backing) && !foundUsers {
		t.Fatal("native Users firmlink was not discovered")
	}
}
