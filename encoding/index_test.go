package encoding

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

func TestIndexHeader_WriteRead(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteIndexHeader(&buf); err != nil {
		t.Fatalf("WriteIndexHeader: %v", err)
	}
	data := buf.Bytes()
	if len(data) != IndexHeaderSize {
		t.Fatalf("header size = %d, want %d", len(data), IndexHeaderSize)
	}
	for i, b := range IndexMagicBytes {
		if data[i] != b {
			t.Errorf("magic byte[%d] = 0x%02x, want 0x%02x", i, data[i], b)
		}
	}
	if data[8] != IndexFormatVersion {
		t.Errorf("version byte = 0x%02x, want 0x%02x", data[8], IndexFormatVersion)
	}
	if err := ReadIndexHeader(bytes.NewReader(data)); err != nil {
		t.Fatalf("ReadIndexHeader on valid header: %v", err)
	}
}

func TestIndexHeader_DistinctFromPulseMagic(t *testing.T) {
	// The sidecar magic must never collide with the .pulse envelope
	// magic — a reader dispatching on the first bytes must be able to
	// tell the two formats apart.
	for i := 0; i < len(MagicBytes) && i < len(IndexMagicBytes); i++ {
		if MagicBytes[i] != IndexMagicBytes[i] {
			return
		}
	}
	t.Fatalf("IndexMagicBytes %v must not share a prefix with MagicBytes %v", IndexMagicBytes, MagicBytes)
}

func TestIndexHeader_CorruptOrShort(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"too_short", []byte{0x50, 0x55}},
		{"wrong_magic", func() []byte {
			d := make([]byte, IndexHeaderSize)
			copy(d, "PULSE\x00\x00\x00")
			d[8] = IndexFormatVersion
			return d
		}()},
		{"wrong_version", func() []byte {
			var buf bytes.Buffer
			_ = WriteIndexHeader(&buf)
			d := buf.Bytes()
			d[8] = 0xFF
			return d
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ReadIndexHeader(bytes.NewReader(tt.data))
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.HasCode(err, errors.ENCODING_INVALID) {
				t.Errorf("expected ENCODING_INVALID, got: %v", err)
			}
		})
	}
}

func sampleFingerprint(seed byte) Fingerprint {
	var fp Fingerprint
	for i := range fp {
		fp[i] = seed
	}
	return fp
}

