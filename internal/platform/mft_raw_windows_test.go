//go:build windows

package platform

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func rawTestGeometry() ntfsRawGeometry {
	return ntfsRawGeometry{serial: 123, sector: 512, cluster: 4096, record: 1024, clusters: 512, length: 32 * 1024, firstLCN: 2}
}
func rawTestRef(number uint64) uint64 { return uint64(7)<<48 | number }
func rawTestBasic(attributes uint32) []byte {
	value := make([]byte, 48)
	binary.LittleEndian.PutUint64(value, 133000000000000000)
	binary.LittleEndian.PutUint64(value[8:], 133000000000000001)
	binary.LittleEndian.PutUint64(value[24:], 133000000000000002)
	binary.LittleEndian.PutUint32(value[32:], attributes)
	return value
}
func rawTestName(parent uint64, name string, namespace byte) []byte {
	value := make([]byte, 66)
	binary.LittleEndian.PutUint64(value, parent)
	value[64], value[65] = byte(len(layoutTestUTF16(name))/2), namespace
	return append(value, layoutTestUTF16(name)...)
}
func rawTestResident(kind uint32, id uint16, name string, value []byte) []byte {
	encoded := layoutTestUTF16(name)
	offset := (24 + len(encoded) + 7) &^ 7
	attribute := make([]byte, (offset+len(value)+7)&^7)
	binary.LittleEndian.PutUint32(attribute, kind)
	binary.LittleEndian.PutUint32(attribute[4:], uint32(len(attribute)))
	attribute[9] = byte(len(encoded) / 2)
	binary.LittleEndian.PutUint16(attribute[10:], 24)
	binary.LittleEndian.PutUint16(attribute[14:], id)
	binary.LittleEndian.PutUint32(attribute[16:], uint32(len(value)))
	binary.LittleEndian.PutUint16(attribute[20:], uint16(offset))
	copy(attribute[24:], encoded)
	copy(attribute[offset:], value)
	return attribute
}
func rawTestNonresident(kind uint32, id uint16, flags uint16, lowest, highest, logical, allocated, physical uint64, pairs []byte) []byte {
	header := 64
	if flags&0x80ff != 0 {
		header = 72
	}
	attribute := make([]byte, (header+len(pairs)+7)&^7)
	binary.LittleEndian.PutUint32(attribute, kind)
	binary.LittleEndian.PutUint32(attribute[4:], uint32(len(attribute)))
	attribute[8] = 1
	binary.LittleEndian.PutUint16(attribute[12:], flags)
	binary.LittleEndian.PutUint16(attribute[14:], id)
	binary.LittleEndian.PutUint64(attribute[16:], lowest)
	binary.LittleEndian.PutUint64(attribute[24:], highest)
	binary.LittleEndian.PutUint16(attribute[32:], uint16(header))
	binary.LittleEndian.PutUint64(attribute[40:], allocated)
	binary.LittleEndian.PutUint64(attribute[48:], logical)
	binary.LittleEndian.PutUint64(attribute[56:], logical)
	if header == 72 {
		binary.LittleEndian.PutUint64(attribute[64:], physical)
	}
	copy(attribute[header:], pairs)
	return attribute
}
func rawTestRecord(size int, number, base uint64, directory bool, attributes ...[]byte) []byte {
	record := make([]byte, size)
	copy(record, "FILE")
	binary.LittleEndian.PutUint16(record[4:], 48)
	count := size/512 + 1
	binary.LittleEndian.PutUint16(record[6:], uint16(count))
	binary.LittleEndian.PutUint16(record[16:], uint16(rawTestRef(number)>>48))
	binary.LittleEndian.PutUint16(record[18:], 1)
	first := (48 + count*2 + 7) &^ 7
	binary.LittleEndian.PutUint16(record[20:], uint16(first))
	flags := uint16(1)
	if directory {
		flags |= 2
	}
	binary.LittleEndian.PutUint16(record[22:], flags)
	binary.LittleEndian.PutUint32(record[28:], uint32(size))
	binary.LittleEndian.PutUint64(record[32:], base)
	binary.LittleEndian.PutUint32(record[44:], uint32(number))
	for _, attribute := range attributes {
		copy(record[first:], attribute)
		first += len(attribute)
	}
	binary.LittleEndian.PutUint32(record[first:], 0xffffffff)
	binary.LittleEndian.PutUint32(record[24:], uint32(first+8))
	binary.LittleEndian.PutUint16(record[48:], 0xbeef)
	for i := 1; i < count; i++ {
		copy(record[48+i*2:], record[i*512-2:i*512])
		binary.LittleEndian.PutUint16(record[i*512-2:], 0xbeef)
	}
	return record
}
func rawTestList(refs ...ntfsRawAttributeRef) []byte {
	list := make([]byte, 32*len(refs))
	for i, ref := range refs {
		entry := list[i*32:]
		binary.LittleEndian.PutUint32(entry, ref.key.kind)
		binary.LittleEndian.PutUint16(entry[4:], 32)
		binary.LittleEndian.PutUint64(entry[8:], uint64(ref.key.vcn))
		binary.LittleEndian.PutUint64(entry[16:], ref.reference)
		binary.LittleEndian.PutUint16(entry[24:], ref.key.instance)
	}
	return list
}

