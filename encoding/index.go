package encoding

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// IndexMagicBytes identifies a Pulse point-lookup sidecar index file.
// 8 bytes: "PULSEIDX". Distinct from MagicBytes ("PULSE\x00\x00\x00")
// so magic-byte dispatch never confuses a sidecar index with a .pulse
// cohort file — the two formats are never read through the same code
// path, but sharing the discriminant style keeps every Pulse binary
// artifact self-identifying the same way.
var IndexMagicBytes = [8]byte{'P', 'U', 'L', 'S', 'E', 'I', 'D', 'X'}

// IndexFormatVersion is the current point-lookup sidecar index format
// version. Independent of encoding.FormatVersion (the .pulse envelope
// version) — this is a separate, standalone sidecar format that
// versions on its own schedule.
//
// v3 (current) adds a fixed-width bucket-offset table immediately
// before the bucket-data region, and moves the source-stat snapshot
// (size + mtime, introduced in v2) earlier — right after the key-spec
// block, before the bucket count — so ReadIndexMeta can read every
// non-bucket field with one short linear pass and never touch bucket
// data. The offset table is what makes ReadBucketByKey an O(1) seek:
// a reader computes bucketIndex = BucketIndex(key, bucketCount), seeks
// to offsetTableStart + bucketIndex*8, reads that one u64, seeks to
// bucketDataStart + offset, and parses only that bucket's
// self-delimited entry_count-prefixed data — never the whole table,
// never the whole file. See WriteIndex's format comment for the full
// v3 layout and IndexMeta's doc comment for the seek anchors.
//
// v1/v2 sidecars are no longer readable: ReadIndexHeader rejects any
// version byte other than the current one with ENCODING_INVALID,
// forcing an explicit `pulse index build` rebuild rather than a
// silent partial read. There is no in-place migration path — sidecars
// are cheap, deterministic rebuild artifacts, never hand-authored or
// long-lived across binary versions.
const IndexFormatVersion byte = 0x03

// IndexHeaderSize is the total byte size of the sidecar index header
// (magic + version), mirroring HeaderSize for the .pulse envelope.
const IndexHeaderSize = 9

// FingerprintSize is the byte length of the embedded .pulse content-hash
// fingerprint (a raw SHA-256 digest).
const FingerprintSize = sha256.Size // 32

// bucketOffsetEntrySize is the fixed on-disk width, in bytes, of one
// bucket-offset table entry (a little-endian u64). The table's
// fixed-width-ness is what makes a single bucket-offset entry
// directly addressable via offsetTableStart + bucketIndex*8 without
// parsing any preceding entry.
const bucketOffsetEntrySize = 8

// maxIndexPreallocHint caps how many elements ReadBucketByKey /
// readIndexBuckets will eagerly preallocate for an entries or row-id
// slice based on an on-disk count field. A corrupt or maliciously
// crafted count (e.g. a seek landing mid-record after a tampered
// bucket-offset entry) must not be able to trigger an out-of-memory
// allocation before a single byte of the claimed count has been
// read-validated; growth beyond this hint falls back to normal
// append() amortized growth, so legitimately large buckets are still
// fully supported — this only bounds the single up-front allocation.
const maxIndexPreallocHint = 1 << 16

// Fingerprint is a raw SHA-256 content-hash digest of the source .pulse
// file an Index was built from. A later story (index build) computes
// one from the live cohort's bytes at build time; the lookup path
// recomputes it and compares against the embedded value to detect a
// stale index without decoding the cohort's records.
type Fingerprint [FingerprintSize]byte

// ComputeFingerprint hashes the full contents of r with SHA-256. Callers
// pass the raw bytes of the source .pulse file (header + schema +
// records) — identical content always yields an identical Fingerprint,
// regardless of platform or filesystem.
func ComputeFingerprint(r io.Reader) (Fingerprint, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return Fingerprint{}, errors.WrapCodedError(err, errors.ENCODING_IO,
			"computing pulse content fingerprint")
	}
	var fp Fingerprint
	copy(fp[:], h.Sum(nil))
	return fp, nil
}

// IndexKeySpec describes one ordered key column carried in the sidecar
// index's key-spec block: the schema field name and its on-wire
// FieldType. Composite keys (more than one IndexKeySpec) concatenate
// every key field's on-wire byte representation, in key-spec order,
// to form a single IndexEntry.Key.
type IndexKeySpec struct {
	Name string
	Type FieldType
}

