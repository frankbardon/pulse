package encoding

import (
	"encoding/binary"
	stderrors "errors"
	"hash/crc32"
	"io"
	"unicode/utf8"

	"github.com/frankbardon/pulse/errors"
)

// archive_append.go is the hand-written zip tail writer behind an
// in-place `shard add` (U73 #492). Go's archive/zip cannot extend an
// existing central directory — zip.Writer builds its directory only from
// the entries it wrote itself — so an append that keeps every stored
// shard where it is must write the zip records by hand:
//
//	[old entries][old directory + EOCD]          <- untouched
//	[new shard local entry][new _schema.pulse]   <- ArchiveAppender.WriteEntry
//	[central directory][zip64 EOCD + locator][EOCD]  <- ArchiveAppender.Finish
//
// ReadArchiveDirectory parses the old tail and returns every central
// directory record VERBATIM (its local-header offset, CRC32, sizes and
// any zip64 extra stay valid: the bytes it points at never move); the
// new directory is those records — minus the superseded `_schema.pulse`
// — plus one record per entry written here. The old directory, the old
// EOCD and the superseded `_schema.pulse` stay in the file as dead bytes
// no directory references (CompactShardArchive reclaims them).
//
// Every byte is the one zip.Writer writes for the same entries
// (TestArchiveAppender_MatchesZipWriterBytes), Zip64 included: a record
// whose size or offset does not fit 32 bits carries the zip64
// extended-information extra field (uncompressed size, compressed size,
// local-header offset) with 0xFFFFFFFF in its 32-bit slots; a directory
// of 65535 records or more, or one larger or later than 4 GiB, adds the
// zip64 end-of-central-directory record and its locator, the plain EOCD
// then carrying the 0xFFFF / 0xFFFFFFFF sentinels.

const (
	zipLocalHeaderSig   = 0x04034b50
	zipCentralHeaderSig = 0x02014b50
	zipEOCDSig          = 0x06054b50
	zip64EOCDSig        = 0x06064b50
	zip64LocatorSig     = 0x07064b50

	zipLocalHeaderLen   = 30
	zipCentralHeaderLen = 46
	zipEOCDLen          = 22
	zip64EOCDLen        = 56
	zip64LocatorLen     = 20

	zipVersion20 = 20
	zipVersion45 = 45

	zipUint16Max = 1<<16 - 1
	zipUint32Max = 1<<32 - 1

	// zipFlagUTF8 marks a name that is UTF-8 and needs to say so.
	zipFlagUTF8 = 0x800
	// zipFlagDataDescriptor marks an entry whose CRC-32 and sizes follow
	// its payload in a data descriptor.
	zipFlagDataDescriptor = 0x8

	zipDataDescriptorSig   = 0x08074b50
	zipDataDescriptor64Len = 24

	// zipEOCDSearch is how far from the end a reader looks for the EOCD:
	// the record plus the longest comment it can carry.
	zipEOCDSearch = zipEOCDLen + zipUint16Max
)

// ArchiveDirectory is a zip archive's parsed tail: the central
// directory's records, verbatim, and where the directory sits.
type ArchiveDirectory struct {
	// Size is the archive's byte length — where an append begins.
	Size int64
	// DirOffset and DirSize locate the central directory.
	DirOffset, DirSize int64
	// Records are the central-directory records in directory order.
	Records []DirectoryRecord
}

// DirectoryRecord is one central-directory record. raw is the whole
// record (fixed header, name, extra, comment) exactly as it is written.
type DirectoryRecord struct {
	Name string
	raw  []byte
}

// Len is the record's byte length in a central directory.
func (r DirectoryRecord) Len() int { return len(r.raw) }

// NotAppendableError reports a VALID archive whose tail the appender
// does not extend in place — prepended data (a non-zero base offset), a
// multi-disk archive, bytes after the EOCD. The caller takes the
// whole-archive rewrite instead; nothing about the archive is wrong.
type NotAppendableError struct {
	Reason string
}

func (e *NotAppendableError) Error() string {
	return "archive tail cannot be extended in place: " + e.Reason
}

// IsNotAppendable reports whether err is a NotAppendableError.
func IsNotAppendable(err error) bool {
	var na *NotAppendableError
	return stderrors.As(err, &na)
}

