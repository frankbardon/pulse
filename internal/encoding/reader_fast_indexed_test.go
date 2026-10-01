package encoding

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
)

// indexedTestRecord is an IndexedReusableRecord that records every
// index-keyed write into name-keyed maps (resolving the position through
// its own schema) so its output can be compared with the name-keyed
// reference decoders. Null handling mirrors reusableTestRecord: a null
// field carries no typed wide value.
type indexedTestRecord struct {
	schema     *encoding.Schema
	values     map[string]float64
	nulls      map[string]bool
	wide       map[string]any
	indexCalls int
}

func newIndexedTestRecord(schema *encoding.Schema) *indexedTestRecord {
	return &indexedTestRecord{
		schema: schema,
		values: make(map[string]float64),
		nulls:  make(map[string]bool),
		wide:   make(map[string]any),
	}
}

func (r *indexedTestRecord) SetNumericAt(idx int, value float64) {
	r.indexCalls++
	r.values[r.schema.Fields[idx].Name] = value
}

func (r *indexedTestRecord) SetNullFieldAt(idx int) {
	r.indexCalls++
	name := r.schema.Fields[idx].Name
	r.nulls[name] = true
	delete(r.wide, name)
}

func (r *indexedTestRecord) SetWideFieldAt(idx int, v any) {
	r.indexCalls++
	r.wide[r.schema.Fields[idx].Name] = v
}

func (r *indexedTestRecord) ClearForRow() {
	clear(r.values)
	clear(r.nulls)
	clear(r.wide)
}

// indexOnlyRecord is the "index-keyed record" row of the table. The
// reader signature takes a ReusableRecord, so it must carry the
// name-keyed method set to be passable at all — but every one of those
// methods fails the test: the decoder must reach it only through the
// index-keyed interface.
type indexOnlyRecord struct {
	*indexedTestRecord
	t *testing.T
}

func (r *indexOnlyRecord) SetNumeric(name string, _ float64) {
	r.t.Errorf("name-keyed SetNumeric(%q) called on an index-keyed record", name)
}

func (r *indexOnlyRecord) SetNullField(name string) {
	r.t.Errorf("name-keyed SetNullField(%q) called on an index-keyed record", name)
}

func (r *indexOnlyRecord) SetWideField(name string, _ any) {
	r.t.Errorf("name-keyed SetWideField(%q) called on an index-keyed record", name)
}

// dualRecord implements BOTH interfaces with working methods writing to
// the same maps, and counts each family, so the test can assert which
// family the decoder picked (the index-keyed one must win).
type dualRecord struct {
	*indexedTestRecord
	nameCalls int
}

func (r *dualRecord) SetNumeric(name string, value float64) {
	r.nameCalls++
	r.values[name] = value
}

func (r *dualRecord) SetNullField(name string) {
	r.nameCalls++
	r.nulls[name] = true
	delete(r.wide, name)
}

func (r *dualRecord) SetWideField(name string, v any) {
	r.nameCalls++
	r.wide[name] = v
}

// recordKind builds one of the three table rows and returns the record
// to hand the reader plus a snapshot of its decoded maps and the two
// call counters (index-keyed, name-keyed).
type recordKind struct {
	name string
	// wantIndexPath is true when the decoder must drive the index-keyed
	// methods (and never the name-keyed ones).
	wantIndexPath bool
	build         func(t *testing.T, schema *encoding.Schema) (ReusableRecord, func() (vals map[string]float64, nulls map[string]bool, wide map[string]any, indexCalls, nameCalls int))
}

func recordKinds() []recordKind {
	return []recordKind{
		{
			name:          "index-keyed",
			wantIndexPath: true,
			build: func(t *testing.T, schema *encoding.Schema) (ReusableRecord, func() (map[string]float64, map[string]bool, map[string]any, int, int)) {
				r := &indexOnlyRecord{indexedTestRecord: newIndexedTestRecord(schema), t: t}
				return r, func() (map[string]float64, map[string]bool, map[string]any, int, int) {
					return r.values, r.nulls, r.wide, r.indexCalls, 0
				}
			},
		},
		{
			name:          "string-keyed-only",
			wantIndexPath: false,
			build: func(_ *testing.T, _ *encoding.Schema) (ReusableRecord, func() (map[string]float64, map[string]bool, map[string]any, int, int)) {
				r := &countingNameRecord{reusableTestRecord: newReusableTestRecord()}
				return r, func() (map[string]float64, map[string]bool, map[string]any, int, int) {
					return r.values, r.nulls, r.wide, 0, r.nameCalls
				}
			},
		},
		{
			name:          "both-index-wins",
			wantIndexPath: true,
			build: func(_ *testing.T, schema *encoding.Schema) (ReusableRecord, func() (map[string]float64, map[string]bool, map[string]any, int, int)) {
				r := &dualRecord{indexedTestRecord: newIndexedTestRecord(schema)}
				return r, func() (map[string]float64, map[string]bool, map[string]any, int, int) {
					return r.values, r.nulls, r.wide, r.indexCalls, r.nameCalls
				}
			},
		},
	}
}

