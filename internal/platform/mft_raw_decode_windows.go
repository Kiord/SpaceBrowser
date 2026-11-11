//go:build windows

package platform

import (
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
)

// NTFS 3.x disk structures are documented in Microsoft's Win32 DevNotes:
// master-file-table, file-record-segment-header, attribute-record-header,
// multi-sector-header, file-name and attribute-list-entry. All offsets are
// checked against the record's used length before any attribute is inspected.
type ntfsRawAttributeKey struct {
	kind     uint32
	instance uint16
	vcn      int64
}
type ntfsRawAttributeRef struct {
	reference uint64
	key       ntfsRawAttributeKey
}
type ntfsRawRecord struct {
	file                 *ntfsLayoutFile
	base                 uint64
	keys                 []ntfsRawAttributeKey
	list                 []ntfsRawAttributeRef
	listRuns             []ntfsRawRun
	listSize             int64
	hasList, basic, data bool
}

func (r *ntfsRawRecord) finalize() {
	// Missing optional accounting is resolved natively through this file's
	// parent. In particular, absence of $DATA does not imply an empty file.
	r.file.native = r.file.native || !r.basic || (!r.data && r.file.attributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) == 0)
}

func decodeNTFSRawRecord(record []byte, number uint64, g ntfsRawGeometry) (ntfsRawRecord, error) {
	bad := func(field string) (ntfsRawRecord, error) {
		return ntfsRawRecord{}, fmt.Errorf("invalid NTFS %s", field)
	}
	if len(record) < 48 || int64(len(record)) != g.record || number > ntfsReferenceMask {
		return bad("record length")
	}
	flags := binary.LittleEndian.Uint16(record[22:])
	if flags&1 == 0 {
		return ntfsRawRecord{}, nil
	} // unused/deleted slots are not files
	if string(record[:4]) != "FILE" {
		return bad("record signature")
	}
	usa, count := int(binary.LittleEndian.Uint16(record[4:])), int(binary.LittleEndian.Uint16(record[6:]))
	// The protection stride is always 512, including on 4 KiB-sector devices.
	if usa < 42 || usa%2 != 0 || count != len(record)/512+1 || usa > 510-count*2 {
		return bad("update-sequence array")
	}
	sequence := binary.LittleEndian.Uint16(record[usa:])
	for i := 1; i < count; i++ {
		end := i*512 - 2
		if binary.LittleEndian.Uint16(record[end:]) != sequence {
			return bad("record fixup (torn or changed record)")
		}
	}
	for i := 1; i < count; i++ {
		copy(record[i*512-2:i*512], record[usa+i*2:usa+i*2+2])
	}
	used, allocated := int(binary.LittleEndian.Uint32(record[24:])), int(binary.LittleEndian.Uint32(record[28:]))
	first := int(binary.LittleEndian.Uint16(record[20:]))
	if allocated != len(record) || used < first+4 || used > len(record) || first < usa+count*2 || first%8 != 0 {
		return bad("attribute bounds")
	}
	if usa >= 48 {
		if stored := binary.LittleEndian.Uint32(record[44:]); stored != 0 && stored != uint32(number) {
			return bad("record ordinal")
		}
	}
	fileSequence := binary.LittleEndian.Uint16(record[16:])
	if fileSequence == 0 {
		return bad("live record sequence")
	}
	r := ntfsRawRecord{file: &ntfsLayoutFile{reference: number | uint64(fileSequence)<<48}, base: binary.LittleEndian.Uint64(record[32:])}
	if flags&2 != 0 {
		r.file.attributes = windows.FILE_ATTRIBUTE_DIRECTORY
	}
	var keyStorage [8]ntfsRawAttributeKey
	keys := keyStorage[:0]
	ended := false
	for offset := first; offset <= used-4; {
		kind := binary.LittleEndian.Uint32(record[offset:])
		if kind == math.MaxUint32 {
			ended = true
			break
		}
		if offset > used-16 {
			return bad("attribute header")
		}
		size := int(binary.LittleEndian.Uint32(record[offset+4:]))
		if size < 24 || size%8 != 0 || size > used-offset {
			return bad("attribute length")
		}
		attribute := record[offset : offset+size]
		nonresident := attribute[8]
		nameLength, nameOffset := int(attribute[9])*2, int(binary.LittleEndian.Uint16(attribute[10:]))
		if nonresident > 1 || (nonresident == 1 && size < 64) {
			return bad("attribute form")
		}
		header := 24
		if nonresident == 1 {
			header = 64
		}
		if nameLength > 0 && (nameOffset < header || nameOffset%2 != 0 || nameOffset > size-nameLength) {
			return bad("attribute name")
		}
		var value []byte
		var vcn int64
		if nonresident == 0 {
			valueLength, valueOffset := int(binary.LittleEndian.Uint32(attribute[16:])), int(binary.LittleEndian.Uint16(attribute[20:]))
			if valueOffset < header || valueOffset > size-valueLength || (nameLength > 0 && valueOffset < nameOffset+nameLength) {
				return bad("resident value bounds")
			}
			value = attribute[valueOffset : valueOffset+valueLength]
		} else {
			vcn = int64(binary.LittleEndian.Uint64(attribute[16:]))
			mapping := int(binary.LittleEndian.Uint16(attribute[32:]))
			if vcn < 0 || mapping < header || mapping >= size || (nameLength > 0 && mapping < nameOffset+nameLength) {
				return bad("attribute VCN")
			}
		}
		key := ntfsRawAttributeKey{kind: kind, instance: binary.LittleEndian.Uint16(attribute[14:]), vcn: vcn}
		if nameLength == 0 && (kind == 0x10 || kind == 0x30 || kind == 0x80 || kind == 0xc0) {
			keys = append(keys, key)
		}
		if nameLength == 0 {
			switch kind {
			case 0x10: // $STANDARD_INFORMATION
				if nonresident != 0 || len(value) < 36 || r.basic {
					return bad("standard information")
				}
				r.basic = true
				r.file.creation = binary.LittleEndian.Uint64(value)
				r.file.write = binary.LittleEndian.Uint64(value[8:])
				r.file.access = binary.LittleEndian.Uint64(value[24:])
				r.file.attributes = binary.LittleEndian.Uint32(value[32:]) | r.file.attributes
			case 0x20: // $ATTRIBUTE_LIST
				if r.hasList {
					return bad("multiple attribute lists")
				}
				r.hasList = true
				if nonresident == 0 {
					var err error
					r.list, err = decodeNTFSRawAttributeList(value)
					if err != nil {
						return ntfsRawRecord{}, err
					}
				} else {
					if vcn != 0 || attribute[12] != 0 || attribute[13] != 0 {
						return bad("attribute-list storage")
					}
					r.listSize = int64(binary.LittleEndian.Uint64(attribute[48:]))
					initialized := int64(binary.LittleEndian.Uint64(attribute[56:]))
					if r.listSize <= 0 || r.listSize > maxNTFSAttributeListBytes || initialized < r.listSize {
						return bad("attribute-list size")
					}
					var err error
					r.listRuns, err = decodeNTFSRawRuns(attribute, g)
					if err != nil {
						return ntfsRawRecord{}, err
					}
				}
			case 0x30: // $FILE_NAME (a second DOS-only name is not a link)
				if nonresident != 0 || len(value) < 66 || value[65] > 3 {
					return bad("file name")
				}
				length := int(value[64]) * 2
				name, ok := ntfsLayoutString(value, 66, length)
				if !ok || name == "" || name == ".." || strings.ContainsAny(name, `\/:`) {
					return bad("file name value")
				}
				if value[65] != 2 {
					r.file.names = append(r.file.names, ntfsLayoutName{binary.LittleEndian.Uint64(value), name})
				}
			case 0x80: // unnamed $DATA only; named data streams retain existing policy
				if nonresident == 0 {
					if r.data {
						return bad("duplicate default data")
					}
					r.data = true
					r.file.logical = int64(len(value))
					r.file.allocated = 0 // data resident in the MFT uses no separate clusters
				} else if vcn == 0 {
					if r.data {
						return bad("duplicate default data")
					}
					r.data = true
					r.file.logical = int64(binary.LittleEndian.Uint64(attribute[48:]))
					r.file.allocated = int64(binary.LittleEndian.Uint64(attribute[40:]))
					attributeFlags := binary.LittleEndian.Uint16(attribute[12:])
					if attributeFlags&0x80ff != 0 { // sparse/compressed physical allocation
						mapping := int(binary.LittleEndian.Uint16(attribute[32:]))
						if size < 72 || mapping < 72 {
							return bad("compressed/sparse physical allocation")
						}
						r.file.allocated = int64(binary.LittleEndian.Uint64(attribute[64:]))
					}
					if r.file.logical < 0 || r.file.allocated < 0 || r.file.allocated%g.cluster != 0 || r.file.allocated > g.clusters*g.cluster {
						return bad("default data size")
					}
				}
			case 0xc0: // $REPARSE_POINT
				if nonresident == 0 && len(value) >= 8 && int(binary.LittleEndian.Uint16(value[4:])) <= len(value)-8 {
					if r.file.hasReparseTag {
						return bad("duplicate reparse data")
					}
					r.file.reparseTag = binary.LittleEndian.Uint32(value)
					r.file.hasReparseTag = true
					// Deduplication allocation may be supplied by its filesystem
					// filter; retain the ordinary scanner's API accounting there.
					if r.file.reparseTag == windowsReparseTagDedup {
						r.file.native = true
					}
				} else {
					r.file.native = true
				}
			}
		}
		offset += size
	}
	if !ended {
		return bad("missing attribute terminator")
	}
	if r.base != 0 || r.hasList {
		r.keys = slices.Clone(keys)
	}
	return r, nil
}