func TestIndex_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		idx  *Index
	}{
		{
			name: "empty_index",
			idx: &Index{
				Fingerprint: sampleFingerprint(0x00),
				Keys:        nil,
				Buckets:     nil,
			},
		},
		{
			name: "single_key_single_row",
			idx: &Index{
				Fingerprint: sampleFingerprint(0xAB),
				Keys: []IndexKeySpec{
					{Name: "customer_id", Type: FieldTypeU32},
				},
				Buckets: []IndexBucket{
					{Entries: []IndexEntry{
						{Key: []byte{0x01, 0x00, 0x00, 0x00}, RowIDs: []uint64{7}},
					}},
					{Entries: nil},
				},
			},
		},
		{
			name: "multi_row_bucket",
			idx: &Index{
				Fingerprint: sampleFingerprint(0xCD),
				Keys: []IndexKeySpec{
					{Name: "region", Type: FieldTypeCategoricalU8},
				},
				Buckets: []IndexBucket{
					{Entries: []IndexEntry{
						{Key: []byte{0x02}, RowIDs: []uint64{1, 2, 3, 4, 5}},
					}},
				},
			},
		},
		{
			name: "composite_key_multi_bucket_collision",
			idx: &Index{
				Fingerprint: sampleFingerprint(0x11),
				Keys: []IndexKeySpec{
					{Name: "account_id", Type: FieldTypeU64},
					{Name: "period", Type: FieldTypeDate},
				},
				Buckets: []IndexBucket{
					{Entries: []IndexEntry{
						{Key: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, RowIDs: []uint64{100}},
						{Key: []byte{9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 0, 0}, RowIDs: []uint64{200, 201}},
					}},
					{Entries: []IndexEntry{}},
					{Entries: []IndexEntry{
						{Key: []byte{}, RowIDs: []uint64{}},
					}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteIndex(&buf, tt.idx); err != nil {
				t.Fatalf("WriteIndex: %v", err)
			}

			// Byte-stability: re-serializing the round-tripped value
			// must produce identical bytes.
			got, err := ReadIndex(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("ReadIndex: %v", err)
			}

			var buf2 bytes.Buffer
			if err := WriteIndex(&buf2, got); err != nil {
				t.Fatalf("WriteIndex (re-encode): %v", err)
			}
			if !bytes.Equal(buf.Bytes(), buf2.Bytes()) {
				t.Fatalf("round-trip not byte-stable:\n first  = % x\n second = % x", buf.Bytes(), buf2.Bytes())
			}

			normalizeIndex(tt.idx)
			normalizeIndex(got)
			if !reflect.DeepEqual(tt.idx, got) {
				t.Fatalf("round-trip mismatch:\n want = %+v\n got  = %+v", tt.idx, got)
			}
		})
	}
}

// normalizeIndex collapses nil vs. empty-slice distinctions so
// reflect.DeepEqual compares semantic content, not Go slice
// nil-ness (the wire format has no way to distinguish nil from
// empty — both encode as a zero count).
func normalizeIndex(idx *Index) {
	if idx.Keys == nil {
		idx.Keys = []IndexKeySpec{}
	}
	if idx.Buckets == nil {
		idx.Buckets = []IndexBucket{}
	}
	for bi := range idx.Buckets {
		if idx.Buckets[bi].Entries == nil {
			idx.Buckets[bi].Entries = []IndexEntry{}
		}
		for ei := range idx.Buckets[bi].Entries {
			if idx.Buckets[bi].Entries[ei].Key == nil {
				idx.Buckets[bi].Entries[ei].Key = []byte{}
			}
			if idx.Buckets[bi].Entries[ei].RowIDs == nil {
				idx.Buckets[bi].Entries[ei].RowIDs = []uint64{}
			}
		}
	}
}

func TestIndex_Read_UnknownKeyFieldType(t *testing.T) {
	idx := &Index{
		Fingerprint: sampleFingerprint(0x01),
		Keys: []IndexKeySpec{
			{Name: "foo", Type: FieldTypeU8},
		},
	}
	var buf bytes.Buffer
	if err := WriteIndex(&buf, idx); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}

	data := buf.Bytes()
	// Locate and corrupt the single key's type byte: header(9) +
	// fingerprint(32) + key_count(2) + name_len(2) + "foo"(3) = 48.
	typeByteOffset := IndexHeaderSize + FingerprintSize + 2 + 2 + len("foo")
	data[typeByteOffset] = 0xFF // unknown field type

	_, err := ReadIndex(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error for unknown key field type byte")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("expected ENCODING_INVALID, got: %v", err)
	}
}

func TestIndex_Read_TruncatedBody(t *testing.T) {
	idx := &Index{
		Fingerprint: sampleFingerprint(0x02),
		Keys: []IndexKeySpec{
			{Name: "id", Type: FieldTypeU32},
		},
		Buckets: []IndexBucket{
			{Entries: []IndexEntry{
				{Key: []byte{1, 2, 3, 4}, RowIDs: []uint64{1, 2}},
			}},
		},
	}
	var buf bytes.Buffer
	if err := WriteIndex(&buf, idx); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}

	full := buf.Bytes()
	for _, cut := range []int{0, IndexHeaderSize, IndexHeaderSize + 5, len(full) - 1, len(full) - 4} {
		if cut > len(full) {
			continue
		}
		_, err := ReadIndex(bytes.NewReader(full[:cut]))
		if err == nil {
			t.Fatalf("expected error for truncated body at cut=%d", cut)
		}
		if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("cut=%d: expected ENCODING_INVALID, got: %v", cut, err)
		}
	}
}

