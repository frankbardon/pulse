package processing

import (
	"bytes"
	"fmt"
	"math/big"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// Run-skip, the Record half: the reuse decoders skip rewriting fields
// whose on-wire bytes repeat the previous row, and the Record decides
// (BeginRunRow) whether its state still is that previous row's decode.
// Every test compares a reused Record against a FRESH Record decoding
// the same row — the non-skipping path — through the public accessors.

func runSkipTestSchema() *encoding.Schema {
	dict := func(n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := range n {
			if _, err := d.Add(fmt.Sprintf("m%03d", i)); err != nil {
				panic(err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU32, Nullable: true},
		{Name: "d", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2, Nullable: true},
		{Name: "s", Type: encoding.FieldTypeSetU8, Dictionary: dict(8), Nullable: true},
		{Name: "w", Type: encoding.FieldTypeSetU128, Dictionary: dict(128)},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict(4)},
		{Name: "b", Type: encoding.FieldTypePackedBool},
	}}
}

// runSkipRow is one row of runSkipTestSchema.
type runSkipRow struct {
	a     uint64
	d     int64
	s     uint64
	w     int // bit set in the set_u128 mask; -1 = empty mask
	c     uint64
	b     bool
	nulls []int // null field positions
}

func (row runSkipRow) encode(t *testing.T, schema *encoding.Schema) []byte {
	t.Helper()
	var buf bytes.Buffer
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(encoding.WriteFieldValue(&buf, schema.Fields[0].Type, row.a))
	dec, err := encoding.NewDecimal128FromBigInt(big.NewInt(row.d))
	must(err)
	must(encoding.WriteDecimal128(&buf, dec))
	must(encoding.WriteFieldValue(&buf, schema.Fields[2].Type, row.s))
	var m encoding.SetMask
	if row.w >= 0 {
		m = m.WithBit(row.w)
	}
	must(encoding.WriteSetMask(&buf, schema.Fields[3].Type, m))
	must(encoding.WriteFieldValue(&buf, schema.Fields[4].Type, row.c))
	must(encoding.WriteBit(&buf, 0, row.b))
	bm := make([]byte, schema.BitmapByteSize())
	for _, i := range row.nulls {
		encoding.BitmapSetNull(bm, i)
	}
	must(encoding.WriteBitmap(&buf, bm))
	return buf.Bytes()
}

// runSkipRows covers repeats, single-field changes, null flips with the
// value bytes unchanged, and the empty-mask / null distinction.
func runSkipRows(t *testing.T, schema *encoding.Schema) [][]byte {
	base := runSkipRow{a: 5, d: 1234, s: 0, w: -1, c: 1, b: true}
	steps := []func(r *runSkipRow){
		func(r *runSkipRow) {},                           // repeat
		func(r *runSkipRow) { r.a = 6 },                  // one numeric change
		func(r *runSkipRow) { r.nulls = []int{0, 1, 2} }, // null flip, same bytes
		func(r *runSkipRow) {},                           // null on both rows
		func(r *runSkipRow) { r.nulls = nil },            // back to non-null: values return
		func(r *runSkipRow) { r.s = 0x81; r.w = 127 },    // set masks change
		func(r *runSkipRow) { r.s = 0; r.w = -1 },        // empty masks again, not null
		func(r *runSkipRow) { r.d = -99; r.b = false },   // decimal + bit-packed
		func(r *runSkipRow) { r.c = 3 },                  // categorical label changes
		func(r *runSkipRow) {},
	}
	rows := [][]byte{base.encode(t, schema)}
	cur := base
	for _, step := range steps {
		step(&cur)
		rows = append(rows, cur.encode(t, schema))
	}
	return rows
}

// freshDecode decodes row into a new Record — the non-skipping path.
func freshDecode(t *testing.T, schema *encoding.Schema, row []byte) *Record {
	t.Helper()
	rec := NewReusableRecord(schema)
	if err := encoding.NewRecordReader(bytes.NewReader(row), schema).ReadRecordReused(rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// assertRecordState compares every public accessor of got and want.
func assertRecordState(t *testing.T, label string, schema *encoding.Schema, got, want *Record) {
	t.Helper()
	for _, f := range schema.Fields {
		n := f.Name
		gv, gok := got.NumericValue(n)
		wv, wok := want.NumericValue(n)
		if gv != wv || gok != wok {
			t.Fatalf("%s: NumericValue(%s) = (%v,%v), want (%v,%v)", label, n, gv, gok, wv, wok)
		}
		if got.IsNull(n) != want.IsNull(n) {
			t.Fatalf("%s: IsNull(%s) = %v, want %v", label, n, got.IsNull(n), want.IsNull(n))
		}
		gw, gwok := got.WideValue(n)
		ww, wwok := want.WideValue(n)
		if gwok != wwok || !reflect.DeepEqual(gw, ww) {
			t.Fatalf("%s: WideValue(%s) = (%v,%v), want (%v,%v)", label, n, gw, gwok, ww, wwok)
		}
		gm, gmok := got.SetMaskValue(n)
		wm, wmok := want.SetMaskValue(n)
		if gm != wm || gmok != wmok {
			t.Fatalf("%s: SetMaskValue(%s) = (%v,%v), want (%v,%v)", label, n, gm, gmok, wm, wmok)
		}
		gs, gsok := got.StringValue(n)
		ws, wsok := want.StringValue(n)
		if gs != ws || gsok != wsok {
			t.Fatalf("%s: StringValue(%s) = (%q,%v), want (%q,%v)", label, n, gs, gsok, ws, wsok)
		}
	}
	if !reflect.DeepEqual(got.AllValues(), want.AllValues()) {
		t.Fatalf("%s: AllValues = %v, want %v", label, got.AllValues(), want.AllValues())
	}
}

func TestRecordRunSkip_MatchesFreshDecode(t *testing.T) {
	schema := runSkipTestSchema()
	rows := runSkipRows(t, schema)
	rec := NewReusableRecord(schema)
	rr := encoding.NewRecordReader(bytes.NewReader(bytes.Join(rows, nil)), schema)
	for k, row := range rows {
		if err := rr.ReadRecordReused(rec); err != nil {
			t.Fatal(err)
		}
		// AllValues is memoised; the next row must discard it.
		_ = rec.AllValues()
		assertRecordState(t, fmt.Sprintf("row %d", k), schema, rec, freshDecode(t, schema, row))
		if k > 0 && rec.runOwner == 0 {
			t.Fatalf("row %d: record left run-skip on a clean scan", k)
		}
	}
}

// TestRecordRunSkip_ExternalMutationForcesRepopulate: any schema-field
// write that did not come from the reuse decoder — a feature or
// attribute writing a schema column's name, a name-keyed decode — must
// make the next row repopulate in full, or a skipped field would carry
// the mutated value.
func TestRecordRunSkip_ExternalMutationForcesRepopulate(t *testing.T) {
	schema := runSkipTestSchema()
	// "a" is null on this row, so un-marking it is a visible mutation.
	row := runSkipRow{a: 5, d: 1234, s: 0x3, w: 9, c: 2, b: true, nulls: []int{0}}.encode(t, schema)
	for _, tc := range []struct {
		name   string
		mutate func(r *Record)
	}{
		// Each name-keyed primitive on its own: every public mutator is
		// built from these, usually two at a time, so a missing reset in
		// one would otherwise hide behind its partner.
		{"putValue", func(r *Record) { r.putValue("c", 3) }},
		{"dropValue", func(r *Record) { r.dropValue("c") }},
		{"markNull", func(r *Record) { r.markNull("c") }},
		{"unmarkNull", func(r *Record) { r.unmarkNull("a") }},
		{"putWide", func(r *Record) { r.putWide("s", uint64(0x10)) }},
		{"dropWide", func(r *Record) { r.dropWide("s") }},
		// A wide value the slot cannot hold typed falls to the overflow
		// map, which a kept row clears — so it must withdraw too.
		{"SetWideFieldAt overflow fallback", func(r *Record) { r.SetWideFieldAt(2, encoding.SetMask{}.WithBit(3)) }},
		{"Set", func(r *Record) { r.Set("a", 999) }},
		{"SetNull", func(r *Record) { r.SetNull("c") }},
		{"SetWide", func(r *Record) { r.SetWide("s", uint64(0xF0)) }},
		{"SetWide wide rung", func(r *Record) { r.SetWide("w", encoding.SetMask{}.WithBit(1)) }},
		{"injectValue", func(r *Record) { r.injectValue("d", 7) }},
		{"SetNumeric", func(r *Record) { r.SetNumeric("b", 0) }},
		{"SetNullField", func(r *Record) { r.SetNullField("a") }},
		{"SetWideField", func(r *Record) { r.SetWideField("d", encoding.Decimal128{}) }},
		{"copyStateInto target", func(r *Record) {
			src := NewReusableRecord(schema)
			src.Set("a", 42)
			src.copyStateInto(r, func(s string) string { return s }, 0, true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := NewReusableRecord(schema)
			rr := encoding.NewRecordReader(bytes.NewReader(append(append([]byte(nil), row...), row...)), schema)
			if err := rr.ReadRecordReused(rec); err != nil {
				t.Fatal(err)
			}
			tc.mutate(rec)
			if err := rr.ReadRecordReused(rec); err != nil {
				t.Fatal(err)
			}
			assertRecordState(t, "after mutation", schema, rec, freshDecode(t, schema, row))
		})
	}
}

// TestRecordRunSkip_OffSchemaWritesKeepRun: attribute labels and derived
// columns land on the overflow side and never alias a schema field, so
// they must NOT withdraw the record from run-skip — otherwise every
// pipeline with an attribute would silently lose the optimisation.
func TestRecordRunSkip_OffSchemaWritesKeepRun(t *testing.T) {
	schema := runSkipTestSchema()
	row := runSkipRow{a: 5, d: 1234, s: 0x3, w: 9, c: 2, b: true}.encode(t, schema)
	rec := NewReusableRecord(schema)
	rr := encoding.NewRecordReader(bytes.NewReader(append(append([]byte(nil), row...), row...)), schema)
	if err := rr.ReadRecordReused(rec); err != nil {
		t.Fatal(err)
	}
	token := rec.runOwner
	rec.Set("derived", 1)
	rec.SetNull("derived_null")
	rec.injectValue("attr", 2)
	rec.SetWide("attr_wide", uint64(3))
	if rec.runOwner != token {
		t.Fatal("an off-schema write withdrew the record from run-skip")
	}
	if err := rr.ReadRecordReused(rec); err != nil {
		t.Fatal(err)
	}
	want := freshDecode(t, schema, row)
	for _, f := range schema.Fields {
		gv, _ := rec.NumericValue(f.Name)
		wv, _ := want.NumericValue(f.Name)
		if gv != wv || rec.IsNull(f.Name) != want.IsNull(f.Name) {
			t.Fatalf("%s differs after a kept row", f.Name)
		}
	}
	// Overflow null / wide marks are per-row state and are cleared on a
	// kept row exactly as ClearForRow clears them; values persist.
	if rec.nullMarked("derived_null") {
		t.Fatal("overflow null mark survived a kept row")
	}
	if _, ok := rec.WideValue("attr_wide"); ok {
		t.Fatal("overflow wide value survived a kept row")
	}
	if v, _ := rec.NumericValue("attr"); v != 2 {
		t.Fatalf("overflow value = %v, want it kept (2) as ClearForRow keeps it", v)
	}
}

// TestRecordRunSkip_IdentityLayoutOnly: a projected layout and a
// duplicated field name both break slot == position, so BeginRunRow
// must refuse to keep, whatever the owner.
func TestRecordRunSkip_IdentityLayoutOnly(t *testing.T) {
	schema := runSkipTestSchema()
	projected := BindRecords(schema, func(n string) bool { return n == "a" || n == "s" }).NewRecord()
	projected.BeginRunRow(7, false)
	if projected.BeginRunRow(7, true) {
		t.Fatal("projected layout kept state for a partial rewrite")
	}

	dup := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeU8},
		{Name: "x", Type: encoding.FieldTypeU8},
	}}
	rec := NewReusableRecord(dup)
	rec.BeginRunRow(7, false)
	if rec.BeginRunRow(7, true) {
		t.Fatal("duplicate-name layout kept state for a partial rewrite")
	}
	// And through the decoder: the SECOND occurrence wins on a full
	// decode; a skip of the unchanged second occurrence would let the
	// changed first one win instead.
	rows := [][]byte{{1, 2}, {3, 2}}
	rr := encoding.NewRecordReader(bytes.NewReader(bytes.Join(rows, nil)), dup)
	for k, row := range rows {
		if err := rr.ReadRecordReused(rec); err != nil {
			t.Fatal(err)
		}
		assertRecordState(t, fmt.Sprintf("dup row %d", k), dup, rec, freshDecode(t, dup, row))
	}
}

// TestRecordRunSkip_TwoReadersAlternate: the token check. Reader B's
// baseline is its own last row, decoded into this record; reader A has
// written the record since, so B must not trust it.
func TestRecordRunSkip_TwoReadersAlternate(t *testing.T) {
	schema := runSkipTestSchema()
	x := runSkipRow{a: 1, d: 10, s: 1, w: 3, c: 0, b: true}.encode(t, schema)
	y := runSkipRow{a: 2, d: 20, s: 2, w: 4, c: 1, b: false, nulls: []int{2}}.encode(t, schema)
	ra := encoding.NewRecordReader(bytes.NewReader(bytes.Join([][]byte{x, x}, nil)), schema)
	rb := encoding.NewRecordReader(bytes.NewReader(bytes.Join([][]byte{y, y}, nil)), schema)
	rec := NewReusableRecord(schema)
	for _, step := range []struct {
		rr   *encoding.RecordReader
		want []byte
	}{{rb, y}, {ra, x}, {rb, y}, {ra, x}} {
		if err := step.rr.ReadRecordReused(rec); err != nil {
			t.Fatal(err)
		}
		assertRecordState(t, "alternating readers", schema, rec, freshDecode(t, schema, step.want))
	}
}

// TestRecordRunSkip_ClearNullAt clears only the null mark.
func TestRecordRunSkip_ClearNullAt(t *testing.T) {
	schema := runSkipTestSchema()
	rec := NewReusableRecord(schema)
	rec.SetNumericAt(0, 4)
	rec.SetNullFieldAt(0)
	rec.ClearNullAt(0)
	if rec.IsNull("a") {
		t.Fatal("ClearNullAt left the null mark")
	}
	if v, ok := rec.NumericValue("a"); !ok || v != 4 {
		t.Fatalf("ClearNullAt disturbed the value: (%v,%v)", v, ok)
	}
}
