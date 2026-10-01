package processing

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// recordMaps renders a Record's state as the three name-keyed maps the
// former map-backed Record carried — values, nulls (true entries only)
// and wide — so tests can compare record state without depending on
// the positional layout.
func recordMaps(r *Record) (map[string]float64, map[string]bool, map[string]any) {
	v := map[string]float64{}
	nl := map[string]bool{}
	w := map[string]any{}
	for i := range r.vals {
		name := r.fieldAt(i).Name
		if r.test(planeHas, i) {
			v[name] = r.vals[i]
		}
		if r.test(planeNull, i) {
			nl[name] = true
		}
		if wv, ok := r.wideAt(i); ok {
			w[name] = wv
		}
	}
	if r.aux != nil {
		for k, x := range r.aux.ovVals {
			v[k] = x
		}
		for k, x := range r.aux.ovNulls {
			if x {
				nl[k] = true
			}
		}
		for k, x := range r.aux.ovWide {
			w[k] = x
		}
	}
	return v, nl, w
}

// legacyRecord is a verbatim model of the map-backed Record this story
// replaced: the same three maps, the same mutator side effects, the same
// accessor logic. The positional Record is checked against it op by op.
//
// One deliberate departure: setNullField (the decoder's null signal)
// also deletes the wide entry, as the map DECODER always did for a
// bitmap-null field. The map-backed Record's own SetNullField kept it
// hidden behind the null mark, where a later Set could resurface it.
type legacyRecord struct {
	schema *encoding.Schema
	values map[string]float64
	nulls  map[string]bool
	wide   map[string]any
	cache  map[string]any
}

func newLegacy(s *encoding.Schema) *legacyRecord {
	return &legacyRecord{schema: s, values: map[string]float64{}, nulls: map[string]bool{}, wide: map[string]any{}}
}

// The mutators invalidate the AllValues cache exactly where the
// map-backed Record did — and, like it, the reuse-path setters do not.
func (r *legacyRecord) set(n string, v float64)        { r.values[n] = v; delete(r.nulls, n); r.cache = nil }
func (r *legacyRecord) setNull(n string)               { r.nulls[n] = true; delete(r.values, n); r.cache = nil }
func (r *legacyRecord) setNumeric(n string, v float64) { r.values[n] = v }
func (r *legacyRecord) setNullField(n string)          { r.nulls[n] = true; delete(r.wide, n) }
func (r *legacyRecord) setWideField(n string, v any)   { r.wide[n] = v }
func (r *legacyRecord) setWide(n string, v any)        { r.wide[n] = v; delete(r.nulls, n); r.cache = nil }
func (r *legacyRecord) clearForRow()                   { clear(r.nulls); clear(r.wide); r.cache = nil }
func (r *legacyRecord) inject(n string, v float64)     { r.values[n] = v; r.cache = nil }

func (r *legacyRecord) numericValue(n string) (float64, bool) {
	if r.nulls[n] {
		return 0, false
	}
	if wv, ok := r.wide[n]; ok {
		if _, isSet := setMaskFromWideValue(wv); isSet {
			return 0, false
		}
	}
	v, ok := r.values[n]
	return v, ok
}

func (r *legacyRecord) isNull(n string) bool {
	if n == "" {
		return false
	}
	if r.nulls[n] {
		return true
	}
	if _, ok := r.values[n]; ok {
		return false
	}
	_, ok := r.wide[n]
	return !ok
}

func (r *legacyRecord) wideValue(n string) (any, bool) {
	if r.nulls[n] {
		return nil, false
	}
	v, ok := r.wide[n]
	return v, ok
}

func (r *legacyRecord) setMaskValue(n string) (encoding.SetMask, bool) {
	if r.nulls[n] {
		return encoding.SetMask{}, false
	}
	v, ok := r.wide[n]
	if !ok {
		return encoding.SetMask{}, false
	}
	return setMaskFromWideValue(v)
}

func (r *legacyRecord) stringValue(n string) (string, bool) {
	if r.nulls[n] {
		return "", false
	}
	f := r.schema.Field(n)
	if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
		return "", false
	}
	v, ok := r.values[n]
	if !ok {
		return "", false
	}
	s := f.Dictionary.Resolve(uint32(v))
	return s, s != ""
}

