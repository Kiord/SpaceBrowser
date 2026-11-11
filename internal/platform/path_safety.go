package platform

import (
	"fmt"
	"path/filepath"
)

// RequireAbsolutePath must run before interpreting a stored filesystem target.
// Resolving it here would guess the working directory of the original scan.
func RequireAbsolutePath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("filesystem actions require an absolute path; rescan this location")
	}
	return nil
}