// IndexEntry is one hash-bucket slot's payload: the exact on-wire key
// bytes (the concatenation of every key field's on-wire representation,
// in key-spec order) plus every row-id sharing that key. RowIDs holds
// more than one value whenever the source cohort has duplicate key
// values — the sidecar format is multimap-capable from day one so a
// later epic's multiplicity work needs no format change.
type IndexEntry struct {
	Key    []byte
	RowIDs []uint64
}

// IndexBucket is one slot of the on-disk hash table. A well-distributed
// build populates each bucket with zero or one entries on average; more
// than one entry means a hash collision on BucketIndex, resolved by the
// reader scanning Entries for a byte-equal Key.
type IndexBucket struct {
	Entries []IndexEntry
}

// Index is the full in-memory representation of a point-lookup sidecar
// index: the .pulse Fingerprint it was built from, the ordered key
// spec, the fixed-size hash-bucket table (len(Buckets) is the table's
// bucket count), and a source-stat snapshot (SourceSize /
// SourceModTime) taken at build time. Encoding/decoding here is pure
// codec — populating an Index from a live cohort and serving lookups
// against one are later stories' responsibility; this package stays
// free of service/processing imports.
type Index struct {
	Fingerprint Fingerprint
	Keys        []IndexKeySpec
	Buckets     []IndexBucket

	// SourceSize is the byte length of the source .pulse file, as
	// reported by the filesystem at build time. Paired with
	// SourceModTime to give a freshness check (Service.VerifyIndex) a
	// cheap fast-path that avoids re-hashing the whole cohort: if
	// either value no longer matches the current file's stat, the
	// cohort has definitely changed and the index is definitely stale
	// — no need to pay for a full content hash to know that. A
	// matching size+mtime pair is NOT by itself sufficient proof of
	// freshness (mtime resolution and pathological same-size rewrites
	// both make silent false negatives possible), so a match always
	// falls through to a full Fingerprint recompute for a conclusive
	// answer. See Service.Lookup / Service.VerifyIndex.
	SourceSize uint64

	// SourceModTime is the source .pulse file's modification time, as
	// Unix nanoseconds, at build time. See SourceSize's doc comment for
	// the fast-path contract this pairs with.
	SourceModTime int64
}

// IndexMeta is the cheap, bucket-data-free subset of a sidecar index:
// everything ReadIndexMeta can read with one short linear pass —
// header, fingerprint, key-spec, source-stat snapshot, and bucket
// count — plus the two byte-offset anchors a seekable reader needs to
// jump straight to one bucket's data without reading the offset table
// or any other bucket:
//
//   - OffsetTableStart: the absolute byte offset (from the start of
//     the sidecar) where the fixed-width bucket-offset table begins.
//     Entry i of that table lives at OffsetTableStart + i*8.
//   - BucketDataStart (a derived method, not a stored field): the
//     absolute byte offset where the bucket-data region begins,
//     immediately after the offset table
//     (OffsetTableStart + bucketCount*8). Every bucket-offset table
//     entry is a byte offset RELATIVE to this anchor, so bucket i's
//     absolute data position is BucketDataStart() + offsets[i].
//
// Staleness checks (Service.VerifyIndex) and arity checks (matching a
// lookup request's key-component count against the sidecar's own
// key-spec) only ever need this struct — never bucket data.
type IndexMeta struct {
	Fingerprint Fingerprint
	Keys        []IndexKeySpec

	SourceSize    uint64
	SourceModTime int64

	// BucketCount is the sidecar's hash-table bucket count — the same
	// value BucketIndex(key, bucketCount) needs to resolve a lookup key
	// to a bucket slot.
	BucketCount uint32

	// OffsetTableStart is the absolute byte offset, from the start of
	// the sidecar file, where the fixed-width bucket-offset table
	// begins. See the type doc comment for the full seek-math contract.
	OffsetTableStart int64
}

// BucketDataStart returns the absolute byte offset, from the start of
// the sidecar file, where the bucket-data region begins — immediately
// after the fixed-width bucket-offset table. Every bucket-offset
// table entry is a byte offset relative to this anchor.
func (m *IndexMeta) BucketDataStart() int64 {
	return m.OffsetTableStart + int64(m.BucketCount)*bucketOffsetEntrySize
}

