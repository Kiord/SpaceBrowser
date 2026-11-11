//go:build windows

package platform

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
)

// The direct reader retains parent/child references, not a second tree of full
// paths. Paths are constructed once, by the application's ordinary tree builder.
// Its exclusions, hard-link accounting, aggregation and previews stay shared.
type ntfsReferenceFilesystem struct {
	ScannerFilesystem
	rootPath                                                  string
	serial                                                    uint64
	directories                                               map[uint64]*ntfsReferenceDirectory
	root                                                      *ntfsReferenceDirectory
	native                                                    ntfsNativeScanDirectory
	duplicateNames, reconciledDirectories, reconciledSubtrees int64
}

type ntfsReferenceDirectory struct {
	filesystem                                  *ntfsReferenceFilesystem
	file                                        *ntfsLayoutFile
	children                                    []ntfsLayoutChild
	state                                       uint8
	depth, pathBytes                            int
	included, native, nativeSubtree, reconciled bool
}

func indexNTFSReferences(base ScannerFilesystem, root string, serial uint64, records map[uint64]*ntfsLayoutFile, check func() error) (*ntfsReferenceFilesystem, error) {
	f := &ntfsReferenceFilesystem{ScannerFilesystem: base, rootPath: base.Canonicalize(root), serial: serial, directories: make(map[uint64]*ntfsReferenceDirectory, len(records)/4)}
	f.native.filesystem = f
	steps := 0
	var cost int64
	checkpoint := func() error {
		steps++
		if steps%1024 == 0 {
			if cost > maxNTFSIndexBytes {
				return fmt.Errorf("NTFS reference index exceeds the estimated 512 MiB safety budget")
			}
			return check()
		}
		return nil
	}
	if err := check(); err != nil {
		return nil, err
	}
	for ref, file := range records {
		if err := checkpoint(); err != nil {
			return nil, err
		}
		if len(file.names) > 1 {
			slices.SortFunc(file.names, func(a, b ntfsLayoutName) int {
				if order := cmp.Compare(a.parent, b.parent); order != 0 {
					return order
				}
				return strings.Compare(a.name, b.name)
			})
			before := len(file.names)
			file.names = slices.Compact(file.names)
			f.duplicateNames += int64(before - len(file.names))
		}
		cost += ntfsLayoutIndexCost(file)
		if file.attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			continue
		}
		directory := &ntfsReferenceDirectory{filesystem: f, file: file}
		f.directories[ref] = directory
		cost += 144
		if ref&ntfsReferenceMask == ntfsRootRecord {
			if f.root != nil {
				return nil, fmt.Errorf("NTFS root reference changed during enumeration")
			}
			f.root = directory
			directory.state = 2
			directory.included = true
			directory.pathBytes = len(f.rootPath)
		} else if ref&ntfsReferenceMask < 16 {
			directory.state = 2
		}
	}
	if f.root == nil {
		return nil, fmt.Errorf("NTFS file table did not contain the root directory")
	}
	// Validate ancestry and sequence numbers in linear time, without joining
	// every directory's path or retaining a path-to-directory lookup map.
	chain := make([]*ntfsReferenceDirectory, 0, 32)
	for _, directory := range f.directories {
		if err := checkpoint(); err != nil {
			return nil, err
		}
		if directory.state == 2 || len(directory.file.names) == 0 {
			continue
		}
		chain = chain[:0]
		current := directory
		for current.state != 2 {
			if current.state == 1 || len(chain) >= 1024 {
				return nil, fmt.Errorf("cyclic or excessively deep NTFS ancestry")
			}
			if len(current.file.names) != 1 {
				return nil, fmt.Errorf("NTFS directory ancestry changed or is unsupported")
			}
			current.state = 1
			chain = append(chain, current)
			parent := current.file.names[0].parent
			current = f.directories[parent]
			if current == nil {
				return nil, fmt.Errorf("missing or stale NTFS parent directory %#x", parent)
			}
		}
		for i := len(chain) - 1; i >= 0; i-- {
			next := chain[i]
			next.state = 2
			next.included = current.included
			next.depth = current.depth + 1
			next.pathBytes = current.pathBytes + 1 + len(next.file.names[0].name)
			if next.depth > 1024 || next.pathBytes > 32760 {
				return nil, fmt.Errorf("NTFS directory exceeds path/depth limits")
			}
			current = next
		}
	}
	for ref, file := range records {
		if err := checkpoint(); err != nil {
			return nil, err
		}
		if ref&ntfsReferenceMask < 16 {
			continue
		}
		for _, name := range file.names {
			if name.name == "." || name.name == ".." || strings.ContainsAny(name.name, `\/:`) {
				return nil, fmt.Errorf("invalid NTFS child name")
			}
			parent := f.directories[name.parent]
			if parent == nil || parent.state != 2 {
				return nil, fmt.Errorf("missing or unresolved NTFS parent %#x", name.parent)
			}
			if !parent.included {
				continue
			}
			parent.children = append(parent.children, ntfsLayoutChild{file, name.name})
			parent.native = parent.native || file.needsNativeMetadata()
			cost += 24
		}
	}
	if cost > maxNTFSIndexBytes {
		return nil, fmt.Errorf("NTFS reference index exceeds the estimated 512 MiB safety budget")
	}
	for _, directory := range f.directories {
		if err := checkpoint(); err != nil {
			return nil, err
		}
		slices.SortFunc(directory.children, func(a, b ntfsLayoutChild) int {
			if order := strings.Compare(a.name, b.name); order != 0 {
				return order
			}
			return cmp.Compare(a.file.reference, b.file.reference)
		})
		for i := 0; i < len(directory.children); {
			end := i + 1
			for end < len(directory.children) && directory.children[end].name == directory.children[i].name {
				end++
			}
			if end-i > 1 {
				// A conflicting child directory must use native reads throughout
				// its subtree, even if a former descendant still has valid IDs.
				if !directory.native {
					directory.native = true
				}
				if !directory.reconciled {
					directory.reconciled = true
					f.reconciledDirectories++
				}
				subtree := false
				for _, child := range directory.children[i:end] {
					if d := f.directories[child.file.reference]; d != nil {
						d.nativeSubtree = true
						subtree = true
					}
				}
				if subtree {
					f.reconciledSubtrees++
				}
			}
			i = end
		}
	}
	return f, nil
}