func archiveCorrupt(what string, details map[string]any) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_ARCHIVE_CORRUPT, what, details)
}

// ReadArchiveDirectory parses the end-of-central-directory record (and
// its zip64 twin when the EOCD carries a sentinel) at the tail of r and
// reads every central-directory record verbatim. It reads the last
// 64 KiB and the directory, never an entry. A malformed tail is
// PULSE_ARCHIVE_CORRUPT; a valid layout the appender does not extend is
// a *NotAppendableError.
func ReadArchiveDirectory(r io.ReaderAt, size int64) (*ArchiveDirectory, error) {
	n := min(size, int64(zipEOCDSearch))
	if n < zipEOCDLen {
		return nil, archiveCorrupt("archive is shorter than an end-of-central-directory record", map[string]any{"size": size})
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, size-n); err != nil && err != io.EOF {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "reading the archive tail")
	}
	p := findEOCD(buf)
	if p < 0 {
		return nil, archiveCorrupt("no end-of-central-directory record in the archive tail", nil)
	}
	eocdOff := size - n + int64(p)
	e := buf[p:]
	commentLen := int(binary.LittleEndian.Uint16(e[20:]))
	if int64(p)+zipEOCDLen+int64(commentLen) != n {
		return nil, &NotAppendableError{Reason: "bytes_after_eocd"}
	}
	if binary.LittleEndian.Uint16(e[4:]) != 0 || binary.LittleEndian.Uint16(e[6:]) != 0 {
		return nil, &NotAppendableError{Reason: "multi_disk"}
	}
	records := uint64(binary.LittleEndian.Uint16(e[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(e[12:]))
	dirOffset := uint64(binary.LittleEndian.Uint32(e[16:]))
	dirEnd := eocdOff

	if records == zipUint16Max || dirSize == zipUint32Max || dirOffset == zipUint32Max {
		z64, err := readZip64End(r, eocdOff)
		if err != nil {
			return nil, err
		}
		if z64 != nil {
			records, dirSize, dirOffset, dirEnd = z64.records, z64.dirSize, z64.dirOffset, z64.offset
		}
	}
	if dirSize > uint64(dirEnd) || dirOffset != uint64(dirEnd)-dirSize {
		// The directory does not end where its end record begins: data
		// prepended to the archive (a non-zero base offset) or a gap.
		return nil, &NotAppendableError{Reason: "directory_not_adjacent"}
	}
	dir := make([]byte, dirSize)
	if _, err := r.ReadAt(dir, int64(dirOffset)); err != nil && err != io.EOF {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "reading the archive central directory")
	}
	recs, err := splitDirectory(dir)
	if err != nil {
		return nil, err
	}
	if uint64(len(recs)) != records {
		return nil, archiveCorrupt("central directory record count disagrees with its end record",
			map[string]any{"records": len(recs), "declared": records})
	}
	return &ArchiveDirectory{Size: size, DirOffset: int64(dirOffset), DirSize: int64(dirSize), Records: recs}, nil
}

// findEOCD returns the offset in b of the LAST end-of-central-directory
// signature whose comment fits b — the record a zip reader resolves.
func findEOCD(b []byte) int {
	for i := len(b) - zipEOCDLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(b[i:]) == zipEOCDSig {
			if i+zipEOCDLen+int(binary.LittleEndian.Uint16(b[i+20:])) <= len(b) {
				return i
			}
		}
	}
	return -1
}

type zip64End struct {
	offset                      int64
	records, dirSize, dirOffset uint64
}

