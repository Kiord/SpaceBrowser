package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	protectedDeletionMessage  = "SpaceBrowser does not allow deleting protected system locations"
	mountRootDeletionMessage  = "SpaceBrowser does not allow deleting mounted filesystem roots"
	mountChildDeletionMessage = "SpaceBrowser does not allow deleting this folder because it contains a mounted filesystem"
	specialDeletionMessage    = "SpaceBrowser does not allow deleting special filesystem objects"
)

func validateDeletionFileType(info os.FileInfo) error {
	mode := info.Mode()
	if mode.IsRegular() || mode.IsDir() || mode&os.ModeSymlink != 0 {
		return nil
	}
	return fmt.Errorf("%s", specialDeletionMessage)
}

func validateDeletionTarget(path string, info os.FileInfo, protectedTrees, protectedExact, mountPoints []string, caseInsensitive bool) error {
	clean, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("inspect deletion target: %w", err)
	}
	clean = filepath.Clean(clean)

	for _, root := range protectedTrees {
		if pathWithin(clean, root, caseInsensitive) {
			return fmt.Errorf("%s", protectedDeletionMessage)
		}
	}
	for _, exact := range protectedExact {
		if samePath(clean, exact, caseInsensitive) {
			return fmt.Errorf("%s", protectedDeletionMessage)
		}
	}
	if err := validateDeletionFileType(info); err != nil {
		return err
	}

	// Removing a symlink removes the link itself, not the mounted or protected
	// tree it may point to. Do not treat its target as a deletion boundary.
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	for _, mountPoint := range mountPoints {
		if samePath(clean, mountPoint, caseInsensitive) {
			return fmt.Errorf("%s", mountRootDeletionMessage)
		}
		if pathWithin(mountPoint, clean, caseInsensitive) {
			return fmt.Errorf("%s", mountChildDeletionMessage)
		}
	}
	return nil
}

func samePath(first, second string, caseInsensitive bool) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if caseInsensitive {
		return strings.EqualFold(first, second)
	}
	return first == second
}

func pathWithin(path, root string, caseInsensitive bool) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if caseInsensitive {
		path = strings.ToLower(path)
		root = strings.ToLower(root)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}
