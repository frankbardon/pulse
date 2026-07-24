package encoding

import (
	"bytes"
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
