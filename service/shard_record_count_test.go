package service

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// strideExactPayload builds a single-file `.pulse` payload holding
// exactly n records under schema: header + schema block + n *
// Schema.RecordByteSize() bytes of record data. The record bytes are
// zero-filled on purpose — every consumer under test counts BYTES, so
// the content is irrelevant and the stride is the whole subject.
func strideExactPayload(t *testing.T, schema *encoding.Schema, stride, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(make([]byte, stride*n))
	return buf.Bytes()
}

// oneShardArchive wraps payload as the single shard of an otherwise
// minimal archive so encoding.Archive.PeekShardRecordCount — the READ
// path's independent derivation — can be asked the same question.
func oneShardArchive(t *testing.T, schema *encoding.Schema, payload []byte, total uint64) []byte {
	t.Helper()
	var doc bytes.Buffer
	if err := encoding.WriteSchemaDoc(&doc, schema, total, 1); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("zip write(%q): %v", name, err)
		}
	}
	write(encoding.ReservedSchemaName, doc.Bytes())
	write("s.pulse", payload)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// recordCountStrideCases covers the two layouts the shard-admin
// aggregate counter used to get wrong: a schema carrying a trailing
// per-record null bitmap, and a schema holding bit-packed fields whose
// FieldType.ByteSize() is 0 but which still consume a whole byte each.
func recordCountStrideCases() []struct {
	name string
	// stride is written out by hand, not derived from the schema. The
	// arithmetic under test reads its stride from encoding; a fixture
	// that asked encoding for the same number would agree with any
	// answer encoding gave, including a wrong one.
	stride int
	schema *encoding.Schema
} {
	return []struct {
		name   string
		stride int
		schema *encoding.Schema
	}{
		{
			name: "plain_fixed_width",
			// u32(4) + f64(8), no nullable field, no bitmap.
			stride: 12,
			schema: &encoding.Schema{Fields: []encoding.Field{
				{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
				{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
			}},
		},
		{
			name: "nullable_carries_bitmap",
			// u32(4) + f64(8) + ceil(2 fields / 8) = 1 bitmap byte.
			stride: 13,
			schema: &encoding.Schema{Fields: []encoding.Field{
				{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
				{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1, Nullable: true},
			}},
		},
		{
			name: "bit_packed_zero_bytesize",
			// packed_bool(1) + u4(1) + u32(4); the two bit-packed
			// fields report ByteSize()==0 yet consume a byte each.
			stride: 6,
			schema: &encoding.Schema{Fields: []encoding.Field{
				{Name: "flag", Type: encoding.FieldTypePackedBool, ByteOffset: 0, CsvColumnIdx: 0},
				{Name: "grade", Type: encoding.FieldTypeU4, ByteOffset: 1, CsvColumnIdx: 1},
				{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 2, CsvColumnIdx: 2},
			}},
		},
		{
			name: "nullable_and_bit_packed",
			// packed_bool(1) + u32(4) + 1 bitmap byte.
			stride: 6,
			schema: &encoding.Schema{Fields: []encoding.Field{
				{Name: "flag", Type: encoding.FieldTypePackedBool, ByteOffset: 0, CsvColumnIdx: 0, Nullable: true},
				{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 1, CsvColumnIdx: 1},
			}},
		},
	}
}

// The shard-admin aggregate counter must derive a shard's record count
// from the SAME stride the rest of the codebase uses. It used to sum
// FieldType.ByteSize() itself, which drops the trailing null bitmap and
// scores every bit-packed field as zero bytes wide — so any nullable or
// bit-packed cohort got a silently inflated aggregate_record_count
// baked into `_schema.pulse`.
func TestRecordCountFromBytes_MatchesCanonicalStride(t *testing.T) {
	for _, tc := range recordCountStrideCases() {
		t.Run(tc.name, func(t *testing.T) {
			const want = 19
			if got := tc.schema.RecordByteSize(); got != tc.stride {
				t.Fatalf("Schema.RecordByteSize() = %d, want the hand-computed %d",
					got, tc.stride)
			}
			payload := strideExactPayload(t, tc.schema, tc.stride, want)

			got, err := recordCountFromBytes(payload, tc.schema)
			if err != nil {
				t.Fatalf("recordCountFromBytes: %v", err)
			}
			if got != want {
				t.Errorf("recordCountFromBytes = %d, want %d (stride %d)",
					got, want, tc.stride)
			}
		})
	}
}

// Canonical-vs-local parity: the shard-admin WRITE path (which bakes
// aggregate_record_count into `_schema.pulse`) and the archive READ
// path (encoding.Archive.PeekShardRecordCount, which every open and
// count arm consults) must derive the same number from the same bytes.
// A divergence here means a cohort reports one record count when
// created and another when opened.
func TestRecordCountFromBytes_ParityWithPeekShardRecordCount(t *testing.T) {
	for _, tc := range recordCountStrideCases() {
		t.Run(tc.name, func(t *testing.T) {
			const want = 17
			payload := strideExactPayload(t, tc.schema, tc.stride, want)
			archBytes := oneShardArchive(t, tc.schema, payload, want)

			arch, err := encoding.OpenArchive(bytes.NewReader(archBytes), int64(len(archBytes)))
			if err != nil {
				t.Fatalf("OpenArchive: %v", err)
			}
			peeked, err := arch.PeekShardRecordCount("s.pulse")
			if err != nil {
				t.Fatalf("PeekShardRecordCount: %v", err)
			}
			local, err := recordCountFromBytes(payload, tc.schema)
			if err != nil {
				t.Fatalf("recordCountFromBytes: %v", err)
			}
			if local != peeked {
				t.Errorf("write-path count %d != read-path count %d", local, peeked)
			}
			if peeked != want {
				t.Errorf("PeekShardRecordCount = %d, want %d", peeked, want)
			}
		})
	}
}

// A truncated tail must FLOOR silently rather than error. The callers
// (CreateShardArchive, AddShard, RemoveShard, CompactShardArchive) hand
// back only an error, and the read path floors silently too; erroring
// here would make compacting or removing a shard fail on an archive
// that still opens and still processes. `pulse shard verify` is the
// diagnostic arm that owns reporting a short tail.
func TestRecordCountFromBytes_TruncatedTailFloorsSilently(t *testing.T) {
	tc := recordCountStrideCases()[1] // nullable, bitmap-bearing
	schema := tc.schema
	payload := strideExactPayload(t, schema, tc.stride, 4)
	truncated := payload[:len(payload)-3]

	got, err := recordCountFromBytes(truncated, schema)
	if err != nil {
		t.Fatalf("recordCountFromBytes on a truncated tail errored: %v", err)
	}
	if got != 3 {
		t.Errorf("recordCountFromBytes = %d, want 3 whole records", got)
	}
}
