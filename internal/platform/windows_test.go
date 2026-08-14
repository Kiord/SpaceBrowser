//go:build windows

package platform

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

func TestWindowsNetworkFilesystemClassification(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		volumeRoot string
		driveType  uint32
		lookupErr  error
		want       bool
	}{
		{name: "UNC", path: `\\server\share\folder`, want: true},
		{name: "mapped drive", path: `Z:\folder`, volumeRoot: `Z:\`, driveType: winapi.DRIVE_REMOTE, want: true},
		{name: "local drive", path: `C:\folder`, volumeRoot: `C:\`, driveType: winapi.DRIVE_FIXED, want: false},
		{name: "lookup failure", path: `Q:\folder`, lookupErr: errors.New("unavailable"), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var cache sync.Map
			volumeCalls := 0
			driveTypeCalls := 0
			volumePath := func(string) (string, error) {
				volumeCalls++
				return test.volumeRoot, test.lookupErr
			}
			driveType := func(string) uint32 {
				driveTypeCalls++
				return test.driveType
			}
			if got := isWindowsNetworkFS(test.path, volumePath, driveType, &cache); got != test.want {
				t.Fatalf("isWindowsNetworkFS(%q) = %v, want %v", test.path, got, test.want)
			}
			if test.name == "mapped drive" {
				if got := isWindowsNetworkFS(test.path, volumePath, driveType, &cache); !got {
					t.Fatal("cached mapped drive was not remote")
				}
				if volumeCalls != 2 || driveTypeCalls != 1 {
					t.Fatalf("cached calls = %d volume and %d type, want 2 and 1", volumeCalls, driveTypeCalls)
				}
			}
		})
	}
}

func TestWindowsLocalTempPathIsNotNetworkFilesystem(t *testing.T) {
	if (Windows{}).IsLikelyNetworkFS(t.TempDir()) {
		t.Fatal("local temporary directory was classified as a network filesystem")
	}
}

func TestWindowsMappedDriveIntegration(t *testing.T) {
	path := os.Getenv("SPACEBROWSER_TEST_MAPPED_DRIVE")
	if path == "" {
		t.Skip("SPACEBROWSER_TEST_MAPPED_DRIVE is not configured")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("mapped drive is inaccessible: %v", err)
	}
	var cache sync.Map
	clean := (Windows{}).Canonicalize(path)
	if !isWindowsNetworkFS(clean, windowsVolumePath, windowsDriveType, &cache) {
		t.Fatalf("mapped drive %q was not classified as a network filesystem", path)
	}
}

func TestWindowsUsageIdentifiesHardLinks(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original.bin")
	link := filepath.Join(dir, "link.bin")
	other := filepath.Join(dir, "other.bin")
	if err := os.WriteFile(original, []byte("shared allocation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("different allocation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, link); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}

	windowsPlatform := Windows{}
	originalInfo, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	linkInfo, err := os.Stat(link)
	if err != nil {
		t.Fatal(err)
	}
	originalUsage := windowsPlatform.UsageFor(original, originalInfo)
	linkUsage := windowsPlatform.UsageFor(link, linkInfo)
	if !originalUsage.HasIdentity || !linkUsage.HasIdentity {
		t.Fatal("Windows file identity is unavailable")
	}
	if originalUsage.Identity != linkUsage.Identity {
		t.Fatalf("hard-link identities differ: %+v and %+v", originalUsage.Identity, linkUsage.Identity)
	}
	if originalUsage.LinkCount < 2 || linkUsage.LinkCount < 2 {
		t.Fatalf("hard-link counts = %d and %d, want at least 2", originalUsage.LinkCount, linkUsage.LinkCount)
	}
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	otherUsage := windowsPlatform.UsageFor(other, otherInfo)
	if !otherUsage.HasIdentity || originalUsage.Identity == otherUsage.Identity {
		t.Fatalf("distinct files have invalid identities: %+v and %+v", originalUsage.Identity, otherUsage.Identity)
	}
}

func TestWindowsUsageReportsOrdinaryClusterAllocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "one-byte.bin")
	if err := os.WriteFile(path, []byte{1}, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	usage := (Windows{}).UsageFor(path, info)
	if usage.AllocatedSize <= info.Size() {
		t.Fatalf("allocated size = %d, want greater than one-byte logical size", usage.AllocatedSize)
	}
	if usage.LinkCount != 1 {
		t.Fatalf("link count = %d, want 1", usage.LinkCount)
	}
}

func TestWindowsUsageReportsSparseAllocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse.bin")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var returned uint32
	if err := winapi.DeviceIoControl(
		winapi.Handle(file.Fd()),
		winapi.FSCTL_SET_SPARSE,
		nil,
		0,
		nil,
		0,
		&returned,
		nil,
	); err != nil {
		t.Skipf("sparse files are unavailable: %v", err)
	}
	const logicalSize = 16 << 20
	if err := file.Truncate(logicalSize); err != nil {
		t.Fatal(err)
	}

	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	usage := (Windows{}).UsageFor(path, info)
	if usage.AllocatedSize >= info.Size() {
		t.Fatalf("allocated size = %d, want less than logical size %d", usage.AllocatedSize, info.Size())
	}
}

func TestWindowsUsageReportsCompressedAllocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compressed.bin")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	const logicalSize = 8 << 20
	data := make([]byte, logicalSize)
	if _, err := file.Write(data); err != nil {
		file.Close()
		t.Fatal(err)
	}
	const compressionFormatDefault uint16 = 1
	compressionFormat := compressionFormatDefault
	var returned uint32
	if err := winapi.DeviceIoControl(
		winapi.Handle(file.Fd()),
		winapi.FSCTL_SET_COMPRESSION,
		(*byte)(unsafe.Pointer(&compressionFormat)),
		uint32(unsafe.Sizeof(compressionFormat)),
		nil,
		0,
		&returned,
		nil,
	); err != nil {
		file.Close()
		t.Skipf("filesystem compression is unavailable: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	pathPtr, err := windowsPathPtr(path)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := winapi.GetFileAttributes(pathPtr)
	if err != nil {
		t.Fatal(err)
	}
	if attributes&winapi.FILE_ATTRIBUTE_COMPRESSED == 0 {
		t.Skip("filesystem accepted compression request without enabling compression")
	}
	usage := (Windows{}).UsageFor(path, info)
	if usage.AllocatedSize >= info.Size() {
		t.Fatalf("allocated size = %d, want less than compressed logical size %d", usage.AllocatedSize, info.Size())
	}
}

func TestWindowsIsHiddenUsesFileAttribute(t *testing.T) {
	dir := t.TempDir()
	hiddenPath := filepath.Join(dir, "hidden.txt")
	dotPath := filepath.Join(dir, ".visible.txt")
	for _, path := range []string{hiddenPath, dotPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	hiddenPtr, err := windowsPathPtr(hiddenPath)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := winapi.GetFileAttributes(hiddenPtr)
	if err != nil {
		t.Fatal(err)
	}
	if err := winapi.SetFileAttributes(hiddenPtr, attributes|winapi.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}

	windowsPlatform := Windows{}
	if !windowsPlatform.IsHidden(hiddenPath) {
		t.Fatal("file with the Windows hidden attribute was not hidden")
	}
	if windowsPlatform.IsHidden(dotPath) {
		t.Fatal("dot-prefixed file without the Windows hidden attribute was hidden")
	}
}

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

func TestWindowsCanonicalizeExtendedUNCPath(t *testing.T) {
	const input = `\\?\UNC\server\share\folder`
	const want = `\\server\share\folder`
	if got := (Windows{}).Canonicalize(input); got != want {
		t.Fatalf("Canonicalize(%q) = %q, want %q", input, got, want)
	}
}

func TestWindowsAssociatedZipIcon(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "archive.zip")
	if err := os.WriteFile(zipPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	windows := Windows{}
	zipIcon, err := windows.AssociatedIcon(zipPath, false)
	if err != nil {
		t.Fatalf("AssociatedIcon(zip) error = %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(zipIcon)); err != nil {
		t.Fatalf("associated zip icon is not PNG: %v", err)
	}

	folderIcon, err := windows.AssociatedIcon(dir, true)
	if err != nil {
		t.Fatalf("AssociatedIcon(folder) error = %v", err)
	}
	if bytes.Equal(zipIcon, folderIcon) {
		t.Fatal("zip association icon unexpectedly matches the folder icon")
	}
}