// readZip64End follows the zip64 locator just before the EOCD at
// eocdOff. No locator is (nil, nil): the sentinels were real values.
func readZip64End(r io.ReaderAt, eocdOff int64) (*zip64End, error) {
	locOff := eocdOff - zip64LocatorLen
	if locOff < 0 {
		return nil, nil
	}
	var loc [zip64LocatorLen]byte
	if _, err := r.ReadAt(loc[:], locOff); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "reading the zip64 locator")
	}
	if binary.LittleEndian.Uint32(loc[0:]) != zip64LocatorSig {
		return nil, nil
	}
	if binary.LittleEndian.Uint32(loc[4:]) != 0 || binary.LittleEndian.Uint32(loc[16:]) != 1 {
		return nil, &NotAppendableError{Reason: "multi_disk"}
	}
	off := binary.LittleEndian.Uint64(loc[8:])
	if off > uint64(locOff) || uint64(locOff)-off < zip64EOCDLen {
		return nil, archiveCorrupt("zip64 locator points outside the archive", map[string]any{"offset": off})
	}
	var rec [zip64EOCDLen]byte
	if _, err := r.ReadAt(rec[:], int64(off)); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "reading the zip64 end-of-central-directory record")
	}
	if binary.LittleEndian.Uint32(rec[0:]) != zip64EOCDSig {
		return nil, archiveCorrupt("zip64 locator does not point at a zip64 end record", map[string]any{"offset": off})
	}
	if binary.LittleEndian.Uint32(rec[16:]) != 0 || binary.LittleEndian.Uint32(rec[20:]) != 0 {
		return nil, &NotAppendableError{Reason: "multi_disk"}
	}
	if int64(off)+zip64EOCDLen != locOff {
		// A zip64 extensible data sector sits between the record and the
		// locator: valid, but not a shape the appender re-emits.
		return nil, &NotAppendableError{Reason: "zip64_extensible_data"}
	}
	return &zip64End{
		offset:    int64(off),
		records:   binary.LittleEndian.Uint64(rec[32:]),
		dirSize:   binary.LittleEndian.Uint64(rec[40:]),
		dirOffset: binary.LittleEndian.Uint64(rec[48:]),
	}, nil
}

// splitDirectory cuts a central directory into its records.
func splitDirectory(dir []byte) ([]DirectoryRecord, error) {
	var out []DirectoryRecord
	for off := 0; off < len(dir); {
		rest := dir[off:]
		if len(rest) < zipCentralHeaderLen || binary.LittleEndian.Uint32(rest) != zipCentralHeaderSig {
			return nil, archiveCorrupt("malformed central directory record", map[string]any{"offset": off})
		}
		nameLen := int(binary.LittleEndian.Uint16(rest[28:]))
		extraLen := int(binary.LittleEndian.Uint16(rest[30:]))
		commentLen := int(binary.LittleEndian.Uint16(rest[32:]))
		l := zipCentralHeaderLen + nameLen + extraLen + commentLen
		if l > len(rest) {
			return nil, archiveCorrupt("central directory record overruns the directory", map[string]any{"offset": off})
		}
		out = append(out, DirectoryRecord{
			Name: string(rest[zipCentralHeaderLen : zipCentralHeaderLen+nameLen]),
			raw:  rest[:l:l],
		})
		off += l
	}
	return out, nil
}

// ArchiveAppender writes store-only zip entries and then a central
// directory at an absolute archive offset: the tail of an append, or a
// whole archive when start is 0. Every byte goes to w in order; the
// caller positions w at start.
type ArchiveAppender struct {
	w     io.Writer
	start int64
	off   int64
}

// NewArchiveAppender writes to w as if w's first byte sat at archive
// offset start.
func NewArchiveAppender(w io.Writer, start int64) *ArchiveAppender {
	return &ArchiveAppender{w: w, start: start, off: start}
}

// Written is the number of bytes written so far.
func (a *ArchiveAppender) Written() int64 { return a.off - a.start }

func (a *ArchiveAppender) write(p []byte) error {
	n, err := a.w.Write(p)
	a.off += int64(n)
	if err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing the archive tail")
	}
	return nil
}

// zipNameFlags is the general-purpose flag word a name needs:
// zip.Writer's rule (detectUTF8) — the UTF-8 bit when the name is valid
// UTF-8 and holds a character outside the CP-437-safe ASCII subset.
func zipNameFlags(name string) uint16 {
	require := false
	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		i += size
		if r < 0x20 || r > 0x7d || r == 0x5c {
			if !utf8.ValidRune(r) || (r == utf8.RuneError && size == 1) {
				return 0
			}
			require = true
		}
	}
	if require {
		return zipFlagUTF8
	}
	return 0
}