func (r *legacyRecord) allValues() map[string]any {
	if r.cache != nil {
		return r.cache
	}
	out := map[string]any{}
	for k, v := range r.values {
		if r.nulls[k] {
			continue
		}
		f := r.schema.Field(k)
		if f != nil && f.Type.IsCategorical() && f.Dictionary != nil {
			out[k] = f.Dictionary.Resolve(uint32(v))
		} else {
			out[k] = v
		}
	}
	for k, v := range r.wide {
		if r.nulls[k] {
			continue
		}
		out[k] = allValuesWide(r.schema.Field(k), v)
	}
	r.cache = out
	return out
}

func positionalTestSchema() *encoding.Schema {
	dict := func(prefix string, n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := 0; i < n; i++ {
			_, _ = d.Add(fmt.Sprintf("%s-%d", prefix, i))
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("c", 5), Nullable: true},
		{Name: "dec", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2, Nullable: true},
		{Name: "s8", Type: encoding.FieldTypeSetU8, Dictionary: dict("s", 6), Nullable: true},
		{Name: "s128", Type: encoding.FieldTypeSetU128, Dictionary: dict("w", 100)},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "u", Type: encoding.FieldTypeU32},
	}}
}

// assertMatchesLegacy compares every public read accessor of the
// positional record against the legacy model for every probe name.
func assertMatchesLegacy(t *testing.T, step string, got *Record, want *legacyRecord, names []string) {
	t.Helper()
	for _, n := range names {
		gv, gok := got.NumericValue(n)
		wv, wok := want.numericValue(n)
		if gv != wv || gok != wok {
			t.Fatalf("%s: NumericValue(%q) = (%v,%v), legacy (%v,%v)", step, n, gv, gok, wv, wok)
		}
		if g, w := got.IsNull(n), want.isNull(n); g != w {
			t.Fatalf("%s: IsNull(%q) = %v, legacy %v", step, n, g, w)
		}
		gw, gwok := got.WideValue(n)
		ww, wwok := want.wideValue(n)
		if !reflect.DeepEqual(gw, ww) || gwok != wwok {
			t.Fatalf("%s: WideValue(%q) = (%v,%v), legacy (%v,%v)", step, n, gw, gwok, ww, wwok)
		}
		gm, gmok := got.SetMaskValue(n)
		wm, wmok := want.setMaskValue(n)
		if gm != wm || gmok != wmok {
			t.Fatalf("%s: SetMaskValue(%q) mismatch", step, n)
		}
		gs, gsok := got.StringValue(n)
		ws, wsok := want.stringValue(n)
		if gs != ws || gsok != wsok {
			t.Fatalf("%s: StringValue(%q) = (%q,%v), legacy (%q,%v)", step, n, gs, gsok, ws, wsok)
		}
	}
	if g, w := got.AllValues(), want.allValues(); !reflect.DeepEqual(g, w) {
		t.Fatalf("%s: AllValues = %v, legacy %v", step, g, w)
	}
	gv, gn, gw := recordMaps(got)
	wn := map[string]bool{}
	for k, x := range want.nulls {
		if x {
			wn[k] = true
		}
	}
	if !reflect.DeepEqual(gv, want.values) || !reflect.DeepEqual(gn, wn) || !reflect.DeepEqual(gw, want.wide) {
		t.Fatalf("%s: stored state diverged\n got  %v %v %v\n want %v %v %v", step, gv, gn, gw, want.values, wn, want.wide)
	}
}

// TestRecord_PositionalMatchesLegacyMapSemantics drives the positional
// Record and a verbatim model of the former map-backed Record through
// the same random sequence of mutations — schema and off-schema names,
// numeric, null and wide writes, ClearForRow, attribute injection — and
// asserts every public accessor, AllValues included (so its cache must
// be invalidated exactly where it was), agrees after every step.
func TestRecord_PositionalMatchesLegacyMapSemantics(t *testing.T) {
	schema := positionalTestSchema()
	names := []string{"n", "cat", "dec", "s8", "s128", "flag", "u", "attr_label", "feat_out", ""}
	wides := []any{
		encoding.NewDecimal128FromInt(-1234),
		uint64(0b101101),
		uint64(0),
		encoding.SetMaskFromUint64(3).WithBit(99),
		encoding.SetMask{},
		// Past set_u128's rung: must stay boxed, never truncated.
		encoding.SetMaskFromUint64(1).WithBit(200),
		// Above 2^53: the float echo would lose it; WideValue must not.
		uint64(1<<63 | 1<<53 | 1),
	}
	keepSome := func(n string) bool { return n == "cat" || n == "s8" || n == "u" }
	bindings := []struct {
		name string
		b    *RecordBinding
	}{
		{"full", BindRecords(schema, nil)},
		{"projected", BindRecords(schema, keepSome)},
	}
	for _, bc := range bindings {
		t.Run(bc.name, func(t *testing.T) { driveAgainstLegacy(t, schema, bc.b, names, wides) })
	}
}

