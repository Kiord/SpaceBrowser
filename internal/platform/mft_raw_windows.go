//go:build windows

package platform

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	directMFTBatchBytes       = 8 << 20
	maxNTFSRawRuns            = 1 << 20
	maxNTFSAttributeListBytes = 8 << 20
)

// Direct reads use the logical volume (not PhysicalDrive), so Windows retains
// responsibility for volume translation, including an unlocked BitLocker drive.
// Both handles request read/metadata access only. No volume lock, dismount,
// write, privilege adjustment or automatic elevation is performed.
func prepareDirectNTFS(base ScannerFilesystem, root string, volume windows.Handle, volumeData []byte, check func() error, timing *FastScanTimings) (ScannerFilesystem, FastScanStats, error) {
	stats := FastScanStats{Method: FastScanNTFSDirect, Timing: timing}
	geometry, err := decodeNTFSRawGeometry(volumeData)
	if err != nil {
		return nil, stats, err
	}
	path, err := windows.UTF16PtrFromString(root + "$MFT")
	if err != nil {
		return nil, stats, err
	}
	mft, err := windows.CreateFile(path, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, stats, fmt.Errorf("open $MFT extent metadata (direct MFT reading requires administrator access): %w", err)
	}
	defer windows.CloseHandle(mft)
	queryRuns := func() ([]ntfsRawRun, error) {
		started := timing.start()
		defer timing.finish(fastScanDirectMap, started)
		return collectNTFSRetrievalPointers(func(input, output []byte) (uint32, error) {
			var returned uint32
			err := windows.DeviceIoControl(mft, windows.FSCTL_GET_RETRIEVAL_POINTERS, &input[0], uint32(len(input)), &output[0], uint32(len(output)), &returned, nil)
			return returned, err
		}, geometry, check)
	}
	runs, err := queryRuns()
	if err != nil {
		return nil, stats, err
	}
	makeReader := func(stage fastScanTimingStage) func([]byte, int64) error {
		return func(output []byte, offset int64) error {
			started := timing.start()
			defer func() {
				if timing != nil {
					elapsed := time.Since(started)
					timing.record(stage, elapsed)
					timing.record(fastScanDirectRead, elapsed)
				}
			}()
			if _, err := windows.Seek(volume, offset, 0); err != nil {
				return err
			}
			var returned uint32
			if err := windows.ReadFile(volume, output, &returned, nil); err != nil {
				return err
			}
			if returned != uint32(len(output)) {
				return fmt.Errorf("short raw volume read: got %d of %d bytes", returned, len(output))
			}
			return nil
		}
	}
	listRead := makeReader(fastScanDirectListRead)
	checkedListRead := func(output []byte, offset int64) error {
		if err := check(); err != nil {
			return err
		}
		return listRead(output, offset)
	}
	records, stats, err := collectDirectMFTWithReaders(geometry, runs, makeReader(fastScanDirectTableRead), checkedListRead, check, timing)
	if err != nil {
		return nil, stats, err
	}
	// A live volume can relocate/grow the table during a scan. Recheck the
	// complete mapping before publishing; do not assemble paths from moved data.
	current, err := queryRuns()
	if err != nil {
		return nil, stats, fmt.Errorf("recheck MFT extents: %w", err)
	}
	var latest [128]byte
	var returned uint32
	if err := windows.DeviceIoControl(volume, windows.FSCTL_GET_NTFS_VOLUME_DATA, nil, 0, &latest[0], uint32(len(latest)), &returned, nil); err != nil {
		return nil, stats, fmt.Errorf("recheck MFT geometry: %w", err)
	}
	if returned > uint32(len(latest)) {
		return nil, stats, fmt.Errorf("invalid NTFS volume response length")
	}
	updated, err := decodeNTFSRawGeometry(latest[:returned])
	if err != nil || geometry != updated || !slices.Equal(runs, current) {
		return nil, stats, fmt.Errorf("MFT storage changed during direct reading")
	}
	started := timing.start()
	indexed, err := indexNTFSReferences(base, root, geometry.serial, records, check)
	timing.finish(fastScanHierarchy, started)
	if err != nil {
		return nil, stats, err
	}
	stats.DuplicateNames, stats.ReconciledDirectories = indexed.duplicateNames, indexed.reconciledDirectories
	stats.ReconciledSubtrees = indexed.reconciledSubtrees
	return indexed, stats, nil
}

