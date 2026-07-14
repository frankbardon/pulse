package encoding

import (
	"bytes"
	"io"
	"math/rand"
	"reflect"
	"testing"
)

// reusableTestRecord is a minimal encoding.ReusableRecord implementation
// used to exercise ReadRecordReused in isolation (encoding_test may not
// import processing/ — that would form an import cycle). Its null handling
// mirrors processing.Record's contract *plus* the ReadRecordWithWide
// reference semantics for wide-on-null (delete the wide entry when a field
// is marked null) so the differential assertion against ReadRecordWithWide
// is exact.
type reusableTestRecord struct {
	values map[string]float64
	nulls  map[string]bool
	wide   map[string]any
}

func newReusableTestRecord() *reusableTestRecord {
	return &reusableTestRecord{
		values: make(map[string]float64),
		nulls:  make(map[string]bool),
		wide:   make(map[string]any),
	}
}

func (r *reusableTestRecord) SetNumeric(name string, value float64) {
	r.values[name] = value
}

func (r *reusableTestRecord) SetNullField(name string) {
	r.nulls[name] = true
	// Mirror ReadRecordWithWide: a null field carries no typed wide value.
	delete(r.wide, name)
}

func (r *reusableTestRecord) SetWideField(name string, v any) {
	r.wide[name] = v
}

func (r *reusableTestRecord) ClearForRow() {
	clear(r.values)
	clear(r.nulls)
	clear(r.wide)
}

// compareReusedAgainstReference decodes raw with the full-decode reference
// (ReadRecordWithWide) and with the buffer-once ReadRecordReused, asserting
// the two produce byte-equal values / nulls / wide maps.
func compareReusedAgainstReference(t *testing.T, schema *Schema, raw []byte, label string) {
	t.Helper()

	// Reference: full per-field decode into caller maps.
	refReader := NewRecordReader(bytes.NewReader(raw), schema)
	refValues := make(map[string]float64)
	refNulls := make(map[string]bool)
	refWide := make(map[string]any)
	if err := refReader.ReadRecordWithWide(refValues, refNulls, refWide); err != nil {
		t.Fatalf("%s: ReadRecordWithWide: %v", label, err)
	}

	// Subject: buffer-once reuse path.
	subjReader := NewRecordReader(bytes.NewReader(raw), schema)
	rec := newReusableTestRecord()
	if err := subjReader.ReadRecordReused(rec); err != nil {
		t.Fatalf("%s: ReadRecordReused: %v", label, err)
	}

	if !reflect.DeepEqual(refValues, rec.values) {
		t.Errorf("%s values mismatch:\n  reference=%v\n  reused=%v", label, refValues, rec.values)
	}
	if !reflect.DeepEqual(refNulls, rec.nulls) {
		t.Errorf("%s nulls mismatch:\n  reference=%v\n  reused=%v", label, refNulls, rec.nulls)
	}
	if !reflect.DeepEqual(refWide, rec.wide) {
		t.Errorf("%s wide mismatch:\n  reference=%v\n  reused=%v", label, refWide, rec.wide)
	}
}

// TestReadRecordReused_MatchesReferencePerType runs the per-type fixture
// corpus (u4, packed_bool, u8/16/32/64, f32/f64, date, decimal128,
// categorical_*, set_*, nullable variants) through both decode paths and
// asserts byte-equal output for every record.
func TestReadRecordReused_MatchesReferencePerType(t *testing.T) {
	for _, f := range perTypeFixtures() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			records := generatePerTypeRecords(t, f, 0x2EC0DE00+int64(len(f.name)))
			for ri, raw := range records {
				compareReusedAgainstReference(t, f.schema, raw, f.name+"/rec="+itoa(ri))
				if t.Failed() {
					return
				}
			}
		})
	}
}

// TestReadRecordReused_MatchesReferenceBigMixed runs the 26-field mixed
// schema (every field type, nullable variants, rotating null patterns,
// bit-packed runs, decimal128, every set width) through both paths.
func TestReadRecordReused_MatchesReferenceBigMixed(t *testing.T) {
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0xB16B0FFE)
	for ri, raw := range records {
		compareReusedAgainstReference(t, schema, raw, "big/rec="+itoa(ri))
		if t.Failed() {
			return
		}
	}
}