// schemaIndex returns name's FIRST position in schema, or -1.
func schemaIndex(schema *encoding.Schema, name string) int {
	for i := range schema.Fields {
		if schema.Fields[i].Name == name {
			return i
		}
	}
	return -1
}

func driveAgainstLegacy(t *testing.T, schema *encoding.Schema, binding *RecordBinding, names []string, wides []any) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		got := binding.NewRecord()
		want := newLegacy(schema)
		for step := 0; step < 120; step++ {
			n := names[rng.Intn(len(names)-1)] // never mutate ""
			v := float64(rng.Intn(7))
			var op string
			switch rng.Intn(11) {
			case 0:
				op = "Set"
				got.Set(n, v)
				want.set(n, v)
			case 1:
				op = "SetNull"
				got.SetNull(n)
				want.setNull(n)
			case 2:
				op = "SetNumeric"
				got.SetNumeric(n, v)
				want.setNumeric(n, v)
			case 3:
				op = "SetNullField"
				got.SetNullField(n)
				want.setNullField(n)
			case 4:
				op = "SetWideField"
				w := wides[rng.Intn(len(wides))]
				got.SetWideField(n, w)
				want.setWideField(n, w)
			case 5:
				op = "SetWide"
				w := wides[rng.Intn(len(wides))]
				got.SetWide(n, w)
				want.setWide(n, w)
			case 6:
				op = "ClearForRow"
				got.ClearForRow()
				want.clearForRow()
			case 7:
				op = "inject"
				got.injectValue(n, v)
				want.inject(n, v)
			case 8:
				// Warm the AllValues cache so a missed invalidation on the
				// next mutation shows up as a stale map.
				op = "AllValues"
				_ = got.AllValues()
				_ = want.allValues()
			case 9, 10:
				// The decoder's typed set writes, keyed by schema position.
				// An off-schema name has no position and cannot reach them.
				idx := schemaIndex(schema, n)
				if idx < 0 {
					op = "skip"
					break
				}
				if rng.Intn(2) == 0 {
					op = "SetNarrowSetAt"
					m := uint64(rng.Int63()) << uint(rng.Intn(2))
					got.SetNarrowSetAt(idx, m)
					want.setWideField(n, m)
				} else {
					op = "SetWideSetAt"
					m := encoding.SetMaskFromUint64(uint64(rng.Intn(50))).WithBit(rng.Intn(256))
					got.SetWideSetAt(idx, m)
					want.setWideField(n, m)
				}
			}
			assertMatchesLegacy(t, fmt.Sprintf("seed %d step %d %s(%q)", seed, step, op, n), got, want, names)
		}
	}
}

