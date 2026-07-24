package encoding

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// buildRecordAtFixture assembles a full single-file .pulse payload
// (header + schema + N records) exercising a scalar field, a bit-packed
// field, a nullable field (bitmap), and a categorical (dictionary)
// field — the mix of on-wire shapes RecordLocator's offset math must
// stay correct across. Returns the raw payload bytes, the schema, and
// the per-record logical values/nulls used to build each row (indexed
// by record number) so tests can assert decoded output against a known
// source of truth.
func buildRecordAtFixture(t *testing.T, n int) ([]byte, *Schema, []map[string]any, []map[string]bool) {
	t.Helper()

	dict := NewDictionary()
	if _, err := dict.Add("red"); err != nil {
		t.Fatalf("dict.Add red: %v", err)
	}
	if _, err := dict.Add("green"); err != nil {
		t.Fatalf("dict.Add green: %v", err)
	}
	if _, err := dict.Add("blue"); err != nil {
		t.Fatalf("dict.Add blue: %v", err)
	}

	schema := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32, ByteOffset: 0},
			{Name: "flag", Type: FieldTypePackedBool, BitPosition: 0},
			{Name: "score", Type: FieldTypeF64, Nullable: true},
			{Name: "color", Type: FieldTypeCategoricalU8, Dictionary: dict},
		},
	}

	var buf bytes.Buffer
	if err := WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	allVals := make([]map[string]any, n)
	allNulls := make([]map[string]bool, n)
	for i := 0; i < n; i++ {
		vals := map[string]any{
			"id":    uint64(1000 + i),
			"flag":  i%2 == 0,
			"score": math.Float64bits(float64(i) * 1.5),
			"color": uint64(i % 3),
		}
		nulls := map[string]bool{}
		// Every third record has a null score to exercise the bitmap
		// path across the index range, not just at the edges.
		if i%3 == 1 {
			nulls["score"] = true
		}
		allVals[i] = vals
		allNulls[i] = nulls

		raw := encodeRecord(t, schema, vals, nulls)
		if _, err := buf.Write(raw); err != nil {
			t.Fatalf("write record %d: %v", i, err)
		}
	}

	return buf.Bytes(), schema, allVals, allNulls
}

// sequentialDecode reads every record from 0 up to (and including)
// target via the ordinary sequential RecordReader, returning the
// target record's decoded maps. Used as the "known good" comparison
// baseline for O(1) reads — the acceptance criterion is byte/value
// equality against a sequential decode up to i.
func sequentialDecode(t *testing.T, payload []byte, schema *Schema, target int) (map[string]float64, map[string]bool, map[string]any) {
	t.Helper()
	r := bytes.NewReader(payload)
	if err := ReadHeader(r); err != nil {
		t.Fatalf("sequential ReadHeader: %v", err)
	}
	if _, err := ReadSchema(r); err != nil {
		t.Fatalf("sequential ReadSchema: %v", err)
	}
	rr := NewRecordReader(r, schema)
	var values map[string]float64
	var nulls map[string]bool
	var wide map[string]any
	for i := 0; i <= target; i++ {
		values = make(map[string]float64)
		nulls = make(map[string]bool)
		wide = make(map[string]any)
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			t.Fatalf("sequential decode record %d: %v", i, err)
		}
	}
	return values, nulls, wide
}

