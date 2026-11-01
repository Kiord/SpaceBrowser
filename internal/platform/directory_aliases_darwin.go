//go:build darwin

package platform

import "os"

func (Darwin) DirectoryAliases() []DirectoryAlias {
	contents, err := os.ReadFile("/usr/share/firmlinks")
	if err != nil {
		// Older systems without firmlinks retain ordinary traversal.
		return nil
	}
	aliases := parseFirmlinkAliases(string(contents), "/", "/System/Volumes/Data")
	verified := aliases[:0]
	for _, alias := range aliases {
		preferred, preferredErr := os.Stat(alias.Preferred)
		backing, backingErr := os.Stat(alias.Backing)
		// Do not hide unrelated paths on an unusual or partially mounted system.
		if preferredErr == nil && backingErr == nil && preferred.IsDir() && backing.IsDir() && os.SameFile(preferred, backing) {
			verified = append(verified, alias)
		}
	}
	return verified
}