// TestRecord_ProjectedBindingSizedToRetained pins that a projected
// binding stores only the retained fields: the record's positional
// storage is len(retained), a decode through the matching plan lands
// every retained field in a slot (no overflow), and the record reads the
// same as a full-layout record decoded the same way.
func TestRecord_ProjectedBindingSizedToRetained(t *testing.T) {
	schema := indexedRecordSchema()
	retained := []string{"hi", "amount", "tags", "tail"}
	set := map[string]bool{}
	for _, n := range retained {
		set[n] = true
	}
	keep := func(n string) bool { return set[n] }
	plan, err := encx.BuildDecodePlan(schema, retained)
	if err != nil {
		t.Fatal(err)
	}
	raw := encodeIndexedRows(t, schema, 16)
	binding := BindRecords(schema, keep)
	projRR := encx.NewRecordReader(bytes.NewReader(raw), schema)
	fullRR := encx.NewRecordReader(bytes.NewReader(raw), schema)
	for row := 0; row < 16; row++ {
		proj := binding.NewRecord()
		full := NewReusableRecord(schema)
		if err := projRR.ReadRecordReusedWithPlan(proj, keep, plan); err != nil {
			t.Fatal(err)
		}
		if err := fullRR.ReadRecordReusedWithPlan(full, keep, plan); err != nil {
			t.Fatal(err)
		}
		if len(proj.vals) != len(retained) {
			t.Fatalf("projected record has %d slots, want %d", len(proj.vals), len(retained))
		}
		if proj.aux != nil && (proj.aux.ovVals != nil || proj.aux.ovNulls != nil || proj.aux.ovWide != nil) {
			t.Fatalf("row %d: projected decode spilled to overflow", row)
		}
		pv, pn, pw := recordMaps(proj)
		fv, fn, fw := recordMaps(full)
		if !reflect.DeepEqual(pv, fv) || !reflect.DeepEqual(pn, fn) || !reflect.DeepEqual(pw, fw) {
			t.Fatalf("row %d: projected %v %v %v, full %v %v %v", row, pv, pn, pw, fv, fn, fw)
		}
		if !reflect.DeepEqual(proj.AllValues(), full.AllValues()) {
			t.Fatalf("row %d: AllValues differ", row)
		}
	}
	// A position outside the projection still decodes correctly (by
	// name, on the overflow side) — sized for the plan, correct for any.
	rec := binding.NewRecord()
	rec.SetNumericAt(0, 42) // "id", not retained
	if v, ok := rec.NumericValue("id"); !ok || v != 42 {
		t.Fatalf("unretained position write: NumericValue(id) = (%v,%v)", v, ok)
	}
}