// TestRecordLocator_MatchesSequentialDecode is the primary acceptance
// test: reading record i via RecordLocator.ReadRecordAt (a single seek,
// no iteration) must byte/value-equal a sequential decode up to i, for
// the first, a middle, and the last record.
func TestRecordLocator_MatchesSequentialDecode(t *testing.T) {
	const n = 11
	payload, schema, _, _ := buildRecordAtFixture(t, n)

	tests := []struct {
		name string
		idx  uint64
	}{
		{"first", 0},
		{"middle", 5},
		{"last", uint64(n - 1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantValues, wantNulls, wantWide := sequentialDecode(t, payload, schema, int(tt.idx))

			r := bytes.NewReader(payload)
			loc, err := NewRecordLocator(r, schema)
			if err != nil {
				t.Fatalf("NewRecordLocator: %v", err)
			}
			if loc.TotalRecords != uint64(n) {
				t.Fatalf("TotalRecords = %d, want %d", loc.TotalRecords, n)
			}

			gotValues := make(map[string]float64)
			gotNulls := make(map[string]bool)
			gotWide := make(map[string]any)
			if err := loc.ReadRecordAt(r, tt.idx, gotValues, gotNulls, gotWide, nil, nil); err != nil {
				t.Fatalf("ReadRecordAt(%d): %v", tt.idx, err)
			}

			if !reflect.DeepEqual(gotValues, wantValues) {
				t.Errorf("values mismatch:\n  got=%v\n  want=%v", gotValues, wantValues)
			}
			if !reflect.DeepEqual(gotNulls, wantNulls) {
				t.Errorf("nulls mismatch:\n  got=%v\n  want=%v", gotNulls, wantNulls)
			}
			if !reflect.DeepEqual(gotWide, wantWide) {
				t.Errorf("wide mismatch:\n  got=%v\n  want=%v", gotWide, wantWide)
			}
		})
	}
}

// TestRecordLocator_ProjectedDecode verifies the projection variant:
// passing a *DecodePlan built from a retained subset decodes only
// those columns, and the values match the full decode's values for the
// retained subset exactly (the unretained columns are simply absent,
// never wrong).
func TestRecordLocator_ProjectedDecode(t *testing.T) {
	const n = 7
	payload, schema, _, _ := buildRecordAtFixture(t, n)

	retained := []string{"color"}
	plan, err := schema.BuildDecodePlan(retained)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	keep := keepFromNames(retained)

	tests := []struct {
		name string
		idx  uint64
	}{
		{"first", 0},
		{"middle", 3},
		{"last", uint64(n - 1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Full decode (unprojected) at the same index as the oracle
			// for the retained field's value.
			fullValues, _, _ := sequentialDecode(t, payload, schema, int(tt.idx))

			r := bytes.NewReader(payload)
			loc, err := NewRecordLocator(r, schema)
			if err != nil {
				t.Fatalf("NewRecordLocator: %v", err)
			}

			values := make(map[string]float64)
			nulls := make(map[string]bool)
			wide := make(map[string]any)
			if err := loc.ReadRecordAt(r, tt.idx, values, nulls, wide, keep, plan); err != nil {
				t.Fatalf("ReadRecordAt(%d) projected: %v", tt.idx, err)
			}

			if len(values) != 1 {
				t.Fatalf("projected decode should retain exactly 1 field, got %v", values)
			}
			got, ok := values["color"]
			if !ok {
				t.Fatalf("projected decode missing retained field 'color': %v", values)
			}
			if got != fullValues["color"] {
				t.Errorf("color = %v, want %v", got, fullValues["color"])
			}
			if _, ok := values["id"]; ok {
				t.Errorf("unprojected field 'id' leaked into projected decode: %v", values)
			}
			if _, ok := values["score"]; ok {
				t.Errorf("unprojected field 'score' leaked into projected decode: %v", values)
			}
		})
	}
}

