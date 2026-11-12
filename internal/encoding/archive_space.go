package encoding

import (
	"encoding/binary"
	"io"
	"sort"

	"github.com/frankbardon/pulse/errors"
)

// archive_space.go measures the DEAD bytes an in-place `shard add`
// leaves behind (U73 E2-S3): every append drops the superseded
// `_schema.pulse` record from the new central directory, so the old
// `_schema.pulse` entry, the old directory, its zip64 records and its
// EOCD stay in the file with nothing pointing at them. No record names
// them, so they are measured as GAPS: the archive's length minus the
// spans its LIVE directory covers — each live entry from its local
// header through its data descriptor, plus the live directory tail
// (directory, zip64 end record + locator, EOCD).
//
// The superseded copies are counted structurally, not by scanning: an
// append writes [new shard][new _schema.pulse][directory][EOCD], the new
// shard stays live, so every dead `_schema.pulse` OPENS a gap (its dead
// directory and EOCD follow it inside the same gap; a create's leading
// `_schema.pulse` opens the gap at offset 0). A gap whose first bytes are
// a local file header named `_schema.pulse` is one dead copy — one small
// read per gap, never a read of the dead bytes themselves.

// ArchiveSpace is how an archive's bytes divide between what its live
// central directory references and what it does not.
type ArchiveSpace struct {
	// Size is the archive's byte length.
	Size int64
	// ShardBytes is the bytes the live shard entries span (local header,
	// name, extra, payload, data descriptor).
	ShardBytes int64
	// SchemaBytes is the span of the live `_schema.pulse` entry.
	SchemaBytes int64
	// TailBytes is the live directory tail: the central directory, any
	// zip64 end record + locator, and the EOCD.
	TailBytes int64
	// DeadBytes is every byte no live record covers.
	DeadBytes int64
	// DeadSchemaCopies counts the superseded `_schema.pulse` entries among
	// the dead bytes.
	DeadSchemaCopies int
}

// DeadAfterAppend is the dead bytes an in-place append would leave: the
// dead bytes now, plus the live `_schema.pulse` and directory tail the
// append supersedes.
func (s *ArchiveSpace) DeadAfterAppend() int64 {
	return s.DeadBytes + s.SchemaBytes + s.TailBytes
}

