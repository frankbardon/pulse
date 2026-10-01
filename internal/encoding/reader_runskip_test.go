package encoding

import (
	"bytes"
	"fmt"
	"io"
	"math/big"
	"math/rand"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// runSkipTestRecord is a map-backed RunSkipRecord: indexedTestRecord's
// maps and write counter, plus the ownership half of the contract. A
// kept row leaves the maps as the previous row's decode left them, so
// any field the decoder wrongly skips surfaces as a stale value.
type runSkipTestRecord struct {
	*dualRecord
	owner      uint64
	keptRows   int
	clearNulls int
}

func newRunSkipTestRecord(schema *encoding.Schema) *runSkipTestRecord {
	return &runSkipTestRecord{dualRecord: &dualRecord{indexedTestRecord: newIndexedTestRecord(schema)}}
}

func (r *runSkipTestRecord) BeginRunRow(token uint64, keep bool) bool {
	if keep && r.owner == token {
		r.keptRows++
		return true
	}
	r.ClearForRow()
	r.owner = token
	return false
}

func (r *runSkipTestRecord) ClearNullAt(idx int) {
	r.clearNulls++
	delete(r.nulls, r.schema.Fields[idx].Name)
}

// writes is every decoder write the record has taken.
func (r *runSkipTestRecord) writes() int { return r.indexCalls + r.clearNulls }

// fieldSpans returns each schema field's [start, end) on-wire span.
func fieldSpans(schema *encoding.Schema) [][2]int {
	out := make([][2]int, len(schema.Fields))
	c := 0
	for i := range schema.Fields {
		w := onWireWidth(schema.Fields[i].Type)
		out[i] = [2]int{c, c + w}
		c += w
	}
	return out
}

// spliceRunRows builds n rows that exercise every run-skip transition.
// Row 0 is records[0]; every fourth row repeats its predecessor byte for
// byte; every other row starts from its predecessor and, per field,
// independently takes a random donor record's value bytes and/or null
// bit — so value-changed-null-unchanged, null-flipped-value-unchanged
// and both occur for every field type in the schema.
func spliceRunRows(schema *encoding.Schema, records [][]byte, n int, seed int64) [][]byte {
	rng := rand.New(rand.NewSource(seed))
	spans := fieldSpans(schema)
	bmSize := schema.BitmapByteSize()
	body := schema.RecordByteSize() - bmSize
	rows := [][]byte{append([]byte(nil), records[0]...)}
	for k := 1; k < n; k++ {
		row := append([]byte(nil), rows[k-1]...)
		if k%4 != 0 {
			donor := records[rng.Intn(len(records))]
			for fi, sp := range spans {
				if rng.Intn(2) == 0 {
					copy(row[sp[0]:sp[1]], donor[sp[0]:sp[1]])
				}
				if bmSize > 0 && schema.Fields[fi].Nullable && rng.Intn(2) == 0 {
					bm := row[body:]
					if encoding.BitmapIsNull(donor[body:], fi) {
						encoding.BitmapSetNull(bm, fi)
					} else {
						bm[fi/8] &^= 1 << uint(fi%8)
					}
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// setBackoff overrides the backoff probe for one test. backoff 0
// disables it, so every eligible row compares — what the equivalence
// tests need to reach every transition.
func setBackoff(t *testing.T, probe, backoff int) {
	t.Helper()
	p, b := runProbeRows, runBackoffRows
	runProbeRows, runBackoffRows = probe, backoff
	t.Cleanup(func() { runProbeRows, runBackoffRows = p, b })
}

// referenceDecode decodes one row into a fresh (non-run-skip) record.
func referenceDecode(t *testing.T, schema *encoding.Schema, row []byte, keep FieldFilter, plan *DecodePlan) *indexedTestRecord {
	t.Helper()
	ref := &dualRecord{indexedTestRecord: newIndexedTestRecord(schema)}
	rr := NewRecordReader(bytes.NewReader(row), schema)
	if err := rr.ReadRecordReusedWithPlan(ref, keep, plan); err != nil {
		t.Fatalf("reference decode: %v", err)
	}
	return ref.indexedTestRecord
}

func assertSameState(t *testing.T, label string, got *runSkipTestRecord, want *indexedTestRecord) {
	t.Helper()
	if !reflect.DeepEqual(want.values, got.values) {
		t.Fatalf("%s values:\n  want=%v\n  got =%v", label, want.values, got.values)
	}
	if !reflect.DeepEqual(want.nulls, got.nulls) {
		t.Fatalf("%s nulls:\n  want=%v\n  got =%v", label, want.nulls, got.nulls)
	}
	if !reflect.DeepEqual(want.wide, got.wide) {
		t.Fatalf("%s wide:\n  want=%v\n  got =%v", label, want.wide, got.wide)
	}
}

// runSkipStream decodes rows through one reader into one run-skip record
// and asserts, row by row, that the record equals a fresh full decode of
// that row. Exact-repeat rows must take no write at all, and every row
// after the first must be a kept (partial) row.
func runSkipStream(t *testing.T, label string, schema *encoding.Schema, rows [][]byte, keep FieldFilter, plan *DecodePlan) {
	t.Helper()
	rec := newRunSkipTestRecord(schema)
	rr := NewRecordReader(bytes.NewReader(bytes.Join(rows, nil)), schema)
	for k, row := range rows {
		before := rec.writes()
		if err := rr.ReadRecordReusedWithPlan(rec, keep, plan); err != nil {
			t.Fatalf("%s row %d: %v", label, k, err)
		}
		assertSameState(t, fmt.Sprintf("%s row %d", label, k), rec, referenceDecode(t, schema, row, keep, plan))
		if k > 0 && bytes.Equal(row, rows[k-1]) && rec.writes() != before {
			t.Fatalf("%s row %d repeats row %d byte for byte but took %d writes", label, k, k-1, rec.writes()-before)
		}
	}
	if rec.keptRows != len(rows)-1 {
		t.Fatalf("%s: %d kept rows, want %d (every row after the first)", label, rec.keptRows, len(rows)-1)
	}
	// (A plan that decodes nothing only Seeks, which a bytes.Reader
	// allows past the end, so only the full path is held to io.EOF.)
	if err := rr.ReadRecordReusedWithPlan(rec, keep, plan); plan == nil && err != io.EOF {
		t.Fatalf("%s: after last row got %v, want io.EOF", label, err)
	}
}

// TestRunSkip_MatchesFullDecode is the load-bearing equivalence: over
// every corpus schema (every field type, nullable and not, bit-packed
// neighbours, decimal128, narrow and wide sets with empty masks), on the
// full-stride path and under every retained set on the plan path, a
// run-skip record equals a fresh full decode after every row.
func TestRunSkip_MatchesFullDecode(t *testing.T) {
	setBackoff(t, 32, 0)
	for _, e := range indexedDecodeCorpus(t) {
		rows := spliceRunRows(e.schema, e.records, 120, 0x5C1F)
		runSkipStream(t, e.name+"/full", e.schema, rows, nil, nil)
		for si, retained := range e.retainedSets() {
			plan, err := BuildDecodePlan(e.schema, retained)
			if err != nil {
				t.Fatalf("%s: BuildDecodePlan: %v", e.name, err)
			}
			runSkipStream(t, fmt.Sprintf("%s/plan%d", e.name, si), e.schema, rows, keepFromNames(retained), plan)
		}
	}
}

// TestRunSkip_NullAndEmptyMaskTransitions walks one nullable field of
// every wide-capable kind through the transitions the null bitmap and
// the empty-mask-is-not-null rule make interesting — with the value
// bytes held IDENTICAL across each null flip, so only the bitmap
// distinguishes the rows.
func TestRunSkip_NullAndEmptyMaskTransitions(t *testing.T) {
	setBackoff(t, 32, 0)
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU32, Nullable: true},
		{Name: "d", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2, Nullable: true},
		{Name: "s8", Type: encoding.FieldTypeSetU8, Dictionary: buildSetDict(8), Nullable: true},
		{Name: "s64", Type: encoding.FieldTypeSetU64, Dictionary: buildSetDict(64), Nullable: true},
		{Name: "s128", Type: encoding.FieldTypeSetU128, Dictionary: buildSetDict(128), Nullable: true},
		{Name: "s256", Type: encoding.FieldTypeSetU256, Dictionary: buildSetDict(256), Nullable: true},
		{Name: "b", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "q", Type: encoding.FieldTypeU4, BitPosition: 4, Nullable: true},
	}}
	empty := map[string]any{
		"n": uint64(0), "d": big.NewInt(0), "s8": uint64(0), "s64": uint64(0),
		"s128": encoding.SetMask{}, "s256": encoding.SetMask{}, "b": false, "q": uint8(0),
	}
	full := map[string]any{
		"n": uint64(7), "d": big.NewInt(-12345), "s8": uint64(0x81), "s64": uint64(1) << 63,
		"s128": encoding.SetMask{}.WithBit(100), "s256": encoding.SetMask{}.WithBit(255), "b": true, "q": uint8(9),
	}
	allNull := map[string]bool{}
	for _, f := range schema.Fields {
		allNull[f.Name] = true
	}
	var rows [][]byte
	for _, step := range []struct {
		vals  map[string]any
		nulls map[string]bool
	}{
		{empty, nil},     // empty masks / zero values, non-null
		{empty, allNull}, // same bytes, now null
		{empty, allNull}, // null on both rows
		{empty, nil},     // same bytes, back to non-null (empty, not null)
		{full, nil},      // values change
		{full, allNull},  // same bytes, null
		{full, nil},      // same bytes, non-null again: value must come back
		{empty, allNull}, // bytes AND null change together
		{full, allNull},  // null on both rows, bytes differ
		{full, nil},
	} {
		rows = append(rows, encodeRecord(t, schema, step.vals, step.nulls))
	}
	runSkipStream(t, "transitions/full", schema, rows, nil, nil)
	names := []string{"n", "d", "s8", "s64", "s128", "s256", "b", "q"}
	for mask := uint64(1); mask < 1<<len(names); mask += 7 {
		retained := subsetFromMask(schema, mask)
		plan, err := BuildDecodePlan(schema, retained)
		if err != nil {
			t.Fatalf("BuildDecodePlan: %v", err)
		}
		runSkipStream(t, fmt.Sprintf("transitions/plan%v", retained), schema, rows, keepFromNames(retained), plan)
	}
}

// TestRunSkip_InvalidatesOnReaderSinkAndPlanChange pins the reader half
// of the ownership contract: a comparison baseline is only ever used for
// the record, reader and decode shape that produced it.
func TestRunSkip_InvalidatesOnReaderSinkAndPlanChange(t *testing.T) {
	setBackoff(t, 32, 0)
	schema := bigMixedSchema()
	rows := spliceRunRows(schema, generateBigSchemaRecords(t, schema, 0x1D3CB16), 40, 0xB0B)
	stream := bytes.Join(rows, nil)

	t.Run("fresh reader repopulates everything", func(t *testing.T) {
		// The shard-iterator shape: one record, a new reader per shard.
		rec := newRunSkipTestRecord(schema)
		for k, row := range rows[:6] {
			rr := NewRecordReader(bytes.NewReader(row), schema)
			if err := rr.ReadRecordReused(rec); err != nil {
				t.Fatal(err)
			}
			assertSameState(t, fmt.Sprintf("row %d", k), rec, referenceDecode(t, schema, row, nil, nil))
		}
		if rec.keptRows != 0 {
			t.Fatalf("a reader's first row kept the previous reader's state (%d kept rows)", rec.keptRows)
		}
	})

	t.Run("alternating records on one reader", func(t *testing.T) {
		a, b := newRunSkipTestRecord(schema), newRunSkipTestRecord(schema)
		rr := NewRecordReader(bytes.NewReader(stream), schema)
		for k, row := range rows {
			rec := a
			if k%3 == 2 {
				rec = b
			}
			if err := rr.ReadRecordReused(rec); err != nil {
				t.Fatal(err)
			}
			assertSameState(t, fmt.Sprintf("row %d", k), rec, referenceDecode(t, schema, row, nil, nil))
		}
	})

	t.Run("alternating decode shapes on one reader", func(t *testing.T) {
		retained := []string{"u8_b", "amount", "tags_u128", "pb_1"}
		plan, err := BuildDecodePlan(schema, retained)
		if err != nil {
			t.Fatal(err)
		}
		keep := keepFromNames(retained)
		rec := newRunSkipTestRecord(schema)
		rr := NewRecordReader(bytes.NewReader(stream), schema)
		for k, row := range rows {
			// Plan rows only ever write the retained fields, so the
			// comparison is against a record that also carries the
			// full rows' values: reference = full decode of the last
			// full row, overlaid with this row's plan decode.
			usePlan := k%2 == 1
			if usePlan {
				err = rr.ReadRecordReusedWithPlan(rec, keep, plan)
			} else {
				err = rr.ReadRecordReused(rec)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !usePlan {
				assertSameState(t, fmt.Sprintf("row %d", k), rec, referenceDecode(t, schema, row, nil, nil))
				continue
			}
			want := referenceDecode(t, schema, row, keep, plan)
			for _, n := range retained {
				if want.values[n] != rec.values[n] || want.nulls[n] != rec.nulls[n] ||
					!reflect.DeepEqual(want.wide[n], rec.wide[n]) {
					t.Fatalf("row %d field %s: plan row after a full row decoded stale state", k, n)
				}
			}
		}
		if rec.keptRows != 0 {
			t.Fatalf("a decode-shape switch kept state across shapes (%d kept rows)", rec.keptRows)
		}
	})

	t.Run("a failed row is never a baseline", func(t *testing.T) {
		rec := newRunSkipTestRecord(schema)
		// Row 0 whole, then a truncated row 1.
		trunc := append(append([]byte(nil), rows[0]...), rows[1][:5]...)
		rr := NewRecordReader(bytes.NewReader(trunc), schema)
		if err := rr.ReadRecordReused(rec); err != nil {
			t.Fatal(err)
		}
		if err := rr.ReadRecordReused(rec); err != io.EOF {
			t.Fatalf("truncated row: got %v, want io.EOF", err)
		}
		if rr.prevValid {
			t.Fatal("a row that failed to read left a comparison baseline behind")
		}
	})
}

// TestRunSkip_BackoffOnScatteredRows: rows that share almost nothing
// trip the probe, the reader decodes the backoff period without
// comparing, then probes again — and the record stays equal to a full
// decode throughout, including across every switch between the modes.
func TestRunSkip_BackoffOnScatteredRows(t *testing.T) {
	setBackoff(t, 4, 6)
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0x1D3CB16)
	// Scattered: consecutive distinct generated records.
	rows := records[:40]
	for _, tc := range []struct {
		name     string
		retained []string
	}{{name: "full"}, {name: "plan", retained: []string{"u8_b", "u4_hi", "amount", "tags_u64", "tags_u256", "u16_c"}}} {
		t.Run(tc.name, func(t *testing.T) {
			var plan *DecodePlan
			var keep FieldFilter
			if tc.retained != nil {
				var err error
				if plan, err = BuildDecodePlan(schema, tc.retained); err != nil {
					t.Fatal(err)
				}
				keep = keepFromNames(tc.retained)
			}
			rec := newRunSkipTestRecord(schema)
			rr := NewRecordReader(bytes.NewReader(bytes.Join(rows, nil)), schema)
			var kept []int
			for k, row := range rows {
				before := rec.keptRows
				if err := rr.ReadRecordReusedWithPlan(rec, keep, plan); err != nil {
					t.Fatal(err)
				}
				if rec.keptRows > before {
					kept = append(kept, k)
				}
				assertSameState(t, fmt.Sprintf("row %d", k), rec, referenceDecode(t, schema, row, keep, plan))
			}
			// Probe rows 1-4 trip the backoff, rows 5-10 decode without
			// comparing, rows 11-14 probe again, and so on.
			want := []int{1, 2, 3, 4, 11, 12, 13, 14, 21, 22, 23, 24, 31, 32, 33, 34}
			if !reflect.DeepEqual(kept, want) {
				t.Fatalf("kept rows %v, want %v", kept, want)
			}
		})
	}
}

// TestRunSkip_SortedRowsNeverBackOff: a repeating shape keeps comparing.
func TestRunSkip_SortedRowsNeverBackOff(t *testing.T) {
	setBackoff(t, 4, 6)
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0x1D3CB16)
	var rows [][]byte
	for i := range 5 {
		for range 8 {
			rows = append(rows, records[i])
		}
	}
	rec := newRunSkipTestRecord(schema)
	rr := NewRecordReader(bytes.NewReader(bytes.Join(rows, nil)), schema)
	for k, row := range rows {
		if err := rr.ReadRecordReused(rec); err != nil {
			t.Fatal(err)
		}
		assertSameState(t, fmt.Sprintf("row %d", k), rec, referenceDecode(t, schema, row, nil, nil))
	}
	if rec.keptRows != len(rows)-1 {
		t.Fatalf("%d kept rows, want %d", rec.keptRows, len(rows)-1)
	}
}

// TestRunSkip_LegacySinksUntouched: a record that does not implement
// RunSkipRecord still gets ClearForRow and a full write every row.
func TestRunSkip_LegacySinksUntouched(t *testing.T) {
	schema := bigMixedSchema()
	rows := spliceRunRows(schema, generateBigSchemaRecords(t, schema, 0x1D3CB16), 12, 0xB0B)
	rec := &dualRecord{indexedTestRecord: newIndexedTestRecord(schema)}
	rr := NewRecordReader(bytes.NewReader(bytes.Join(rows, nil)), schema)
	perRow := -1
	for k := range rows {
		before := rec.indexCalls
		if err := rr.ReadRecordReused(rec); err != nil {
			t.Fatal(err)
		}
		n := rec.indexCalls - before
		// Writes per row vary only with the row's null count; a skip
		// would drop them to a handful on the exact-repeat rows.
		if k > 0 && bytes.Equal(rows[k], rows[k-1]) && n != perRow {
			t.Fatalf("row %d: a legacy sink took %d writes on a repeat row, want %d", k, n, perRow)
		}
		perRow = n
	}
	if rr.prevValid {
		t.Fatal("a legacy sink left a run-skip baseline")
	}
}

func BenchmarkReadRecordReused_RunSkip(b *testing.B) {
	schema := wideBenchSchema()
	raw := buildBenchRecords(b, schema, 1024, 7)
	stride := schema.RecordByteSize()
	sorted := make([]byte, 0, len(raw))
	// Each distinct row repeated 12 times: a sorted parent/child shape.
	for i := 0; i+stride <= len(raw) && len(sorted) < len(raw); i += stride {
		for range 12 {
			sorted = append(sorted, raw[i:i+stride]...)
		}
	}
	sorted = sorted[:len(raw)/stride*stride]
	for _, tc := range []struct {
		name string
		data []byte
	}{{"scattered", raw}, {"sorted", sorted}} {
		b.Run(tc.name, func(b *testing.B) {
			rec := newRunSkipTestRecord(schema)
			b.ReportAllocs()
			for b.Loop() {
				rr := NewRecordReader(bytes.NewReader(tc.data), schema)
				for rr.ReadRecordReused(rec) == nil {
				}
			}
		})
	}
}