// TestReadRecordReused_EOFOnExhaustion asserts that the reuse path returns
// io.EOF once the stream is drained, and that a partial trailing record
// surfaces as io.EOF as well (io.ReadFull maps a nonzero short read to
// io.ErrUnexpectedEOF, which mapEOF normalizes to io.EOF).
func TestReadRecordReused_EOFOnExhaustion(t *testing.T) {
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0xE0FE0F00)
	const n = 5
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		buf.Write(records[i])
	}

	rr := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	rec := newReusableTestRecord()
	for i := 0; i < n; i++ {
		if err := rr.ReadRecordReused(rec); err != nil {
			t.Fatalf("record %d: unexpected error: %v", i, err)
		}
	}
	if err := rr.ReadRecordReused(rec); err != io.EOF {
		t.Fatalf("expected io.EOF after %d records, got %v", n, err)
	}

	// Partial trailing record: append a truncated record.
	stride := schema.RecordByteSize()
	partial := append([]byte{}, buf.Bytes()...)
	partial = append(partial, records[0][:stride/2]...)
	rr2 := NewRecordReader(bytes.NewReader(partial), schema)
	for i := 0; i < n; i++ {
		if err := rr2.ReadRecordReused(rec); err != nil {
			t.Fatalf("record %d (partial run): unexpected error: %v", i, err)
		}
	}
	if err := rr2.ReadRecordReused(rec); err != io.EOF {
		t.Fatalf("expected io.EOF on partial trailing record, got %v", err)
	}
}

// --- Benchmarks -------------------------------------------------------------

// narrowBenchSchema is a small mixed schema representative of typical
// cohorts: a handful of scalars, one bit-packed run, one nullable field.
func narrowBenchSchema() *Schema {
	return &Schema{Fields: []Field{
		{Name: "id", Type: FieldTypeU32},
		{Name: "amt", Type: FieldTypeU64},
		{Name: "rate", Type: FieldTypeF64, Nullable: true},
		{Name: "flag", Type: FieldTypePackedBool, BitPosition: 0},
		{Name: "cat", Type: FieldTypeCategoricalU16, Dictionary: buildCategoricalDict(1000)},
	}}
}

// wideBenchSchema is a 512-field u32 schema — the pathology E1-S1 targets:
// one io.ReadFull per field dominates via runtime.memmove on wide records.
func wideBenchSchema() *Schema {
	fields := make([]Field, 0, 512)
	for i := 0; i < 512; i++ {
		fields = append(fields, Field{Name: "f" + itoa(i), Type: FieldTypeU32})
	}
	return &Schema{Fields: fields}
}

// buildBenchRecords concatenates `count` synthetic records for a schema
// into one contiguous byte buffer.
func buildBenchRecords(b *testing.B, schema *Schema, count int, seed int64) []byte {
	b.Helper()
	r := rand.New(rand.NewSource(seed))
	var buf bytes.Buffer
	for i := 0; i < count; i++ {
		vals := map[string]any{}
		for fi := range schema.Fields {
			f := &schema.Fields[fi]
			switch f.Type {
			case FieldTypePackedBool:
				vals[f.Name] = r.Intn(2) == 1
			case FieldTypeU4:
				vals[f.Name] = uint8(r.Intn(16))
			case FieldTypeF64:
				vals[f.Name] = r.Uint64()
			case FieldTypeCategoricalU16:
				vals[f.Name] = uint64(r.Intn(1000))
			default:
				vals[f.Name] = uint64(r.Uint32())
			}
		}
		buf.Write(encodeBenchRecord(b, schema, vals))
	}
	return buf.Bytes()
}

// encodeBenchRecord mirrors encodeRecord but without *testing.T (benches
// use *testing.B). No nullable fields carry nulls in the bench corpus.
func encodeBenchRecord(b *testing.B, schema *Schema, vals map[string]any) []byte {
	b.Helper()
	var buf bytes.Buffer
	for i := range schema.Fields {
		f := &schema.Fields[i]
		switch f.Type {
		case FieldTypePackedBool:
			bv, _ := vals[f.Name].(bool)
			if err := WriteBit(&buf, uint(f.BitPosition), bv); err != nil {
				b.Fatalf("write %s: %v", f.Name, err)
			}
		case FieldTypeU4:
			v, _ := vals[f.Name].(uint8)
			if err := WriteNibble(&buf, f.BitPosition > 0, v); err != nil {
				b.Fatalf("write %s: %v", f.Name, err)
			}
		default:
			raw, _ := vals[f.Name].(uint64)
			if err := WriteFieldValue(&buf, f.Type, raw); err != nil {
				b.Fatalf("write %s: %v", f.Name, err)
			}
		}
	}
	if bmSize := schema.BitmapByteSize(); bmSize > 0 {
		bm := make([]byte, bmSize)
		if err := WriteBitmap(&buf, bm); err != nil {
			b.Fatalf("write bitmap: %v", err)
		}
	}
	return buf.Bytes()
}

func benchReadRecordReused(b *testing.B, schema *Schema) {
	const records = 2000
	raw := buildBenchRecords(b, schema, records, 0xBEEF)
	rec := newReusableTestRecord()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := NewRecordReader(bytes.NewReader(raw), schema)
		for {
			if err := rr.ReadRecordReused(rec); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatalf("ReadRecordReused: %v", err)
			}
		}
	}
}

func BenchmarkReadRecordReused_Narrow(b *testing.B) {
	benchReadRecordReused(b, narrowBenchSchema())
}

func BenchmarkReadRecordReused_Wide(b *testing.B) {
	benchReadRecordReused(b, wideBenchSchema())
}