func TestComputeFingerprint_Deterministic(t *testing.T) {
	content := []byte("some pulse cohort bytes, header + schema + records")

	fp1, err := ComputeFingerprint(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	fp2, err := ComputeFingerprint(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if fp1 != fp2 {
		t.Fatalf("identical content must yield identical fingerprint: %x vs %x", fp1, fp2)
	}

	other, err := ComputeFingerprint(strings.NewReader("different content entirely"))
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if fp1 == other {
		t.Fatal("different content must yield different fingerprint")
	}

	var zero Fingerprint
	if fp1 == zero {
		t.Fatal("fingerprint of non-empty content must not be the zero value")
	}
}

func TestHashKey_BucketIndex_Deterministic(t *testing.T) {
	key := []byte{1, 2, 3, 4}
	h1 := HashKey(key)
	h2 := HashKey(key)
	if h1 != h2 {
		t.Fatalf("HashKey must be deterministic: %d vs %d", h1, h2)
	}

	other := HashKey([]byte{4, 3, 2, 1})
	if h1 == other {
		t.Fatal("distinct keys should not usually collide (FNV-1a sanity check)")
	}

	if got := BucketIndex(key, 0); got != 0 {
		t.Errorf("BucketIndex with bucketCount=0 = %d, want 0", got)
	}
	idx := BucketIndex(key, 16)
	if idx >= 16 {
		t.Errorf("BucketIndex(%v, 16) = %d, out of range", key, idx)
	}
	if got := BucketIndex(key, 16); got != idx {
		t.Errorf("BucketIndex must be deterministic: %d vs %d", idx, got)
	}
}

func TestIndex_AferoRoundTrip(t *testing.T) {
	cfg := fs.NewMemMap()

	idx := &Index{
		Fingerprint: sampleFingerprint(0x42),
		Keys: []IndexKeySpec{
			{Name: "order_id", Type: FieldTypeU64},
		},
		Buckets: []IndexBucket{
			{Entries: []IndexEntry{
				{Key: []byte{1, 0, 0, 0, 0, 0, 0, 0}, RowIDs: []uint64{10, 11, 12}},
			}},
		},
	}

	const path = "cohort.pulse.idx"
	if err := WriteIndexFile(cfg.Fs(), path, idx); err != nil {
		t.Fatalf("WriteIndexFile: %v", err)
	}

	exists, err := afero.Exists(cfg.Fs(), path)
	if err != nil {
		t.Fatalf("afero.Exists: %v", err)
	}
	if !exists {
		t.Fatal("WriteIndexFile did not create the sidecar file on the afero.Fs")
	}

	got, err := ReadIndexFile(cfg.Fs(), path)
	if err != nil {
		t.Fatalf("ReadIndexFile: %v", err)
	}

	normalizeIndex(idx)
	normalizeIndex(got)
	if !reflect.DeepEqual(idx, got) {
		t.Fatalf("afero round-trip mismatch:\n want = %+v\n got  = %+v", idx, got)
	}
}

func TestIndex_AferoRoundTrip_MissingFile(t *testing.T) {
	cfg := fs.NewMemMap()
	_, err := ReadIndexFile(cfg.Fs(), "does-not-exist.idx")
	if err == nil {
		t.Fatal("expected error reading a missing sidecar index file")
	}
	if !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("expected ENCODING_IO, got: %v", err)
	}
}

// ---------------------------------------------------------------------
// E7-S1: v3 seekable bucket-offset table.
// ---------------------------------------------------------------------

func TestIndex_FormatVersionIsV3(t *testing.T) {
	if IndexFormatVersion != 0x03 {
		t.Fatalf("IndexFormatVersion = 0x%02x, want 0x03", IndexFormatVersion)
	}
}

// TestIndexHeader_RejectsOldV2Sidecar proves a v2 sidecar (the format
// this story bumps past) is explicitly rejected, not silently
// misparsed — a hand-crafted header with version byte 0x02 (the actual
// prior IndexFormatVersion, not an arbitrary "wrong version" probe).
func TestIndexHeader_RejectsOldV2Sidecar(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteIndexHeader(&buf); err != nil {
		t.Fatalf("WriteIndexHeader: %v", err)
	}
	data := buf.Bytes()
	data[8] = 0x02 // the real prior (v2) format version byte

	err := ReadIndexHeader(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error reading a v2 sidecar header under v3 code")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("expected ENCODING_INVALID, got: %v", err)
	}
}

// keyForN encodes n as an 8-byte big-endian key — a simple, fully
// deterministic key generator used across the v3 seek tests to derive
// reproducible BucketIndex placements without any randomness.
func keyForN(n uint64) []byte {
	var kb [8]byte
	binary.BigEndian.PutUint64(kb[:], n)
	return kb[:]
}

// buildManyBucketIndexFixture builds an Index whose bucket placement
// mirrors Service.BuildIndex's real write-side contract: every key's
// bucket slot is BucketIndex(key, bucketCount), so a reader computing
// the same function independently (as ReadBucketByKey does) always
// resolves to the bucket that actually holds the key. keyCount <
// bucketCount by a wide margin leaves a realistic mix of empty,
// single-entry, and (occasionally, via genuine hash collision)
// multi-entry buckets — the same "well-distributed build" shape
// IndexBucket's doc comment describes.
func buildManyBucketIndexFixture(bucketCount uint32, keyCount int) (*Index, [][]byte) {
	buckets := make([]IndexBucket, bucketCount)
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		k := keyForN(uint64(i))
		keys[i] = k
		bi := BucketIndex(k, bucketCount)
		buckets[bi].Entries = append(buckets[bi].Entries, IndexEntry{
			Key:    append([]byte(nil), k...),
			RowIDs: []uint64{uint64(i), uint64(i) + 1}, // 2 row-ids per key
		})
	}
	return &Index{
		Fingerprint:   sampleFingerprint(0x77),
		Keys:          []IndexKeySpec{{Name: "id", Type: FieldTypeU64}},
		Buckets:       buckets,
		SourceSize:    123456789,
		SourceModTime: 987654321,
	}, keys
}