// HashKey returns the 64-bit FNV-1a digest of raw key bytes. Builders
// and lookup callers both route through this (and BucketIndex) so the
// two sides never diverge on hash choice.
func HashKey(key []byte) uint64 {
	h := fnv.New64a()
	h.Write(key)
	return h.Sum64()
}

// BucketIndex maps a key's hash onto a bucket slot in a table sized
// bucketCount. bucketCount == 0 always resolves to 0 — callers must
// guard the empty-index case themselves via len(Index.Buckets) before
// indexing.
func BucketIndex(key []byte, bucketCount uint32) uint32 {
	if bucketCount == 0 {
		return 0
	}
	return uint32(HashKey(key) % uint64(bucketCount))
}

// WriteIndexHeader writes the sidecar index header (magic + version) to w.
func WriteIndexHeader(w io.Writer) error {
	var hdr [IndexHeaderSize]byte
	copy(hdr[:8], IndexMagicBytes[:])
	hdr[8] = IndexFormatVersion
	if _, err := w.Write(hdr[:]); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index header")
	}
	return nil
}

// ReadIndexHeader reads and validates the sidecar index header from r.
// Returns ENCODING_INVALID on a truncated header, wrong magic, or an
// unsupported version byte — mirroring ReadHeader's contract for the
// .pulse envelope.
func ReadIndexHeader(r io.Reader) error {
	var hdr [IndexHeaderSize]byte
	n, err := io.ReadFull(r, hdr[:])
	if err != nil || n != IndexHeaderSize {
		return errors.NewCodedError(errors.ENCODING_INVALID, "truncated index header")
	}

	for i := 0; i < len(IndexMagicBytes); i++ {
		if hdr[i] != IndexMagicBytes[i] {
			return errors.NewCodedError(errors.ENCODING_INVALID, "invalid index magic bytes")
		}
	}

	if hdr[8] != IndexFormatVersion {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"unsupported index format version",
			map[string]any{"version": hdr[8]})
	}

	return nil
}

// WriteIndex serializes idx to w.
//
// Format (v3):
//
//	9-byte header: magic "PULSEIDX" + version 0x03
//	32-byte fingerprint: raw SHA-256 digest
//	key-spec block:
//	  u16 key_count
//	  per key: u16 name_len + utf8 name, u8 type
//	source-stat snapshot:
//	  u64 source_size
//	  i64 source_mod_time_unix_nano
//	u32 bucket_count
//	bucket-offset table (fixed-width, bucket_count entries):
//	  per bucket: u64 byte_offset — relative to the start of the
//	    bucket-data region (i.e. relative to the byte immediately
//	    following this table). offsets[0] is always 0.
//	bucket-data region (bucket_count self-delimited blocks, in order):
//	  per bucket:
//	    u32 entry_count
//	    per entry:
//	      u16 key_len + raw key bytes
//	      u32 row_id_count
//	      per row_id: u64 (little-endian)
//
// The offset table's fixed width is what lets a reader (ReadBucketByKey)
// seek directly to entry bucketIndex without parsing any other bucket:
// offsetTableStart + bucketIndex*8 locates the one relevant offset, and
// bucketDataStart + offsets[bucketIndex] locates that bucket's
// self-delimited data. See IndexMeta's doc comment for the anchor
// definitions.
func WriteIndex(w io.Writer, idx *Index) error {
	if err := WriteIndexHeader(w); err != nil {
		return err
	}
	if _, err := w.Write(idx.Fingerprint[:]); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index fingerprint")
	}
	if err := writeIndexKeySpec(w, idx.Keys); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, idx.SourceSize); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index source size")
	}
	if err := binary.Write(w, binary.LittleEndian, idx.SourceModTime); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index source mod time")
	}
	if err := writeIndexBuckets(w, idx.Buckets); err != nil {
		return err
	}
	return nil
}

func writeIndexKeySpec(w io.Writer, keys []IndexKeySpec) error {
	if err := binary.Write(w, binary.LittleEndian, uint16(len(keys))); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index key count")
	}
	for _, k := range keys {
		nameBytes := []byte(k.Name)
		if err := binary.Write(w, binary.LittleEndian, uint16(len(nameBytes))); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index key name length")
		}
		if _, err := w.Write(nameBytes); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index key name")
		}
		if err := binary.Write(w, binary.LittleEndian, byte(k.Type)); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index key type")
		}
	}
	return nil
}

