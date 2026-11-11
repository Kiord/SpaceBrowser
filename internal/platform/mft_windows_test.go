//go:build windows

package platform

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNTFSLayoutBatchContinuationAndBufferGrowth(t *testing.T) {
	first := layoutTestBatch(layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, ".", 1}}})
	second := layoutTestBatch(layoutTestFile{reference: 30, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 5000, 8192}}})
	calls := 0
	records, stats, err := collectNTFSLayout(func(input, output []byte) (uint32, error) {
		calls++
		restart := binary.LittleEndian.Uint32(input[4:])&queryLayoutRestart != 0
		if restart != (calls <= 2) {
			t.Fatalf("incorrect restart on call %d", calls)
		}
		if uintptr(unsafe.Pointer(&input[0]))%8 != 0 || uintptr(unsafe.Pointer(&output[0]))%8 != 0 {
			t.Fatal("unaligned control buffers")
		}
		switch calls {
		case 1:
			return 0, windows.ERROR_INSUFFICIENT_BUFFER
		case 2:
			if len(output) != 8<<20 {
				t.Fatal("buffer did not grow")
			}
			copy(output, first)
			return uint32(len(first)), nil
		case 3:
			copy(output, second)
			return uint32(len(second)), nil
		default:
			return 0, windows.ERROR_HANDLE_EOF
		}
	}, func() error { return nil }, nil)
	if err != nil || len(records) != 2 || calls != 4 || stats.Records != 2 || stats.Batches != 2 || stats.Bytes != int64(len(first)+len(second)) {
		t.Fatalf("bad continuation: %+v, %d, %v", stats, calls, err)
	}
}

func TestNTFSLayoutBatchFailuresAreNotPartialSuccess(t *testing.T) {
	batch := layoutTestBatch(layoutTestFile{reference: 30, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 1, 0}}})
	for _, mode := range []string{"cancel", "duplicate", "read-error", "empty", "length"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			records, _, err := collectNTFSLayout(func(_, output []byte) (uint32, error) {
				call := calls.Add(1)
				if mode == "read-error" && call == 2 {
					return 0, windows.ERROR_ACCESS_DENIED
				}
				if mode == "empty" {
					return 16, nil
				}
				if mode == "length" {
					return uint32(len(output) + 1), nil
				}
				copy(output, batch)
				return uint32(len(batch)), nil
			}, func() error {
				if mode == "cancel" && calls.Load() > 0 {
					return context.Canceled
				}
				return nil
			}, nil)
			if err == nil || records != nil {
				t.Fatalf("published partial index: %+v, %v", records, err)
			}
		})
	}
}

func TestNTFSLayoutBatchReadAheadPreservesConsumerBuffer(t *testing.T) {
	secondStarted := make(chan struct{}, 1)
	thirdStarted := make(chan struct{}, 1)
	calls := 0
	batches := newNTFSLayoutBatches(func(_, output []byte) (uint32, error) {
		calls++
		switch calls {
		case 1:
			output[0] = 1
		case 2:
			output[0] = 2
			secondStarted <- struct{}{}
		default:
			thirdStarted <- struct{}{}
			return 0, windows.ERROR_HANDLE_EOF
		}
		return 1, nil
	}, nil)
	defer batches.close()
	receive := func() ntfsLayoutBatch {
		t.Helper()
		select {
		case batch, ok := <-batches.results:
			if !ok {
				t.Fatal("query worker stopped early")
			}
			return batch
		case <-time.After(2 * time.Second):
			t.Fatal("query worker stalled")
			return ntfsLayoutBatch{}
		}
	}
	first := receive()
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("next query did not start while the consumer held the first batch")
	}
	second := receive()
	if first.buffer[0] != 1 || second.buffer[0] != 2 || &first.buffer[0] == &second.buffer[0] {
		t.Fatal("read-ahead overwrote a buffer still owned by the consumer")
	}
	select {
	case <-thirdStarted:
		t.Fatal("query advanced without an available output buffer")
	default:
	}
	batches.release(first)
	last := receive()
	if !errors.Is(last.err, windows.ERROR_HANDLE_EOF) {
		t.Fatalf("missing terminal response: %v", last.err)
	}
	batches.release(second)
}