func bucketsDeepEqual(a, b IndexBucket) bool {
	na, nb := a, b
	if na.Entries == nil {
		na.Entries = []IndexEntry{}
	}
	if nb.Entries == nil {
		nb.Entries = []IndexEntry{}
	}
	for i := range na.Entries {
		if na.Entries[i].RowIDs == nil {
			na.Entries[i].RowIDs = []uint64{}
		}
	}
	for i := range nb.Entries {
		if nb.Entries[i].RowIDs == nil {
			nb.Entries[i].RowIDs = []uint64{}
		}
	}
	return reflect.DeepEqual(na, nb)
}

func TestReadIndexMeta_MatchesFullIndexFields(t *testing.T) {
	idx, _ := buildManyBucketIndexFixture(37, 50)
	var buf bytes.Buffer
	if err := WriteIndex(&buf, idx); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}
	full := buf.Bytes()

	meta, err := ReadIndexMeta(bytes.NewReader(full))
	if err != nil {
		t.Fatalf("ReadIndexMeta: %v", err)
	}
	if meta.Fingerprint != idx.Fingerprint {
		t.Errorf("Fingerprint mismatch")
	}
	if !reflect.DeepEqual(meta.Keys, idx.Keys) {
		t.Errorf("Keys = %+v, want %+v", meta.Keys, idx.Keys)
	}
	if meta.SourceSize != idx.SourceSize {
		t.Errorf("SourceSize = %d, want %d", meta.SourceSize, idx.SourceSize)
	}
	if meta.SourceModTime != idx.SourceModTime {
		t.Errorf("SourceModTime = %d, want %d", meta.SourceModTime, idx.SourceModTime)
	}
	if meta.BucketCount != uint32(len(idx.Buckets)) {
		t.Errorf("BucketCount = %d, want %d", meta.BucketCount, len(idx.Buckets))
	}

	// The offset table must immediately follow the bucket_count field,
	// and must be immediately followed by the first bucket's data
	// (offset 0 always resolves to bucketDataStart itself).
	wantOffsetTableStart := int64(IndexHeaderSize) + int64(FingerprintSize) + indexKeySpecByteSize(idx.Keys) + 8 + 8 + 4
	if meta.OffsetTableStart != wantOffsetTableStart {
		t.Errorf("OffsetTableStart = %d, want %d", meta.OffsetTableStart, wantOffsetTableStart)
	}
	wantBucketDataStart := wantOffsetTableStart + int64(meta.BucketCount)*8
	if got := meta.BucketDataStart(); got != wantBucketDataStart {
		t.Errorf("BucketDataStart() = %d, want %d", got, wantBucketDataStart)
	}
}

