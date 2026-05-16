package encoding

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// ZipMagic is the standard PKZip local-file-header magic. Pulse shard
// archives begin with these four bytes when written as zip containers;
// single-file .pulse cohorts begin with MagicBytes ("PULSE\x00\x00\x00").
// Pulse.Open dispatches on the first four bytes to pick the read path.
var ZipMagic = [4]byte{'P', 'K', 0x03, 0x04}

// ReservedSchemaName is the canonical entry name inside a Pulse shard
// archive that carries the cohort's canonical schema, dictionaries,
// and sharding metadata (aggregate record count, shard count). The
// name is reserved — inserting a shard with this basename raises
// PULSE_SHARD_RESERVED_NAME at write time. Read-side enumeration of
// shards EXCLUDES this entry.
const ReservedSchemaName = "_schema.pulse"

// IsArchive reports whether the first four bytes of r identify a zip
// container. It does no central-directory parsing — magic-byte check
// only — so it is cheap enough to call on every Open. Errors are
// returned for I/O failures; a short file returns (false, nil).
func IsArchive(r io.ReaderAt, size int64) (bool, error) {
	if size < int64(len(ZipMagic)) {
		return false, nil
	}
	var buf [4]byte
	n, err := r.ReadAt(buf[:], 0)
	if err != nil && err != io.EOF {
		return false, errors.WrapCodedError(err, errors.ENCODING_IO,
			"reading archive magic bytes")
	}
	if n < len(ZipMagic) {
		return false, nil
	}
	return buf == ZipMagic, nil
}

// ArchiveEntry is a single payload inside a Pulse shard archive. Offset
// is the byte offset of the entry's compressed/stored payload within
// the archive (the equivalent of zip.File.DataOffset), NOT the offset
// of the local file header. Pulse stores entries uncompressed (Method
// 0), so Offset is a direct pointer into the shard's bytes.
type ArchiveEntry struct {
	// Name is the zip entry name (basename inside the archive). For
	// flat archives this is the shard filename, e.g. "20190101.pulse".
	Name string

	// Size is the uncompressed payload size in bytes.
	Size int64

	// Offset is the byte offset of the payload within the archive.
	Offset int64
}

// Archive is a parsed Pulse shard archive: zip central-directory cache
// plus the underlying ReaderAt. Callers Open or OpenAt entries by name.
// The zero value is not usable; construct via OpenArchive.
type Archive struct {
	r       io.ReaderAt
	size    int64
	zr      *zip.Reader
	entries []ArchiveEntry
	byName  map[string]*zip.File
}

// OpenArchive parses the zip central directory at the tail of r and
// returns an Archive ready for entry enumeration. The ReaderAt is
// retained — the caller is responsible for its lifetime. Returns
// PULSE_ARCHIVE_MAGIC_INVALID when r does not start with the zip magic,
// and PULSE_ARCHIVE_CORRUPT when the EOCD or central directory cannot
// be parsed.
func OpenArchive(r io.ReaderAt, size int64) (*Archive, error) {
	ok, err := IsArchive(r, size)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.NewCodedError(errors.PULSE_ARCHIVE_MAGIC_INVALID,
			"archive does not begin with the zip magic PK\\x03\\x04")
	}

	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_ARCHIVE_CORRUPT,
			"parsing zip end-of-central-directory or central directory")
	}

	entries := make([]ArchiveEntry, 0, len(zr.File))
	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		off, derr := f.DataOffset()
		if derr != nil {
			return nil, errors.WrapCodedError(derr, errors.PULSE_ARCHIVE_CORRUPT,
				fmt.Sprintf("locating data offset for entry %q", f.Name))
		}
		entries = append(entries, ArchiveEntry{
			Name:   f.Name,
			Size:   int64(f.UncompressedSize64),
			Offset: off,
		})
		byName[f.Name] = f
	}

	return &Archive{
		r:       r,
		size:    size,
		zr:      zr,
		entries: entries,
		byName:  byName,
	}, nil
}

// Entries returns every entry in central-directory order (equals shard
// insertion order for Pulse-written archives). The slice is a fresh
// copy; callers may mutate freely.
func (a *Archive) Entries() []ArchiveEntry {
	out := make([]ArchiveEntry, len(a.entries))
	copy(out, a.entries)
	return out
}

// Open returns an io.ReadCloser over the named entry's payload.
// Returns PULSE_SHARD_MISSING when no entry of that name exists in the
// central directory. Suitable for streaming reads; the closer must be
// drained or closed when done.
func (a *Archive) Open(name string) (io.ReadCloser, error) {
	f, ok := a.byName[name]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_MISSING,
			fmt.Sprintf("shard %q not present in archive central directory", name),
			map[string]any{"entry": name})
	}
	rc, err := f.Open()
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_ARCHIVE_CORRUPT,
			fmt.Sprintf("opening archive entry %q", name))
	}
	return rc, nil
}

// OpenAt returns an io.SectionReader covering the named entry's stored
// payload. Only safe for store-only (Method 0) entries — Pulse always
// writes Method 0, but third-party archives that pass through this
// path may carry deflated entries; the caller should verify the
// entry's Method (see Archive.IsStored) before consuming the section
// reader. Returns PULSE_SHARD_MISSING for unknown names.
func (a *Archive) OpenAt(name string) (io.SectionReader, error) {
	f, ok := a.byName[name]
	if !ok {
		return io.SectionReader{}, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_MISSING,
			fmt.Sprintf("shard %q not present in archive central directory", name),
			map[string]any{"entry": name})
	}
	off, err := f.DataOffset()
	if err != nil {
		return io.SectionReader{}, errors.WrapCodedError(err, errors.PULSE_ARCHIVE_CORRUPT,
			fmt.Sprintf("locating data offset for entry %q", name))
	}
	return *io.NewSectionReader(a.r, off, int64(f.UncompressedSize64)), nil
}

// IsStored reports whether the named entry uses store-only (Method 0)
// compression. Pulse-written shards are always store-only; this helper
// is exposed for callers that want to confirm the contract before
// using OpenAt on a section reader.
func (a *Archive) IsStored(name string) bool {
	f, ok := a.byName[name]
	if !ok {
		return false
	}
	return f.Method == zip.Store
}

// PeekShardHeader reads the first encoding.HeaderSize bytes of the
// named entry and validates them as a single-file Pulse header (magic
// + format version). Returns PULSE_SHARD_HEADER_INVALID on mismatch so
// callers can surface a structured error without parsing the full
// schema. Used by S2+ phases when peeking shard metadata on first
// read.
func (a *Archive) PeekShardHeader(name string) error {
	rc, err := a.Open(name)
	if err != nil {
		return err
	}
	defer rc.Close()
	var hdr [HeaderSize]byte
	n, err := io.ReadFull(rc, hdr[:])
	if err != nil || n != HeaderSize {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("shard %q is shorter than a Pulse header", name),
			map[string]any{"entry": name})
	}
	if !bytes.Equal(hdr[:len(MagicBytes)], MagicBytes[:]) {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("shard %q does not begin with the Pulse magic", name),
			map[string]any{"entry": name})
	}
	if hdr[len(MagicBytes)] != FormatVersion {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("shard %q has unsupported format version", name),
			map[string]any{"entry": name, "version": hdr[len(MagicBytes)]})
	}
	return nil
}