// WriteEntry writes one store-only entry and returns its
// central-directory record. The bytes are exactly those
// zip.Writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
// writes for the payload — local header with zeroed CRC-32 and sizes,
// payload, data descriptor (flag 0x8) — so an appended entry is
// indistinguishable from a rewritten one, and a later raw copy
// (CopyStoredEntry) re-emits it byte-identically.
func (a *ArchiveAppender) WriteEntry(name string, payload []byte) (DirectoryRecord, error) {
	if len(name) == 0 || len(name) > zipUint16Max {
		return DirectoryRecord{}, errors.NewCodedErrorWithDetails(errors.ENCODING_IO,
			"archive entry name length out of range", map[string]any{"entry": name})
	}
	crc := crc32.ChecksumIEEE(payload)
	size := uint64(len(payload))
	offset := uint64(a.off)
	flags := zipNameFlags(name) | zipFlagDataDescriptor

	if err := a.write(localFileHeader(name, flags)); err != nil {
		return DirectoryRecord{}, err
	}
	if err := a.write(payload); err != nil {
		return DirectoryRecord{}, err
	}
	if err := a.write(dataDescriptor(crc, size)); err != nil {
		return DirectoryRecord{}, err
	}
	return CentralRecord(name, flags, crc, size, offset), nil
}

// localFileHeader is a store-only entry's local header in zip.Writer's
// streaming form: CRC-32 and sizes zero (they follow in the data
// descriptor), version needed 2.0, no extra field.
func localFileHeader(name string, flags uint16) []byte {
	b := make([]byte, 0, zipLocalHeaderLen+len(name))
	b = binary.LittleEndian.AppendUint32(b, zipLocalHeaderSig)
	b = binary.LittleEndian.AppendUint16(b, zipVersion20)
	b = binary.LittleEndian.AppendUint16(b, flags)
	b = binary.LittleEndian.AppendUint16(b, 0) // method: store
	b = binary.LittleEndian.AppendUint16(b, 0) // mod time
	b = binary.LittleEndian.AppendUint16(b, 0) // mod date
	b = binary.LittleEndian.AppendUint32(b, 0) // crc32: in the descriptor
	b = binary.LittleEndian.AppendUint32(b, 0) // compressed size
	b = binary.LittleEndian.AppendUint32(b, 0) // uncompressed size
	b = binary.LittleEndian.AppendUint16(b, uint16(len(name)))
	b = binary.LittleEndian.AppendUint16(b, 0) // extra length
	return append(b, name...)
}

// dataDescriptor follows an entry's payload: signature, CRC-32, then
// both sizes — 64-bit for an entry of 4 GiB or more, as zip.Writer
// writes it.
func dataDescriptor(crc uint32, size uint64) []byte {
	b := make([]byte, 0, zipDataDescriptor64Len)
	b = binary.LittleEndian.AppendUint32(b, zipDataDescriptorSig)
	b = binary.LittleEndian.AppendUint32(b, crc)
	if size >= zipUint32Max {
		b = binary.LittleEndian.AppendUint64(b, size) // compressed
		return binary.LittleEndian.AppendUint64(b, size)
	}
	b = binary.LittleEndian.AppendUint32(b, uint32(size))
	return binary.LittleEndian.AppendUint32(b, uint32(size))
}

