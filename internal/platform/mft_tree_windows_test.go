//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestNTFSFilterBackedFilesUseProjectedNativeMetadata(t *testing.T) {
	for _, tag := range []uint32{0x80000017, 0x9000001a, windowsReparseTagDedup, 0x8000fffe} {
		t.Run(fmt.Sprintf("tag=%x", tag), func(t *testing.T) {
			for _, reference := range []bool{false, true} {
				root := &ntfsLayoutFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY}
				file := &ntfsLayoutFile{reference: 30, attributes: windows.FILE_ATTRIBUTE_REPARSE_POINT, names: []ntfsLayoutName{{5, "projected"}}, hasReparseTag: true, reparseTag: tag, logical: 0, allocated: 0}
				entry := DirectoryEntry{DirEntry: windowsBatchDirEntry{windowsBatchFileInfo{name: "projected", size: 5000, mode: 0666}}, Usage: FileUsage{Identity: FileIdentity{Volume: 123, Low: 30}, HasIdentity: true, AllocatedSize: 4096}, HasUsage: true}
				base := &ntfsCurrentDirectoryFS{reads: make(map[string]int), entries: map[string][]DirectoryEntry{`C:\`: {entry}}}
				records := map[uint64]*ntfsLayoutFile{5: root, 30: file}
				var fs ScannerFilesystem
				var err error
				if reference {
					fs, err = indexNTFSReferences(base, `C:\`, 123, records, func() error { return nil })
				} else {
					fs, err = indexNTFSLayout(base, `C:\`, 123, records, func() error { return nil })
				}
				if err != nil {
					t.Fatal(err)
				}
				entries, err := fs.ReadDir(`C:\`)
				if err != nil || len(entries) != 1 {
					t.Fatalf("native projection missing: %+v %v", entries, err)
				}
				info, _ := entries[0].Info()
				if !info.Mode().IsRegular() || info.Size() != 5000 || entries[0].Usage.AllocatedSize != 4096 || base.reads[`C:\`] != 1 {
					t.Fatalf("raw filter metadata escaped: %+v %+v", info, entries[0])
				}
			}
		})
	}
}

func TestNTFSReferenceDirectoriesMatchPathIndex(t *testing.T) {
	records := ntfsFixtureRecords(t,
		layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
		layoutTestFile{reference: 20, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "folder", 1}}},
		layoutTestFile{reference: 11, attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
		layoutTestFile{reference: 21, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{11, "internal", 1}}},
		layoutTestFile{reference: 30, names: []layoutTestName{{5, "first", 1}, {20, "alias", 1}, {20, "alias", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 5000, 8192}}},
		layoutTestFile{reference: 31, names: []layoutTestName{{21, "internal-file", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 100, 4096}}},
	)
	base := &ntfsNativeFallbackFS{}
	flat, err := indexNTFSReferences(base, `C:\`, 123, records, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	old, err := indexNTFSLayout(base, `C:\`, 123, records, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{`C:\`, `C:\folder`} {
		got, err := flat.ReadDir(path)
		if err != nil {
			t.Fatal(err)
		}
		want, err := old.ReadDir(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("different directory contents: %+v %+v", got, want)
		}
		for i := range got {
			a, _ := got[i].Info()
			b, _ := want[i].Info()
			if got[i].Name() != want[i].Name() || a.Size() != b.Size() || a.Mode() != b.Mode() || got[i].Usage != want[i].Usage {
				t.Fatalf("directory metadata differs: %+v %+v", got[i], want[i])
			}
			if got[i].IsDir() && got[i].Directory == nil {
				t.Fatal("child cursor missing")
			}
		}
	}
	if flat.duplicateNames != 1 || base.calls != 0 {
		t.Fatalf("flat traversal required paths/native reads: %+v", flat)
	}
	if _, err := indexNTFSReferences(base, `C:\`, 123, records, func() error { return context.Canceled }); err != context.Canceled {
		t.Fatal("linking ignored cancellation")
	}
}

func TestNTFSReferenceAncestryValidation(t *testing.T) {
	for _, mode := range []string{"stale", "cycle", "missing", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			records := ntfsFixtureRecords(t, layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY}, layoutTestFile{reference: 20, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "folder", 1}}})
			switch mode {
			case "stale":
				records[20].names[0].parent = 5 | 1<<48
			case "cycle":
				records[20].names[0].parent = 20
			case "missing":
				delete(records, 5)
			case "ambiguous":
				records[20].names = append(records[20].names, ntfsLayoutName{5, "other"})
			}
			if _, err := indexNTFSReferences(Default{}, `C:\`, 123, records, func() error { return nil }); err == nil {
				t.Fatal("published invalid ancestry")
			}
		})
	}
}

func TestNTFSReferenceConflictSubtreesStayNative(t *testing.T) {
	records := ntfsFixtureRecords(t,
		layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
		layoutTestFile{reference: 20, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "replaced", 1}}},
		layoutTestFile{reference: 21, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "replaced", 1}}},
		layoutTestFile{reference: 22, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{20, "descendant", 1}}},
		layoutTestFile{reference: 30, names: []layoutTestName{{22, "stale", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 10, 4096}}},
	)
	dirEntry := func(name string, id uint64) DirectoryEntry {
		return DirectoryEntry{DirEntry: windowsBatchDirEntry{windowsBatchFileInfo{name: name, mode: os.ModeDir | 0755}}, Usage: FileUsage{Identity: FileIdentity{Volume: 123, Low: id}, HasIdentity: true}, HasUsage: true}
	}
	base := &ntfsCurrentDirectoryFS{reads: make(map[string]int), entries: map[string][]DirectoryEntry{
		`C:\`: {dirEntry("replaced", 20)}, `C:\replaced`: {dirEntry("descendant", 22)}, `C:\replaced\descendant`: {{DirEntry: windowsBatchDirEntry{windowsBatchFileInfo{name: "current", mode: 0666}}}},
	}}
	flat, err := indexNTFSReferences(base, `C:\`, 123, records, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	cursor := flat.RootScanDirectory(`C:\`)
	for _, path := range []string{`C:\`, `C:\replaced`} {
		entries, _, _, err := cursor.ReadScanDirectory(path, false)
		if err != nil || len(entries) != 1 || entries[0].Directory == nil {
			t.Fatalf("native cursor missing: %+v %v", entries, err)
		}
		cursor = entries[0].Directory
	}
	entries, _, _, err := cursor.ReadScanDirectory(`C:\replaced\descendant`, false)
	if err != nil || len(entries) != 1 || entries[0].Name() != "current" || len(base.reads) != 3 || flat.reconciledDirectories != 1 || flat.reconciledSubtrees != 1 {
		t.Fatalf("stale subtree leaked: %+v %v %+v", entries, err, base.reads)
	}
}

func TestNTFSReferenceNativeChildrenRequireTrustedMatchingIDs(t *testing.T) {
	for _, mode := range []string{"match", "unknown", "untrusted"} {
		t.Run(mode, func(t *testing.T) {
			records := ntfsFixtureRecords(t, layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY}, layoutTestFile{reference: 20, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "folder", 1}}}, layoutTestFile{reference: 30, names: []layoutTestName{{20, "indexed", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 10, 4096}}})
			records[31] = &ntfsLayoutFile{reference: 31, attributes: windows.FILE_ATTRIBUTE_REPARSE_POINT, names: []ntfsLayoutName{{5, "filtered"}}, hasReparseTag: true, reparseTag: 0x80000017}
			entry := DirectoryEntry{DirEntry: windowsBatchDirEntry{windowsBatchFileInfo{name: "folder", mode: os.ModeDir | 0755}}, Usage: FileUsage{Identity: FileIdentity{Volume: 123, Low: 20}, HasIdentity: true}, HasUsage: true}
			if mode == "unknown" {
				entry.Usage.Identity.Low = 100
			}
			if mode == "untrusted" {
				entry.Usage.IdentityNeedsConfirmation = true
			}
			base := &ntfsCurrentDirectoryFS{reads: make(map[string]int), entries: map[string][]DirectoryEntry{`C:\`: {entry}, `C:\folder`: {{DirEntry: windowsBatchDirEntry{windowsBatchFileInfo{name: "native", mode: 0666}}}}}}
			flat, err := indexNTFSReferences(base, `C:\`, 123, records, func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			entries, _, _, err := flat.root.ReadScanDirectory(`C:\`, true)
			if err != nil {
				t.Fatal(err)
			}
			children, _, _, err := entries[0].Directory.ReadScanDirectory(`C:\folder`, false)
			want := "native"
			if mode == "match" {
				want = "indexed"
			}
			if err != nil || len(children) != 1 || children[0].Name() != want || base.timed != 1 {
				t.Fatalf("wrong native-child scope: %+v %v", children, err)
			}
		})
	}
}

func BenchmarkNTFSDirectoryLinking(b *testing.B) {
	records := map[uint64]*ntfsLayoutFile{5: {reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY}}
	for i := uint64(20); i < 10020; i++ {
		records[i] = &ntfsLayoutFile{reference: i, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []ntfsLayoutName{{5, fmt.Sprintf("directory-%d", i)}}}
		for j := uint64(0); j < 5; j++ {
			id := 10020 + (i-20)*5 + j
			records[id] = &ntfsLayoutFile{reference: id, names: []ntfsLayoutName{{i, fmt.Sprintf("file-%d", j)}}, logical: 5000, allocated: 8192}
		}
	}
	check := func() error { return nil }
	b.Run("path-index", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := indexNTFSLayout(Default{}, `C:\`, 123, records, check); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("reference-index", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := indexNTFSReferences(Default{}, `C:\`, 123, records, check); err != nil {
				b.Fatal(err)
			}
		}
	})
}