// countingNameRecord is the "string-keyed-only" row: the pre-existing
// reusableTestRecord (name-keyed methods only) plus a call counter.
type countingNameRecord struct {
	*reusableTestRecord
	nameCalls int
}

func (r *countingNameRecord) SetNumeric(name string, value float64) {
	r.nameCalls++
	r.reusableTestRecord.SetNumeric(name, value)
}

func (r *countingNameRecord) SetNullField(name string) {
	r.nameCalls++
	r.reusableTestRecord.SetNullField(name)
}

func (r *countingNameRecord) SetWideField(name string, v any) {
	r.nameCalls++
	r.reusableTestRecord.SetWideField(name, v)
}

// Compile-time: the three rows satisfy exactly the interfaces they claim.
var (
	_ IndexedReusableRecord = (*indexOnlyRecord)(nil)
	_ IndexedReusableRecord = (*dualRecord)(nil)
	_ ReusableRecord        = (*countingNameRecord)(nil)
)

// indexedDecodeCorpus is every schema the index-keyed path must route
// identically: the per-type fixtures (u4, packed_bool, every numeric,
// date/datetime, decimal128, categorical_*, narrow set_*, nullable
// variants), the 29-field big mixed schema (bit-packed runs, decimal128,
// every set width including set_u128/set_u256, rotating null bitmap
// patterns) and the wide-set schema.
type indexedCorpusEntry struct {
	name    string
	schema  *encoding.Schema
	records [][]byte
	// subsets are retained sets for the plan path; nil ⇒ every 2^N subset.
	subsets [][]string
}

func indexedDecodeCorpus(t *testing.T) []indexedCorpusEntry {
	t.Helper()
	var out []indexedCorpusEntry
	for _, f := range perTypeFixtures() {
		out = append(out, indexedCorpusEntry{
			name:    f.name,
			schema:  f.schema,
			records: generatePerTypeRecords(t, f, 0x1D3C0000+int64(len(f.name))),
		})
	}
	big := bigMixedSchema()
	out = append(out, indexedCorpusEntry{
		name:    "big_mixed",
		schema:  big,
		records: generateBigSchemaRecords(t, big, 0x1D3CB16),
		subsets: [][]string{
			{"u8_a"},
			{"u4_hi"},
			{"pb_1"},
			{"u4_lo", "pb_1"},
			{"u32_a", "amount"},
			{"tags_u16", "tags_u128", "u16_b"},
			{"tags_u64", "tags_u8", "tags_u256"},
			{"u8_b", "u32_a", "date_a", "f64_a", "amount", "tags_u16", "tags_u64", "tags_u128", "u16_b"},
			{"u16_c"}, // last field only: every earlier position is skipped
		},
	})
	ws := wideSetDecodeSchema()
	wsRecords, _ := generateWideSetRecords(t, ws, 64, 0x1D3C5E7)
	out = append(out, indexedCorpusEntry{
		name:    "wide_set",
		schema:  ws,
		records: wsRecords,
		subsets: [][]string{
			{"tags128"},
			{"tags256", "tail"},
			{"flag", "score"},
			{"nib", "tags128", "score"},
		},
	})
	return out
}

func (e indexedCorpusEntry) retainedSets() [][]string {
	if e.subsets != nil {
		return e.subsets
	}
	n := uint(len(e.schema.Fields))
	var out [][]string
	for mask := uint64(0); mask < uint64(1)<<n; mask++ {
		out = append(out, subsetFromMask(e.schema, mask))
	}
	return out
}