type ntfsRawGeometry struct {
	serial                     uint64
	sector, cluster, record    int64
	clusters, length, firstLCN int64
}

func decodeNTFSRawGeometry(data []byte) (ntfsRawGeometry, error) {
	var g ntfsRawGeometry
	if len(data) < 104 || binary.LittleEndian.Uint16(data[100:]) != 3 || binary.LittleEndian.Uint16(data[102:]) > 1 {
		return g, fmt.Errorf("direct MFT reading supports NTFS 3.0/3.1 volume metadata")
	}
	g.serial = binary.LittleEndian.Uint64(data)
	g.sector, g.cluster, g.record = int64(binary.LittleEndian.Uint32(data[40:])), int64(binary.LittleEndian.Uint32(data[44:])), int64(binary.LittleEndian.Uint32(data[48:]))
	g.clusters, g.length, g.firstLCN = int64(binary.LittleEndian.Uint64(data[16:])), int64(binary.LittleEndian.Uint64(data[56:])), int64(binary.LittleEndian.Uint64(data[64:]))
	powerOfTwo := func(n int64) bool { return n > 0 && n&(n-1) == 0 }
	if !powerOfTwo(g.sector) || g.sector < 512 || g.sector > 65536 || !powerOfTwo(g.cluster) || g.cluster < g.sector || g.cluster > 2<<20 || !powerOfTwo(g.record) || g.record < g.sector || g.record > 65536 || g.clusters <= 0 || g.clusters > math.MaxInt64/g.cluster || g.length <= 0 || g.length%g.record != 0 || g.length > g.clusters*g.cluster || g.firstLCN < 0 || g.firstLCN >= g.clusters {
		return ntfsRawGeometry{}, fmt.Errorf("invalid or unsupported direct MFT geometry")
	}
	return g, nil
}

// VCN/LCN and count are in clusters. lcn == -1 denotes a sparse run.
type ntfsRawRun struct{ vcn, lcn, count int64 }

func collectNTFSRetrievalPointers(query func([]byte, []byte) (uint32, error), g ntfsRawGeometry, check func() error) ([]ntfsRawRun, error) {
	input, output := alignedLayoutBuffer(8), alignedLayoutBuffer(64<<10)
	var runs []ntfsRawRun
	var next int64
	for {
		if err := check(); err != nil {
			return nil, err
		}
		binary.LittleEndian.PutUint64(input, uint64(next))
		returned, err := query(input, output)
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, fmt.Errorf("read $MFT extent map: %w", err)
		}
		if returned < 16 || returned > uint32(len(output)) {
			return nil, fmt.Errorf("invalid $MFT retrieval-pointer response length")
		}
		data := output[:returned]
		count := int(binary.LittleEndian.Uint32(data))
		start := int64(binary.LittleEndian.Uint64(data[8:]))
		if count == 0 || count > (len(data)-16)/16 || start != next || len(runs)+count > maxNTFSRawRuns {
			return nil, fmt.Errorf("invalid or discontinuous $MFT extent map")
		}
		for i := 0; i < count; i++ {
			extent := data[16+i*16:]
			end, lcn := int64(binary.LittleEndian.Uint64(extent)), int64(binary.LittleEndian.Uint64(extent[8:]))
			if end <= next || end > g.clusters || lcn < 0 || lcn >= g.clusters || end-next > g.clusters-lcn {
				return nil, fmt.Errorf("invalid or sparse $MFT storage extent")
			}
			runs = append(runs, ntfsRawRun{next, lcn, end - next})
			next = end
		}
		if err == nil {
			break
		}
	}
	if len(runs) == 0 || runs[0].lcn != g.firstLCN || next < (g.length+g.cluster-1)/g.cluster {
		return nil, fmt.Errorf("$MFT extent map does not cover the table")
	}
	return runs, nil
}

