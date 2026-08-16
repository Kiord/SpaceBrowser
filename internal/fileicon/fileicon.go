package fileicon

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrUnavailable = errors.New("system file icon unavailable")

const maximumCachedIcons = 256

type Icon struct {
	Data      []byte
	MediaType string
}

type backend interface {
	Lookup(path string, isFolder bool) (Icon, error)
}

type cachedIcon struct {
	icon Icon
	err  error
}

type Service struct {
	backend backend
	mu      sync.Mutex
	cache   map[string]cachedIcon
	order   []string
}

func NewService() *Service {
	return newService(newPlatformBackend())
}

func newService(backend backend) *Service {
	return &Service{backend: backend, cache: make(map[string]cachedIcon)}
}

func (s *Service) DataURL(path string, isFolder bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("missing path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect icon path: %w", err)
	}
	isFolder = isFolder || info.IsDir()
	key := cacheKey(path, isFolder)

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, found := s.cache[key]
	if !found {
		icon, lookupErr := s.backend.Lookup(path, isFolder)
		entry = cachedIcon{icon: cloneIcon(icon), err: lookupErr}
		if len(s.order) >= maximumCachedIcons {
			delete(s.cache, s.order[0])
			s.order = s.order[1:]
		}
		s.cache[key] = entry
		s.order = append(s.order, key)
	}
	if entry.err != nil {
		return "", entry.err
	}
	if len(entry.icon.Data) == 0 || entry.icon.MediaType == "" {
		return "", ErrUnavailable
	}
	return "data:" + entry.icon.MediaType + ";base64," + base64.StdEncoding.EncodeToString(entry.icon.Data), nil
}

func cacheKey(path string, isFolder bool) string {
	if isFolder {
		return "folder:" + filepath.Clean(path)
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension == "" {
		return "file:<none>"
	}
	// Executables, shortcuts, and icon files can carry per-file artwork.
	switch extension {
	case ".exe", ".lnk", ".ico", ".app", ".desktop":
		return "path:" + filepath.Clean(path)
	default:
		return "extension:" + extension
	}
}

func cloneIcon(icon Icon) Icon {
	return Icon{Data: append([]byte(nil), icon.Data...), MediaType: icon.MediaType}
}