func (f *ntfsReferenceFilesystem) RootScanDirectory(path string) ScanDirectory {
	if f.Canonicalize(path) != f.rootPath {
		return nil
	}
	return f.root
}

func (d *ntfsReferenceDirectory) ReadScanDirectory(path string, timing bool) ([]DirectoryEntry, *DirectoryReadDiagnostic, *DirectoryReadTimings, error) {
	f := d.filesystem
	if d.nativeSubtree {
		return f.native.ReadScanDirectory(path, timing)
	}
	if d.native {
		entries, diagnostic, profile, err := f.readNative(path, timing)
		for i := range entries {
			entry := &entries[i]
			if !entry.IsDir() {
				continue
			}
			entry.Directory = &f.native
			usage := entry.Usage
			if !usage.HasIdentity || usage.IdentityNeedsConfirmation || usage.Identity.Volume != f.serial || usage.Identity.High != 0 {
				continue
			}
			child := f.directories[usage.Identity.Low]
			if child != nil && len(child.file.names) == 1 && child.file.names[0].parent == d.file.reference && child.file.names[0].name == entry.Name() {
				entry.Directory = child
			}
		}
		return entries, diagnostic, profile, err
	}
	entries := make([]DirectoryEntry, len(d.children))
	for i, child := range d.children {
		entries[i] = ntfsDirectoryEntry(child, f.serial)
		if entries[i].IsDir() {
			if next := f.directories[child.file.reference]; next != nil {
				entries[i].Directory = next
			} else {
				entries[i].Directory = &f.native
			}
		}
	}
	return entries, nil, nil, nil
}

func (f *ntfsReferenceFilesystem) readNative(path string, timing bool) ([]DirectoryEntry, *DirectoryReadDiagnostic, *DirectoryReadTimings, error) {
	if timing {
		return ReadDirWithTimings(f.ScannerFilesystem, path)
	}
	entries, diagnostic, err := ReadDirWithDiagnostics(f.ScannerFilesystem, path)
	return entries, diagnostic, nil, err
}

type ntfsNativeScanDirectory struct{ filesystem *ntfsReferenceFilesystem }

func (d *ntfsNativeScanDirectory) ReadScanDirectory(path string, timing bool) ([]DirectoryEntry, *DirectoryReadDiagnostic, *DirectoryReadTimings, error) {
	entries, diagnostic, profile, err := d.filesystem.readNative(path, timing)
	for i := range entries {
		if entries[i].IsDir() {
			entries[i].Directory = d
		}
	}
	return entries, diagnostic, profile, err
}

// Path reads remain available for callers outside the scanner. Resolve only the
// requested route; the full-drive builder uses identity cursors above instead.
func (f *ntfsReferenceFilesystem) cursorForPath(path string) ScanDirectory {
	clean := f.Canonicalize(path)
	if clean == f.rootPath {
		return f.root
	}
	rel, err := filepath.Rel(f.rootPath, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, `..\`) {
		return &f.native
	}
	current := f.root
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		if current.native || current.nativeSubtree {
			return &f.native
		}
		var child *ntfsReferenceDirectory
		for _, entry := range current.children {
			if entry.name == name {
				child = f.directories[entry.file.reference]
				break
			}
		}
		if child == nil {
			return &f.native
		}
		current = child
	}
	return current
}

func (f *ntfsReferenceFilesystem) ReadDir(path string) ([]DirectoryEntry, error) {
	entries, _, err := f.ReadDirWithDiagnostics(path)
	return entries, err
}
func (f *ntfsReferenceFilesystem) ReadDirWithDiagnostics(path string) ([]DirectoryEntry, *DirectoryReadDiagnostic, error) {
	entries, diagnostic, _, err := f.cursorForPath(path).ReadScanDirectory(path, false)
	return entries, diagnostic, err
}
func (f *ntfsReferenceFilesystem) ReadDirWithTimings(path string) ([]DirectoryEntry, *DirectoryReadDiagnostic, *DirectoryReadTimings, error) {
	return f.cursorForPath(path).ReadScanDirectory(path, true)
}