func alignedNTFSRawBuffer(size int, alignment int64) []byte {
	buffer := make([]byte, size+int(alignment)-1)
	shift := int((-uintptr(unsafe.Pointer(&buffer[0]))) & uintptr(alignment-1))
	return buffer[shift : shift+size]
}

// Reads a logical metadata stream across fragmented extents. The caller uses
// sector-aligned offsets, lengths and buffers; holes are never interpreted as
// valid metadata. The read callback owns the volume's synchronous file pointer.
func readNTFSRawStream(output []byte, offset int64, runs []ntfsRawRun, g ntfsRawGeometry, read func([]byte, int64) error) error {
	if offset < 0 || offset%g.sector != 0 || int64(len(output))%g.sector != 0 {
		return fmt.Errorf("unaligned metadata stream read")
	}
	for len(output) > 0 {
		vcn := offset / g.cluster
		i := sort.Search(len(runs), func(i int) bool { return runs[i].vcn+runs[i].count > vcn })
		if i == len(runs) || runs[i].vcn > vcn || runs[i].lcn < 0 {
			return fmt.Errorf("metadata stream has an unmapped or sparse extent")
		}
		run := runs[i]
		within := offset - run.vcn*g.cluster
		length := min(int64(len(output)), run.count*g.cluster-within)
		if length <= 0 || run.lcn > g.clusters-run.count {
			return fmt.Errorf("invalid metadata stream extent")
		}
		if err := read(output[:int(length)], run.lcn*g.cluster+within); err != nil {
			return err
		}
		output = output[int(length):]
		offset += length
	}
	return nil
}

// The table is streamed in bounded batches; only parsed metadata is retained.
// Record fixups mutate the batch, so no consumer retains slices into it.
func collectDirectMFT(g ntfsRawGeometry, runs []ntfsRawRun, read func([]byte, int64) error, check func() error, timing *FastScanTimings) (map[uint64]*ntfsLayoutFile, FastScanStats, error) {
	return collectDirectMFTWithReaders(g, runs, read, read, check, timing)
}

