//go:build windows

package platform

import (
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fsctlQueryFileLayout = 0x00090277
	queryLayoutRestart   = 0x01
	// Names, streams, basic information, and streams with no allocated clusters.
	// Extents and stream contents are deliberately omitted: the kernel already
	// supplies the allocated size, including compressed and sparse streams.
	queryLayoutFlags  = 0x02 | 0x04 | 0x10 | 0x20
	ntfsReferenceMask = uint64(1<<48 - 1)
	ntfsRootRecord    = 5
	maxLayoutBuffer   = 64 << 20
	maxNTFSIndexBytes = 512 << 20
)

// PrepareFastScan tries read-only direct MFT acquisition before the supported
// bulk layout interface. No elevation is requested; inaccessible or unsupported
// metadata falls back without publishing a partial index.
func (w Windows) PrepareFastScan(ctx context.Context, root string, checkpoint func() error, options FastScanOptions) (ScannerFilesystem, FastScanStats, error) {
	var stats FastScanStats
	if options.Timing {
		stats.Timing = &FastScanTimings{}
	}
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
	root = w.Canonicalize(root)
	volume := filepath.VolumeName(root)
	if len(volume) != 2 || volume[1] != ':' || !w.IsMountRoot(root) || w.IsLikelyNetworkFS(root) {
		return nil, stats, fmt.Errorf("fast NTFS scanning requires a local drive root")
	}
	_, filesystem := windowsVolumeInformation(root)
	if !strings.EqualFold(filesystem, "NTFS") {
		return nil, stats, fmt.Errorf("fast NTFS scanning is unavailable on filesystem %q", filesystem)
	}
	path, err := windows.UTF16PtrFromString(`\\.\` + volume)
	if err != nil {
		return nil, stats, err
	}
	handle, err := openNTFSLayoutVolume(path)
	if err != nil {
		return nil, stats, fmt.Errorf("open NTFS volume for bulk metadata (volume read permission may require running as administrator): %w", err)
	}
	defer windows.CloseHandle(handle)
	var volumeData [128]byte
	var returned uint32
	if err := windows.DeviceIoControl(handle, windows.FSCTL_GET_NTFS_VOLUME_DATA, nil, 0, &volumeData[0], uint32(len(volumeData)), &returned, nil); err != nil {
		return nil, stats, fmt.Errorf("read NTFS volume identity: %w", err)
	}
	if returned < 96 || returned > uint32(len(volumeData)) {
		return nil, stats, fmt.Errorf("truncated NTFS volume identity")
	}
	serial := binary.LittleEndian.Uint64(volumeData[:8])
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if checkpoint != nil {
			return checkpoint()
		}
		return nil
	}
	indexed, directStats, directErr := prepareDirectNTFS(w, root, handle, volumeData[:returned], check, stats.Timing)
	if directErr == nil {
		return indexed, directStats, nil
	}
	if err := check(); err != nil {
		return nil, directStats, err
	}
	if errors.Is(directErr, context.Canceled) || errors.Is(directErr, context.DeadlineExceeded) {
		return nil, directStats, directErr
	}
	return prepareNTFSLayout(w, root, serial, func(input, output []byte) (uint32, error) {
		var returned uint32
		err := windows.DeviceIoControl(handle, fsctlQueryFileLayout, &input[0], uint32(len(input)), &output[0], uint32(len(output)), &returned, nil)
		return returned, err
	}, check, directStats, directErr)
}

// Keep the fallback boundary testable without real volume access. Failed direct
// reads retain their timings and counters, but never their partial record map.
func prepareNTFSLayout(base ScannerFilesystem, root string, serial uint64, query func([]byte, []byte) (uint32, error), check func() error, direct FastScanStats, directErr error) (ScannerFilesystem, FastScanStats, error) {
	records, stats, err := collectNTFSLayout(query, check, direct.Timing)
	stats.Method = FastScanNTFSLayout
	if directErr != nil {
		stats.DirectFallbackReason = directErr.Error()
		stats.DirectRecords, stats.DirectBatches, stats.DirectBytes = direct.Records, direct.Batches, direct.Bytes
	}
	if err != nil {
		return nil, stats, err
	}
	started := stats.Timing.start()
	indexed, err := indexNTFSLayout(base, root, serial, records, check)
	stats.Timing.finish(fastScanHierarchy, started)
	if err != nil {
		return nil, stats, err
	}
	stats.DuplicateNames = indexed.duplicateNames
	stats.ReconciledDirectories = indexed.reconciledDirectories
	stats.ReconciledSubtrees = int64(len(indexed.nativeSubtrees))
	return indexed, stats, nil
}

func collectNTFSLayout(query func([]byte, []byte) (uint32, error), check func() error, timing *FastScanTimings) (map[uint64]*ntfsLayoutFile, FastScanStats, error) {
	stats := FastScanStats{Timing: timing}
	records := make(map[uint64]*ntfsLayoutFile)
	if err := check(); err != nil {
		return nil, stats, err
	}
	batches := newNTFSLayoutBatches(query, timing)
	// A synchronous control cannot be interrupted here. Join its worker before
	// returning so the volume handle and its buffers outlive every pending call.
	defer batches.close()
	var indexBytes int64
	for {
		if err := check(); err != nil {
			return nil, stats, err
		}
		started := timing.start()
		result, ok := <-batches.results
		timing.finish(fastScanWait, started)
		if !ok {
			return nil, stats, fmt.Errorf("NTFS query worker stopped without an end-of-file response")
		}
		if errors.Is(result.err, windows.ERROR_HANDLE_EOF) || errors.Is(result.err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if result.err != nil {
			return nil, stats, result.err
		}
		started = timing.start()
		batch, err := decodeNTFSLayout(result.buffer[:result.returned])
		timing.finish(fastScanDecode, started)
		if err != nil {
			return nil, stats, fmt.Errorf("NTFS metadata batch %d (%d bytes): %w", stats.Batches+1, result.returned, err)
		}
		if len(batch) == 0 {
			return nil, stats, fmt.Errorf("NTFS layout enumeration made no progress")
		}
		stats.Batches++
		stats.Bytes += int64(result.returned)
		started = timing.start()
		for _, file := range batch {
			if _, exists := records[file.reference]; exists {
				timing.finish(fastScanRecords, started)
				return nil, stats, fmt.Errorf("NTFS file table changed during enumeration (duplicate reference %#x)", file.reference)
			}
			records[file.reference] = file
			indexBytes += ntfsLayoutIndexCost(file)
		}
		timing.finish(fastScanRecords, started)
		stats.Records += int64(len(batch))
		if indexBytes > maxNTFSIndexBytes {
			return nil, stats, fmt.Errorf("NTFS metadata index exceeds the 512 MiB safety budget")
		}
		batches.release(result)
	}
	return records, stats, nil
}

type ntfsLayoutBatch struct {
	buffer   []byte
	returned uint32
	err      error
}

type ntfsLayoutBatches struct {
	results    chan ntfsLayoutBatch
	buffers    chan []byte
	stop, done chan struct{}
}

// One producer owns the API cursor and input buffer. Two output buffers let
// decoding overlap the next query without concurrent calls on the same cursor.
// A buffer is reused only after its consumer has finished decoding/indexing it.
func newNTFSLayoutBatches(query func([]byte, []byte) (uint32, error), timing *FastScanTimings) *ntfsLayoutBatches {
	batches := &ntfsLayoutBatches{results: make(chan ntfsLayoutBatch), buffers: make(chan []byte, 2), stop: make(chan struct{}), done: make(chan struct{})}
	// uint64 backing storage explicitly satisfies the control's alignment rule.
	for range 2 {
		batches.buffers <- alignedLayoutBuffer(4 << 20)
	}
	go func() {
		defer close(batches.done)
		defer close(batches.results)
		var input [4]uint64
		inputBytes := unsafe.Slice((*byte)(unsafe.Pointer(&input[0])), 32)
		flags := uint32(queryLayoutFlags | queryLayoutRestart)
		for {
			var buffer []byte
			select {
			case <-batches.stop:
				return
			case buffer = <-batches.buffers:
			}
			for {
				select {
				case <-batches.stop:
					return
				default:
				}
				binary.LittleEndian.PutUint32(inputBytes[4:8], flags)
				started := timing.start()
				returned, err := query(inputBytes, buffer)
				timing.finish(fastScanQuery, started)
				if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) && len(buffer) < maxLayoutBuffer {
					buffer = alignedLayoutBuffer(len(buffer) * 2)
					continue
				}
				if err != nil && !errors.Is(err, windows.ERROR_HANDLE_EOF) && !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
					err = fmt.Errorf("query NTFS file-table layout: %w", err)
				}
				if err == nil && returned > uint32(len(buffer)) {
					err = fmt.Errorf("invalid NTFS layout response length")
				}
				result := ntfsLayoutBatch{buffer: buffer, returned: returned, err: err}
				select {
				case <-batches.stop:
					return
				case batches.results <- result:
				}
				if err != nil {
					return
				}
				flags &^= queryLayoutRestart
				break
			}
		}
	}()
	return batches
}

func (b *ntfsLayoutBatches) release(batch ntfsLayoutBatch) { b.buffers <- batch.buffer }
func (b *ntfsLayoutBatches) close()                        { close(b.stop); <-b.done }

func ntfsLayoutIndexCost(file *ntfsLayoutFile) int64 {
	// Conservative estimate including maps, child links and name storage.
	cost := int64(192 + len(file.names)*48)
	for _, name := range file.names {
		cost += int64(len(name.name))
	}
	return cost
}

func openNTFSLayoutVolume(path *uint16) (windows.Handle, error) {
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, 0, 0)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// FILE_ANY_ACCESS controls may also work with a metadata-only handle,
		// depending on the volume's security policy. Never request write access.
		return windows.CreateFile(path, 0, share, nil, windows.OPEN_EXISTING, 0, 0)
	}
	return handle, err
}

func alignedLayoutBuffer(size int) []byte {
	words := make([]uint64, (size+7)/8)
	return unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), size)
}

type ntfsLayoutName struct {
	parent uint64
	name   string
}
type ntfsLayoutFile struct {
	reference               uint64
	attributes              uint32
	names                   []ntfsLayoutName
	logical, allocated      int64
	creation, access, write uint64
	native                  bool // optional metadata must be obtained by directory enumeration
	reparseTag              uint32
	hasReparseTag           bool
}

func (f *ntfsLayoutFile) needsNativeMetadata() bool {
	if f.native {
		return true
	}
	if f.attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return f.hasReparseTag
	}
	if !f.hasReparseTag {
		return true
	}
	// Other tags may represent ordinary files whose sizes, allocation and
	// attributes are projected by a filter (WOF, cloud, deduplication, etc.).
	// Their raw on-disk state must not be classified as a non-regular file.
	switch f.reparseTag {
	case windows.IO_REPARSE_TAG_SYMLINK, windows.IO_REPARSE_TAG_MOUNT_POINT, windowsReparseTagAFUnix:
		return false
	default:
		return true
	}
}

// All offsets from the kernel are checked before slicing. Each linked entry
// must move forward within the response, preventing cycles/overreads. A next-file
// offset locates another header; it is not a documented bound on this file's
// separately referenced metadata.
func decodeNTFSLayout(data []byte) ([]*ntfsLayoutFile, error) {
	var count, fileIndex int
	fileIndex = -1
	var reference uint64
	bad := func(field string, offset int, format string, args ...any) ([]*ntfsLayoutFile, error) {
		detail := fmt.Sprintf(format, args...)
		if fileIndex < 0 {
			return nil, fmt.Errorf("NTFS layout %s at byte %d: %s", field, offset, detail)
		}
		return nil, fmt.Errorf("NTFS layout file %d/%d (reference %#x), %s at byte %d: %s", fileIndex+1, count, reference, field, offset, detail)
	}
	if len(data) < 16 {
		return bad("output header", 0, "need 16 bytes, got %d", len(data))
	}
	countValue := binary.LittleEndian.Uint32(data)
	if uint64(countValue) > uint64((len(data)-16)/40) {
		return bad("FileEntryCount", 0, "%d headers cannot fit in %d bytes", countValue, len(data))
	}
	count = int(countValue)
	position := int(binary.LittleEndian.Uint32(data[4:]))
	files := make([]*ntfsLayoutFile, 0, count)
	for i := 0; i < count; i++ {
		fileIndex, reference = i, 0
		if position < 16 || position > len(data)-40 {
			return bad("file header", position, "40-byte header is outside the %d-byte response", len(data))
		}
		entry := data[position:]
		reference = binary.LittleEndian.Uint64(entry[16:])
		version := binary.LittleEndian.Uint32(entry)
		if version != 1 {
			return bad("file Version", position, "got %d, supported version is 1", version)
		}
		next := int(binary.LittleEndian.Uint32(entry[4:]))
		if next != 0 {
			if next < 40 || next > len(entry) || i == count-1 {
				return bad("NextFileOffset", position+4, "got %d with %d remaining bytes", next, len(entry))
			}
		} else if i != count-1 {
			return bad("NextFileOffset", position+4, "chain ended before FileEntryCount")
		}
		file := &ntfsLayoutFile{reference: reference, attributes: binary.LittleEndian.Uint32(entry[12:])}
		infoOffset := int(binary.LittleEndian.Uint32(entry[32:]))
		if infoOffset != 0 {
			if infoOffset < 40 || infoOffset > len(entry) {
				return bad("ExtraInfoOffset", position+32, "got %d with %d remaining bytes", infoOffset, len(entry))
			}
			infoLength := int(binary.LittleEndian.Uint32(entry[36:]))
			if infoLength < 0 || infoLength > len(entry)-infoOffset {
				return bad("ExtraInfoLength", position+36, "got %d with %d accessible bytes", infoLength, len(entry)-infoOffset)
			}
			// A zero length is the legacy form, guaranteed through Usn. With
			// an explicit length, consume only the basic fields we actually use.
			if infoLength == 0 {
				infoLength = 56
			}
			if infoLength < 36 {
				file.native = true
			} else {
				if infoOffset > len(entry)-36 {
					return bad("basic file information", position+infoOffset, "need 36 accessible bytes")
				}
				file.creation = binary.LittleEndian.Uint64(entry[infoOffset:])
				file.access = binary.LittleEndian.Uint64(entry[infoOffset+8:])
				file.write = binary.LittleEndian.Uint64(entry[infoOffset+16:])
				file.attributes = binary.LittleEndian.Uint32(entry[infoOffset+32:])
			}
		} else {
			// The API explicitly allows missing extra information. Obtain the
			// entry's timestamps and attributes through its parent when needed.
			file.native = true
		}
		if reference&ntfsReferenceMask == ntfsRootRecord {
			file.attributes |= windows.FILE_ATTRIBUTE_DIRECTORY
		}
		nameOffset := int(binary.LittleEndian.Uint32(entry[24:]))
		for nameOffset != 0 {
			if nameOffset < 40 || nameOffset > len(entry)-24 {
				return bad("name offset", position+nameOffset, "need 24 accessible bytes within %d remaining bytes", len(entry))
			}
			nameEntry := entry[nameOffset:]
			nextName := int(binary.LittleEndian.Uint32(nameEntry))
			if nextName != 0 {
				if nextName < 24 || nextName > len(nameEntry) {
					return bad("NextNameOffset", position+nameOffset, "got %d with %d accessible bytes", nextName, len(nameEntry))
				}
				nameEntry = nameEntry[:nextName]
			}
			nameLength := int(binary.LittleEndian.Uint32(nameEntry[16:]))
			name, valid := ntfsLayoutString(nameEntry, 24, nameLength)
			if !valid || name == "" || strings.ContainsAny(name, "\\/:\x00") || name == ".." {
				return bad("FileName", position+nameOffset+24, "invalid component or UTF-16 length %d (%d accessible bytes)", nameLength, len(nameEntry)-24)
			}
			nameFlags := binary.LittleEndian.Uint32(nameEntry[4:])
			if nameFlags&3 != 2 { // DOS-only aliases are not additional hard links.
				file.names = append(file.names, ntfsLayoutName{binary.LittleEndian.Uint64(nameEntry[8:]), name})
			}
			if nextName == 0 {
				break
			}
			nameOffset += nextName
		}
		streamOffset := int(binary.LittleEndian.Uint32(entry[28:]))
		hasData := false
		for streamOffset != 0 {
			if streamOffset < 40 || streamOffset > len(entry)-48 {
				return bad("stream offset", position+streamOffset, "need 48 accessible bytes within %d remaining bytes", len(entry))
			}
			stream := entry[streamOffset:]
			streamVersion := binary.LittleEndian.Uint32(stream)
			if streamVersion != 1 && streamVersion != 2 {
				return bad("stream Version", position+streamOffset, "got %d, supported versions are 1 and 2", streamVersion)
			}
			nextStream := int(binary.LittleEndian.Uint32(stream[4:]))
			if nextStream != 0 {
				if nextStream < 48 || nextStream > len(stream) {
					return bad("NextStreamOffset", position+streamOffset+4, "got %d with %d accessible bytes", nextStream, len(stream))
				}
				stream = stream[:nextStream]
			}
			identifierLength := int(binary.LittleEndian.Uint32(stream[44:]))
			identifier, valid := ntfsLayoutString(stream, 48, identifierLength)
			if !valid {
				return bad("StreamIdentifierLength", position+streamOffset+44, "got %d with %d accessible bytes", identifierLength, len(stream)-48)
			}
			// In v1 bytes 32..39 are reserved, not an attribute type. The
			// common header and identifier positions are identical in v1/v2.
			attributeType := binary.LittleEndian.Uint32(stream[36:])
			isData := streamVersion == 1 || attributeType == 0 || attributeType == 0x80
			if isData && (identifier == "" || identifier == "::$DATA") {
				if hasData {
					return bad("default data stream", position+streamOffset, "more than one unnamed data stream")
				}
				hasData = true
				file.allocated = int64(binary.LittleEndian.Uint64(stream[16:]))
				file.logical = int64(binary.LittleEndian.Uint64(stream[24:]))
				if file.allocated < 0 || file.logical < 0 {
					return bad("stream sizes", position+streamOffset+16, "negative allocated size %d or logical size %d", file.allocated, file.logical)
				}
			}
			if nextStream == 0 {
				break
			}
			streamOffset += nextStream
		}
		if !hasData && file.attributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) == 0 && len(file.names) > 0 && file.reference&ntfsReferenceMask >= 16 {
			// Empty files and unusual stream sets can omit the default stream.
			// Resolve their real sizes natively, never infer zero allocation.
			file.native = true
		}
		files = append(files, file)
		position += next
	}
	return files, nil
}

func ntfsLayoutString(data []byte, offset, byteLength int) (string, bool) {
	if offset < 0 || byteLength < 0 || byteLength%2 != 0 || offset > len(data)-byteLength {
		return "", false
	}
	for i := offset; i < offset+byteLength; i += 2 {
		if binary.LittleEndian.Uint16(data[i:]) == 0 {
			return "", false
		}
	}
	return windowsUTF16String(data, offset, byteLength)
}

type ntfsLayoutChild struct {
	file *ntfsLayoutFile
	name string
}
type ntfsLayoutDirectory struct {
	children   []ntfsLayoutChild
	native     bool
	reconciled bool
}
type ntfsIndexedFilesystem struct {
	ScannerFilesystem
	serial                                uint64
	directories                           map[string]*ntfsLayoutDirectory
	nativeSubtrees                        map[string]struct{}
	duplicateNames, reconciledDirectories int64
}

func indexNTFSLayout(base ScannerFilesystem, root string, serial uint64, records map[uint64]*ntfsLayoutFile, check func() error) (*ntfsIndexedFilesystem, error) {
	indexed := &ntfsIndexedFilesystem{ScannerFilesystem: base, serial: serial, directories: make(map[string]*ntfsLayoutDirectory), nativeSubtrees: make(map[string]struct{})}
	paths := make(map[uint64]string)
	var indexBytes int64
	var rootReference uint64
	steps := 0
	for ref, file := range records {
		steps++
		if steps%1024 == 0 {
			if err := check(); err != nil {
				return nil, err
			}
		}
		// Repeated namespace entries describe one link. Deduplicate before
		// resolving ancestry or computing link counts, including directories.
		if len(file.names) > 1 {
			slices.SortFunc(file.names, func(a, b ntfsLayoutName) int {
				if order := cmp.Compare(a.parent, b.parent); order != 0 {
					return order
				}
				return strings.Compare(a.name, b.name)
			})
			before := len(file.names)
			file.names = slices.Compact(file.names)
			indexed.duplicateNames += int64(before - len(file.names))
		}
		indexBytes += ntfsLayoutIndexCost(file)
		if ref&ntfsReferenceMask == ntfsRootRecord && file.attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			if rootReference != 0 {
				return nil, fmt.Errorf("NTFS root record changed during enumeration")
			}
			rootReference = ref
		}
	}
	if rootReference == 0 {
		return nil, fmt.Errorf("NTFS file table did not contain the root directory")
	}
	paths[rootReference] = root
	indexed.directories[root] = &ntfsLayoutDirectory{}
	// Resolve directory ancestors iteratively, retaining full sequence numbers.
	// Files under NTFS's reserved metadata directories are left unaccounted, as
	// in the ordinary scanner; MFT bookkeeping is not presented as user files.
	resolve := func(reference uint64) (string, error) {
		chain := make([]uint64, 0, 16)
		current := reference
		for {
			if _, known := paths[current]; known {
				break
			}
			if current&ntfsReferenceMask < 16 {
				if current&ntfsReferenceMask == ntfsRootRecord {
					return "", fmt.Errorf("NTFS root sequence number changed during enumeration")
				}
				metadata := records[current]
				if metadata == nil || metadata.attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
					return "", fmt.Errorf("missing NTFS metadata parent directory")
				}
				paths[current] = ""
				break
			}
			file := records[current]
			if file == nil || file.attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || len(file.names) != 1 {
				return "", fmt.Errorf("NTFS directory ancestry changed or is unsupported")
			}
			if len(chain) >= 1024 {
				return "", fmt.Errorf("cyclic or excessively deep NTFS directory ancestry")
			}
			chain = append(chain, current)
			current = file.names[0].parent
		}
		path := paths[current]
		for i := len(chain) - 1; i >= 0; i-- {
			ref := chain[i]
			if path != "" {
				path = filepath.Join(path, records[ref].names[0].name)
			}
			if len(path) > 32760 {
				return "", fmt.Errorf("NTFS path exceeds Windows path limit")
			}
			indexBytes += int64(len(path)) + 96
			if indexBytes > maxNTFSIndexBytes {
				return "", fmt.Errorf("NTFS metadata index exceeds the estimated 512 MiB safety budget")
			}
			paths[ref] = path
		}
		return paths[reference], nil
	}
	for ref, file := range records {
		steps++
		if steps%1024 == 0 {
			if err := check(); err != nil {
				return nil, err
			}
		}
		if ref&ntfsReferenceMask < 16 || len(file.names) == 0 {
			continue
		}
		if file.attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			path, err := resolve(ref)
			if err != nil {
				return nil, err
			}
			if path != "" {
				if _, exists := indexed.directories[path]; exists {
					// The path can refer to an old and a new directory while a
					// live table is read. Descendants also need current native
					// reads; mixing either stale subtree would be inaccurate.
					indexed.nativeSubtrees[path] = struct{}{}
					indexed.reconcileDirectory(path)
					continue
				}
				indexed.directories[path] = &ntfsLayoutDirectory{}
			}
		}
	}
	for ref, file := range records {
		steps++
		if steps%1024 == 0 {
			if err := check(); err != nil {
				return nil, err
			}
		}
		if ref&ntfsReferenceMask < 16 {
			continue
		}
		for _, name := range file.names {
			if name.name == "." {
				return nil, fmt.Errorf("unexpected NTFS directory self-reference")
			}
			parent, err := resolve(name.parent)
			if err != nil {
				return nil, err
			}
			if parent == "" {
				continue
			}
			directory := indexed.directories[parent]
			if directory == nil {
				return nil, fmt.Errorf("missing NTFS parent directory")
			}
			directory.children = append(directory.children, ntfsLayoutChild{file, name.name})
			// Preserve native handling of junctions, deduplication, cloud files,
			// symlinks and other reparse types without guessing their tags.
			directory.native = directory.native || file.needsNativeMetadata()
		}
	}
	for path, directory := range indexed.directories {
		steps++
		if steps%1024 == 0 {
			if err := check(); err != nil {
				return nil, err
			}
		}
		slices.SortFunc(directory.children, func(a, b ntfsLayoutChild) int {
			if order := strings.Compare(a.name, b.name); order != 0 {
				return order
			}
			return cmp.Compare(a.file.reference, b.file.reference)
		})
		children := directory.children[:0]
		for i := 0; i < len(directory.children); {
			end := i + 1
			for end < len(directory.children) && directory.children[end].name == directory.children[i].name {
				end++
			}
			if end-i == 1 {
				children = append(children, directory.children[i])
			} else {
				indexed.reconcileDirectory(path)
				// Do not choose a file ID or combine allocations for a path
				// claimed by different records. Reread this parent instead.
				for _, child := range directory.children[i:end] {
					if child.file.attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
						indexed.nativeSubtrees[filepath.Join(path, child.name)] = struct{}{}
					}
				}
			}
			i = end
		}
		clear(directory.children[len(children):])
		directory.children = children
	}
	if err := check(); err != nil {
		return nil, err
	}
	return indexed, nil
}

func (f *ntfsIndexedFilesystem) reconcileDirectory(path string) {
	directory := f.directories[path]
	directory.native = true
	if !directory.reconciled {
		directory.reconciled = true
		f.reconciledDirectories++
	}
}

func (f *ntfsIndexedFilesystem) indexedDirectory(path string) *ntfsLayoutDirectory {
	path = f.Canonicalize(path)
	directory := f.directories[path]
	if directory == nil || directory.native {
		return nil
	}
	if len(f.nativeSubtrees) > 0 {
		for current := path; ; {
			if _, native := f.nativeSubtrees[current]; native {
				return nil
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}
	return directory
}

func (f *ntfsIndexedFilesystem) ReadDir(path string) ([]DirectoryEntry, error) {
	entries, _, err := f.ReadDirWithDiagnostics(path)
	return entries, err
}

func (f *ntfsIndexedFilesystem) ReadDirWithDiagnostics(path string) ([]DirectoryEntry, *DirectoryReadDiagnostic, error) {
	directory := f.indexedDirectory(path)
	if directory == nil {
		return ReadDirWithDiagnostics(f.ScannerFilesystem, path)
	}
	return f.directoryEntries(directory), nil, nil
}

func (f *ntfsIndexedFilesystem) ReadDirWithTimings(path string) ([]DirectoryEntry, *DirectoryReadDiagnostic, *DirectoryReadTimings, error) {
	directory := f.indexedDirectory(path)
	if directory == nil {
		return ReadDirWithTimings(f.ScannerFilesystem, path)
	}
	return f.directoryEntries(directory), nil, nil, nil
}

func (f *ntfsIndexedFilesystem) directoryEntries(directory *ntfsLayoutDirectory) []DirectoryEntry {
	entries := make([]DirectoryEntry, len(directory.children))
	for i, child := range directory.children {
		entries[i] = ntfsDirectoryEntry(child, f.serial)
	}
	return entries
}

func ntfsDirectoryEntry(child ntfsLayoutChild, serial uint64) DirectoryEntry {
	file := child.file
	filetime := func(value uint64) syscall.Filetime {
		return syscall.Filetime{LowDateTime: uint32(value), HighDateTime: uint32(value >> 32)}
	}
	write := filetime(file.write)
	info := windowsBatchFileInfo{name: child.name, size: file.logical, mode: windowsMode(file.attributes, file.reparseTag), modTime: time.Unix(0, write.Nanoseconds()), attributes: file.attributes, creation: filetime(file.creation), access: filetime(file.access), write: write}
	return DirectoryEntry{DirEntry: windowsBatchDirEntry{info}, Usage: FileUsage{AllocatedSize: file.allocated, Identity: FileIdentity{Volume: serial, Low: file.reference}, HasIdentity: true, LinkCount: uint64(len(file.names)), HasLinkCount: true}, HasUsage: true, Hidden: file.attributes&windows.FILE_ATTRIBUTE_HIDDEN != 0, HasHidden: true}
}

var _ FastScanProvider = Windows{}
