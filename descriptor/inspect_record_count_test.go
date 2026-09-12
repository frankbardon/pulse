package descriptor

import (
	"bytes"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// buildCohortBytes returns the bytes of a single-file .pulse cohort
// carrying exactly nRecord records under schema. Records are written
// through the real encoding primitives — WriteFieldValue for
// fixed-width fields, WriteNibble/WriteBit for bit-packed ones (each
// consumes a whole byte on-wire), and a trailing WriteBitmap when the
// schema declares any nullable field — so the fixture's stride is the
// encoder's, never this test's arithmetic.
func buildCohortBytes(t *testing.T, schema *encoding.Schema, nRecord int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	headerSchemaLen := buf.Len()

	for r := 0; r < nRecord; r++ {
		for i := range schema.Fields {
			f := schema.Fields[i]
			switch f.Type {
			case encoding.FieldTypeU4:
				if err := encoding.WriteNibble(&buf, f.BitPosition >= 4, uint8(r%16)); err != nil {
					t.Fatalf("WriteNibble(%s): %v", f.Name, err)
				}
			case encoding.FieldTypePackedBool:
				if err := encoding.WriteBit(&buf, uint(f.BitPosition), r%2 == 0); err != nil {
					t.Fatalf("WriteBit(%s): %v", f.Name, err)
				}
			default:
				if err := encoding.WriteFieldValue(&buf, f.Type, uint64(r)); err != nil {
					t.Fatalf("WriteFieldValue(%s): %v", f.Name, err)
				}
			}
		}
		if n := schema.BitmapByteSize(); n > 0 {
			if err := encoding.WriteBitmap(&buf, make([]byte, n)); err != nil {
				t.Fatalf("WriteBitmap: %v", err)
			}
		}
	}

	// Sanity-check the fixture against the schema's own declared stride
	// so a mis-built fixture fails here rather than as a count mismatch.
	if want := headerSchemaLen + nRecord*schema.RecordByteSize(); buf.Len() != want {
		t.Fatalf("fixture length = %d, want %d (stride %d)", buf.Len(), want, schema.RecordByteSize())
	}
	return buf.Bytes()
}

func inspectRecordCount(t *testing.T, data []byte) (*InspectResult, *Envelope) {
	t.Helper()
	env := InspectFromBytes(data, nil)
	result, ok := env.Data.(*InspectResult)
	if !ok {
		t.Fatalf("Data is not *InspectResult: %T", env.Data)
	}
	return result, env
}

// TestInspect_RecordCountSingleFileExact pins the exact record count
// for single-file cohorts across the stride shapes that can silently
// go wrong: fixed-width only, a null-bitmap-carrying schema (the
// trailing ceil(field_count/8) bytes ride the stride), and an
// all-bit-packed schema (u4 / packed_bool report ByteSize() == 0 but
// occupy one whole byte each on-wire).
func TestInspect_RecordCountSingleFileExact(t *testing.T) {
	fixedWidth := func(t *testing.T) *encoding.Schema {
		t.Helper()
		return &encoding.Schema{Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU64, Description: "Unique record identifier value"},
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, Description: "Primary color category", Dictionary: makeDictionary(t, "red", "green")},
		}}
	}
	nullable := func(t *testing.T) *encoding.Schema {
		t.Helper()
		return &encoding.Schema{Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, Description: "Unique record identifier value"},
			{Name: "score", Type: encoding.FieldTypeF64, Nullable: true, Description: "Student test score value"},
			{Name: "age", Type: encoding.FieldTypeU8, Description: "Age of the participant in years"},
		}}
	}
	bitPacked := func(t *testing.T) *encoding.Schema {
		t.Helper()
		return &encoding.Schema{Fields: []encoding.Field{
			{Name: "tier", Type: encoding.FieldTypeU4, Description: "Loyalty tier bucket index"},
			{Name: "active", Type: encoding.FieldTypePackedBool, Description: "Whether the account is active"},
			{Name: "band", Type: encoding.FieldTypeU4, BitPosition: 4, Description: "Age band bucket index"},
		}}
	}

	cases := []struct {
		name    string
		schema  func(*testing.T) *encoding.Schema
		records int
	}{
		{"fixed_width_7_records", fixedWidth, 7},
		{"fixed_width_zero_records", fixedWidth, 0},
		// 23 records is deliberate: with the bitmap wrongly excluded from
		// the stride the floor count diverges (322/13 = 24), so the count
		// itself catches the mistake and not only the trailing-byte warning.
		{"null_bitmap_23_records", nullable, 23},
		{"null_bitmap_zero_records", nullable, 0},
		{"bit_packed_only_11_records", bitPacked, 11},
		{"bit_packed_only_zero_records", bitPacked, 0},
		{"fixed_width_386324_records", fixedWidth, 386324},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.schema(t)
			data := buildCohortBytes(t, schema, tc.records)
			result, env := inspectRecordCount(t, data)
			if len(env.Errors) != 0 {
				t.Fatalf("unexpected errors: %v", env.Errors)
			}
			if result.RecordCount != int64(tc.records) {
				t.Errorf("RecordCount = %d, want %d (stride %d)",
					result.RecordCount, tc.records, schema.RecordByteSize())
			}
			for _, w := range env.Warnings {
				t.Errorf("unexpected warning: %s %s %v", w.Code, w.Message, w.Details)
			}
			if len(result.Shards) != 0 {
				t.Errorf("Shards = %v, want empty for a single-file cohort", result.Shards)
			}
		})
	}
}