func TestNTFSLayoutBatchCloseJoinsPendingQuery(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	queryFinished := make(chan struct{})
	batches := newNTFSLayoutBatches(func(_, _ []byte) (uint32, error) {
		close(started)
		<-finish
		close(queryFinished)
		return 0, windows.ERROR_HANDLE_EOF
	}, nil)
	<-started
	closed := make(chan struct{})
	go func() {
		batches.close()
		close(closed)
	}()
	// The query owns its buffers and volume handle until it returns.
	select {
	case <-closed:
		t.Error("close returned while a query was still pending")
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("query worker did not stop after its pending call returned")
	}
	select {
	case <-queryFinished:
	default:
		t.Fatal("close did not join the query")
	}
}

type layoutTestName struct {
	parent uint64
	name   string
	flags  uint32
}
type layoutTestStream struct {
	identifier         string
	attribute          uint32
	logical, allocated int64
}
type layoutTestFile struct {
	reference  uint64
	attributes uint32
	names      []layoutTestName
	streams    []layoutTestStream
}

func layoutTestUTF16(value string) []byte {
	words := utf16.Encode([]rune(value))
	data := make([]byte, len(words)*2)
	for i, word := range words {
		binary.LittleEndian.PutUint16(data[i*2:], word)
	}
	return data
}

func layoutTestBatch(files ...layoutTestFile) []byte {
	data := make([]byte, 16)
	binary.LittleEndian.PutUint32(data, uint32(len(files)))
	binary.LittleEndian.PutUint32(data[4:], 16)
	binary.LittleEndian.PutUint32(data[8:], 1)
	for i, file := range files {
		entry := make([]byte, 96) // file header and pre-RS5 extra info
		binary.LittleEndian.PutUint32(entry, 1)
		binary.LittleEndian.PutUint32(entry[12:], file.attributes)
		binary.LittleEndian.PutUint64(entry[16:], file.reference)
		binary.LittleEndian.PutUint32(entry[32:], 40)
		binary.LittleEndian.PutUint64(entry[56:], 133000000000000000)
		binary.LittleEndian.PutUint32(entry[72:], file.attributes)
		for j, name := range file.names {
			if j == 0 {
				binary.LittleEndian.PutUint32(entry[24:], uint32(len(entry)))
			}
			encoded := layoutTestUTF16(name.name)
			size := (24 + len(encoded) + 7) &^ 7
			value := make([]byte, size)
			if j != len(file.names)-1 {
				binary.LittleEndian.PutUint32(value, uint32(size))
			}
			binary.LittleEndian.PutUint32(value[4:], name.flags)
			binary.LittleEndian.PutUint64(value[8:], name.parent)
			binary.LittleEndian.PutUint32(value[16:], uint32(len(encoded)))
			copy(value[24:], encoded)
			entry = append(entry, value...)
		}
		for j, stream := range file.streams {
			if j == 0 {
				binary.LittleEndian.PutUint32(entry[28:], uint32(len(entry)))
			}
			encoded := layoutTestUTF16(stream.identifier)
			size := (48 + len(encoded) + 7) &^ 7
			value := make([]byte, size)
			binary.LittleEndian.PutUint32(value, 2)
			if j != len(file.streams)-1 {
				binary.LittleEndian.PutUint32(value[4:], uint32(size))
			}
			binary.LittleEndian.PutUint64(value[16:], uint64(stream.allocated))
			binary.LittleEndian.PutUint64(value[24:], uint64(stream.logical))
			binary.LittleEndian.PutUint32(value[36:], stream.attribute)
			binary.LittleEndian.PutUint32(value[44:], uint32(len(encoded)))
			copy(value[48:], encoded)
			entry = append(entry, value...)
		}
		if i != len(files)-1 {
			binary.LittleEndian.PutUint32(entry[4:], uint32(len(entry)))
		}
		data = append(data, entry...)
	}
	return data
}