// TestReadBucketByKey_MatchesFullReadForEveryBucket is the correctness
// backbone: for every key inserted into a many-bucket fixture,
// ReadBucketByKey (seek-addressed) must return exactly the same bucket
// content ReadIndex's full parse assigns to that key's BucketIndex slot
// — proving the offset table + seek math resolve to the right bucket,
// not just "a" bucket.
func TestReadBucketByKey_MatchesFullReadForEveryBucket(t *testing.T) {
	idx, keys := buildManyBucketIndexFixture(37, 150)
	var buf bytes.Buffer
	if err := WriteIndex(&buf, idx); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}
	full := buf.Bytes()

	meta, err := ReadIndexMeta(bytes.NewReader(full))
	if err != nil {
		t.Fatalf("ReadIndexMeta: %v", err)
	}

	for i, key := range keys {
		rs := bytes.NewReader(full)
		got, err := ReadBucketByKey(rs, meta, key)
		if err != nil {
			t.Fatalf("key[%d]: ReadBucketByKey: %v", i, err)
		}
		bi := BucketIndex(key, meta.BucketCount)
		want := idx.Buckets[bi]
		if !bucketsDeepEqual(*got, want) {
			t.Fatalf("key[%d] (bucket %d): got %+v, want %+v", i, bi, got, want)
		}
	}
}

// TestReadBucketByKey_SingleMultiEmptyBuckets covers the three shapes
// IndexBucket's doc comment describes explicitly, each via a small,
// fully deterministic (no randomness) fixture:
//   - single-entry bucket: exactly one key exists, so its own bucket
//     (and, with overwhelming probability at a large bucket count,
//     every other bucket) holds exactly one entry.
//   - empty bucket: a key NEVER inserted, deliberately searched for one
//     that resolves to a bucket holding zero entries.
//   - multi-entry (collision) bucket: bucketCount == 1 forces every
//     key into the same single bucket — a trivially deterministic way
//     to construct a guaranteed collision without hunting for one.
func TestReadBucketByKey_SingleMultiEmptyBuckets(t *testing.T) {
	t.Run("single_entry_bucket", func(t *testing.T) {
		const bucketCount = 997 // prime, keeps collisions rare with 1 key
		keyX := keyForN(42)
		idx := &Index{
			Fingerprint: sampleFingerprint(0x01),
			Keys:        []IndexKeySpec{{Name: "id", Type: FieldTypeU64}},
			Buckets:     make([]IndexBucket, bucketCount),
		}
		bi := BucketIndex(keyX, bucketCount)
		idx.Buckets[bi] = IndexBucket{Entries: []IndexEntry{{Key: keyX, RowIDs: []uint64{7}}}}

		var buf bytes.Buffer
		if err := WriteIndex(&buf, idx); err != nil {
			t.Fatalf("WriteIndex: %v", err)
		}
		full := buf.Bytes()
		meta, err := ReadIndexMeta(bytes.NewReader(full))
		if err != nil {
			t.Fatalf("ReadIndexMeta: %v", err)
		}

		got, err := ReadBucketByKey(bytes.NewReader(full), meta, keyX)
		if err != nil {
			t.Fatalf("ReadBucketByKey: %v", err)
		}
		if len(got.Entries) != 1 {
			t.Fatalf("Entries = %d, want 1", len(got.Entries))
		}
		if !bytes.Equal(got.Entries[0].Key, keyX) {
			t.Errorf("Entry key = % x, want % x", got.Entries[0].Key, keyX)
		}
		if !reflect.DeepEqual(got.Entries[0].RowIDs, []uint64{7}) {
			t.Errorf("Entry row-ids = %v, want [7]", got.Entries[0].RowIDs)
		}
	})

	t.Run("empty_bucket", func(t *testing.T) {
		const bucketCount = 997
		keyX := keyForN(42)
		idx := &Index{
			Fingerprint: sampleFingerprint(0x02),
			Keys:        []IndexKeySpec{{Name: "id", Type: FieldTypeU64}},
			Buckets:     make([]IndexBucket, bucketCount),
		}
		occupiedBI := BucketIndex(keyX, bucketCount)
		idx.Buckets[occupiedBI] = IndexBucket{Entries: []IndexEntry{{Key: keyX, RowIDs: []uint64{7}}}}

		// Deterministically search for a key that resolves to a
		// DIFFERENT (necessarily still-empty, since only one bucket is
		// occupied) bucket.
		var emptyKey []byte
		for n := uint64(1000); n < 1000+bucketCount*2; n++ {
			k := keyForN(n)
			if BucketIndex(k, bucketCount) != occupiedBI {
				emptyKey = k
				break
			}
		}
		if emptyKey == nil {
			t.Fatal("could not find a key resolving to an empty bucket — fixture assumption broken")
		}

		var buf bytes.Buffer
		if err := WriteIndex(&buf, idx); err != nil {
			t.Fatalf("WriteIndex: %v", err)
		}
		full := buf.Bytes()
		meta, err := ReadIndexMeta(bytes.NewReader(full))
		if err != nil {
			t.Fatalf("ReadIndexMeta: %v", err)
		}

		got, err := ReadBucketByKey(bytes.NewReader(full), meta, emptyKey)
		if err != nil {
			t.Fatalf("ReadBucketByKey: %v", err)
		}
		if len(got.Entries) != 0 {
			t.Fatalf("Entries = %d, want 0 (empty bucket)", len(got.Entries))
		}
	})

	t.Run("multi_entry_collision_bucket", func(t *testing.T) {
		// bucketCount == 1 (a single-element Buckets slice below) forces
		// every key into bucket 0.
		keyX := keyForN(1)
		keyY := keyForN(2)
		idx := &Index{
			Fingerprint: sampleFingerprint(0x03),
			Keys:        []IndexKeySpec{{Name: "id", Type: FieldTypeU64}},
			Buckets: []IndexBucket{
				{Entries: []IndexEntry{
					{Key: keyX, RowIDs: []uint64{1}},
					{Key: keyY, RowIDs: []uint64{2, 3}},
				}},
			},
		}

		var buf bytes.Buffer
		if err := WriteIndex(&buf, idx); err != nil {
			t.Fatalf("WriteIndex: %v", err)
		}
		full := buf.Bytes()
		meta, err := ReadIndexMeta(bytes.NewReader(full))
		if err != nil {
			t.Fatalf("ReadIndexMeta: %v", err)
		}

		got, err := ReadBucketByKey(bytes.NewReader(full), meta, keyX)
		if err != nil {
			t.Fatalf("ReadBucketByKey: %v", err)
		}
		if len(got.Entries) != 2 {
			t.Fatalf("Entries = %d, want 2 (collision bucket)", len(got.Entries))
		}
		if !bucketsDeepEqual(*got, idx.Buckets[0]) {
			t.Fatalf("got %+v, want %+v", got, idx.Buckets[0])
		}
	})
}

