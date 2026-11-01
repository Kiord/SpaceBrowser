package platform

import (
	"path"
	"path/filepath"
	"strings"
)

// DirectoryAlias names two paths to the same directory. Preferred is the
// user-visible path; Backing is its duplicate in the underlying volume.
type DirectoryAlias struct {
	Preferred string
	Backing   string
}

// DirectoryAliasProvider is optional: most filesystems do not expose a second
// directory hierarchy independently of symbolic links.
type DirectoryAliasProvider interface {
	DirectoryAliases() []DirectoryAlias
}

func parseFirmlinkAliases(contents, systemRoot, dataRoot string) []DirectoryAlias {
	var aliases []DirectoryAlias
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		source := strings.TrimPrefix(fields[0], "/")
		target := fields[1]
		// The table uses POSIX paths, including when parsed in portable tests.
		if source == "." || target == "." || path.Clean(source) != source || path.Clean(target) != target ||
			!filepath.IsLocal(filepath.FromSlash(source)) || !filepath.IsLocal(filepath.FromSlash(target)) {
			continue
		}
		aliases = append(aliases, DirectoryAlias{
			Preferred: filepath.Join(systemRoot, filepath.FromSlash(source)),
			Backing:   filepath.Join(dataRoot, filepath.FromSlash(target)),
		})
	}
	return aliases
}