// TestRecord_MapConstructorsConvertOnEntry pins the four map-taking
// constructors: the result matches the legacy model built from the same
// maps, schema fields land positionally (no overflow map), off-schema
// names land in overflow, and the caller's maps are not retained.
func TestRecord_MapConstructorsConvertOnEntry(t *testing.T) {
	schema := positionalTestSchema()
	names := []string{"n", "cat", "dec", "s8", "s128", "flag", "u", "extra"}
	values := map[string]float64{"n": 2.5, "cat": 3, "dec": 12.34, "s8": 5, "u": 0}
	nulls := map[string]bool{"flag": true, "n": false}
	wide := map[string]any{"dec": encoding.NewDecimal128FromInt(1234), "s8": uint64(5)}

	for _, tc := range []struct {
		name string
		rec  *Record
		want *legacyRecord
	}{
		{"NewRecord", NewRecord(schema, values), &legacyRecord{schema: schema, values: values, nulls: map[string]bool{}, wide: map[string]any{}}},
		{"NewRecordWithNulls", NewRecordWithNulls(schema, values, nulls), &legacyRecord{schema: schema, values: values, nulls: nulls, wide: map[string]any{}}},
		{"NewRecordWithWide", NewRecordWithWide(schema, values, nulls, wide), &legacyRecord{schema: schema, values: values, nulls: nulls, wide: wide}},
		{"NewRecordWithWide-nil-maps", NewRecordWithWide(schema, values, nil, nil), &legacyRecord{schema: schema, values: values, nulls: map[string]bool{}, wide: map[string]any{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertMatchesLegacy(t, tc.name, tc.rec, tc.want, names)
			if tc.rec.aux != nil && (tc.rec.aux.ovVals != nil || tc.rec.aux.ovNulls != nil || tc.rec.aux.ovWide != nil) {
				t.Fatalf("schema-only record allocated overflow maps: %+v", tc.rec.aux)
			}
		})
	}

	// Off-schema names go to overflow, schema names never do.
	r := NewRecord(schema, map[string]float64{"n": 1, "derived": 7})
	if r.aux == nil || len(r.aux.ovVals) != 1 || r.aux.ovVals["derived"] != 7 {
		t.Fatalf("off-schema value not in overflow: %+v", r.aux)
	}
	if v, ok := r.NumericValue("derived"); !ok || v != 7 {
		t.Fatalf("NumericValue(derived) = (%v,%v)", v, ok)
	}

	// The caller's map is converted, not aliased.
	m := map[string]float64{"n": 1}
	r = NewRecord(schema, m)
	m["n"] = 99
	if v, _ := r.NumericValue("n"); v != 1 {
		t.Fatalf("record aliases the constructor map: NumericValue(n) = %v after caller mutation", v)
	}

	// Zero-value and nil-schema records answer "absent" and accept
	// writes through the overflow side.
	for _, z := range []*Record{{}, NewRecord(nil, nil)} {
		if _, ok := z.NumericValue("x"); ok || !z.IsNull("x") {
			t.Fatal("empty record reports a value")
		}
		z.Set("x", 4)
		if v, ok := z.NumericValue("x"); !ok || v != 4 {
			t.Fatalf("empty record Set/NumericValue = (%v,%v)", v, ok)
		}
	}
}

// TestRecord_AbsentDistinctFromZero pins the presence plane: a field
// never written (projected out) is absent — NumericValue ok=false,
// IsNull true, missing from AllValues — while the same field written as
// 0 is present.
func TestRecord_AbsentDistinctFromZero(t *testing.T) {
	schema := positionalTestSchema()
	r := NewReusableRecord(schema)
	r.SetNumericAt(6, 0) // "u" = 0, present
	if v, ok := r.NumericValue("u"); !ok || v != 0 {
		t.Fatalf("present zero: NumericValue(u) = (%v,%v), want (0,true)", v, ok)
	}
	if r.IsNull("u") {
		t.Fatal("present zero reported null")
	}
	if _, ok := r.NumericValue("n"); ok {
		t.Fatal("never-written field reported present")
	}
	if !r.IsNull("n") {
		t.Fatal("never-written field not reported null/absent")
	}
	if _, in := r.AllValues()["n"]; in {
		t.Fatal("never-written field appears in AllValues")
	}
	if got := r.AllValues(); len(got) != 1 {
		t.Fatalf("AllValues = %v, want only u", got)
	}
}

// TestRecord_LayoutResolvedOncePerSchema pins name → position
// resolution to one shared layout per schema: every record over a
// schema carries the same layout pointer (no per-record or per-row
// index), and a schema whose field list changed length gets a fresh one.
func TestRecord_LayoutResolvedOncePerSchema(t *testing.T) {
	schema := positionalTestSchema()
	a := NewReusableRecord(schema)
	b := NewRecord(schema, map[string]float64{"n": 1})
	c := NewRecordWithWide(schema, nil, nil, nil)
	if a.layout != b.layout || b.layout != c.layout {
		t.Fatal("records over one schema do not share a layout")
	}
	other := positionalTestSchema()
	if NewReusableRecord(other).layout == a.layout {
		t.Fatal("distinct schema reused another schema's layout")
	}
	// Alternate schemas so the one-entry fast path misses and the weak
	// cache has to answer.
	if NewReusableRecord(schema).layout != a.layout {
		t.Fatal("weak-keyed cache did not return the existing layout")
	}
	schema.Fields = append(schema.Fields, encoding.Field{Name: "late", Type: encoding.FieldTypeU8})
	d := NewReusableRecord(schema)
	if d.layout == a.layout || len(d.vals) != len(schema.Fields) {
		t.Fatal("layout not rebuilt after the schema grew")
	}
	d.Set("late", 3)
	if d.aux != nil {
		t.Fatal("newly added schema field routed to overflow")
	}
}

// TestRecord_LayoutCacheReleasesCollectedSchemas pins that the layout
// cache does not pin schemas: once a schema is unreachable its cache
// entry is dropped.
func TestRecord_LayoutCacheReleasesCollectedSchemas(t *testing.T) {
	count := func() int {
		n := 0
		layoutCache.Range(func(_, _ any) bool { n++; return true })
		return n
	}
	before := count()
	func() {
		for i := 0; i < 8; i++ {
			s := positionalTestSchema()
			_ = NewReusableRecord(s)
		}
	}()
	if count() < before+1 {
		t.Fatalf("layouts not cached: before %d after %d", before, count())
	}
	lastLayout.Store(nil)
	deadline := time.Now().Add(5 * time.Second)
	for count() > before {
		if time.Now().After(deadline) {
			t.Fatalf("layout cache still holds %d entries (baseline %d) after the schemas were collected", count(), before)
		}
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRecord_ReuseDecodeStaysPositional pins that a record populated by
// the reuse decoder stores every schema field positionally: no overflow
// map is allocated on the hot path.
func TestRecord_ReuseDecodeStaysPositional(t *testing.T) {
	schema := indexedRecordSchema()
	raw := encodeIndexedRows(t, schema, 4)
	rec := NewReusableRecord(schema)
	rr := encx.NewRecordReader(bytes.NewReader(raw), schema)
	for row := 0; row < 4; row++ {
		if err := rr.ReadRecordReused(rec); err != nil {
			t.Fatalf("row %d: %v", row, err)
		}
		if rec.aux == nil || rec.aux.decs == nil {
			t.Fatalf("row %d: typed decimal storage not populated", row)
		}
		if rec.aux.ovVals != nil || rec.aux.ovNulls != nil || rec.aux.ovWide != nil {
			t.Fatalf("row %d: reuse decode allocated overflow maps", row)
		}
		// One decimal; set_u8 (1 word) + set_u128 (2 words).
		// Mask words ride the spare capacity of vals, past its length.
		if words := cap(rec.vals) - len(rec.vals); len(rec.aux.decs) != 1 || words != 3 {
			t.Fatalf("row %d: %d decimal slots / %d mask words, want 1 / 3",
				row, len(rec.aux.decs), words)
		}
	}
}

// TestHashJoin_PositionalCopyMatchesNamePath pins the join's positional
// fast copy against the name-resolved copy it replaces.
func TestHashJoin_PositionalCopyMatchesNamePath(t *testing.T) {
	left := positionalTestSchema()
	right := &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "rs", Type: encoding.FieldTypeSetU8, Dictionary: encoding.NewDictionary()},
	}}
	joined := &encoding.Schema{}
	joined.Fields = append(append(joined.Fields, left.Fields...), encoding.Field{Name: "r_n", Type: encoding.FieldTypeF64, Nullable: true}, encoding.Field{Name: "r_rs", Type: encoding.FieldTypeSetU8})
	rename := func(s string) string { return "r_" + s }

	l := NewRecordWithWide(left, map[string]float64{"n": 1, "cat": 2, "dec": 3.5, "attr": 9}, map[string]bool{"flag": true}, map[string]any{"dec": encoding.NewDecimal128FromInt(350)})
	r := NewRecordWithWide(right, map[string]float64{"n": 4, "rs": 1}, nil, map[string]any{"rs": uint64(1)})

	fast := newPositionalRecord(joined)
	l.copyStateInto(fast, joinIdentity, 0, true)
	r.copyStateInto(fast, rename, len(left.Fields), true)
	slow := newPositionalRecord(joined)
	l.copyStateInto(slow, joinIdentity, 0, false)
	r.copyStateInto(slow, rename, len(left.Fields), false)

	fv, fn, fw := recordMaps(fast)
	sv, sn, sw := recordMaps(slow)
	if !reflect.DeepEqual(fv, sv) || !reflect.DeepEqual(fn, sn) || !reflect.DeepEqual(fw, sw) {
		t.Fatalf("positional copy diverged from name copy:\n fast %v %v %v\n slow %v %v %v", fv, fn, fw, sv, sn, sw)
	}
	if fv["r_n"] != 4 || fv["attr"] != 9 || !fn["flag"] {
		t.Fatalf("joined state wrong: %v %v", fv, fn)
	}
}