func decodeNTFSRawAttributeList(data []byte) ([]ntfsRawAttributeRef, error) {
	if len(data) > maxNTFSAttributeListBytes {
		return nil, fmt.Errorf("NTFS attribute list exceeds the metadata budget")
	}
	var refs []ntfsRawAttributeRef
	for offset := 0; offset < len(data); {
		if len(data)-offset < 26 {
			return nil, fmt.Errorf("truncated NTFS attribute list")
		}
		entry := data[offset:]
		kind := binary.LittleEndian.Uint32(entry)
		size := int(binary.LittleEndian.Uint16(entry[4:]))
		nameLength, nameOffset := int(entry[6])*2, int(entry[7])
		if size < 26 || size > len(entry) || (nameLength > 0 && (nameOffset < 26 || nameOffset > size-nameLength)) {
			return nil, fmt.Errorf("invalid NTFS attribute-list entry")
		}
		vcn := int64(binary.LittleEndian.Uint64(entry[8:]))
		if vcn < 0 {
			return nil, fmt.Errorf("invalid attribute-list VCN")
		}
		if nameLength == 0 && (kind == 0x10 || kind == 0x30 || kind == 0x80 || kind == 0xc0) {
			refs = append(refs, ntfsRawAttributeRef{binary.LittleEndian.Uint64(entry[16:]), ntfsRawAttributeKey{kind, binary.LittleEndian.Uint16(entry[24:]), vcn}})
		}
		offset += size
	}
	return refs, nil
}