func TestNTFSLayoutDecodesNamesSizesAndStreams(t *testing.T) {
	data := layoutTestBatch(layoutTestFile{reference: 0x9000000000020, attributes: windows.FILE_ATTRIBUTE_SPARSE_FILE | windows.FILE_ATTRIBUTE_HIDDEN,
		names:   []layoutTestName{{5, "large file.dat", 1}, {5, "LARGEF~1.DAT", 2}, {30, "another.dat", 0}},
		streams: []layoutTestStream{{":secret:$DATA", 0x80, 99999, 131072}, {"::$DATA", 0x80, 1 << 34, 8192}, {":$I30:$INDEX_ALLOCATION", 0xa0, 900, 4096}},
	})
	files, err := decodeNTFSLayout(data)
	if err != nil {
		t.Fatal(err)
	}
	file := files[0]
	if file.reference != 0x9000000000020 || file.logical != 1<<34 || file.allocated != 8192 || len(file.names) != 2 || file.names[1].parent != 30 {
		t.Fatalf("incorrect metadata: %+v", file)
	}
	if file.write != 133000000000000000 {
		t.Fatalf("timestamp: %d", file.write)
	}
	// Resident/empty data streams still exist and can have zero allocation.
	files, err = decodeNTFSLayout(layoutTestBatch(layoutTestFile{reference: 40, names: []layoutTestName{{5, "resident", 3}}, streams: []layoutTestStream{{"", 0x80, 42, 0}}}))
	if err != nil || files[0].logical != 42 || files[0].allocated != 0 || len(files[0].names) != 1 {
		t.Fatalf("resident stream: %+v, %v", files, err)
	}
}

func TestNTFSLayoutRejectsMalformedResponses(t *testing.T) {
	valid := layoutTestBatch(layoutTestFile{reference: 30, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 123, 4096}}})
	for end := 0; end < len(valid)-8; end++ {
		if _, err := decodeNTFSLayout(valid[:end]); err == nil {
			t.Fatalf("accepted truncation at %d", end)
		}
	}
	mutations := []func([]byte){
		func(b []byte) { binary.LittleEndian.PutUint32(b, 0xffffffff) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[16:], 2) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[20:], 1) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[40:], 0xffffffff) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[112:], 1) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[128:], 3) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[52:], 0xffffffff) },
	}
	for i, mutate := range mutations {
		data := append([]byte(nil), valid...)
		mutate(data)
		if _, err := decodeNTFSLayout(data); err == nil {
			t.Fatalf("accepted malformed case %d", i)
		}
	}
}

func TestNTFSLayoutLegacyAndCurrentStreamVersions(t *testing.T) {
	for _, version := range []uint32{1, 2} {
		for _, identifier := range []string{"", "::$DATA"} {
			t.Run(fmt.Sprintf("v%d/%s", version, identifier), func(t *testing.T) {
				data := layoutTestBatch(layoutTestFile{reference: 30, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{":ads:$DATA", 0x80, 999, 4096}, {identifier, 0x80, 1 << 34, 8192}, {"::$STANDARD_INFORMATION", 0x10, 72, 0}}})
				position := 16 + int(binary.LittleEndian.Uint32(data[44:]))
				for {
					binary.LittleEndian.PutUint32(data[position:], version)
					if version == 1 {
						// The v1 SDK declares all eight bytes reserved. No type
						// code may be inferred from their contents.
						binary.LittleEndian.PutUint64(data[position+32:], 0xfedcba9876543210)
					}
					next := int(binary.LittleEndian.Uint32(data[position+4:]))
					if next == 0 {
						break
					}
					position += next
				}
				files, err := decodeNTFSLayout(data)
				if err != nil {
					t.Fatal(err)
				}
				if len(files) != 1 || files[0].native || files[0].logical != 1<<34 || files[0].allocated != 8192 {
					t.Fatalf("stream sizes were not preserved: %+v", files)
				}
			})
		}
	}
	// v2 may leave AttributeTypeCode unset. An explicit default-data identifier
	// still distinguishes it from metadata streams and named alternate streams.
	data := layoutTestBatch(layoutTestFile{reference: 30, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{"::$DATA", 0, 100, 4096}}})
	files, err := decodeNTFSLayout(data)
	if err != nil || files[0].allocated != 4096 || files[0].native {
		t.Fatalf("default data without type: %+v, %v", files, err)
	}
}