// TestApplyAttributes_InjectionInvalidatesAllValuesCache pins the one
// in-package write that bypassed the accessors (attribute injection in
// processor.go): after a pre-attribute stage has warmed each record's
// AllValues cache (as FILTER_EXPRESSION does), an injected attribute
// label must be visible to a later ATTR_FORMULA that reads AllValues.
func TestApplyAttributes_InjectionInvalidatesAllValuesCache(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
	records := []*Record{
		NewRecord(schema, map[string]float64{"x": 1}),
		NewRecord(schema, map[string]float64{"x": 2}),
		NewRecord(schema, map[string]float64{"x": 6}),
	}
	for _, r := range records {
		_ = r.AllValues() // warm the cache, as an expression filter would
	}
	p := NewProcessor(schema)
	err := p.applyAttributes([]*types.Attribute{
		{Type: types.ATTR_FORMULA, Field: "x", Expression: "x * 10", Label: "x10"},
		{Type: types.ATTR_FORMULA, Field: "x10", Expression: "x10 + 1", Label: "x10p1"},
	}, records)
	if err != nil {
		t.Fatalf("applyAttributes: %v", err)
	}
	for i, r := range records {
		x, _ := r.NumericValue("x")
		got, ok := r.NumericValue("x10p1")
		if !ok || got != x*10+1 {
			t.Fatalf("record %d: x10p1 = (%v,%v), want %v", i, got, ok, x*10+1)
		}
		if av := r.AllValues()["x10p1"]; av != x*10+1 {
			t.Fatalf("record %d: AllValues[x10p1] = %v, want %v", i, av, x*10+1)
		}
	}
}
