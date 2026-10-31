//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type testRecycleOperation struct {
	flags                                     uint32
	path                                      string
	calls                                     []string
	flagsErr, queueErr, performErr, statusErr error
	cancelled                                 bool
	destination                               string
}

func (op *testRecycleOperation) setFlags(flags uint32) error {
	op.calls = append(op.calls, "flags")
	op.flags = flags
	return op.flagsErr
}

func (op *testRecycleOperation) deleteItem(path string) error {
	op.calls = append(op.calls, "queue")
	op.path = path
	return op.queueErr
}

func (op *testRecycleOperation) perform() error {
	op.calls = append(op.calls, "perform")
	if op.performErr != nil || op.cancelled {
		return op.performErr
	}
	return os.Rename(op.path, op.destination)
}

func (op *testRecycleOperation) aborted() (bool, error) {
	op.calls = append(op.calls, "status")
	return op.cancelled, op.statusErr
}

func TestWindowsRecycleOnlyFailuresKeepTarget(t *testing.T) {
	failure := errors.New("native failure")
	for _, test := range []struct {
		name    string
		op      testRecycleOperation
		calls   []string
		wantErr error
	}{
		{"flags rejected", testRecycleOperation{flagsErr: failure}, []string{"flags"}, failure},
		{"target rejected", testRecycleOperation{queueErr: failure}, []string{"flags", "queue"}, failure},
		{"non-recyclable target", testRecycleOperation{performErr: failure}, []string{"flags", "queue", "perform", "status"}, failure},
		{"cancelled with successful HRESULT", testRecycleOperation{cancelled: true}, []string{"flags", "queue", "perform", "status"}, ErrOperationCancelled},
		{"status unavailable", testRecycleOperation{cancelled: true, statusErr: failure}, []string{"flags", "queue", "perform", "status"}, failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "confirmed.txt")
			if err := os.WriteFile(path, []byte("keep this content"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := recycleWindowsItem(path, &test.op)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("recycling error = %v, want %v", err, test.wantErr)
			}
			if !reflect.DeepEqual(test.op.calls, test.calls) {
				t.Fatalf("calls = %v, want %v (no fallback or retry)", test.op.calls, test.calls)
			}
			if test.op.path != "" && test.op.path != path {
				t.Fatalf("queued path = %q, want confirmed path %q", test.op.path, path)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "keep this content" {
				t.Fatalf("failed recycling changed the target: %q, %v", data, err)
			}
		})
	}
}

func TestWindowsRecycleOnlyMovesConfirmedPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "confirmed.txt")
	neighbor := filepath.Join(dir, "unconfirmed.txt")
	for _, file := range []string{path, neighbor} {
		if err := os.WriteFile(file, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	op := &testRecycleOperation{destination: filepath.Join(dir, "recycled.txt")}
	if err := recycleWindowsItem(path, op); err != nil {
		t.Fatal(err)
	}
	// Assert the native values independently of the production constants:
	// RECYCLEONDELETE, EARLYFAILURE, ADDUNDORECORD, SILENT,
	// NOCONFIRMATION, NOERRORUI, NO_CONNECTED_ELEMENTS.
	if op.flags != 0x20182414 {
		t.Fatalf("operation flags = 0x%08X, want recycle-only flags 0x20182414", op.flags)
	}
	if op.path != path {
		t.Fatalf("queued %q, want %q", op.path, path)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}
	for file, want := range map[string]string{op.destination: path, neighbor: neighbor} {
		data, err := os.ReadFile(file)
		if err != nil || string(data) != want {
			t.Fatalf("content at %q = %q, %v; want %q", file, data, err, want)
		}
	}
}

func TestWindowsRecycleRejectsRelativePath(t *testing.T) {
	op := &testRecycleOperation{}
	if err := recycleWindowsItem("relative.txt", op); err == nil {
		t.Fatal("relative recycling path accepted")
	}
	if !reflect.DeepEqual(op.calls, []string{"flags"}) {
		t.Fatalf("invalid path reached the shell: %v", op.calls)
	}
}

func TestWindowsRecycleRejectsMissingAndNULPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path + "-missing", path + "\x00" + path} {
		if err := (Windows{}).MoveToTrash(target); err == nil {
			t.Fatalf("invalid path %q was accepted", target)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
		t.Fatalf("invalid path changed prefix target: %q, %v", data, err)
	}
}

func TestWindowsRecycleNativeRoundTrip(t *testing.T) {
	testWindowsRecycleNativeRoundTrip(t)
}

func TestWindowsRecycleNativeRoundTripShortTempPath(t *testing.T) {
	dir, err := resolvePhysicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ptr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	n, err := windows.GetShortPathName(ptr, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		t.Fatalf("get short temporary path: length %d, %v", n, err)
	}
	short := windows.UTF16ToString(buffer[:n])
	if strings.EqualFold(short, dir) {
		t.Skip("8.3 short paths are unavailable on the temporary volume")
	}
	// Windows CI can expose TEMP through an alias such as RUNNER~1.
	t.Setenv("TMP", short)
	t.Setenv("TEMP", short)
	testWindowsRecycleNativeRoundTrip(t)
}