func decodeNTFSRawRuns(attribute []byte, g ntfsRawGeometry) ([]ntfsRawRun, error) {
	if len(attribute) < 64 {
		return nil, fmt.Errorf("truncated NTFS mapping header")
	}
	vcn, high := int64(binary.LittleEndian.Uint64(attribute[16:])), int64(binary.LittleEndian.Uint64(attribute[24:]))
	offset := int(binary.LittleEndian.Uint16(attribute[32:]))
	if vcn < 0 || high < vcn || high >= g.clusters || high == math.MaxInt64 || offset < 64 || offset >= len(attribute) {
		return nil, fmt.Errorf("invalid NTFS mapping bounds")
	}
	var runs []ntfsRawRun
	var lcn int64
	for offset < len(attribute) {
		code := attribute[offset]
		offset++
		if code == 0 {
			if vcn != high+1 {
				return nil, fmt.Errorf("incomplete NTFS mapping pairs")
			}
			return runs, nil
		}
		lengthBytes, deltaBytes := int(code&15), int(code>>4)
		if lengthBytes < 1 || lengthBytes > 8 || deltaBytes > 8 || lengthBytes+deltaBytes > len(attribute)-offset || len(runs) >= maxNTFSRawRuns {
			return nil, fmt.Errorf("invalid NTFS mapping pair")
		}
		var count uint64
		for i := 0; i < lengthBytes; i++ {
			count |= uint64(attribute[offset+i]) << uint(i*8)
		}
		offset += lengthBytes
		if count == 0 || count > uint64(high+1-vcn) {
			return nil, fmt.Errorf("invalid NTFS mapping run length")
		}
		physical := int64(-1)
		if deltaBytes > 0 {
			var encoded uint64
			for i := 0; i < deltaBytes; i++ {
				encoded |= uint64(attribute[offset+i]) << uint(i*8)
			}
			shift := uint(64 - deltaBytes*8)
			delta := int64(encoded<<shift) >> shift
			if (delta > 0 && lcn > math.MaxInt64-delta) || (delta < 0 && lcn < math.MinInt64-delta) {
				return nil, fmt.Errorf("NTFS mapping delta overflow")
			}
			lcn += delta
			if lcn < 0 || lcn >= g.clusters || count > uint64(g.clusters-lcn) {
				return nil, fmt.Errorf("NTFS mapping outside the volume")
			}
			physical = lcn
		}
		offset += deltaBytes
		runs = append(runs, ntfsRawRun{vcn, physical, int64(count)})
		vcn += int64(count)
	}
	return nil, fmt.Errorf("unterminated NTFS mapping pairs")
}