// assertKindDecode checks one decoded row: maps equal the reference and
// the decoder picked the call family the row demands.
func assertKindDecode(t *testing.T, label string, kind recordKind,
	snap func() (map[string]float64, map[string]bool, map[string]any, int, int),
	wantVals map[string]float64, wantNulls map[string]bool, wantWide map[string]any) {
	t.Helper()
	vals, nulls, wide, indexCalls, nameCalls := snap()
	if !reflect.DeepEqual(wantVals, vals) {
		t.Errorf("%s values mismatch:\n  want=%v\n  got=%v", label, wantVals, vals)
	}
	if !reflect.DeepEqual(wantNulls, nulls) {
		t.Errorf("%s nulls mismatch:\n  want=%v\n  got=%v", label, wantNulls, nulls)
	}
	if !reflect.DeepEqual(wantWide, wide) {
		t.Errorf("%s wide mismatch:\n  want=%v\n  got=%v", label, wantWide, wide)
	}
	wroteSomething := len(wantVals) > 0
	if kind.wantIndexPath {
		if nameCalls != 0 {
			t.Errorf("%s: %d name-keyed calls, want 0 (index-keyed must win)", label, nameCalls)
		}
		if wroteSomething && indexCalls == 0 {
			t.Errorf("%s: index-keyed path never driven", label)
		}
	} else if wroteSomething && nameCalls == 0 {
		t.Errorf("%s: string-keyed-only record received no writes", label)
	}
}

// TestReadRecordReused_IndexedKinds is the full-stride half of the table:
// for every record kind × corpus schema × record, ReadRecordReused must
// produce exactly the ReadRecordWithWide reference maps, through the
// index-keyed methods whenever the record implements them.
func TestReadRecordReused_IndexedKinds(t *testing.T) {
	corpus := indexedDecodeCorpus(t)
	for _, kind := range recordKinds() {
		t.Run(kind.name, func(t *testing.T) {
			for _, e := range corpus {
				for ri, raw := range e.records {
					label := fmt.Sprintf("%s/%s/rec=%d", kind.name, e.name, ri)

					refRR := NewRecordReader(bytes.NewReader(raw), e.schema)
					wantVals := make(map[string]float64)
					wantNulls := make(map[string]bool)
					wantWide := make(map[string]any)
					if err := refRR.ReadRecordWithWide(wantVals, wantNulls, wantWide); err != nil {
						t.Fatalf("%s: reference: %v", label, err)
					}

					rec, snap := kind.build(t, e.schema)
					rr := NewRecordReader(bytes.NewReader(raw), e.schema)
					if err := rr.ReadRecordReused(rec); err != nil {
						t.Fatalf("%s: ReadRecordReused: %v", label, err)
					}
					assertKindDecode(t, label, kind, snap, wantVals, wantNulls, wantWide)
					if t.Failed() {
						return
					}
				}
			}
		})
	}
}

// TestReadRecordReusedWithPlan_IndexedKinds is the plan half: every kind
// × schema × retained set × record. The schema position handed to an
// index-keyed write must stay correct across SkipBytes segments, so the
// expected output is the full reference restricted to the retained set.
func TestReadRecordReusedWithPlan_IndexedKinds(t *testing.T) {
	corpus := indexedDecodeCorpus(t)
	for _, kind := range recordKinds() {
		t.Run(kind.name, func(t *testing.T) {
			for _, e := range corpus {
				for si, retained := range e.retainedSets() {
					plan, err := BuildDecodePlan(e.schema, retained)
					if err != nil {
						t.Fatalf("%s: BuildDecodePlan: %v", e.name, err)
					}
					for ri, raw := range e.records {
						label := fmt.Sprintf("%s/%s/subset=%d/rec=%d", kind.name, e.name, si, ri)
						want := restrictReusedRecord(decodeFullReused(t, e.schema, raw, label), retained)

						rec, snap := kind.build(t, e.schema)
						rr := NewRecordReader(bytes.NewReader(raw), e.schema)
						if err := rr.ReadRecordReusedWithPlan(rec, keepFromNames(retained), plan); err != nil {
							t.Fatalf("%s: ReadRecordReusedWithPlan: %v", label, err)
						}
						assertKindDecode(t, label, kind, snap, want.values, want.nulls, want.wide)
						if t.Failed() {
							return
						}
					}
				}
			}
		})
	}
}