func testWindowsRecycleNativeRoundTrip(t *testing.T) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []bool{false, true} {
		name := "file"
		if directory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			// Recycle Bin metadata records the expanded physical path, even
			// when TEMP uses an 8.3 alias or a directory junction.
			dir, err := resolvePhysicalPath(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "recycle-é-文件.txt")
			contentPath := path
			if directory {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				contentPath = filepath.Join(path, "child.txt")
			}
			if err := os.WriteFile(contentPath, []byte("recoverable content"), 0o600); err != nil {
				t.Fatal(err)
			}
			neighbor := filepath.Join(dir, "unconfirmed.txt")
			if err := os.WriteFile(neighbor, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(filepath.VolumeName(path)+`\`, "$Recycle.Bin", user.User.Sid.String())
			// Recover only this test's item, even if an assertion fails after
			// recycling. Never empty or otherwise alter the user's bin.
			restore := func() bool {
				entries, err := os.ReadDir(bin)
				if err != nil {
					return false
				}
				for _, entry := range entries {
					if !strings.HasPrefix(entry.Name(), "$I") {
						continue
					}
					infoPath := filepath.Join(bin, entry.Name())
					original, err := readWindowsRecycleInfo(infoPath)
					if err != nil || !strings.EqualFold(original, path) {
						continue
					}
					dataPath := filepath.Join(bin, "$R"+entry.Name()[2:])
					info := TrashRestoreInfo{TargetPath: dataPath, OriginalPath: original}
					if err := (Windows{}).RestoreTrashItem(dataPath, info); err != nil {
						t.Errorf("restore test item: %v", err)
					}
					return true
				}
				return false
			}
			t.Cleanup(func() { restore() })
			if err := (Windows{}).MoveToTrash(path); err != nil {
				if data, readErr := os.ReadFile(contentPath); readErr != nil || string(data) != "recoverable content" {
					t.Fatalf("rejected recycling changed the target: %q, %v (operation: %v)", data, readErr, err)
				}
				// Developer sandboxes may lack a Recycle Bin. CI's Windows
				// runner must exercise success, so refusal must fail there.
				if errors.Is(err, errRecycleUnavailable) && os.Getenv("SPACEBROWSER_TEST_REQUIRE_RECYCLE") != "1" {
					t.Skipf("recycling is unavailable for the test account/volume; verified target was preserved: %v", err)
				}
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("recycled source is still present: %v", err)
			}
			if !restore() {
				t.Fatal("source disappeared without a recoverable Recycle Bin item")
			}
			if data, err := os.ReadFile(contentPath); err != nil || string(data) != "recoverable content" {
				t.Fatalf("restored content = %q, %v", data, err)
			}
			if data, err := os.ReadFile(neighbor); err != nil || string(data) != "keep" {
				t.Fatalf("recycling changed unconfirmed neighbor: %q, %v", data, err)
			}
		})
	}
}

func TestWindowsRecycleSinkVetoesPermanentDeletion(t *testing.T) {
	for _, flags := range []uint32{0, 0x40, 0x400} {
		sink := newRecycleProgressSink()
		hr, _, _ := syscall.SyscallN(sink.vtable[11], uintptr(unsafe.Pointer(sink)), uintptr(flags), 0)
		if uint32(hr) != recycleAbort || sink.err == nil {
			t.Fatalf("permanent deletion flags 0x%X were accepted: HRESULT 0x%X, %v", flags, hr, sink.err)
		}
		if sink.preDelete(0x80) != recycleAbort {
			t.Fatal("a later callback erased the permanent-deletion veto")
		}
		// A subsequent success notification must not erase the veto.
		syscall.SyscallN(sink.vtable[12], uintptr(unsafe.Pointer(sink)), uintptr(flags), 0, 0, 1)
		if sink.recycled || sink.err == nil {
			t.Fatal("a success notification erased the permanent-deletion veto")
		}
	}
	for _, flags := range []uint32{0x80, 0xC0} {
		sink := newRecycleProgressSink()
		hr, _, _ := syscall.SyscallN(sink.vtable[11], uintptr(unsafe.Pointer(sink)), uintptr(flags), 0)
		if hr != 0 || sink.err != nil {
			t.Fatalf("recycling flags 0x%X were rejected: HRESULT 0x%X, %v", flags, hr, sink.err)
		}
		syscall.SyscallN(sink.vtable[12], uintptr(unsafe.Pointer(sink)), uintptr(flags), 0, 0, 1)
		if !sink.recycled || sink.err != nil {
			t.Fatalf("successful recycling was not recorded: %v", sink.err)
		}
	}
}

func TestWindowsRecycleSinkRejectsFailedOrUnrecoverableResults(t *testing.T) {
	for _, result := range []struct {
		name     string
		hr, item uintptr
	}{
		{"insufficient capacity", 0x80070070, 0}, // HRESULT_FROM_WIN32(ERROR_DISK_FULL)
		{"cancelled", 0x800704C7, 0},
		{"no recoverable item", 0, 0},
	} {
		t.Run(result.name, func(t *testing.T) {
			sink := newRecycleProgressSink()
			sink.preDelete(0x80)
			hr, _, _ := syscall.SyscallN(sink.vtable[12], uintptr(unsafe.Pointer(sink)), 0x80, 0, result.hr, result.item)
			if uint32(hr) != recycleAbort || sink.err == nil || sink.recycled {
				t.Fatalf("failed recycling reported success: HRESULT 0x%X, %v", hr, sink.err)
			}
		})
	}
}

func TestWindowsRecycleNativeMappedDriveKeepsTarget(t *testing.T) {
	root := os.Getenv("SPACEBROWSER_TEST_MAPPED_DRIVE")
	if root == "" {
		t.Skip("SPACEBROWSER_TEST_MAPPED_DRIVE is not configured")
	}
	dir, err := os.MkdirTemp(root, "spacebrowser-recycle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(path, []byte("non-recyclable content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Windows{}).MoveToTrash(path); err == nil {
		t.Fatal("recycling a non-recyclable network target reported success")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "non-recyclable content" {
		t.Fatalf("network target was changed instead of rejected: %q, %v", data, err)
	}
}