// indexKeySpecByteSize returns the exact on-wire byte length of the
// key-spec block writeIndexKeySpec would emit for keys: 2 bytes
// (key_count) plus, per key, 2 bytes (name_len) + len(name) bytes +
// 1 byte (type). ReadIndexMeta uses this to compute OffsetTableStart
// analytically from the already-parsed Keys slice, without a counting
// reader wrapper.
func indexKeySpecByteSize(keys []IndexKeySpec) int64 {
	size := int64(2)
	for _, k := range keys {
		size += 2 + int64(len(k.Name)) + 1
	}
	return size
}

// writeIndexBucket writes one bucket's self-delimited data block
// (entry_count followed by every entry) to w — the same shape whether
// called from writeIndexBuckets (full write) or, on the read side,
// parsed back by readIndexBucket (full read or single-bucket seek
// read) — the two must stay in lockstep.
func writeIndexBucket(w io.Writer, b IndexBucket) error {
	if err := binary.Write(w, binary.LittleEndian, uint32(len(b.Entries))); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index bucket entry count")
	}
	for _, e := range b.Entries {
		if err := binary.Write(w, binary.LittleEndian, uint16(len(e.Key))); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index entry key length")
		}
		if _, err := w.Write(e.Key); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index entry key")
		}
		if err := binary.Write(w, binary.LittleEndian, uint32(len(e.RowIDs))); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index entry row-id count")
		}
		for _, id := range e.RowIDs {
			if err := binary.Write(w, binary.LittleEndian, id); err != nil {
				return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index entry row-id")
			}
		}
	}
	return nil
}

// writeIndexBuckets emits the v3 bucket section: bucket_count, the
// fixed-width bucket-offset table, then every bucket's self-delimited
// data block in order. Each bucket is first serialized into its own
// buffer so its exact byte length is known before the offset table
// (which must precede all bucket data) is written — idx.Buckets is
// already fully resident in memory (the Index struct holds every
// bucket), so buffering the encoded form adds no new order-of-magnitude
// memory cost.
func writeIndexBuckets(w io.Writer, buckets []IndexBucket) error {
	if err := binary.Write(w, binary.LittleEndian, uint32(len(buckets))); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index bucket count")
	}

	encoded := make([][]byte, len(buckets))
	offsets := make([]uint64, len(buckets))
	var cum uint64
	for i, b := range buckets {
		var buf bytes.Buffer
		if err := writeIndexBucket(&buf, b); err != nil {
			return err
		}
		encoded[i] = buf.Bytes()
		offsets[i] = cum
		cum += uint64(len(encoded[i]))
	}

	for _, off := range offsets {
		if err := binary.Write(w, binary.LittleEndian, off); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index bucket offset table entry")
		}
	}
	for _, data := range encoded {
		if _, err := w.Write(data); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index bucket data")
		}
	}
	return nil
}

// ReadIndex deserializes a sidecar Index from r. Returns ENCODING_INVALID
// on a truncated/corrupt header, an unknown key FieldType byte, or a
// truncated body. A full read — every bucket's every entry — for
// callers that genuinely need the whole index (e.g. Service.ListIndexes'
// distinct-key / indexed-record summary). Callers that only need
// metadata should prefer ReadIndexMeta; callers that only need one
// bucket should prefer ReadBucketByKey.
func ReadIndex(r io.Reader) (*Index, error) {
	if err := ReadIndexHeader(r); err != nil {
		return nil, err
	}

	idx := &Index{}
	if _, err := io.ReadFull(r, idx.Fingerprint[:]); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index fingerprint")
	}

	keys, err := readIndexKeySpec(r)
	if err != nil {
		return nil, err
	}
	idx.Keys = keys

	if err := binary.Read(r, binary.LittleEndian, &idx.SourceSize); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index source size")
	}
	if err := binary.Read(r, binary.LittleEndian, &idx.SourceModTime); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index source mod time")
	}

	buckets, err := readIndexBuckets(r)
	if err != nil {
		return nil, err
	}
	idx.Buckets = buckets

	return idx, nil
}