// MeasureArchiveSpace measures the archive dir describes (dir from
// ReadArchiveDirectory over the same r). It reads each live entry's
// local header and data-descriptor signature, and the first bytes of
// each gap — never an entry's payload. An entry whose span leaves the
// region before the directory is PULSE_ARCHIVE_CORRUPT.
func MeasureArchiveSpace(r io.ReaderAt, dir *ArchiveDirectory) (*ArchiveSpace, error) {
	sp := &ArchiveSpace{Size: dir.Size, TailBytes: dir.Size - dir.DirOffset}
	type span struct{ start, end int64 }
	spans := make([]span, 0, len(dir.Records))
	for _, rec := range dir.Records {
		start, end, err := entrySpan(r, rec, dir.DirOffset)
		if err != nil {
			return nil, err
		}
		if rec.Name == ReservedSchemaName {
			sp.SchemaBytes += end - start
		} else {
			sp.ShardBytes += end - start
		}
		spans = append(spans, span{start, end})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	var covered int64
	cursor := int64(0)
	gap := func(from, to int64) error {
		if to <= from {
			return nil
		}
		dead, err := opensWithSchemaHeader(r, from, to)
		if err != nil {
			return err
		}
		if dead {
			sp.DeadSchemaCopies++
		}
		return nil
	}
	for _, s := range spans {
		if err := gap(cursor, s.start); err != nil {
			return nil, err
		}
		if s.end > cursor {
			covered += s.end - max(cursor, s.start)
			cursor = s.end
		}
	}
	if err := gap(cursor, dir.DirOffset); err != nil {
		return nil, err
	}
	sp.DeadBytes = dir.DirOffset - covered
	return sp, nil
}

// opensWithSchemaHeader reports whether the gap [from, to) begins with a
// local file header named `_schema.pulse`.
func opensWithSchemaHeader(r io.ReaderAt, from, to int64) (bool, error) {
	n := int64(zipLocalHeaderLen + len(ReservedSchemaName))
	if to-from < n {
		return false, nil
	}
	b := make([]byte, n)
	if _, err := r.ReadAt(b, from); err != nil && err != io.EOF {
		return false, errors.WrapCodedError(err, errors.ENCODING_IO, "reading an unreferenced archive region")
	}
	return binary.LittleEndian.Uint32(b) == zipLocalHeaderSig &&
		int(binary.LittleEndian.Uint16(b[26:])) == len(ReservedSchemaName) &&
		string(b[zipLocalHeaderLen:]) == ReservedSchemaName, nil
}

// entrySpan is the byte range a directory record's entry occupies: its
// local header through its data descriptor (when flag 0x8 says it has
// one; with or without the optional signature; 64-bit sizes for an
// entry of 4 GiB or more, as zip.Writer writes them).
func entrySpan(r io.ReaderAt, rec DirectoryRecord, limit int64) (int64, int64, error) {
	offset, compSize, flags, err := recordLocation(rec)
	if err != nil {
		return 0, 0, err
	}
	var lh [zipLocalHeaderLen]byte
	if offset+zipLocalHeaderLen > uint64(limit) {
		return 0, 0, archiveCorrupt("archive entry lies outside the archive", map[string]any{"entry": rec.Name})
	}
	if _, err := r.ReadAt(lh[:], int64(offset)); err != nil && err != io.EOF {
		return 0, 0, errors.WrapCodedError(err, errors.ENCODING_IO, "reading an archive local header")
	}
	if binary.LittleEndian.Uint32(lh[:]) != zipLocalHeaderSig {
		return 0, 0, archiveCorrupt("archive entry has no local header", map[string]any{"entry": rec.Name})
	}
	end := offset + zipLocalHeaderLen +
		uint64(binary.LittleEndian.Uint16(lh[26:])) + uint64(binary.LittleEndian.Uint16(lh[28:])) + compSize
	if flags&zipFlagDataDescriptor != 0 {
		sizes := uint64(8)
		if compSize >= zipUint32Max {
			sizes = 16
		}
		desc := 4 + sizes // CRC-32 + both sizes
		var sig [4]byte
		if end+4 <= uint64(limit) {
			if _, err := r.ReadAt(sig[:], int64(end)); err != nil && err != io.EOF {
				return 0, 0, errors.WrapCodedError(err, errors.ENCODING_IO, "reading an archive data descriptor")
			}
			if binary.LittleEndian.Uint32(sig[:]) == zipDataDescriptorSig {
				desc += 4
			}
		}
		end += desc
	}
	if end > uint64(limit) {
		return 0, 0, archiveCorrupt("archive entry overruns the central directory", map[string]any{"entry": rec.Name})
	}
	return int64(offset), int64(end), nil
}

// recordLocation reads a central-directory record's local-header
// offset, compressed size and flags, resolving zip64 sentinels from its
// extended-information extra field (present values in the spec's order:
// uncompressed size, compressed size, offset — each only when its
// 32-bit slot is the sentinel).
func recordLocation(rec DirectoryRecord) (offset, compSize uint64, flags uint16, err error) {
	b := rec.raw
	flags = binary.LittleEndian.Uint16(b[8:])
	compSize = uint64(binary.LittleEndian.Uint32(b[20:]))
	uncomp := uint64(binary.LittleEndian.Uint32(b[24:]))
	offset = uint64(binary.LittleEndian.Uint32(b[42:]))
	if compSize != zipUint32Max && uncomp != zipUint32Max && offset != zipUint32Max {
		return offset, compSize, flags, nil
	}
	nameLen := int(binary.LittleEndian.Uint16(b[28:]))
	extraLen := int(binary.LittleEndian.Uint16(b[30:]))
	extra := b[zipCentralHeaderLen+nameLen : zipCentralHeaderLen+nameLen+extraLen]
	for len(extra) >= 4 {
		id := binary.LittleEndian.Uint16(extra)
		n := int(binary.LittleEndian.Uint16(extra[2:]))
		if 4+n > len(extra) {
			break
		}
		field := extra[4 : 4+n]
		extra = extra[4+n:]
		if id != zip64ExtraID {
			continue
		}
		next := func(v *uint64) bool {
			if len(field) < 8 {
				return false
			}
			*v = binary.LittleEndian.Uint64(field)
			field = field[8:]
			return true
		}
		ok := true
		if uncomp == zipUint32Max {
			ok = next(&uncomp)
		}
		if ok && compSize == zipUint32Max {
			ok = next(&compSize)
		}
		if ok && offset == zipUint32Max {
			ok = next(&offset)
		}
		if !ok {
			break
		}
		return offset, compSize, flags, nil
	}
	return 0, 0, 0, archiveCorrupt("archive record's zip64 extra field is missing or short", map[string]any{"entry": rec.Name})
}

// AppendLength is the exact byte count an append writes at dir.Size:
// one store-only entry per name (with the matching payload size), in
// order, then Finish over dir's records minus every one named drop plus
// one record per entry — the zip64 end record + locator when the
// directory needs them, and the EOCD. It mirrors WriteEntry + Finish
// (TestAppendLength_MatchesAppender).
func AppendLength(dir *ArchiveDirectory, drop string, names []string, sizes []uint64) int64 {
	off := dir.Size
	var dirLen, count uint64
	for i, name := range names {
		size := sizes[i]
		rec := CentralRecord(name, 0, 0, size, uint64(off))
		desc := int64(len(dataDescriptor(0, size)))
		off += zipLocalHeaderLen + int64(len(name)) + int64(size) + desc
		dirLen += uint64(rec.Len())
		count++
	}
	for _, r := range dir.Records {
		if r.Name != drop {
			dirLen += uint64(r.Len())
			count++
		}
	}
	end := uint64(off) + dirLen + zipEOCDLen
	if count >= zipUint16Max || dirLen >= zipUint32Max || uint64(off) >= zipUint32Max {
		end += zip64EOCDLen + zip64LocatorLen
	}
	return int64(end) - dir.Size
}