// CentralRecord is the central-directory record of a store-only entry
// whose local header sits at offset, in zip.Writer.Close's form: when
// the size reaches 4 GiB or the offset does, both 32-bit size slots
// hold 0xFFFFFFFF and a zip64 extra field carries the uncompressed
// size, the compressed size and the offset (a reader consults only the
// values whose slot is the sentinel); the offset slot holds 0xFFFFFFFF
// when the offset does not fit. An entry of 4 GiB or more needs version
// 4.5.
func CentralRecord(name string, flags uint16, crc uint32, size, offset uint64) DirectoryRecord {
	var extra []byte
	size32 := uint32(size)
	version := uint16(zipVersion20)
	if size >= zipUint32Max {
		version = zipVersion45
	}
	if size >= zipUint32Max || offset >= zipUint32Max {
		size32 = zipUint32Max
		extra = binary.LittleEndian.AppendUint16(extra, zip64ExtraID)
		extra = binary.LittleEndian.AppendUint16(extra, 24)
		extra = binary.LittleEndian.AppendUint64(extra, size) // uncompressed
		extra = binary.LittleEndian.AppendUint64(extra, size) // compressed
		extra = binary.LittleEndian.AppendUint64(extra, offset)
	}
	off32 := uint32(offset)
	if offset > zipUint32Max {
		off32 = zipUint32Max
	}
	b := make([]byte, 0, zipCentralHeaderLen+len(name)+len(extra))
	b = binary.LittleEndian.AppendUint32(b, zipCentralHeaderSig)
	b = binary.LittleEndian.AppendUint16(b, zipVersion20) // version made by
	b = binary.LittleEndian.AppendUint16(b, version)      // version needed
	b = binary.LittleEndian.AppendUint16(b, flags)
	b = binary.LittleEndian.AppendUint16(b, 0) // method: store
	b = binary.LittleEndian.AppendUint16(b, 0) // mod time
	b = binary.LittleEndian.AppendUint16(b, 0) // mod date
	b = binary.LittleEndian.AppendUint32(b, crc)
	b = binary.LittleEndian.AppendUint32(b, size32) // compressed
	b = binary.LittleEndian.AppendUint32(b, size32) // uncompressed
	b = binary.LittleEndian.AppendUint16(b, uint16(len(name)))
	b = binary.LittleEndian.AppendUint16(b, uint16(len(extra)))
	b = binary.LittleEndian.AppendUint16(b, 0) // comment length
	b = binary.LittleEndian.AppendUint16(b, 0) // disk number start
	b = binary.LittleEndian.AppendUint16(b, 0) // internal attributes
	b = binary.LittleEndian.AppendUint32(b, 0) // external attributes
	b = binary.LittleEndian.AppendUint32(b, off32)
	b = append(b, name...)
	b = append(b, extra...)
	return DirectoryRecord{Name: name, raw: b}
}

// Finish writes the central directory — records, in order — then the
// zip64 end record and locator when the directory needs them, then the
// end-of-central-directory record. It returns the directory's offset.
func (a *ArchiveAppender) Finish(records []DirectoryRecord) (int64, error) {
	start := a.off
	for _, r := range records {
		if err := a.write(r.raw); err != nil {
			return 0, err
		}
	}
	end := a.off
	count := uint64(len(records))
	size := uint64(end - start)
	offset := uint64(start)
	if count >= zipUint16Max || size >= zipUint32Max || offset >= zipUint32Max {
		b := make([]byte, 0, zip64EOCDLen+zip64LocatorLen)
		b = binary.LittleEndian.AppendUint32(b, zip64EOCDSig)
		b = binary.LittleEndian.AppendUint64(b, zip64EOCDLen-12) // record size past sig + this field
		b = binary.LittleEndian.AppendUint16(b, zipVersion45)    // version made by
		b = binary.LittleEndian.AppendUint16(b, zipVersion45)    // version needed
		b = binary.LittleEndian.AppendUint32(b, 0)               // this disk
		b = binary.LittleEndian.AppendUint32(b, 0)               // directory's disk
		b = binary.LittleEndian.AppendUint64(b, count)           // records on this disk
		b = binary.LittleEndian.AppendUint64(b, count)           // records total
		b = binary.LittleEndian.AppendUint64(b, size)
		b = binary.LittleEndian.AppendUint64(b, offset)
		b = binary.LittleEndian.AppendUint32(b, zip64LocatorSig)
		b = binary.LittleEndian.AppendUint32(b, 0)           // disk of the zip64 end record
		b = binary.LittleEndian.AppendUint64(b, uint64(end)) // zip64 end record offset
		b = binary.LittleEndian.AppendUint32(b, 1)           // total disks
		if err := a.write(b); err != nil {
			return 0, err
		}
		count, size, offset = zipUint16Max, zipUint32Max, zipUint32Max
	}
	b := make([]byte, 0, zipEOCDLen)
	b = binary.LittleEndian.AppendUint32(b, zipEOCDSig)
	b = binary.LittleEndian.AppendUint16(b, 0) // this disk
	b = binary.LittleEndian.AppendUint16(b, 0) // directory's disk
	b = binary.LittleEndian.AppendUint16(b, uint16(count))
	b = binary.LittleEndian.AppendUint16(b, uint16(count))
	b = binary.LittleEndian.AppendUint32(b, uint32(size))
	b = binary.LittleEndian.AppendUint32(b, uint32(offset))
	b = binary.LittleEndian.AppendUint16(b, 0) // comment length
	if err := a.write(b); err != nil {
		return 0, err
	}
	return start, nil
}