// ReadIndexMeta deserializes only the bucket-data-free prefix of a
// sidecar index from r: header, fingerprint, key-spec, source-stat
// snapshot, and bucket count — never touching the bucket-offset table
// or any bucket data. r must start at byte 0 of the sidecar (the same
// assumption ReadIndex and ReadIndexHeader already make) so the
// returned IndexMeta.OffsetTableStart — computed analytically from the
// exact byte lengths of the sections just read, not by seeking — is a
// correct absolute file offset.
//
// This is the cheap path Service.VerifyIndex's staleness check and a
// lookup request's key-arity check both want: neither needs a single
// bucket, let alone the whole table.
func ReadIndexMeta(r io.Reader) (*IndexMeta, error) {
	if err := ReadIndexHeader(r); err != nil {
		return nil, err
	}

	meta := &IndexMeta{}
	if _, err := io.ReadFull(r, meta.Fingerprint[:]); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index fingerprint")
	}

	keys, err := readIndexKeySpec(r)
	if err != nil {
		return nil, err
	}
	meta.Keys = keys

	if err := binary.Read(r, binary.LittleEndian, &meta.SourceSize); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index source size")
	}
	if err := binary.Read(r, binary.LittleEndian, &meta.SourceModTime); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index source mod time")
	}
	if err := binary.Read(r, binary.LittleEndian, &meta.BucketCount); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index bucket count")
	}

	meta.OffsetTableStart = int64(IndexHeaderSize) + int64(FingerprintSize) +
		indexKeySpecByteSize(keys) + 8 /* source_size */ + 8 /* source_mod_time */ + 4 /* bucket_count */

	return meta, nil
}

// ReadBucketByKey seeks directly to and parses exactly ONE bucket of a
// v3 sidecar index — the bucket key hashes to via
// BucketIndex(key, meta.BucketCount) — never reading the bucket-offset
// table's other entries or any other bucket's data. meta must have
// been produced by ReadIndexMeta (or ReadIndexMetaFile) against the
// SAME sidecar rs reads from; rs's absolute byte offset 0 must
// correspond to the start of that sidecar (a freshly opened file
// handle, or one explicitly Seek'd back to 0), since
// meta.OffsetTableStart is an absolute file offset.
//
// Returns an empty *IndexBucket (no error) when meta.BucketCount == 0
// (an empty index has no buckets to seek into — mirrors the read side's
// existing empty-index handling). Returns ENCODING_INVALID on a seek
// failure, a truncated/corrupt bucket-offset entry, an offset that
// decodes to a value larger than a signed 64-bit byte offset can
// represent, or a truncated/corrupt bucket-data block at the resolved
// position.
func ReadBucketByKey(rs io.ReadSeeker, meta *IndexMeta, key []byte) (*IndexBucket, error) {
	if meta == nil {
		return nil, errors.NewCodedError(errors.ENCODING_INVALID, "read bucket by key requires non-nil index meta")
	}
	if meta.BucketCount == 0 {
		return &IndexBucket{}, nil
	}

	bi := BucketIndex(key, meta.BucketCount)
	offsetPos := meta.OffsetTableStart + int64(bi)*bucketOffsetEntrySize
	if _, err := rs.Seek(offsetPos, io.SeekStart); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "seeking to index bucket offset table entry")
	}

	var relOffset uint64
	if err := binary.Read(rs, binary.LittleEndian, &relOffset); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index bucket offset table entry")
	}
	if relOffset > uint64(math.MaxInt64) {
		return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"corrupt index bucket offset: value overflows a signed 64-bit byte offset",
			map[string]any{"bucket_index": bi, "offset": relOffset})
	}

	bucketPos := meta.BucketDataStart() + int64(relOffset)
	if _, err := rs.Seek(bucketPos, io.SeekStart); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "seeking to index bucket data")
	}

	bucket, err := readIndexBucket(rs)
	if err != nil {
		return nil, err
	}
	return bucket, nil
}

func readIndexKeySpec(r io.Reader) ([]IndexKeySpec, error) {
	var keyCount uint16
	if err := binary.Read(r, binary.LittleEndian, &keyCount); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index key count")
	}

	keys := make([]IndexKeySpec, 0, keyCount)
	for i := 0; i < int(keyCount); i++ {
		var nameLen uint16
		if err := binary.Read(r, binary.LittleEndian, &nameLen); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index key name length")
		}
		nameBuf := make([]byte, nameLen)
		if _, err := io.ReadFull(r, nameBuf); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index key name")
		}

		var typeByte uint8
		if err := binary.Read(r, binary.LittleEndian, &typeByte); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index key type")
		}
		ft := FieldType(typeByte)
		if !ft.IsKnown() {
			return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"unknown index key field type byte",
				map[string]any{"byte": typeByte, "key_index": i})
		}

		keys = append(keys, IndexKeySpec{Name: string(nameBuf), Type: ft})
	}
	return keys, nil
}