// TestInspect_RecordCountZeroIsDistinguishable is the guard the
// original defect slipped past: an unpopulated RecordCount and a
// genuinely empty cohort both report 0, so the assertion has to pin a
// non-zero cohort and an empty one built from the SAME schema.
func TestInspect_RecordCountZeroIsDistinguishable(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Unique record identifier value"},
	}}

	empty, _ := inspectRecordCount(t, buildCohortBytes(t, schema, 0))
	if empty.RecordCount != 0 {
		t.Fatalf("empty cohort RecordCount = %d, want 0", empty.RecordCount)
	}
	one, _ := inspectRecordCount(t, buildCohortBytes(t, schema, 1))
	if one.RecordCount != 1 {
		t.Fatalf("one-record cohort RecordCount = %d, want 1", one.RecordCount)
	}
	if empty.RecordCount == one.RecordCount {
		t.Fatal("empty and one-record cohorts report the same count")
	}
}

// TestInspect_RecordCountTruncatedPayloadWarns covers a payload length
// that is not a whole multiple of the record stride (truncated or
// corrupt tail): the count is the floor and the envelope carries an
// ENCODING_INVALID warning naming the trailing bytes, rather than the
// division happening silently.
func TestInspect_RecordCountTruncatedPayloadWarns(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Unique record identifier value"},
		{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
	}}
	stride := schema.RecordByteSize()
	data := buildCohortBytes(t, schema, 4)
	data = append(data, make([]byte, stride-1)...) // partial trailing record

	result, env := inspectRecordCount(t, data)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", env.Errors)
	}
	if result.RecordCount != 4 {
		t.Errorf("RecordCount = %d, want 4 (floor over stride %d)", result.RecordCount, stride)
	}
	if len(env.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one", env.Warnings)
	}
	w := env.Warnings[0]
	if w.Code != string(errors.ENCODING_INVALID) {
		t.Errorf("warning code = %q, want %q", w.Code, string(errors.ENCODING_INVALID))
	}
	if got := w.Details["trailing_bytes"]; got != int64(stride-1) {
		t.Errorf("details[trailing_bytes] = %v, want %d", got, stride-1)
	}
	if got := w.Details["record_stride"]; got != stride {
		t.Errorf("details[record_stride] = %v, want %d", got, stride)
	}
}

// TestInspect_RecordCountAnchorShardPayload pins the
// archive.pulse#shard.pulse anchor form. pulse.Inspect extracts the
// named shard's standalone payload and hands those bytes to
// InspectFromBytes, so the anchor resolves down the single-file path
// and must report that one shard's own count — not the archive
// aggregate, which the archive path still reports for the whole file.
func TestInspect_RecordCountAnchorShardPayload(t *testing.T) {
	schema := twoShardInspectSchema(t)
	shards := []struct {
		Name    string
		NRecord int
	}{
		{"20190101.pulse", 3},
		{"20190201.pulse", 5},
	}

	anchorBytes := writeShardPayload(t, schema, shards[1].NRecord)
	anchored, env := inspectRecordCount(t, anchorBytes)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", env.Errors)
	}
	if anchored.RecordCount != int64(shards[1].NRecord) {
		t.Errorf("anchored RecordCount = %d, want %d", anchored.RecordCount, shards[1].NRecord)
	}

	whole, wenv := inspectRecordCount(t, buildShardArchiveBytes(t, schema, shards))
	if len(wenv.Errors) != 0 {
		t.Fatalf("unexpected archive errors: %v", wenv.Errors)
	}
	if whole.RecordCount != 8 {
		t.Errorf("archive RecordCount = %d, want 8 (3+5)", whole.RecordCount)
	}
	if len(whole.Shards) != 2 {
		t.Errorf("archive Shards = %d, want 2", len(whole.Shards))
	}
}

// TestInspect_LeavesStreamAtPayloadStart keeps the header-only
// contract honest: deriving the record count is a seek to EOF and
// back, so the stream must be left exactly where reading the header +
// schema finished and no record byte may be consumed.
func TestInspect_LeavesStreamAtPayloadStart(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Unique record identifier value"},
	}}
	headerSchema := buildCohortBytes(t, schema, 0)
	data := buildCohortBytes(t, schema, 6)

	rs := bytes.NewReader(data)
	env := Inspect(rs, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", env.Errors)
	}
	result := env.Data.(*InspectResult)
	if result.RecordCount != 6 {
		t.Errorf("RecordCount = %d, want 6", result.RecordCount)
	}
	pos, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if pos != int64(len(headerSchema)) {
		t.Errorf("stream position = %d, want %d (end of header + schema)", pos, len(headerSchema))
	}
}
