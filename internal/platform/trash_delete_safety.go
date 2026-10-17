package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Keep deletion relative to an open Trash directory. Root operations enforce
// containment even if a descendant link changes after validation.
func removeTrashPaths(root string, dataPaths, metadataPaths []string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || trashPathIsLink(info) {
		return fmt.Errorf("Trash directory must not be a link or reparse point")
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	relativePaths := make([]string, 0, len(dataPaths)+len(metadataPaths))
	for _, path := range append(append([]string{}, dataPaths...), metadataPaths...) {
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || !filepath.IsLocal(relative) {
			return fmt.Errorf("deletion target is outside the Trash data directory")
		}
		parts := strings.Split(relative, string(filepath.Separator))
		for i := 1; i < len(parts); i++ {
			ancestor := filepath.Join(parts[:i]...)
			info, err := directory.Lstat(ancestor)
			if err != nil {
				return fmt.Errorf("inspect Trash ancestor: %w", err)
			}
			if !info.IsDir() || trashPathIsLink(info) {
				return fmt.Errorf("deletion through a Trash link or reparse point is not allowed")
			}
		}
		relativePaths = append(relativePaths, relative)
	}
	for i, relative := range relativePaths {
		if i < len(dataPaths) {
			err = directory.RemoveAll(relative)
		} else {
			err = directory.Remove(relative)
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("permanently delete Trash item: %w", err)
		}
	}
	return nil
}
