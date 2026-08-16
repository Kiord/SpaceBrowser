package fileicon

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type recordingBackend struct {
	mu    sync.Mutex
	calls int
}

func (b *recordingBackend) Lookup(string, bool) (Icon, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	return Icon{Data: []byte("icon"), MediaType: "image/png"}, nil
}

func TestServiceReturnsMIMEAwareDataURLAndCachesExtensions(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.txt")
	second := filepath.Join(directory, "second.TXT")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backend := &recordingBackend{}
	service := newService(backend)

	dataURL, err := service.DataURL(first, false)
	if err != nil {
		t.Fatalf("DataURL(first): %v", err)
	}
	if _, err := service.DataURL(second, false); err != nil {
		t.Fatalf("DataURL(second): %v", err)
	}
	prefix := "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		t.Fatalf("data URL = %q", dataURL)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil || string(decoded) != "icon" {
		t.Fatalf("decoded data URL = %q, %v", decoded, err)
	}
	if backend.calls != 1 {
		t.Fatalf("backend calls = %d, want 1", backend.calls)
	}
}

func TestServiceCachesFoldersByPath(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	backend := &recordingBackend{}
	service := newService(backend)
	if _, err := service.DataURL(first, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DataURL(first, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DataURL(second, true); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 2 {
		t.Fatalf("backend calls = %d, want 2", backend.calls)
	}
}

func TestServiceCacheIsBounded(t *testing.T) {
	backend := &recordingBackend{}
	service := newService(backend)
	for index := 0; index < maximumCachedIcons+5; index++ {
		path := filepath.Join(t.TempDir(), "folder")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := service.DataURL(path, true); err != nil {
			t.Fatal(err)
		}
	}
	if len(service.cache) != maximumCachedIcons {
		t.Fatalf("cache size = %d, want %d", len(service.cache), maximumCachedIcons)
	}
}