// TestRecordLocator_OutOfRange asserts the out-of-range contract: an
// index at or beyond TotalRecords returns a coded ENCODING_INVALID
// error, never a panic and never a seek past the end of the payload.
func TestRecordLocator_OutOfRange(t *testing.T) {
	const n = 4
	payload, schema, _, _ := buildRecordAtFixture(t, n)

	r := bytes.NewReader(payload)
	loc, err := NewRecordLocator(r, schema)
	if err != nil {
		t.Fatalf("NewRecordLocator: %v", err)
	}

	tests := []struct {
		name string
		idx  uint64
	}{
		{"exactly_total_records", uint64(n)},
		{"far_beyond", uint64(n) + 1000},
		{"max_uint64", ^uint64(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := make(map[string]float64)
			nulls := make(map[string]bool)
			wide := make(map[string]any)

			func() {
				defer func() {
					if rec := recover(); rec != nil {
						t.Fatalf("ReadRecordAt(%d) panicked: %v", tt.idx, rec)
					}
				}()
				err := loc.ReadRecordAt(r, tt.idx, values, nulls, wide, nil, nil)
				if err == nil {
					t.Fatalf("ReadRecordAt(%d) = nil error, want ENCODING_INVALID", tt.idx)
				}
				if !errors.HasCode(err, errors.ENCODING_INVALID) {
					t.Errorf("ReadRecordAt(%d) error = %v, want ENCODING_INVALID", tt.idx, err)
				}
			}()
		})
	}
}

// TestRecordLocator_EmptyCohort verifies that a payload with zero
// records yields TotalRecords == 0 and every index — including 0 —
// is out of range.
func TestRecordLocator_EmptyCohort(t *testing.T) {
	payload, schema, _, _ := buildRecordAtFixture(t, 0)

	r := bytes.NewReader(payload)
	loc, err := NewRecordLocator(r, schema)
	if err != nil {
		t.Fatalf("NewRecordLocator: %v", err)
	}
	if loc.TotalRecords != 0 {
		t.Fatalf("TotalRecords = %d, want 0", loc.TotalRecords)
	}

	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	err = loc.ReadRecordAt(r, 0, values, nulls, wide, nil, nil)
	if err == nil {
		t.Fatal("ReadRecordAt(0) on empty cohort = nil error, want ENCODING_INVALID")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("ReadRecordAt(0) error = %v, want ENCODING_INVALID", err)
	}
}

// TestRecordLocator_Offset verifies the raw offset arithmetic directly
// against the recordRegionStart + i*stride formula the offset math
// precedent (service/parallel_decode.go) uses.
func TestRecordLocator_Offset(t *testing.T) {
	const n = 5
	payload, schema, _, _ := buildRecordAtFixture(t, n)

	r := bytes.NewReader(payload)
	loc, err := NewRecordLocator(r, schema)
	if err != nil {
		t.Fatalf("NewRecordLocator: %v", err)
	}

	stride := int64(schema.RecordByteSize())
	if loc.Stride != stride {
		t.Fatalf("loc.Stride = %d, want %d", loc.Stride, stride)
	}

	for i := uint64(0); i < n; i++ {
		want := loc.RecordRegionStart + int64(i)*stride
		got := loc.Offset(i)
		if got != want {
			t.Errorf("Offset(%d) = %d, want %d", i, got, want)
		}
	}
}

// TestRecordLocator_NilGuards exercises the defensive nil checks: a nil
// schema or nil reader must never panic and must surface a coded
// ENCODING_INVALID error.
func TestRecordLocator_NilGuards(t *testing.T) {
	payload, schema, _, _ := buildRecordAtFixture(t, 1)

	t.Run("nil_schema", func(t *testing.T) {
		r := bytes.NewReader(payload)
		_, err := NewRecordLocator(r, nil)
		if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("NewRecordLocator(nil schema) error = %v, want ENCODING_INVALID", err)
		}
	})

	t.Run("nil_reader", func(t *testing.T) {
		_, err := NewRecordLocator(nil, schema)
		if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("NewRecordLocator(nil reader) error = %v, want ENCODING_INVALID", err)
		}
	})

	t.Run("nil_reader_read_record_at", func(t *testing.T) {
		r := bytes.NewReader(payload)
		loc, err := NewRecordLocator(r, schema)
		if err != nil {
			t.Fatalf("NewRecordLocator: %v", err)
		}
		values := make(map[string]float64)
		nulls := make(map[string]bool)
		wide := make(map[string]any)
		err = loc.ReadRecordAt(nil, 0, values, nulls, wide, nil, nil)
		if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("ReadRecordAt(nil reader) error = %v, want ENCODING_INVALID", err)
		}
	})
}