func collectDirectMFTWithReaders(g ntfsRawGeometry, runs []ntfsRawRun, tableRead, listRead func([]byte, int64) error, check func() error, timing *FastScanTimings) (map[uint64]*ntfsLayoutFile, FastScanStats, error) {
	stats := FastScanStats{Method: FastScanNTFSDirect, Timing: timing}
	records := make(map[uint64]*ntfsLayoutFile)
	complex := make(map[uint64]*ntfsRawRecord)
	extensions := make(map[uint64]*ntfsRawRecord)
	if err := check(); err != nil {
		return nil, stats, err
	}
	batches := newDirectMFTBatches(g, runs, tableRead)
	closed := false
	defer func() {
		if !closed {
			batches.close()
		}
	}()
	// Reuse storage for ordinary record wrappers. Only files requiring extension
	// assembly retain one on the heap, avoiding an extra allocation per file.
	decoded := make([]ntfsRawRecord, 0, directMFTBatchBytes/g.record)
	var cost int64
	var processedEnd int64
	for {
		if err := check(); err != nil {
			return nil, stats, err
		}
		started := timing.start()
		batch, ok := <-batches.results
		timing.finish(fastScanWait, started)
		if !ok {
			if processedEnd != g.length {
				return nil, stats, fmt.Errorf("direct MFT reader stopped at byte %d of %d", processedEnd, g.length)
			}
			break
		}
		if batch.err != nil {
			return nil, stats, batch.err
		}
		buffer, offset, length := batch.buffer, batch.offset, batch.length
		processedEnd = offset + length
		stats.Bytes += length
		stats.Batches++
		started = timing.start()
		var decodeErr error
		for pos := int64(0); pos < length; pos += g.record {
			number := (offset + pos) / g.record
			if number%1024 == 0 {
				if decodeErr = check(); decodeErr != nil {
					break
				}
			}
			var record ntfsRawRecord
			record, decodeErr = decodeNTFSRawRecord(buffer[int(pos):int(pos+g.record)], uint64(number), g)
			if decodeErr != nil {
				decodeErr = fmt.Errorf("MFT record %d: %w", number, decodeErr)
				break
			}
			if record.file == nil {
				continue
			}
			decoded = append(decoded, record)
		}
		timing.finish(fastScanDecode, started)
		if decodeErr != nil {
			return nil, stats, decodeErr
		}
		started = timing.start()
		for i := range decoded {
			record := &decoded[i]
			cost += ntfsLayoutIndexCost(record.file) + int64(len(record.keys)*16+len(record.list)*24+len(record.listRuns)*24) + record.listSize*2
			if record.hasList || record.base != 0 {
				cost += 160
			}
			if cost > maxNTFSIndexBytes {
				decodeErr = fmt.Errorf("direct MFT index exceeds the 512 MiB safety budget")
				break
			}
			if record.base != 0 {
				// Extensions containing only directory indexes or named streams
				// have no effect on our default-stream totals. Their list entries
				// are filtered out too; do not require them as accounting members.
				if len(record.keys) > 0 || record.hasList {
					retained := *record
					extensions[record.file.reference] = &retained
				}
				continue
			}
			records[record.file.reference] = record.file
			stats.Records++
			if record.hasList {
				retained := *record
				complex[record.file.reference] = &retained
			} else {
				record.finalize()
			}
		}
		timing.finish(fastScanRecords, started)
		if decodeErr != nil {
			return nil, stats, decodeErr
		}
		clear(decoded)
		decoded = decoded[:0]
		batches.buffers <- buffer
	}
	// Joining before attribute-list reads is essential: both readers use the
	// same synchronous volume handle and therefore share its seek position.
	batches.close()
	closed = true
	started := timing.start()
	err := mergeNTFSRawExtensions(complex, extensions, g, listRead, check)
	timing.finish(fastScanRecords, started)
	if err != nil {
		return nil, stats, err
	}
	return records, stats, nil
}

type directMFTBatch struct {
	buffer         []byte
	offset, length int64
	err            error
}

type directMFTBatches struct {
	results    chan directMFTBatch
	buffers    chan []byte
	stop, done chan struct{}
	closeOnce  sync.Once
}

func newDirectMFTBatches(g ntfsRawGeometry, runs []ntfsRawRun, read func([]byte, int64) error) *directMFTBatches {
	b := &directMFTBatches{results: make(chan directMFTBatch), buffers: make(chan []byte, 2), stop: make(chan struct{}), done: make(chan struct{})}
	for range 2 {
		b.buffers <- alignedNTFSRawBuffer(directMFTBatchBytes, g.sector)
	}
	readFragment := func(output []byte, offset int64) error {
		select {
		case <-b.stop:
			return context.Canceled
		default:
		}
		return read(output, offset)
	}
	go func() {
		defer close(b.done)
		defer close(b.results)
		for offset := int64(0); offset < g.length; {
			var buffer []byte
			select {
			case <-b.stop:
				return
			case buffer = <-b.buffers:
			}
			select {
			case <-b.stop:
				return
			default:
			}
			length := min(int64(len(buffer)), g.length-offset)
			err := readNTFSRawStream(buffer[:int(length)], offset, runs, g, readFragment)
			if err != nil {
				err = fmt.Errorf("read raw MFT at byte %d: %w", offset, err)
			}
			select {
			case <-b.stop:
				return
			case b.results <- directMFTBatch{buffer, offset, length, err}:
			}
			if err != nil {
				return
			}
			offset += length
		}
	}()
	return b
}

func (b *directMFTBatches) close() { b.closeOnce.Do(func() { close(b.stop) }); <-b.done }