// TestReadBucketByKey_EmptyIndex covers the bucketCount == 0 case: no
// buckets exist at all (an Index built from a cohort with zero rows,
// or zero distinct keys). ReadBucketByKey must return an empty bucket,
// not an error — mirrors service/lookup.go's existing
// len(idx.Buckets) == 0 short-circuit.
func TestReadBucketByKey_EmptyIndex(t *testing.T) {
	idx := &Index{
		Fingerprint: sampleFingerprint(0x04),
		Keys:        []IndexKeySpec{{Name: "id", Type: FieldTypeU64}},
	}
	var buf bytes.Buffer
	if err := WriteIndex(&buf, idx); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}
	full := buf.Bytes()
	meta, err := ReadIndexMeta(bytes.NewReader(full))
	if err != nil {
		t.Fatalf("ReadIndexMeta: %v", err)
	}
	if meta.BucketCount != 0 {
		t.Fatalf("BucketCount = %d, want 0", meta.BucketCount)
	}

	got, err := ReadBucketByKey(bytes.NewReader(full), meta, keyForN(1))
	if err != nil {
		t.Fatalf("ReadBucketByKey: %v", err)
	}
	if len(got.Entries) != 0 {
		t.Fatalf("Entries = %d, want 0", len(got.Entries))
	}
}

// countingReadSeeker wraps an io.ReadSeeker and records the total
// number of bytes actually delivered via Read — never via Seek, which
// costs nothing on a real seekable handle (a file or, in this test, an
// in-memory *bytes.Reader). This is the seek-only-one-bucket proof
// instrument: if ReadBucketByKey ever fell back to reading the whole
// file (or the whole offset table, or another bucket), the recorded
// byte count would balloon accordingly.
type countingReadSeeker struct {
	r         io.ReadSeeker
	bytesRead int
}