// readIndexBuckets performs a full sequential read of the v3 bucket
// section: bucket_count, the fixed-width offset table (consumed but not
// used — a full linear read needs no seek math), then every bucket's
// self-delimited data block in order.
func readIndexBuckets(r io.Reader) ([]IndexBucket, error) {
	var bucketCount uint32
	if err := binary.Read(r, binary.LittleEndian, &bucketCount); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index bucket count")
	}

	for i := uint32(0); i < bucketCount; i++ {
		var off uint64
		if err := binary.Read(r, binary.LittleEndian, &off); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index bucket offset table entry")
		}
	}

	buckets := make([]IndexBucket, 0, capIndexPrealloc(bucketCount))
	for i := uint32(0); i < bucketCount; i++ {
		b, err := readIndexBucket(r)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, *b)
	}
	return buckets, nil
}

// readIndexBucket reads one bucket's self-delimited data block
// (entry_count followed by every entry) from r. Used both by
// readIndexBuckets' full-index loop and by ReadBucketByKey's
// single-bucket seek read — the block is self-delimited (entry_count
// prefix, then per-entry key_len/row_id_count prefixes) so either
// caller can parse it correctly without knowing where it ends in
// advance.
func readIndexBucket(r io.Reader) (*IndexBucket, error) {
	var entryCount uint32
	if err := binary.Read(r, binary.LittleEndian, &entryCount); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index bucket entry count")
	}

	entries := make([]IndexEntry, 0, capIndexPrealloc(entryCount))
	for j := uint32(0); j < entryCount; j++ {
		var keyLen uint16
		if err := binary.Read(r, binary.LittleEndian, &keyLen); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index entry key length")
		}
		keyBuf := make([]byte, keyLen)
		if _, err := io.ReadFull(r, keyBuf); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index entry key")
		}

		var rowIDCount uint32
		if err := binary.Read(r, binary.LittleEndian, &rowIDCount); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index entry row-id count")
		}
		rowIDs := make([]uint64, 0, capIndexPrealloc(rowIDCount))
		for k := uint32(0); k < rowIDCount; k++ {
			var id uint64
			if err := binary.Read(r, binary.LittleEndian, &id); err != nil {
				return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index entry row-id")
			}
			rowIDs = append(rowIDs, id)
		}

		entries = append(entries, IndexEntry{Key: keyBuf, RowIDs: rowIDs})
	}

	return &IndexBucket{Entries: entries}, nil
}

// capIndexPrealloc bounds an on-disk count field's use as a slice
// preallocation hint — see maxIndexPreallocHint's doc comment.
func capIndexPrealloc(n uint32) int {
	if n > maxIndexPreallocHint {
		return maxIndexPreallocHint
	}
	return int(n)
}

// WriteIndexFile serializes idx and writes it to path on fsys, creating
// or truncating the file as needed. All sidecar index I/O goes through
// afero.Fs so callers get the same hermetic-test story as the rest of
// Pulse's file surface (fs.NewMemMap() in tests, fs.Default() / a
// caller-injected afero.Fs in production).
func WriteIndexFile(fsys afero.Fs, path string, idx *Index) error {
	f, err := fsys.Create(path)
	if err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("creating sidecar index file: %s", path))
	}
	defer f.Close()

	if err := WriteIndex(f, idx); err != nil {
		return err
	}
	return nil
}

// ReadIndexFile opens path on fsys and deserializes a sidecar Index.
func ReadIndexFile(fsys afero.Fs, path string) (*Index, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("opening sidecar index file: %s", path))
	}
	defer f.Close()

	return ReadIndex(f)
}

// ReadIndexMetaFile opens path on fsys and deserializes only the
// bucket-data-free IndexMeta prefix — the afero-backed convenience
// counterpart to ReadIndexMeta, mirroring ReadIndexFile's relationship
// to ReadIndex. Callers that only need staleness/arity metadata (e.g.
// Service.VerifyIndex) should prefer this over ReadIndexFile.
func ReadIndexMetaFile(fsys afero.Fs, path string) (*IndexMeta, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("opening sidecar index file: %s", path))
	}
	defer f.Close()

	return ReadIndexMeta(f)
}