func TestDirectMFTResidentSparseCompressedAndReparseAccounting(t *testing.T) {
	g := rawTestGeometry()
	for _, tc := range []struct {
		name               string
		data               []byte
		logical, allocated int64
	}{
		{"resident", rawTestResident(0x80, 4, "", []byte("abc")), 3, 0},
		{"empty", rawTestResident(0x80, 4, "", nil), 0, 0},
		{"normal", rawTestNonresident(0x80, 4, 0, 0, 1, 5000, 8192, 0, []byte{0x11, 2, 20, 0}), 5000, 8192},
		{"sparse", rawTestNonresident(0x80, 4, 0x8000, 0, 15, 65536, 65536, 4096, []byte{0x11, 1, 20, 0x01, 15, 0}), 65536, 4096},
		{"compressed", rawTestNonresident(0x80, 4, 1, 0, 15, 65536, 65536, 8192, []byte{0x11, 2, 20, 0x01, 14, 0}), 65536, 8192},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := rawTestRecord(1024, 30, 0, false,
				rawTestResident(0x10, 0, "", rawTestBasic(windows.FILE_ATTRIBUTE_HIDDEN)),
				rawTestResident(0x30, 1, "", rawTestName(rawTestRef(5), "long-name", 1)),
				rawTestResident(0x30, 2, "", rawTestName(rawTestRef(5), "LONG-N~1", 2)),
				rawTestResident(0x30, 3, "", rawTestName(rawTestRef(5), "other-name", 0)), tc.data,
				rawTestResident(0x80, 5, "ignored-ADS", []byte("named stream data")))
			parsed, err := decodeNTFSRawRecord(record, 30, g)
			if err != nil {
				t.Fatal(err)
			}
			parsed.finalize()
			f := parsed.file
			if f.logical != tc.logical || f.allocated != tc.allocated || len(f.names) != 2 || f.reference != rawTestRef(30) || f.native || f.write != 133000000000000001 || f.attributes != windows.FILE_ATTRIBUTE_HIDDEN {
				t.Fatalf("incorrect raw metadata: %+v", f)
			}
		})
	}
	reparse := make([]byte, 8)
	binary.LittleEndian.PutUint32(reparse, windows.IO_REPARSE_TAG_SYMLINK)
	parsed, err := decodeNTFSRawRecord(rawTestRecord(1024, 30, 0, false, rawTestResident(0x10, 0, "", rawTestBasic(windows.FILE_ATTRIBUTE_REPARSE_POINT)), rawTestResident(0x30, 1, "", rawTestName(rawTestRef(5), "link", 1)), rawTestResident(0xc0, 2, "", reparse)), 30, g)
	if err != nil {
		t.Fatal(err)
	}
	parsed.finalize()
	root := &ntfsLayoutFile{reference: rawTestRef(5), attributes: windows.FILE_ATTRIBUTE_DIRECTORY}
	indexed, err := indexNTFSLayout(Default{}, `C:\`, 123, map[uint64]*ntfsLayoutFile{root.reference: root, parsed.file.reference: parsed.file}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	entries, err := indexed.ReadDir(`C:\`)
	if err != nil || len(entries) != 1 || entries[0].Type()&os.ModeSymlink == 0 || indexed.indexedDirectory(`C:\`) == nil {
		t.Fatalf("known reparse tag triggered a directory read or lost symlink semantics: %+v %v", entries, err)
	}
}

func TestDirectMFTFixupsUse512ByteStrideOn4KSectors(t *testing.T) {
	g := rawTestGeometry()
	g.sector, g.record = 4096, 4096
	value := make([]byte, 2200)
	for i := range value {
		value[i] = byte(i)
	}
	record := rawTestRecord(4096, 30, 0, false, rawTestResident(0x80, 1, "", value))
	parsed, err := decodeNTFSRawRecord(record, 30, g)
	if err != nil || parsed.file.logical != 2200 {
		t.Fatalf("4K fixups: %+v %v", parsed, err)
	}
	// Restore bytes inside the resident data, including every 512-byte tail.
	first := int(binary.LittleEndian.Uint16(record[20:]))
	if !slices.Equal(record[first+24:first+24+len(value)], value) {
		t.Fatal("fixups did not restore record payload")
	}
}

func TestDirectMFTMalformedRecordsFailClosed(t *testing.T) {
	for _, mode := range []string{"signature", "fixup", "usa", "length", "attribute", "resident-value", "sequence", "ordinal", "terminator"} {
		t.Run(mode, func(t *testing.T) {
			record := rawTestRecord(1024, 30, 0, false, rawTestResident(0x80, 1, "", []byte("abc")))
			first := int(binary.LittleEndian.Uint16(record[20:]))
			switch mode {
			case "signature":
				record[0] = 'X'
			case "fixup":
				record[510] = 0
			case "usa":
				binary.LittleEndian.PutUint16(record[4:], 1023)
			case "length":
				binary.LittleEndian.PutUint32(record[24:], 1025)
			case "attribute":
				binary.LittleEndian.PutUint32(record[first+4:], 0)
			case "resident-value":
				binary.LittleEndian.PutUint32(record[first+16:], 0xffffffff)
			case "sequence":
				binary.LittleEndian.PutUint16(record[16:], 0)
			case "ordinal":
				binary.LittleEndian.PutUint32(record[44:], 31)
			case "terminator":
				binary.LittleEndian.PutUint32(record[first+32:], 0)
			}
			if _, err := decodeNTFSRawRecord(record, 30, rawTestGeometry()); err == nil {
				t.Fatal("accepted malformed record")
			}
		})
	}
	record := rawTestRecord(1024, 30, 0, false)
	binary.LittleEndian.PutUint16(record[22:], 0)
	if parsed, err := decodeNTFSRawRecord(record, 30, rawTestGeometry()); parsed.file != nil || err != nil {
		t.Fatalf("deleted file was not ignored: %+v %v", parsed, err)
	}
}

func TestDirectMFTMappingPairsAndFragmentedReads(t *testing.T) {
	g := rawTestGeometry()
	attribute := rawTestNonresident(0x20, 1, 0, 0, 3, 16384, 16384, 0, []byte{0x11, 1, 10, 0x11, 2, 0xfe, 0x01, 1, 0})
	runs, err := decodeNTFSRawRuns(attribute, g)
	if err != nil || !slices.Equal(runs, []ntfsRawRun{{0, 10, 1}, {1, 8, 2}, {3, -1, 1}}) {
		t.Fatalf("mapping pairs: %+v %v", runs, err)
	}
	for _, pairs := range [][]byte{{0x10, 1, 0}, {0x19, 1, 0}, {0x11, 0, 1, 0}, {0x11, 5, 1, 0}, {0x11, 1, 0x80, 0}, {0x11, 1, 1}} {
		if _, err := decodeNTFSRawRuns(rawTestNonresident(0x20, 1, 0, 0, 3, 1, 4096, 0, pairs), g); err == nil {
			t.Fatalf("accepted bad run list %x", pairs)
		}
	}
	output := alignedNTFSRawBuffer(3*4096, 512)
	var offsets []int64
	read := func(data []byte, offset int64) error {
		offsets = append(offsets, offset)
		if uintptr(unsafe.Pointer(&data[0]))%512 != 0 || offset%512 != 0 || len(data)%512 != 0 {
			t.Fatal("unaligned volume read")
		}
		for i := range data {
			data[i] = byte(offset / 4096)
		}
		return nil
	}
	if err := readNTFSRawStream(output, 0, runs, g, read); err != nil || !slices.Equal(offsets, []int64{10 * 4096, 8 * 4096}) || output[4096] != 8 {
		t.Fatalf("fragmented reads: %v %v", offsets, err)
	}
	if err := readNTFSRawStream(output[:512], 3*4096, runs, g, read); err == nil {
		t.Fatal("sparse metadata treated as valid records")
	}
}

func TestDirectMFTRetrievalPointerPagination(t *testing.T) {
	g := rawTestGeometry()
	calls := 0
	runs, err := collectNTFSRetrievalPointers(func(input, output []byte) (uint32, error) {
		calls++
		want := uint64(0)
		end, lcn := uint64(4), uint64(2)
		if calls == 2 {
			want, end, lcn = 4, 8, 32
		}
		if binary.LittleEndian.Uint64(input) != want {
			t.Fatalf("wrong continuation: %x", input)
		}
		binary.LittleEndian.PutUint32(output, 1)
		binary.LittleEndian.PutUint64(output[8:], want)
		binary.LittleEndian.PutUint64(output[16:], end)
		binary.LittleEndian.PutUint64(output[24:], lcn)
		if calls == 1 {
			return 32, windows.ERROR_MORE_DATA
		}
		return 32, nil
	}, g, func() error { return nil })
	if err != nil || !slices.Equal(runs, []ntfsRawRun{{0, 2, 4}, {4, 32, 4}}) || calls != 2 {
		t.Fatalf("retrieval map: %+v %v", runs, err)
	}
	for _, mode := range []string{"short", "oversize", "hole", "gap", "no-progress", "coverage", "denied"} {
		t.Run(mode, func(t *testing.T) {
			_, err := collectNTFSRetrievalPointers(func(_, output []byte) (uint32, error) {
				binary.LittleEndian.PutUint32(output, 1)
				binary.LittleEndian.PutUint64(output[16:], 8)
				binary.LittleEndian.PutUint64(output[24:], 2)
				switch mode {
				case "short":
					return 4, nil
				case "oversize":
					return uint32(len(output) + 1), nil
				case "hole":
					binary.LittleEndian.PutUint64(output[24:], ^uint64(0))
				case "gap":
					binary.LittleEndian.PutUint64(output[8:], 1)
				case "no-progress":
					binary.LittleEndian.PutUint64(output[16:], 0)
				case "coverage":
					binary.LittleEndian.PutUint64(output[16:], 1)
				case "denied":
					return 0, windows.ERROR_ACCESS_DENIED
				}
				return 32, nil
			}, g, func() error { return nil })
			if err == nil {
				t.Fatal("accepted invalid retrieval map")
			}
		})
	}
}

func rawTestVolume(t *testing.T, nonresidentList bool) (ntfsRawGeometry, []ntfsRawRun, []byte) {
	t.Helper()
	g := rawTestGeometry()
	runs := []ntfsRawRun{{0, 2, 4}, {4, 32, 4}}
	logical := make([]byte, int(g.length))
	put := func(number int, record []byte) { copy(logical[number*1024:], record) }
	put(5, rawTestRecord(1024, 5, 0, true, rawTestResident(0x10, 0, "", rawTestBasic(windows.FILE_ATTRIBUTE_DIRECTORY)), rawTestResident(0x30, 1, "", rawTestName(rawTestRef(5), ".", 1))))
	refs := []ntfsRawAttributeRef{
		{rawTestRef(30), ntfsRawAttributeKey{0x10, 0, 0}},
		{rawTestRef(30), ntfsRawAttributeKey{0x30, 1, 0}},
		{rawTestRef(31), ntfsRawAttributeKey{0x30, 2, 0}},
		{rawTestRef(31), ntfsRawAttributeKey{0x80, 3, 0}},
	}
	list := rawTestList(refs...)
	listAttribute := rawTestResident(0x20, 4, "", list)
	if nonresidentList {
		listAttribute = rawTestNonresident(0x20, 4, 0, 0, 0, uint64(len(list)), 4096, 0, []byte{0x11, 1, 64, 0})
	}
	put(30, rawTestRecord(1024, 30, 0, false, rawTestResident(0x10, 0, "", rawTestBasic(0)), listAttribute, rawTestResident(0x30, 1, "", rawTestName(rawTestRef(5), "first", 1))))
	put(31, rawTestRecord(1024, 31, rawTestRef(30), false, rawTestResident(0x30, 2, "", rawTestName(rawTestRef(5), "second", 1)), rawTestNonresident(0x80, 3, 0, 0, 1, 5000, 8192, 0, []byte{0x11, 2, 60, 0})))
	// Named data and directory-index-only extensions do not contribute to the
	// default stream. Their omitted list entries must not force volume fallback.
	put(29, rawTestRecord(1024, 29, rawTestRef(30), false, rawTestResident(0x80, 9, "ignored-ADS", []byte("external stream"))))
	disk := make([]byte, 65*4096)
	copy(disk[2*4096:], logical[:4*4096])
	copy(disk[32*4096:], logical[4*4096:])
	copy(disk[64*4096:], list)
	return g, runs, disk
}

func TestDirectMFTFragmentedTableAndExtensionRecords(t *testing.T) {
	for _, nonresident := range []bool{false, true} {
		t.Run(fmt.Sprint(nonresident), func(t *testing.T) {
			g, runs, disk := rawTestVolume(t, nonresident)
			read := func(data []byte, offset int64) error {
				if offset < 0 || int(offset)+len(data) > len(disk) || offset%512 != 0 || len(data)%512 != 0 {
					t.Fatalf("unexpected volume read: %d %d", offset, len(data))
				}
				copy(data, disk[offset:int(offset)+len(data)])
				return nil
			}
			timing := &FastScanTimings{}
			records, stats, err := collectDirectMFT(g, runs, read, func() error { return nil }, timing)
			if err != nil {
				t.Fatal(err)
			}
			f := records[rawTestRef(30)]
			if len(records) != 2 || stats.Records != 2 || stats.Bytes != g.length || stats.Method != FastScanNTFSDirect || f == nil || f.native || f.logical != 5000 || f.allocated != 8192 || len(f.names) != 2 {
				t.Fatalf("bad assembled table: %+v %+v", stats, f)
			}
			indexed, err := indexNTFSLayout(Default{}, `C:\`, g.serial, records, func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			entries, err := indexed.ReadDir(`C:\`)
			if err != nil || len(entries) != 2 || entries[0].Usage.Identity.Low != rawTestRef(30) || entries[0].Usage.LinkCount != 2 {
				t.Fatalf("hard links were not preserved: %+v %v", entries, err)
			}
			if timing.Decode.Calls != 1 || timing.Records.Calls != 2 || timing.Query.Calls != 0 {
				t.Fatalf("unexpected direct timing: %+v", timing)
			}
		})
	}
}

func TestDirectMFTFailuresNeverPublishPartialMetadata(t *testing.T) {
	for _, mode := range []string{"read-error", "cancel", "torn", "stale-base", "stale-list", "unlisted", "missing-member"} {
		t.Run(mode, func(t *testing.T) {
			g, runs, disk := rawTestVolume(t, false)
			offset := 32*4096 + (31-16)*1024
			switch mode {
			case "torn":
				disk[offset+510] = 0
			case "stale-base":
				binary.LittleEndian.PutUint64(disk[offset+32:], rawTestRef(30)+(1<<48))
			case "stale-list":
				binary.LittleEndian.PutUint16(disk[offset+16:], 8)
			case "unlisted":
				binary.LittleEndian.PutUint16(disk[offset+56+14:], 19)
			case "missing-member":
				binary.LittleEndian.PutUint16(disk[offset+22:], 0)
			}
			var readCalls atomic.Int32
			records, stats, err := collectDirectMFT(g, runs, func(data []byte, offset int64) error {
				readCalls.Add(1)
				if mode == "read-error" {
					return os.ErrPermission
				}
				copy(data, disk[offset:int(offset)+len(data)])
				return nil
			}, func() error {
				if mode == "cancel" && readCalls.Load() > 0 {
					return context.Canceled
				}
				return nil
			}, nil)
			if records != nil || err == nil {
				t.Fatalf("partial index escaped: %+v %+v %v", records, stats, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
		})
	}
}

func TestDirectMFTExtensionsAcrossBatchBoundaries(t *testing.T) {
	g := rawTestGeometry()
	g.clusters = 4096
	g.length = directMFTBatchBytes + 2*g.record
	table := make([]byte, int(g.length))
	put := func(number uint64, record []byte) { copy(table[int(number)*int(g.record):], record) }
	put(5, rawTestRecord(1024, 5, 0, true, rawTestResident(0x10, 0, "", rawTestBasic(windows.FILE_ATTRIBUTE_DIRECTORY))))
	extensionNumber := uint64(directMFTBatchBytes / g.record)
	refs := []ntfsRawAttributeRef{
		{rawTestRef(30), ntfsRawAttributeKey{0x10, 0, 0}},
		{rawTestRef(30), ntfsRawAttributeKey{0x30, 1, 0}},
		{rawTestRef(extensionNumber), ntfsRawAttributeKey{0x80, 2, 0}},
	}
	put(30, rawTestRecord(1024, 30, 0, false, rawTestResident(0x10, 0, "", rawTestBasic(0)), rawTestResident(0x20, 3, "", rawTestList(refs...)), rawTestResident(0x30, 1, "", rawTestName(rawTestRef(5), "file", 1))))
	put(extensionNumber, rawTestRecord(1024, extensionNumber, rawTestRef(30), false, rawTestResident(0x80, 2, "", []byte("abc"))))
	runs := []ntfsRawRun{{0, g.firstLCN, (g.length + g.cluster - 1) / g.cluster}}
	for _, cancelled := range []bool{false, true} {
		reads := 0
		timing := &FastScanTimings{}
		records, stats, err := collectDirectMFT(g, runs, func(data []byte, offset int64) error {
			reads++
			logical := offset - g.firstLCN*g.cluster
			copy(data, table[int(logical):int(logical)+len(data)])
			return nil
		}, func() error {
			if cancelled && timing.Records.Calls > 0 {
				return context.Canceled
			}
			return nil
		}, timing)
		if cancelled {
			if !errors.Is(err, context.Canceled) || records != nil || stats.Records != 2 {
				t.Fatalf("cancelled scan published or lost its first-batch state: %+v %v", stats, err)
			}
			continue
		}
		file := records[rawTestRef(30)]
		if err != nil || reads != 2 || stats.Batches != 2 || file == nil || file.logical != 3 || file.native || file.names[0].name != "file" {
			t.Fatalf("reused batch storage lost extension state: %+v %+v %v", stats, file, err)
		}
	}
}

func TestDirectMFTFallbackUsesOnlyFreshBulkRecords(t *testing.T) {
	batch := layoutTestBatch(layoutTestFile{reference: 5, attributes: windows.FILE_ATTRIBUTE_DIRECTORY}, layoutTestFile{reference: 30, names: []layoutTestName{{5, "fresh", 1}}, streams: []layoutTestStream{{"::$DATA", 0x80, 20, 4096}}})
	calls := 0
	index, stats, err := prepareNTFSLayout(Default{}, `C:\`, 123, func(_, output []byte) (uint32, error) {
		calls++
		if calls > 1 {
			return 0, windows.ERROR_HANDLE_EOF
		}
		copy(output, batch)
		return uint32(len(batch)), nil
	}, func() error { return nil }, FastScanStats{Records: 9, Batches: 2, Bytes: 8192, Timing: &FastScanTimings{}}, errors.New("torn direct record"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := index.ReadDir(`C:\`)
	if err != nil || len(entries) != 1 || entries[0].Name() != "fresh" || stats.Method != FastScanNTFSLayout || stats.DirectFallbackReason != "torn direct record" || stats.DirectRecords != 9 || stats.DirectBytes != 8192 || stats.Records != 2 {
		t.Fatalf("bad fallback: %+v %+v %v", stats, entries, err)
	}
}

func TestDirectMFTReadAheadOwnsTwoIndependentBuffers(t *testing.T) {
	g := rawTestGeometry()
	g.clusters = 8192
	g.length = 2*directMFTBatchBytes + g.record
	runs := []ntfsRawRun{{0, 2, (g.length + g.cluster - 1) / g.cluster}}
	secondStarted := make(chan struct{}, 1)
	var calls atomic.Int32
	b := newDirectMFTBatches(g, runs, func(data []byte, offset int64) error {
		call := calls.Add(1)
		data[0] = byte(call)
		if call == 2 {
			secondStarted <- struct{}{}
		}
		return nil
	})
	defer b.close()
	receive := func() directMFTBatch {
		t.Helper()
		select {
		case batch, ok := <-b.results:
			if !ok {
				t.Fatal("raw reader stopped early")
			}
			return batch
		case <-time.After(2 * time.Second):
			t.Fatal("raw read-ahead stalled")
			return directMFTBatch{}
		}
	}
	first := receive()
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("next table read did not overlap consumer work")
	}
	second := receive()
	if first.buffer[0] != 1 || second.buffer[0] != 2 || &first.buffer[0] == &second.buffer[0] || calls.Load() != 2 {
		t.Fatal("prefetch reused an owned buffer or read more than one batch ahead")
	}
	b.buffers <- first.buffer
	last := receive()
	if last.buffer[0] != 3 || last.offset != 2*directMFTBatchBytes || last.length != g.record {
		t.Fatal("last raw batch bounds changed")
	}
	b.buffers <- second.buffer
	b.buffers <- last.buffer
}

func TestDirectMFTReadAheadCloseJoinsPendingRead(t *testing.T) {
	g := rawTestGeometry()
	started, finish, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	b := newDirectMFTBatches(g, []ntfsRawRun{{0, 2, 8}}, func([]byte, int64) error { close(started); <-finish; close(finished); return nil })
	defer b.close()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(finish)
		t.Fatal("raw read never started")
	}
	closed := make(chan struct{})
	go func() { b.close(); close(closed) }()
	select {
	case <-closed:
		t.Error("raw buffers/volume handle released before pending read completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("raw worker did not join")
	}
	select {
	case <-finished:
	default:
		t.Fatal("pending read outlived reader shutdown")
	}
}

func TestDirectMFTReadAheadStopsBetweenStorageFragments(t *testing.T) {
	g := rawTestGeometry()
	var runs []ntfsRawRun
	for i := int64(0); i < 8; i++ {
		runs = append(runs, ntfsRawRun{i, 2 + i*2, 1})
	}
	started, finish := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	b := newDirectMFTBatches(g, runs, func([]byte, int64) error {
		if reads.Add(1) == 1 {
			close(started)
			<-finish
		}
		return nil
	})
	defer b.close()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(finish)
		t.Fatal("fragment read did not start")
	}
	// Signal shutdown before releasing the in-flight syscall; later fragments
	// must not delay cancellation even when one batch spans many extents.
	b.closeOnce.Do(func() { close(b.stop) })
	close(finish)
	b.close()
	if reads.Load() != 1 {
		t.Fatal("reader continued through fragments after shutdown")
	}
}

func TestDirectMFTAttributeListReadsFollowPhysicalOrder(t *testing.T) {
	g := rawTestGeometry()
	bases := make(map[uint64]*ntfsRawRecord)
	lists := make(map[int64][]byte)
	for i, lcn := range []int64{64, 100, 20} {
		ref := rawTestRef(uint64(30 + i))
		key := ntfsRawAttributeKey{kind: 0x80, instance: 1}
		list := rawTestList(ntfsRawAttributeRef{ref, key})
		bases[ref] = &ntfsRawRecord{file: &ntfsLayoutFile{reference: ref}, keys: []ntfsRawAttributeKey{key}, listRuns: []ntfsRawRun{{0, lcn, 1}}, listSize: int64(len(list)), basic: true, data: true, hasList: true}
		lists[lcn*g.cluster] = list
	}
	var offsets []int64
	err := mergeNTFSRawExtensions(bases, map[uint64]*ntfsRawRecord{}, g, func(data []byte, offset int64) error {
		offsets = append(offsets, offset)
		copy(data, lists[offset])
		return nil
	}, func() error { return nil })
	if err != nil || !slices.Equal(offsets, []int64{20 * g.cluster, 64 * g.cluster, 100 * g.cluster}) {
		t.Fatalf("attribute reads remain randomly ordered: %v %v", offsets, err)
	}
}

func TestDirectMFTGeometryValidation(t *testing.T) {
	data := make([]byte, 128)
	binary.LittleEndian.PutUint64(data, 123)
	binary.LittleEndian.PutUint64(data[16:], 512)
	binary.LittleEndian.PutUint32(data[40:], 512)
	binary.LittleEndian.PutUint32(data[44:], 4096)
	binary.LittleEndian.PutUint32(data[48:], 1024)
	binary.LittleEndian.PutUint64(data[56:], 32*1024)
	binary.LittleEndian.PutUint64(data[64:], 2)
	binary.LittleEndian.PutUint16(data[100:], 3)
	binary.LittleEndian.PutUint16(data[102:], 1)
	g, err := decodeNTFSRawGeometry(data)
	if err != nil || g != rawTestGeometry() {
		t.Fatalf("geometry: %+v %v", g, err)
	}
	for _, offset := range []int{16, 40, 44, 48, 56, 100} {
		broken := slices.Clone(data)
		for i := 0; i < 8 && offset+i < len(broken); i++ {
			broken[offset+i] = 0
		}
		if _, err := decodeNTFSRawGeometry(broken); err == nil {
			t.Fatalf("accepted bad geometry at %d", offset)
		}
	}
}

func FuzzDirectMFTRecordDecoder(f *testing.F) {
	f.Add(rawTestRecord(1024, 30, 0, false, rawTestResident(0x10, 0, "", rawTestBasic(0)), rawTestResident(0x80, 1, "", []byte("abc"))))
	f.Add(make([]byte, 1024))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) != 1024 {
			return
		}
		_, _ = decodeNTFSRawRecord(slices.Clone(data), 30, rawTestGeometry())
	})
}

func FuzzDirectMFTAttributeListDecoder(f *testing.F) {
	f.Add(rawTestList(ntfsRawAttributeRef{rawTestRef(30), ntfsRawAttributeKey{0x30, 1, 0}}))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			return
		}
		_, _ = decodeNTFSRawAttributeList(data)
	})
}

func FuzzDirectMFTMappingDecoder(f *testing.F) {
	f.Add(rawTestNonresident(0x20, 1, 0, 0, 3, 16384, 16384, 0, []byte{0x11, 1, 10, 0x11, 2, 0xfe, 0x01, 1, 0}))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			return
		}
		_, _ = decodeNTFSRawRuns(data, rawTestGeometry())
	})
}

// One raw record only, opt-in and read-only. This does not perform a full scan.
func TestDirectMFTLiveRecord(t *testing.T) {
	if os.Getenv("SPACEBROWSER_TEST_NTFS") != "1" {
		t.Skip("set SPACEBROWSER_TEST_NTFS=1 for a read-only native volume check")
	}
	root := filepath.VolumeName(t.TempDir()) + `\`
	path, _ := windows.UTF16PtrFromString(`\\.\` + filepath.VolumeName(root))
	volume, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Skipf("direct volume access needs elevation: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(volume)
	var data [128]byte
	var returned uint32
	err = windows.DeviceIoControl(volume, windows.FSCTL_GET_NTFS_VOLUME_DATA, nil, 0, &data[0], uint32(len(data)), &returned, nil)
	if errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Skipf("native NTFS controls unavailable: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if returned > uint32(len(data)) {
		t.Fatal("invalid native NTFS geometry response length")
	}
	g, err := decodeNTFSRawGeometry(data[:returned])
	if err != nil {
		t.Fatal(err)
	}
	buffer := alignedNTFSRawBuffer(int(g.record), g.sector)
	if _, err = windows.Seek(volume, g.firstLCN*g.cluster, 0); err != nil {
		t.Fatal(err)
	}
	if err = windows.ReadFile(volume, buffer, &returned, nil); err != nil {
		t.Fatal(err)
	}
	if int(returned) != len(buffer) {
		t.Fatal("short MFT record read")
	}
	record, err := decodeNTFSRawRecord(buffer, 0, g)
	if err != nil || record.file == nil || record.file.reference&ntfsReferenceMask != 0 {
		t.Fatalf("invalid live MFT record: %+v %v", record, err)
	}
	if !slices.ContainsFunc(record.file.names, func(name ntfsLayoutName) bool { return name.name == "$MFT" }) {
		t.Fatal("first record did not name $MFT")
	}
	metadataPath, _ := windows.UTF16PtrFromString(root + "$MFT")
	mft, err := windows.CreateFile(metadataPath, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(mft)
	runs, err := collectNTFSRetrievalPointers(func(input, output []byte) (uint32, error) {
		var n uint32
		err := windows.DeviceIoControl(mft, windows.FSCTL_GET_RETRIEVAL_POINTERS, &input[0], uint32(len(input)), &output[0], uint32(len(output)), &n, nil)
		return n, err
	}, g, func() error { return nil })
	if err != nil || len(runs) == 0 {
		t.Fatalf("live MFT extent mapping failed: %+v %v", runs, err)
	}
}