func (c *countingReadSeeker) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.bytesRead += n
	return n, err
}

func (c *countingReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return c.r.Seek(offset, whence)
}

// TestReadBucketByKey_ReadsOnlyTargetBucket is the acceptance-criteria
// seek-only proof: on a many-bucket, many-entry fixture, ReadBucketByKey
// must read only the one target bucket-offset entry (8 bytes) plus the
// one target bucket's self-delimited data — never the whole offset
// table and never any other bucket's data — so the bytes actually read
// stay a tiny, bounded fraction of the whole sidecar file, regardless
// of how many OTHER buckets/entries the file holds.
func TestReadBucketByKey_ReadsOnlyTargetBucket(t *testing.T) {
	const bucketCount = 5000
	idx, keys := buildManyBucketIndexFixture(bucketCount, 3000)

	var buf bytes.Buffer
	if err := WriteIndex(&buf, idx); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}
	full := buf.Bytes()

	meta, err := ReadIndexMeta(bytes.NewReader(full))
	if err != nil {
		t.Fatalf("ReadIndexMeta: %v", err)
	}

	targetKey := keys[len(keys)/2]
	bi := BucketIndex(targetKey, meta.BucketCount)
	targetBucket := idx.Buckets[bi]

	// Exact expected byte count for this one bucket's self-delimited
	// data: u32 entry_count + per entry (u16 key_len + key + u32
	// row_id_count + 8*row_id_count).
	wantBucketBytes := 4
	for _, e := range targetBucket.Entries {
		wantBucketBytes += 2 + len(e.Key) + 4 + 8*len(e.RowIDs)
	}
	wantTotalBytes := 8 /* one offset table entry */ + wantBucketBytes

	counting := &countingReadSeeker{r: bytes.NewReader(full)}
	got, err := ReadBucketByKey(counting, meta, targetKey)
	if err != nil {
		t.Fatalf("ReadBucketByKey: %v", err)
	}
	if !bucketsDeepEqual(*got, targetBucket) {
		t.Fatalf("got %+v, want %+v", got, targetBucket)
	}

	if counting.bytesRead != wantTotalBytes {
		t.Errorf("bytesRead = %d, want exactly %d (one offset entry + one bucket)", counting.bytesRead, wantTotalBytes)
	}

	// Whole-file comparison: the fixture's full serialized size must be
	// dramatically larger than what a single-bucket seek read — proving
	// this is genuinely a small fraction of the file, not an accident
	// of a tiny fixture.
	if len(full) < wantTotalBytes*50 {
		t.Fatalf("fixture too small to prove seek-only behavior meaningfully: file=%d bytes, single-bucket-read=%d bytes", len(full), wantTotalBytes)
	}
	if counting.bytesRead*20 > len(full) {
		t.Errorf("bytesRead (%d) is not a small fraction of the whole file (%d) — ReadBucketByKey may be over-reading", counting.bytesRead, len(full))
	}
}

