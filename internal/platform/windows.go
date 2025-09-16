//go:build windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Windows struct{ Default }

func (Windows) BaseName(p string) string {
	b := filepath.Base(p)
	if b == "." || b == string(os.PathSeparator) || b == "" {
		if vol := filepath.VolumeName(p); vol != "" {
			return vol + `\`
		}
	}
	return b
}

// Canonicalize turns a user input path into a scanning-safe, OS-correct path.
// Key behavior: "D:" -> "D:\" (drive root), strip \\?\ for logic, normalize slashes.
func (Windows) Canonicalize(p string) string {
	p = strings.TrimSpace(p)

	// Strip extended prefix for logic; the scanner doesn't need it.
	if strings.HasPrefix(p, `\\?\`) {
		p = p[4:]
	}

	// Normalize slashes
	p = strings.ReplaceAll(p, "/", `\`)

	// Bare drive letter? Make it the drive root.
	if len(p) == 2 && p[1] == ':' && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		p += `\`
	}

	// UNC paths are fine; for everything else, clean (not Abs!)
	return filepath.Clean(p)
}

func (w Windows) IsMountRoot(p string) bool {
	// Compare against canonicalized input (no Abs; Abs would pick the drive's current dir)
	clean := w.Canonicalize(p)

	// Drive root like "D:\"
	if vol := filepath.VolumeName(clean); vol != "" {
		root := vol + `\`
		return strings.EqualFold(clean, root)
	}

	// UNC share root: "\\server\share" (two components)
	if strings.HasPrefix(clean, `\\`) {
		parts := strings.Split(clean, `\`)
		// ["", "", "server", "share"] or with trailing slash
		if len(parts) == 4 || (len(parts) == 5 && parts[4] == "") {
			return true
		}
	}
	return false
}

func (Windows) OpenInFileBrowser(p string) error {
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return exec.Command("explorer", "/select,", p).Run()
	}
	return exec.Command("explorer", p).Run()
}

func (Windows) DefaultStartPath() string {
	drv := os.Getenv("SystemDrive") // typically "C:"
	if drv == "" {
		drv = "C:"
	}
	p := drv + `\` // ensure root, avoids "D:" current-dir semantics
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p
	}
	// Fallback to C:\ even if SystemDrive was odd/missing
	return `C:\`
}

func init() { Impl = Windows{} }
