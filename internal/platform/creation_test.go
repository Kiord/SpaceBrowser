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
	old := time.Unix(1000000000, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if got := FileCreationTime(path); got != created {
		t.Fatalf("modification date changed creation date: %d != %d", got, created)
	}
}