// TestReadBucketByKey_CorruptOrTruncatedOffset covers the negative
// space: a tampered or truncated bucket-offset table entry must fail
// with a coded error, never a silent misread or a panic.
func TestReadBucketByKey_CorruptOrTruncatedOffset(t *testing.T) {
	idx, keys := buildManyBucketIndexFixture(37, 20)
	var buf bytes.Buffer
	if err := WriteIndex(&buf, idx); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}
	full := buf.Bytes()

	meta, err := ReadIndexMeta(bytes.NewReader(full))
	if err != nil {
		t.Fatalf("ReadIndexMeta: %v", err)
	}

	targetKey := keys[0]
	bi := BucketIndex(targetKey, meta.BucketCount)
	offsetPos := meta.OffsetTableStart + int64(bi)*8

	t.Run("offset_overflows_int64", func(t *testing.T) {
		corrupted := append([]byte(nil), full...)
		binary.LittleEndian.PutUint64(corrupted[offsetPos:offsetPos+8], uint64(math.MaxInt64)+1)

		_, err := ReadBucketByKey(bytes.NewReader(corrupted), meta, targetKey)
		if err == nil {
			t.Fatal("expected error for an offset overflowing a signed 64-bit byte offset")
		}
		if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("expected ENCODING_INVALID, got: %v", err)
		}
	})

	t.Run("offset_points_past_eof", func(t *testing.T) {
		corrupted := append([]byte(nil), full...)
		binary.LittleEndian.PutUint64(corrupted[offsetPos:offsetPos+8], uint64(len(full)*100))

		_, err := ReadBucketByKey(bytes.NewReader(corrupted), meta, targetKey)
		if err == nil {
			t.Fatal("expected error for an offset pointing past EOF")
		}
		if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("expected ENCODING_INVALID, got: %v", err)
		}
	})

	t.Run("offset_table_entry_truncated", func(t *testing.T) {
		truncated := full[:offsetPos+4] // cut mid-way through the target u64 offset entry
		meta2, err := ReadIndexMeta(bytes.NewReader(truncated))
		if err != nil {
			t.Fatalf("ReadIndexMeta on truncated-body prefix: %v", err)
		}
		_, err = ReadBucketByKey(bytes.NewReader(truncated), meta2, targetKey)
		if err == nil {
			t.Fatal("expected error reading a truncated bucket-offset table entry")
		}
		if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("expected ENCODING_INVALID, got: %v", err)
		}
	})

	t.Run("nil_meta", func(t *testing.T) {
		_, err := ReadBucketByKey(bytes.NewReader(full), nil, targetKey)
		if err == nil {
			t.Fatal("expected error for nil meta")
		}
		if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("expected ENCODING_INVALID, got: %v", err)
		}
	})
}

func TestReadIndexMetaFile_AferoRoundTrip(t *testing.T) {
	cfg := fs.NewMemMap()
	idx, _ := buildManyBucketIndexFixture(11, 5)

	const path = "meta.pulse.idx"
	if err := WriteIndexFile(cfg.Fs(), path, idx); err != nil {
		t.Fatalf("WriteIndexFile: %v", err)
	}

	meta, err := ReadIndexMetaFile(cfg.Fs(), path)
	if err != nil {
		t.Fatalf("ReadIndexMetaFile: %v", err)
	}
	if meta.Fingerprint != idx.Fingerprint {
		t.Errorf("Fingerprint mismatch")
	}
	if meta.SourceSize != idx.SourceSize || meta.SourceModTime != idx.SourceModTime {
		t.Errorf("source-stat mismatch: got size=%d mtime=%d, want size=%d mtime=%d",
			meta.SourceSize, meta.SourceModTime, idx.SourceSize, idx.SourceModTime)
	}
	if meta.BucketCount != uint32(len(idx.Buckets)) {
		t.Errorf("BucketCount = %d, want %d", meta.BucketCount, len(idx.Buckets))
	}
}

func TestReadIndexMetaFile_MissingFile(t *testing.T) {
	cfg := fs.NewMemMap()
	_, err := ReadIndexMetaFile(cfg.Fs(), "does-not-exist.idx")
	if err == nil {
		t.Fatal("expected error reading a missing sidecar index file")
	}
	if !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("expected ENCODING_IO, got: %v", err)
	}
}

// TestIndex_V3RoundTrip_ByteStable_ManyBuckets extends
// TestIndex_RoundTrip's byte-stability proof to a much larger,
// realistically-shaped many-bucket fixture — belt-and-suspenders on top
// of the smaller hand-written cases already covered there.
func TestIndex_V3RoundTrip_ByteStable_ManyBuckets(t *testing.T) {
	idx, _ := buildManyBucketIndexFixture(101, 250)

	var buf bytes.Buffer
	if err := WriteIndex(&buf, idx); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}

	got, err := ReadIndex(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}

	var buf2 bytes.Buffer
	if err := WriteIndex(&buf2, got); err != nil {
		t.Fatalf("WriteIndex (re-encode): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), buf2.Bytes()) {
		t.Fatal("v3 round-trip not byte-stable on a many-bucket fixture")
	}

	normalizeIndex(idx)
	normalizeIndex(got)
	if !reflect.DeepEqual(idx, got) {
		t.Fatal("v3 round-trip mismatch on a many-bucket fixture")
	}
}
