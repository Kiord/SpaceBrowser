package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFileCreationTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if FileCreationTime(path) != 0 {
		t.Fatal("missing file should have no creation date")
	}
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	created := FileCreationTime(path)
	if created == 0 && runtime.GOOS == "linux" {
		return
	} // birth time is optional
	if delta := time.Now().Unix() - created; delta < -5 || delta > 10 {
		t.Fatalf("unexpected creation time %d", created)
	}
	// macOS can move the birth time backward when mtime predates it.
	// Move mtime forward to test that creation time is read independently.
	modified := time.Unix(created, 0).Add(time.Hour)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.ModTime().Unix(); got != modified.Unix() {
		t.Fatalf("modification date = %d, want %d", got, modified.Unix())
	}
	if got := FileCreationTime(path); got != created {
		t.Fatalf("modification date changed creation date: %d != %d", got, created)
	}
}
