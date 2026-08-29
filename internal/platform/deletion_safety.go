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
	physicalDeletionMessage   = "SpaceBrowser could not resolve the physical deletion target, so deletion was blocked"
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
	physical, err := physicalDeletionPath(clean, info)
	if err != nil {
		return fmt.Errorf("%s: %w", physicalDeletionMessage, err)
	}
	paths := []string{clean}
	if !samePath(clean, physical, caseInsensitive) {
		paths = append(paths, physical)
	}

	for _, candidate := range paths {
		for _, root := range protectedTrees {
			if pathWithin(candidate, root, caseInsensitive) {
				return fmt.Errorf("%s", protectedDeletionMessage)
			}
		}
		for _, exact := range protectedExact {
			if samePath(candidate, exact, caseInsensitive) {
				return fmt.Errorf("%s", protectedDeletionMessage)
			}
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
	for _, candidate := range paths {
		for _, mountPoint := range mountPoints {
			if samePath(candidate, mountPoint, caseInsensitive) {
				return fmt.Errorf("%s", mountRootDeletionMessage)
			}
			if pathWithin(mountPoint, candidate, caseInsensitive) {
				return fmt.Errorf("%s", mountChildDeletionMessage)
			}
		}
	}
	return nil
}

// physicalDeletionPath resolves aliases in the path that RemoveAll would
// traverse. A final symlink (including a Windows junction represented as a
// symlink) is deliberately preserved because deleting it removes the link
// itself rather than its target.
func physicalDeletionPath(clean string, info os.FileInfo) (string, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		parent, err := resolvePhysicalPath(filepath.Dir(clean))
		if err != nil {
			return "", err
		}
		return filepath.Join(parent, filepath.Base(clean)), nil
	}
	return resolvePhysicalPath(clean)
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
