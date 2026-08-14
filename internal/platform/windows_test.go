//go:build windows

package platform

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	winapi "golang.org/x/sys/windows"
)

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
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	otherUsage := windowsPlatform.UsageFor(other, otherInfo)
	if !otherUsage.HasIdentity || originalUsage.Identity == otherUsage.Identity {
		t.Fatalf("distinct files have invalid identities: %+v and %+v", originalUsage.Identity, otherUsage.Identity)
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
