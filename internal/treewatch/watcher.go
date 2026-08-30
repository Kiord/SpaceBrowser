package treewatch

import (
	"errors"
	"path/filepath"
	"strings"
)

var ErrCapacity = errors.New("filesystem watch capacity reached")

type Watcher interface {
	AddDirectory(string) error
	Close() error
}

// logicalEventPath translates an event reported through the physical spelling
// of a watched root back to the spelling used by the scanner and cache.
func logicalEventPath(logicalRoot, physicalRoot, changed string) string {
	logicalRoot = filepath.Clean(logicalRoot)
	physicalRoot = filepath.Clean(physicalRoot)
	changed = filepath.Clean(changed)
	relative, err := filepath.Rel(physicalRoot, changed)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return changed
	}
	if relative == "." {
		return logicalRoot
	}
	return filepath.Join(logicalRoot, relative)
}