func TestNTFSLayoutOptionalMetadataUsesNativeParent(t *testing.T) {
	for _, mode := range []string{"extra-info", "short-extra-info", "default-stream"} {
		t.Run(mode, func(t *testing.T) {
			data := layoutTestBatch(layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY}, layoutTestFile{reference: 30, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 100, 4096}}})
			filePosition := 16 + int(binary.LittleEndian.Uint32(data[20:]))
			if mode == "extra-info" {
				binary.LittleEndian.PutUint32(data[filePosition+32:], 0)
			} else if mode == "short-extra-info" {
				binary.LittleEndian.PutUint32(data[filePosition+36:], 16)
			} else {
				binary.LittleEndian.PutUint32(data[filePosition+28:], 0)
			}
			files, err := decodeNTFSLayout(data)
			if err != nil {
				t.Fatal(err)
			}
			if !files[1].native {
				t.Fatal("optional metadata was silently replaced by zeros")
			}
			records := map[uint64]*ntfsLayoutFile{5: files[0], 30: files[1]}
			base := &ntfsNativeFallbackFS{}
			indexed, err := indexNTFSLayout(base, `C:\`, 123, records, func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = indexed.ReadDir(`C:\`); err == nil || base.calls != 1 {
				t.Fatalf("missing metadata was not resolved natively: %v, %d", err, base.calls)
			}
		})
	}
}

func TestNTFSLayoutReadsOnlyAvailableBasicInformation(t *testing.T) {
	for _, length := range []uint32{0, 36, 40, 56} {
		data := layoutTestBatch(layoutTestFile{reference: 30, attributes: windows.FILE_ATTRIBUTE_HIDDEN, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 1, 0}}})
		binary.LittleEndian.PutUint32(data[52:], length)
		files, err := decodeNTFSLayout(data)
		if err != nil || files[0].native || files[0].attributes != windows.FILE_ATTRIBUTE_HIDDEN || files[0].write != 133000000000000000 {
			t.Fatalf("basic information length %d: %+v %v", length, files, err)
		}
	}
}

func TestNTFSLayoutMetadataNeedNotPrecedeNextFileHeader(t *testing.T) {
	first := layoutTestBatch(layoutTestFile{reference: 30, names: []layoutTestName{{5, "first", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 1, 4096}}})[16:]
	second := layoutTestBatch(layoutTestFile{reference: 31, names: []layoutTestName{{5, "second", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 2, 8192}}})[16:]
	// Place the two headers first, followed by each file's referenced payload.
	data := make([]byte, 96)
	binary.LittleEndian.PutUint32(data, 2)
	binary.LittleEndian.PutUint32(data[4:], 16)
	copy(data[16:56], first[:40])
	copy(data[56:96], second[:40])
	binary.LittleEndian.PutUint32(data[20:], 40)
	for i, entry := range [][]byte{first, second} {
		header := 16 + i*40
		delta := len(data) - (header + 40)
		for _, field := range []int{24, 28, 32} {
			offset := binary.LittleEndian.Uint32(data[header+field:])
			if offset != 0 {
				binary.LittleEndian.PutUint32(data[header+field:], offset+uint32(delta))
			}
		}
		data = append(data, entry[40:]...)
	}
	files, err := decodeNTFSLayout(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].allocated != 4096 || files[1].allocated != 8192 || files[0].names[0].name != "first" || files[1].names[0].name != "second" {
		t.Fatalf("noncontiguous payloads: %+v", files)
	}
}

func TestNTFSLayoutFailureIdentifiesRecordAndField(t *testing.T) {
	batch := layoutTestBatch(layoutTestFile{reference: 0x9000000000020, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 1, 4096}}})
	stream := 16 + int(binary.LittleEndian.Uint32(batch[44:]))
	binary.LittleEndian.PutUint32(batch[stream:], 99)
	_, _, err := collectNTFSLayout(func(_, output []byte) (uint32, error) { copy(output, batch); return uint32(len(batch)), nil }, func() error { return nil }, nil)
	if err == nil {
		t.Fatal("accepted unknown stream version")
	}
	for _, expected := range []string{"batch 1", "file 1/1", "0x9000000000020", "stream Version", "byte", "got 99"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("missing %q: %v", expected, err)
		}
	}
}

func FuzzNTFSLayoutDecoder(f *testing.F) {
	f.Add(layoutTestBatch(layoutTestFile{reference: 40, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 5, 4096}}}))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = decodeNTFSLayout(data) })
}

type ntfsNativeFallbackFS struct {
	Default
	calls int
}

type ntfsCurrentDirectoryFS struct {
	Default
	reads   map[string]int
	entries map[string][]DirectoryEntry
	errors  map[string]error
	timed   int
}

func (f *ntfsCurrentDirectoryFS) ReadDir(path string) ([]DirectoryEntry, error) {
	f.reads[path]++
	return f.entries[path], f.errors[path]
}
func (f *ntfsCurrentDirectoryFS) ReadDirWithTimings(path string) ([]DirectoryEntry, *DirectoryReadDiagnostic, *DirectoryReadTimings, error) {
	f.timed++
	entries, err := f.ReadDir(path)
	return entries, nil, &DirectoryReadTimings{ExtendedAttempts: 1}, err
}

func ntfsFixtureRecords(t *testing.T, files ...layoutTestFile) map[uint64]*ntfsLayoutFile {
	t.Helper()
	decoded, err := decodeNTFSLayout(layoutTestBatch(files...))
	if err != nil {
		t.Fatal(err)
	}
	records := make(map[uint64]*ntfsLayoutFile)
	for _, file := range decoded {
		records[file.reference] = file
	}
	return records
}

func TestNTFSRepeatedNamesDoNotInflateLinksOrTriggerFallback(t *testing.T) {
	records := ntfsFixtureRecords(t,
		layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
		layoutTestFile{reference: 20, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "folder", 1}, {5, "folder", 0}}},
		layoutTestFile{reference: 30, names: []layoutTestName{{5, "file", 1}, {5, "file", 0}, {20, "alias", 0}, {20, "alias", 0}}, streams: []layoutTestStream{{"::$DATA", 0x80, 5000, 8192}}},
	)
	base := &ntfsCurrentDirectoryFS{reads: make(map[string]int)}
	indexed, err := indexNTFSLayout(base, `C:\`, 123, records, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	entries, err := indexed.ReadDir(`C:\`)
	if err != nil || len(entries) != 2 {
		t.Fatalf("root entries: %+v %v", entries, err)
	}
	aliases, err := indexed.ReadDir(`C:\folder`)
	if err != nil || len(aliases) != 1 {
		t.Fatalf("alias entries: %+v %v", aliases, err)
	}
	if aliases[0].Usage.LinkCount != 2 || aliases[0].Usage.AllocatedSize != 8192 {
		t.Fatalf("repeated names changed link accounting: %+v", aliases[0].Usage)
	}
	if indexed.duplicateNames != 3 || indexed.reconciledDirectories != 0 || len(base.reads) != 0 {
		t.Fatalf("unexpected reconciliation: duplicates=%d conflicts=%d native=%v", indexed.duplicateNames, indexed.reconciledDirectories, base.reads)
	}
}

func TestNTFSConflictingFilesReconcileOnlyTheirParent(t *testing.T) {
	root := t.TempDir()
	changed := filepath.Join(root, "changed")
	stable := filepath.Join(root, "stable")
	records := ntfsFixtureRecords(t,
		layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
		layoutTestFile{reference: 20, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "changed", 1}}},
		layoutTestFile{reference: 21, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "stable", 1}}},
		layoutTestFile{reference: 30, names: []layoutTestName{{20, "same", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 5000, 8192}}},
		layoutTestFile{reference: 31, names: []layoutTestName{{20, "same", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 10000, 12288}}},
		layoutTestFile{reference: 32, names: []layoutTestName{{21, "indexed", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 2000, 4096}}},
	)
	current := DirectoryEntry{DirEntry: windowsBatchDirEntry{windowsBatchFileInfo{name: "same", size: 20000, mode: 0666}}, Usage: FileUsage{AllocatedSize: 20480, Identity: FileIdentity{Volume: 123, Low: 31}, HasIdentity: true, LinkCount: 1, HasLinkCount: true}, HasUsage: true}
	base := &ntfsCurrentDirectoryFS{reads: make(map[string]int), entries: map[string][]DirectoryEntry{changed: {current}}, errors: make(map[string]error)}
	indexed, err := indexNTFSLayout(base, root, 123, records, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	entries, err := indexed.ReadDir(changed)
	if err != nil || len(entries) != 1 || entries[0].Usage.AllocatedSize != 20480 || entries[0].Usage.Identity.Low != 31 {
		t.Fatalf("did not use current native file metadata: %+v %v", entries, err)
	}
	entries, err = indexed.ReadDir(stable)
	if err != nil || len(entries) != 1 || entries[0].Name() != "indexed" {
		t.Fatalf("stable index was discarded: %+v %v", entries, err)
	}
	if len(base.reads) != 1 || base.reads[changed] != 1 || indexed.reconciledDirectories != 1 || len(indexed.nativeSubtrees) != 0 {
		t.Fatalf("reconciliation was not local: %+v, subtrees=%v", base.reads, indexed.nativeSubtrees)
	}
	if base.timed != 0 {
		t.Fatal("an unprofiled scan collected native timings")
	}
	_, _, timing, err := indexed.ReadDirWithTimings(changed)
	if err != nil || timing == nil || base.timed != 1 {
		t.Fatalf("profiled reconciliation omitted timings: %+v %v", timing, err)
	}
	base.errors[changed] = os.ErrPermission
	if _, err := indexed.ReadDir(changed); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("reconciliation hid a real access error: %v", err)
	}
}

func TestNTFSDirectoryReplacementReconcilesDescendants(t *testing.T) {
	for _, replacementIsDirectory := range []bool{true, false} {
		t.Run(fmt.Sprintf("new-directory=%t", replacementIsDirectory), func(t *testing.T) {
			root := t.TempDir()
			replaced := filepath.Join(root, "replaced")
			descendant := filepath.Join(replaced, "child")
			stable := filepath.Join(root, "replaced-extra")
			replacement := layoutTestFile{reference: 21, names: []layoutTestName{{5, "replaced", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 5, 4096}}}
			if replacementIsDirectory {
				replacement.attributes = windows.FILE_ATTRIBUTE_DIRECTORY
				replacement.streams = nil
			}
			records := ntfsFixtureRecords(t,
				layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
				layoutTestFile{reference: 20, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "replaced", 1}}}, replacement,
				layoutTestFile{reference: 22, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{5, "replaced-extra", 1}}},
				layoutTestFile{reference: 30, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{20, "child", 1}}},
				layoutTestFile{reference: 40, names: []layoutTestName{{30, "stale", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 5, 4096}}},
				layoutTestFile{reference: 41, names: []layoutTestName{{22, "stable", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 6, 4096}}},
			)
			current := DirectoryEntry{DirEntry: windowsBatchDirEntry{windowsBatchFileInfo{name: "current", size: 12, mode: 0666}}}
			base := &ntfsCurrentDirectoryFS{reads: make(map[string]int), entries: map[string][]DirectoryEntry{descendant: {current}}}
			indexed, err := indexNTFSLayout(base, root, 123, records, func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if indexed.indexedDirectory(root) != nil || indexed.indexedDirectory(descendant) != nil || len(indexed.nativeSubtrees) != 1 {
				t.Fatalf("replacement did not invalidate its subtree: %v", indexed.nativeSubtrees)
			}
			entries, err := indexed.ReadDir(descendant)
			if err != nil || len(entries) != 1 || entries[0].Name() != "current" {
				t.Fatalf("stale descendants leaked: %+v %v", entries, err)
			}
			entries, err = indexed.ReadDir(stable)
			if err != nil || len(entries) != 1 || entries[0].Name() != "stable" || len(base.reads) != 1 {
				t.Fatalf("unrelated prefix used native reads: %+v %v %v", entries, err, base.reads)
			}
		})
	}
}

func TestNTFSBulkTimingsAreOptionalAndRetainedOnFailure(t *testing.T) {
	var disabled *FastScanTimings
	if !disabled.start().IsZero() {
		t.Fatal("disabled timing read the clock")
	}
	disabled.finish(fastScanDecode, time.Time{})
	batch := layoutTestBatch(layoutTestFile{reference: 30, names: []layoutTestName{{5, "file", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 5, 4096}}})
	for _, enabled := range []bool{false, true} {
		var timing *FastScanTimings
		if enabled {
			timing = &FastScanTimings{}
		}
		calls := 0
		_, stats, err := collectNTFSLayout(func(_, output []byte) (uint32, error) {
			calls++
			if calls > 1 {
				return 0, windows.ERROR_ACCESS_DENIED
			}
			copy(output, batch)
			return uint32(len(batch)), nil
		}, func() error { return nil }, timing)
		if err == nil || stats.Records != 1 || stats.Timing != timing {
			t.Fatalf("lost failed attempt statistics: %+v %v", stats, err)
		}
		if timing != nil && (timing.Query.Calls != 2 || timing.Decode.Calls != 1 || timing.Records.Calls != 1 || timing.Hierarchy.Calls != 0 || timing.Wait.Calls != 2) {
			t.Fatalf("incorrect operation counts: %+v", timing)
		}
	}
}

func (f *ntfsNativeFallbackFS) ReadDir(string) ([]DirectoryEntry, error) {
	f.calls++
	return nil, errors.New("native read")
}

func TestNTFSIndexAncestryIdentityAndNativeReparseFallback(t *testing.T) {
	rootRef := uint64(0x1000000000005)
	dirRef := uint64(0x2000000000020)
	files, err := decodeNTFSLayout(layoutTestBatch(
		layoutTestFile{reference: rootRef, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{rootRef, ".", 1}}},
		layoutTestFile{reference: dirRef, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []layoutTestName{{rootRef, "folder", 1}}},
		layoutTestFile{reference: 40, attributes: windows.FILE_ATTRIBUTE_HIDDEN, names: []layoutTestName{{rootRef, "hardlink", 1}, {dirRef, "otherlink", 0}}, streams: []layoutTestStream{{"::$DATA", 0x80, 10000, 4096}}},
		layoutTestFile{reference: 41, attributes: windows.FILE_ATTRIBUTE_REPARSE_POINT, names: []layoutTestName{{dirRef, "cloud-file", 1}}},
	))
	if err != nil {
		t.Fatal(err)
	}
	records := make(map[uint64]*ntfsLayoutFile)
	for _, file := range files {
		records[file.reference] = file
	}
	base := &ntfsNativeFallbackFS{}
	root := `C:\`
	indexed, err := indexNTFSLayout(base, root, 1234, records, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	entries, err := indexed.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("root: %+v, %v", entries, err)
	}
	for _, entry := range entries {
		if entry.Name() == "hardlink" {
			if !entry.HasUsage || !entry.Hidden || !entry.HasHidden || entry.Usage.Identity.Low != 40 || entry.Usage.Identity.Volume != 1234 || entry.Usage.LinkCount != 2 || !entry.Usage.HasLinkCount || entry.Usage.AllocatedSize != 4096 {
				t.Fatalf("wrong entry: %+v", entry)
			}
			info, _ := entry.Info()
			if info.Size() != 10000 {
				t.Fatal("logical size lost")
			}
		}
	}
	_, err = indexed.ReadDir(filepath.Join(root, "folder"))
	if err == nil || base.calls != 1 {
		t.Fatalf("reparse parent did not use native enumeration: %v, %d", err, base.calls)
	}
	// A stale sequence number must not resolve to a reused directory record.
	records[dirRef].names[0].parent = rootRef + 1<<48
	if _, err := indexNTFSLayout(base, root, 1234, records, func() error { return nil }); err == nil {
		t.Fatal("accepted stale parent reference")
	}
	records[dirRef].names[0].parent = dirRef
	if _, err := indexNTFSLayout(base, root, 1234, records, func() error { return nil }); err == nil {
		t.Fatal("accepted cyclic ancestry")
	}
}

func TestNTFSIndexRootWithoutNameAndReservedMetadata(t *testing.T) {
	records := map[uint64]*ntfsLayoutFile{
		5:  {reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
		11: {reference: 11, attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
		30: {reference: 30, attributes: windows.FILE_ATTRIBUTE_DIRECTORY, names: []ntfsLayoutName{{11, "metadata"}}},
		31: {reference: 31, names: []ntfsLayoutName{{30, "metadata-file"}}},
		32: {reference: 32, names: []ntfsLayoutName{{5, "user-file"}}},
	}
	check := func() error { return nil }
	indexed, err := indexNTFSLayout(Default{}, `C:\`, 123, records, check)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := indexed.ReadDir(`C:\`)
	if err != nil || len(entries) != 1 || entries[0].Name() != "user-file" {
		t.Fatalf("metadata leaked or root was omitted: %+v %v", entries, err)
	}
	records[33] = &ntfsLayoutFile{reference: 33, names: []ntfsLayoutName{{5, "user-file"}}}
	conflicted, err := indexNTFSLayout(Default{}, `C:\`, 123, records, check)
	if err != nil || conflicted.indexedDirectory(`C:\`) != nil || conflicted.reconciledDirectories != 1 {
		t.Fatalf("conflicting paths did not schedule a native read: %+v %v", conflicted, err)
	}
	delete(records, 33)
	if _, err := indexNTFSLayout(Default{}, `C:\`, 123, records, func() error { return context.Canceled }); !errors.Is(err, context.Canceled) {
		t.Fatalf("index ignored cancellation: %v", err)
	}
}

// Opt-in real-kernel smoke test. It reads one layout batch only, never scans the
// entire host volume. Lack of volume permission is an expected explicit skip.
func TestNTFSLayoutLiveBatch(t *testing.T) {
	if os.Getenv("SPACEBROWSER_TEST_NTFS") != "1" {
		t.Skip("set SPACEBROWSER_TEST_NTFS=1 for a read-only native volume check")
	}
	root := filepath.VolumeName(t.TempDir()) + `\`
	_, filesystem := windowsVolumeInformation(root)
	if filesystem != "NTFS" {
		t.Skip("test volume is not NTFS")
	}
	path, _ := windows.UTF16PtrFromString(`\\.\` + filepath.VolumeName(root))
	handle, err := openNTFSLayoutVolume(path)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Skipf("volume access denied: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	var returned uint32
	var volumeData [96]byte
	if err := windows.DeviceIoControl(handle, windows.FSCTL_GET_NTFS_VOLUME_DATA, nil, 0, &volumeData[0], uint32(len(volumeData)), &returned, nil); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			t.Skipf("NTFS volume metadata is unavailable in this session: %v", err)
		}
		t.Fatal(err)
	}
	var input [4]uint64
	in := unsafe.Slice((*byte)(unsafe.Pointer(&input[0])), 32)
	binary.LittleEndian.PutUint32(in[4:], queryLayoutFlags|queryLayoutRestart)
	out := alignedLayoutBuffer(4 << 20)
	if err := windows.DeviceIoControl(handle, fsctlQueryFileLayout, &in[0], uint32(len(in)), &out[0], uint32(len(out)), &returned, nil); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			t.Skipf("NTFS layout control is unavailable in this session: %v", err)
		}
		t.Fatal(err)
	}
	files, err := decodeNTFSLayout(out[:returned])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("decoded %d native file-table records (%d bytes)", len(files), returned)
}
