//go:build darwin

package platform

import "testing"

func TestDarwinNetworkFilesystemTypes(t *testing.T) {
	if !isNetworkFilesystemType("smbfs") {
		t.Fatal("smbfs should be classified as a network filesystem")
	}
	if isNetworkFilesystemType("apfs") {
		t.Fatal("apfs should not be classified as a network filesystem")
	}
}