func mergeNTFSRawExtensions(bases, extensions map[uint64]*ntfsRawRecord, g ntfsRawGeometry, read func([]byte, int64) error, check func() error) error {
	for _, extension := range extensions {
		if bases[extension.base] == nil {
			return fmt.Errorf("MFT extension %#x has an absent or stale base %#x", extension.file.reference, extension.base)
		}
	}
	// Attribute lists are separate metadata streams. Visit their first extents
	// in physical order rather than map order to reduce random seek traffic.
	ordered := make([]*ntfsRawRecord, 0, len(bases))
	for _, base := range bases {
		ordered = append(ordered, base)
	}
	slices.SortFunc(ordered, func(a, b *ntfsRawRecord) int {
		var x, y int64 = -1, -1
		if len(a.listRuns) > 0 {
			x = a.listRuns[0].lcn
		}
		if len(b.listRuns) > 0 {
			y = b.listRuns[0].lcn
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		return 0
	})
	for _, base := range ordered {
		ref := base.file.reference
		if err := check(); err != nil {
			return err
		}
		if base.listRuns != nil {
			size := (base.listSize + g.sector - 1) / g.sector * g.sector
			buffer := alignedNTFSRawBuffer(int(size), g.sector)
			if err := readNTFSRawStream(buffer, 0, base.listRuns, g, read); err != nil {
				return fmt.Errorf("read MFT attribute list for %#x: %w", ref, err)
			}
			var err error
			base.list, err = decodeNTFSRawAttributeList(buffer[:int(base.listSize)])
			if err != nil {
				return err
			}
		}
		members := map[uint64]*ntfsRawRecord{ref: base}
		listed := make(map[ntfsRawAttributeRef]bool, len(base.list))
		for _, entry := range base.list {
			if listed[entry] {
				return fmt.Errorf("duplicate NTFS attribute-list reference")
			}
			listed[entry] = true
			part := base
			if entry.reference != ref {
				part = extensions[entry.reference]
				if part == nil || part.base != ref || part.hasList {
					return fmt.Errorf("missing or stale NTFS extension reference %#x", entry.reference)
				}
			}
			members[entry.reference] = part
		}
		// Match both directions, in linear time even for files with many links:
		// every listed attribute exists, and no unlisted extension is attached.
		for memberRef, part := range members {
			for _, key := range part.keys {
				entry := ntfsRawAttributeRef{memberRef, key}
				if !listed[entry] {
					return fmt.Errorf("unlisted NTFS extension attribute")
				}
				delete(listed, entry)
			}
		}
		if len(listed) != 0 {
			return fmt.Errorf("NTFS attribute-list member is missing or changed")
		}
		for memberRef, part := range members {
			if memberRef == ref {
				continue
			}
			if base.basic && part.basic || base.data && part.data || base.file.hasReparseTag && part.file.hasReparseTag {
				return fmt.Errorf("conflicting NTFS extension metadata")
			}
			if part.basic {
				base.basic = true
				base.file.creation, base.file.access, base.file.write = part.file.creation, part.file.access, part.file.write
				base.file.attributes = part.file.attributes | base.file.attributes
			}
			if part.data {
				base.data = true
				base.file.logical, base.file.allocated = part.file.logical, part.file.allocated
			}
			if part.file.hasReparseTag {
				base.file.reparseTag, base.file.hasReparseTag = part.file.reparseTag, true
			}
			base.file.native = base.file.native || part.file.native
			base.file.names = append(base.file.names, part.file.names...)
			delete(extensions, memberRef)
		}
		base.finalize()
	}
	if len(extensions) > 0 {
		return fmt.Errorf("MFT has unreferenced live extension records")
	}
	return nil
}