// TestBuildDecodePlan_IndicesAreSchemaPositions pins DecodeFields.Indices
// to the builder's schema walk: parallel to Fields, each entry the
// field's position in Schema.Fields, including after SkipBytes gaps and
// for the trailing nullable-set bitmap segment.
func TestBuildDecodePlan_IndicesAreSchemaPositions(t *testing.T) {
	schema := bigMixedSchema()
	position := make(map[string]int, len(schema.Fields))
	for i := range schema.Fields {
		position[schema.Fields[i].Name] = i
	}
	for _, retained := range [][]string{
		{"u16_c"},
		{"u4_hi", "amount", "tags_u256"},
		{"u8_b", "tags_u128", "u64_b"},
		subsetFromMask(schema, (uint64(1)<<len(schema.Fields))-1),
	} {
		plan, err := BuildDecodePlan(schema, retained)
		if err != nil {
			t.Fatalf("BuildDecodePlan(%v): %v", retained, err)
		}
		for si, seg := range plan.Segments {
			df, ok := seg.(DecodeFields)
			if !ok {
				continue
			}
			if len(df.Indices) != len(df.Fields) {
				t.Fatalf("retained=%v seg=%d: len(Indices)=%d, len(Fields)=%d", retained, si, len(df.Indices), len(df.Fields))
			}
			for k, f := range df.Fields {
				if df.Indices[k] != position[f.Name] {
					t.Errorf("retained=%v seg=%d field %q: Indices[%d]=%d, want %d",
						retained, si, f.Name, k, df.Indices[k], position[f.Name])
				}
				if &schema.Fields[df.Indices[k]] != f {
					t.Errorf("retained=%v seg=%d field %q: Indices[%d] does not address the same *Field", retained, si, f.Name, k)
				}
			}
		}
	}
}

// TestReadRecordReusedWithPlan_HandBuiltSegmentWithoutIndices keeps an
// externally constructed DecodeFields (Fields only, no Indices) decoding
// correctly through the index-keyed path, and refuses one naming a field
// the reader's schema does not carry.
func TestReadRecordReusedWithPlan_HandBuiltSegmentWithoutIndices(t *testing.T) {
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0x4A4D)
	retained := []string{"u4_hi", "amount", "tags_u128"}
	built, err := BuildDecodePlan(schema, retained)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	stripped := &DecodePlan{}
	for _, seg := range built.Segments {
		if df, ok := seg.(DecodeFields); ok {
			stripped.Segments = append(stripped.Segments, DecodeFields{Fields: df.Fields})
			continue
		}
		stripped.Segments = append(stripped.Segments, seg)
	}

	for ri, raw := range records[:50] {
		label := fmt.Sprintf("rec=%d", ri)
		want := restrictReusedRecord(decodeFullReused(t, schema, raw, label), retained)
		rec := &dualRecord{indexedTestRecord: newIndexedTestRecord(schema)}
		rr := NewRecordReader(bytes.NewReader(raw), schema)
		if err := rr.ReadRecordReusedWithPlan(rec, keepFromNames(retained), stripped); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if rec.nameCalls != 0 {
			t.Fatalf("%s: hand-built plan fell back to name-keyed writes (%d)", label, rec.nameCalls)
		}
		if !reflect.DeepEqual(want.values, rec.values) || !reflect.DeepEqual(want.nulls, rec.nulls) || !reflect.DeepEqual(want.wide, rec.wide) {
			t.Fatalf("%s: hand-built plan mismatch:\n  want=%v %v %v\n  got=%v %v %v",
				label, want.values, want.nulls, want.wide, rec.values, rec.nulls, rec.wide)
		}
	}

	// A trailing SkipBytes keeps the foreign segment from being read as
	// the bitmap segment (the LAST DecodeFields of a bitmap-bearing schema).
	foreign := &DecodePlan{Segments: []Segment{
		DecodeFields{Fields: []*encoding.Field{{Name: "not_in_schema", Type: encoding.FieldTypeU8}}},
		SkipBytes{N: schema.RecordByteSize() - 1},
	}}
	rr := NewRecordReader(bytes.NewReader(records[0]), schema)
	err = rr.ReadRecordReusedWithPlan(&dualRecord{indexedTestRecord: newIndexedTestRecord(schema)}, nil, foreign)
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.ENCODING_INVALID {
		t.Fatalf("foreign-field plan: got %v, want ENCODING_INVALID", err)
	}
}

// benchReadRecordReusedIndexed mirrors benchReadRecordReused with an
// index-keyed record, so the two paths can be compared side by side.
func benchReadRecordReusedIndexed(b *testing.B, schema *encoding.Schema) {
	const records = 2000
	raw := buildBenchRecords(b, schema, records, 0xBEEF)
	rec := &dualRecord{indexedTestRecord: newIndexedTestRecord(schema)}
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

func BenchmarkReadRecordReusedIndexed_Narrow(b *testing.B) {
	benchReadRecordReusedIndexed(b, narrowBenchSchema())
}

func BenchmarkReadRecordReusedIndexed_Wide(b *testing.B) {
	benchReadRecordReusedIndexed(b, wideBenchSchema())
}
