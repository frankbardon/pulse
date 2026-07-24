package encoding

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"

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
// v2 (current) appends a trailing source-stat snapshot (size + mtime)
// after the bucket block — see Index.SourceSize / Index.SourceModTime
// and the WriteIndex format comment. v1 sidecars (no trailing stat
// snapshot) are no longer readable: ReadIndexHeader rejects any
// version byte other than the current one with ENCODING_INVALID,
// forcing an explicit `pulse index build` rebuild rather than a
// silent partial read. There is no in-place migration path — sidecars
// are cheap, deterministic rebuild artifacts, never hand-authored or
// long-lived across binary versions.
const IndexFormatVersion byte = 0x02

// IndexHeaderSize is the total byte size of the sidecar index header
// (magic + version), mirroring HeaderSize for the .pulse envelope.
const IndexHeaderSize = 9

// FingerprintSize is the byte length of the embedded .pulse content-hash
// fingerprint (a raw SHA-256 digest).
const FingerprintSize = sha256.Size // 32

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
// Format (v2):
//
//	9-byte header: magic "PULSEIDX" + version 0x02
//	32-byte fingerprint: raw SHA-256 digest
//	key-spec block:
//	  u16 key_count
//	  per key: u16 name_len + utf8 name, u8 type
//	bucket block:
//	  u32 bucket_count
//	  per bucket:
//	    u32 entry_count
//	    per entry:
//	      u16 key_len + raw key bytes
//	      u32 row_id_count
//	      per row_id: u64 (little-endian)
//	trailing source-stat snapshot (v2):
//	  u64 source_size
//	  i64 source_mod_time_unix_nano
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
	if err := writeIndexBuckets(w, idx.Buckets); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, idx.SourceSize); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index source size")
	}
	if err := binary.Write(w, binary.LittleEndian, idx.SourceModTime); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index source mod time")
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

func writeIndexBuckets(w io.Writer, buckets []IndexBucket) error {
	if err := binary.Write(w, binary.LittleEndian, uint32(len(buckets))); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing index bucket count")
	}
	for _, b := range buckets {
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
	}
	return nil
}

// ReadIndex deserializes a sidecar Index from r. Returns ENCODING_INVALID
// on a truncated/corrupt header, an unknown key FieldType byte, or a
// truncated body.
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

	buckets, err := readIndexBuckets(r)
	if err != nil {
		return nil, err
	}
	idx.Buckets = buckets

	if err := binary.Read(r, binary.LittleEndian, &idx.SourceSize); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index source size")
	}
	if err := binary.Read(r, binary.LittleEndian, &idx.SourceModTime); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index source mod time")
	}

	return idx, nil
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

func readIndexBuckets(r io.Reader) ([]IndexBucket, error) {
	var bucketCount uint32
	if err := binary.Read(r, binary.LittleEndian, &bucketCount); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index bucket count")
	}

	buckets := make([]IndexBucket, 0, bucketCount)
	for i := uint32(0); i < bucketCount; i++ {
		var entryCount uint32
		if err := binary.Read(r, binary.LittleEndian, &entryCount); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index bucket entry count")
		}

		entries := make([]IndexEntry, 0, entryCount)
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
			rowIDs := make([]uint64, rowIDCount)
			for k := uint32(0); k < rowIDCount; k++ {
				if err := binary.Read(r, binary.LittleEndian, &rowIDs[k]); err != nil {
					return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading index entry row-id")
				}
			}

			entries = append(entries, IndexEntry{Key: keyBuf, RowIDs: rowIDs})
		}

		buckets = append(buckets, IndexBucket{Entries: entries})
	}
	return buckets, nil
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
