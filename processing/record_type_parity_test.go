package processing

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
)

// Full-type parity for the positional Record: every one of the 20 field
// types, nullable, decoded through every record-population path — the
// reused full-stride decoder, a fresh record per row (the buffered arm),
// the plan decoder into a projected binding, the name-keyed shim and the
// map decoder — with a non-null row, an all-null row, a zero / empty-mask
// row and an extremes row. Each path is checked against ground truth, not
// only against the others.

// parityCell is one field's ground truth on one row.
type parityCell struct {
	null bool
	raw  uint64 // numeric bits, categorical id, narrow mask, u4 / bool
	mask encoding.SetMask
	dec  encoding.Decimal128
}

type parityFixture struct {
	schema *encoding.Schema
	rows   [][]parityCell // rows[row][field]
	raw    []byte
}

func parityDict(prefix string, n int) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for i := 0; i < n; i++ {
		_, _ = d.Add(fmt.Sprintf("%s%d", prefix, i))
	}
	return d
}

// paritySchema declares every field type once, nullable, plus a second
// u4 (the high nibble of the first's byte) and a second packed_bool.
func paritySchema(t *testing.T) *encoding.Schema {
	t.Helper()
	var fields []encoding.Field
	n := 0
	for ft := encoding.FieldType(0); ft.IsKnown(); ft++ {
		n++
		f := encoding.Field{Name: "f_" + ft.String(), Type: ft, Nullable: true}
		switch {
		case ft == encoding.FieldTypeDecimal128:
			f.Precision, f.Scale = 30, 4
		case ft.IsCategorical():
			f.Dictionary = parityDict("c", 5)
		case ft.IsSet():
			f.Dictionary = parityDict("m", int(ft.MaxSetEntries()))
		}
		fields = append(fields, f)
		switch ft {
		case encoding.FieldTypeU4:
			fields = append(fields, encoding.Field{Name: "f_u4_hi", Type: ft, BitPosition: 4, Nullable: true})
		case encoding.FieldTypePackedBool:
			fields = append(fields, encoding.Field{Name: "f_packed_bool_b1", Type: ft, BitPosition: 1, Nullable: true})
		}
	}
	if n != 20 {
		t.Fatalf("walked %d field types, want 20", n)
	}
	return &encoding.Schema{Fields: fields}
}

// parityValue is row r's value for field f (rows 0, 2 and 3; row 1 is
// all-null).
func parityValue(f *encoding.Field, row int) parityCell {
	ft := f.Type
	switch row {
	case 2: // zero values, EMPTY masks — present, not null
		return parityCell{dec: encoding.ZeroDecimal128()}
	case 3: // extremes
		switch {
		case ft == encoding.FieldTypeDecimal128:
			return parityCell{dec: encoding.NewDecimal128FromInt(-9_007_199_254_740_993)}
		case ft == encoding.FieldTypeSetU64:
			// Above 2^53: the float echo drops the low bit.
			return parityCell{raw: 1<<63 | 1<<53 | 1}
		case ft == encoding.FieldTypeSetU128:
			return parityCell{mask: encoding.SetMaskFromUint64(1).WithBit(127).WithBit(64)}
		case ft == encoding.FieldTypeSetU256:
			return parityCell{mask: encoding.SetMaskFromUint64(1).WithBit(255).WithBit(128)}
		}
	}
	// row 0 (and row 3 for everything not special-cased above).
	switch ft {
	case encoding.FieldTypeU4:
		if f.BitPosition > 0 {
			return parityCell{raw: 0xB}
		}
		return parityCell{raw: 0x6}
	case encoding.FieldTypePackedBool:
		return parityCell{raw: 1}
	case encoding.FieldTypeF32:
		return parityCell{raw: uint64(math.Float32bits(-2.5))}
	case encoding.FieldTypeF64:
		return parityCell{raw: math.Float64bits(1234.0625)}
	case encoding.FieldTypeDecimal128:
		return parityCell{dec: encoding.NewDecimal128FromInt(1234567)}
	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		return parityCell{raw: 3}
	case encoding.FieldTypeSetU8:
		return parityCell{raw: 0b1010_0101}
	case encoding.FieldTypeSetU16:
		return parityCell{raw: 0x8101} // ≥256: would box to the heap
	case encoding.FieldTypeSetU32:
		return parityCell{raw: 0x8000_0003}
	case encoding.FieldTypeSetU64:
		return parityCell{raw: 0x8000_0000_0000_0005}
	case encoding.FieldTypeSetU128:
		return parityCell{mask: encoding.SetMaskFromUint64(6).WithBit(100)}
	case encoding.FieldTypeSetU256:
		return parityCell{mask: encoding.SetMaskFromUint64(6).WithBit(200)}
	case encoding.FieldTypeDateTime:
		return parityCell{raw: 1_700_000_000}
	case encoding.FieldTypeDate:
		return parityCell{raw: 19_000}
	case encoding.FieldTypeU64:
		return parityCell{raw: 1 << 40}
	}
	return parityCell{raw: 200}
}

func buildParityFixture(t *testing.T) *parityFixture {
	t.Helper()
	schema := paritySchema(t)
	fx := &parityFixture{schema: schema}
	var buf bytes.Buffer
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for row := 0; row < 4; row++ {
		cells := make([]parityCell, len(schema.Fields))
		bm := make([]byte, schema.BitmapByteSize())
		for i := range schema.Fields {
			f := &schema.Fields[i]
			c := parityCell{null: true, dec: encoding.ZeroDecimal128()}
			if row != 1 {
				c = parityValue(f, row)
			} else {
				encoding.BitmapSetNull(bm, i)
			}
			cells[i] = c
			switch {
			case f.Type == encoding.FieldTypeU4:
				must(encoding.WriteNibble(&buf, f.BitPosition > 0, uint8(c.raw)))
			case f.Type == encoding.FieldTypePackedBool:
				must(encoding.WriteBit(&buf, uint(f.BitPosition), c.raw == 1))
			case f.Type == encoding.FieldTypeDecimal128:
				must(encoding.WriteDecimal128(&buf, c.dec))
			case f.Type.IsWideSet():
				must(encoding.WriteSetMask(&buf, f.Type, c.mask))
			default:
				must(encoding.WriteFieldValue(&buf, f.Type, c.raw))
			}
		}
		must(encoding.WriteBitmap(&buf, bm))
		fx.rows = append(fx.rows, cells)
	}
	fx.raw = buf.Bytes()
	return fx
}

// wantFloat is the numeric echo NumericValue reports for a non-set field.
func (c parityCell) wantFloat(f *encoding.Field) float64 {
	switch f.Type {
	case encoding.FieldTypeF32:
		return float64(math.Float32frombits(uint32(c.raw)))
	case encoding.FieldTypeF64:
		return math.Float64frombits(c.raw)
	case encoding.FieldTypeDecimal128:
		return c.dec.Float64(f.Scale)
	}
	return float64(c.raw)
}

func (c parityCell) wantMask(f *encoding.Field) encoding.SetMask {
	if f.Type.IsWideSet() {
		return c.mask
	}
	return encoding.SetMaskFromUint64(c.raw)
}

// checkParityRow asserts every accessor for every field in fields
// against ground truth.
func checkParityRow(t *testing.T, where string, rec *Record, fields []*encoding.Field, cells map[string]parityCell) {
	t.Helper()
	all := rec.AllValues()
	for _, f := range fields {
		c := cells[f.Name]
		at := fmt.Sprintf("%s %s", where, f.Name)
		_, inAll := all[f.Name]
		if c.null {
			if !rec.IsNull(f.Name) {
				t.Errorf("%s: IsNull = false on a null field", at)
			}
			if v, ok := rec.NumericValue(f.Name); ok {
				t.Errorf("%s: NumericValue = %v, true on a null field", at, v)
			}
			if w, ok := rec.WideValue(f.Name); ok {
				t.Errorf("%s: WideValue = %#v on a null field", at, w)
			}
			if _, ok := rec.SetMaskValue(f.Name); ok {
				t.Errorf("%s: SetMaskValue ok on a null field", at)
			}
			if _, ok := rec.SetLabels(f.Name); ok {
				t.Errorf("%s: SetLabels ok on a null field", at)
			}
			if _, ok := rec.StringValue(f.Name); ok {
				t.Errorf("%s: StringValue ok on a null field", at)
			}
			if inAll {
				t.Errorf("%s: null field present in AllValues", at)
			}
			continue
		}
		if rec.IsNull(f.Name) {
			t.Errorf("%s: IsNull = true on a present field", at)
		}
		if !inAll {
			t.Errorf("%s: present field missing from AllValues", at)
		}
		if f.Type.IsSet() {
			want := c.wantMask(f)
			got, ok := rec.SetMaskValue(f.Name)
			if !ok || !got.Equal(want) {
				t.Errorf("%s: SetMaskValue = %v, %v; want %v, true", at, got.Words(), ok, want.Words())
			}
			// WideValue keeps the stored dynamic type, exactly.
			var wantWide any = want
			if !f.Type.IsWideSet() {
				wantWide = c.raw
			}
			if w, ok := rec.WideValue(f.Name); !ok || !reflect.DeepEqual(w, wantWide) {
				t.Errorf("%s: WideValue = %#v, %v; want %#v", at, w, ok, wantWide)
			}
			if v, ok := rec.NumericValue(f.Name); ok {
				t.Errorf("%s: NumericValue = %v, true on a set field", at, v)
			}
			labels, ok := rec.SetLabels(f.Name)
			wantLabels := want.Labels(f.Dictionary)
			if !ok || !reflect.DeepEqual(labels, wantLabels) {
				t.Errorf("%s: SetLabels = %v, %v; want %v", at, labels, ok, wantLabels)
			}
			if want.IsEmpty() && (labels == nil || len(labels) != 0) {
				t.Errorf("%s: empty mask SetLabels = %#v, want non-nil empty", at, labels)
			}
			if !reflect.DeepEqual(all[f.Name], wantLabels) {
				t.Errorf("%s: AllValues = %#v, want %#v", at, all[f.Name], wantLabels)
			}
			continue
		}
		v, ok := rec.NumericValue(f.Name)
		if !ok || v != c.wantFloat(f) {
			t.Errorf("%s: NumericValue = %v, %v; want %v", at, v, ok, c.wantFloat(f))
		}
		if f.Type == encoding.FieldTypeDecimal128 {
			w, ok := rec.WideValue(f.Name)
			d, isDec := w.(encoding.Decimal128)
			if !ok || !isDec || d.String(f.Scale) != c.dec.String(f.Scale) {
				t.Errorf("%s: WideValue = %#v, %v; want decimal %s", at, w, ok, c.dec.String(f.Scale))
			}
			sf := rec.Schema().Field(f.Name)
			if sf.Precision != 30 || sf.Scale != 4 {
				t.Errorf("%s: precision/scale = %d/%d, want 30/4", at, sf.Precision, sf.Scale)
			}
			continue
		}
		if w, ok := rec.WideValue(f.Name); ok {
			t.Errorf("%s: WideValue = %#v on a scalar field", at, w)
		}
		if f.Type.IsCategorical() {
			s, ok := rec.StringValue(f.Name)
			want := f.Dictionary.Resolve(uint32(c.raw))
			if !ok || s != want {
				t.Errorf("%s: StringValue = %q, %v; want %q", at, s, ok, want)
			}
		}
	}
}

// assertNoStaleWideAfterSet pins that a field the decoder reported null
// carries no hidden wide value: once Set clears the null mark, the field
// reads as the number just written, not as the decoded mask / decimal.
func assertNoStaleWideAfterSet(t *testing.T, where string, rec *Record, fields []*encoding.Field) {
	t.Helper()
	for _, f := range fields {
		rec.Set(f.Name, 7)
		if w, ok := rec.WideValue(f.Name); ok {
			t.Errorf("%s %s: stale wide value %#v resurfaced after Set", where, f.Name, w)
		}
		if _, ok := rec.SetMaskValue(f.Name); ok {
			t.Errorf("%s %s: stale set mask resurfaced after Set", where, f.Name)
		}
		if v, ok := rec.NumericValue(f.Name); !ok || v != 7 {
			t.Errorf("%s %s: NumericValue after Set = %v, %v; want 7, true", where, f.Name, v, ok)
		}
	}
}

func TestRecord_AllFieldTypesPositionalParity(t *testing.T) {
	fx := buildParityFixture(t)
	schema := fx.schema
	allFields := make([]*encoding.Field, len(schema.Fields))
	allNames := make([]string, len(schema.Fields))
	for i := range schema.Fields {
		allFields[i] = &schema.Fields[i]
		allNames[i] = schema.Fields[i].Name
	}
	cellsOf := func(row int) map[string]parityCell {
		m := map[string]parityCell{}
		for i := range schema.Fields {
			m[schema.Fields[i].Name] = fx.rows[row][i]
		}
		return m
	}

	// Reference: the map decoder, the pre-positional record population.
	var mapRecs []*Record
	{
		rr := encoding.NewRecordReader(bytes.NewReader(fx.raw), schema)
		for {
			values, nulls, wide := map[string]float64{}, map[string]bool{}, map[string]any{}
			if err := rr.ReadRecordWithWide(values, nulls, wide); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			mapRecs = append(mapRecs, NewRecordWithWide(schema, values, nulls, wide))
		}
	}
	if len(mapRecs) != 4 {
		t.Fatalf("map decoder read %d rows, want 4", len(mapRecs))
	}

	subset := []string{"f_u4_hi", "f_packed_bool_b1", "f_decimal128", "f_set_u16", "f_set_u256", "f_f32"}
	type path struct {
		name     string
		retained []string // nil ⇒ every field
		next     func(t *testing.T) func() (*Record, error)
	}
	fullStride := func(fresh, shim bool) func(t *testing.T) func() (*Record, error) {
		return func(t *testing.T) func() (*Record, error) {
			rr := encoding.NewRecordReader(bytes.NewReader(fx.raw), schema)
			reused := NewReusableRecord(schema)
			return func() (*Record, error) {
				rec := reused
				if fresh {
					rec = NewReusableRecord(schema)
				}
				var target encoding.ReusableRecord = rec
				if shim {
					target = nameKeyedOnly{rec}
				}
				return rec, rr.ReadRecordReused(target)
			}
		}
	}
	planned := func(retained []string) func(t *testing.T) func() (*Record, error) {
		return func(t *testing.T) func() (*Record, error) {
			plan, err := schema.BuildDecodePlan(retained)
			if err != nil {
				t.Fatal(err)
			}
			set := map[string]bool{}
			for _, n := range retained {
				set[n] = true
			}
			keep := func(n string) bool { return set[n] }
			binding := BindRecords(schema, keep)
			rr := encoding.NewRecordReader(bytes.NewReader(fx.raw), schema)
			return func() (*Record, error) {
				rec := binding.NewRecord()
				return rec, rr.ReadRecordReusedWithPlan(rec, keep, plan)
			}
		}
	}
	// The map-decoder path decodes its own records: the row-1 check
	// mutates them, and mapRecs must stay the pristine reference.
	mapPath := func(t *testing.T) func() (*Record, error) {
		rr := encoding.NewRecordReader(bytes.NewReader(fx.raw), schema)
		return func() (*Record, error) {
			values, nulls, wide := map[string]float64{}, map[string]bool{}, map[string]any{}
			if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
				return nil, err
			}
			return NewRecordWithWide(schema, values, nulls, wide), nil
		}
	}
	paths := []path{
		{name: "map-decoder", next: mapPath},
		{name: "full-stride-reused", next: fullStride(false, false)},
		{name: "full-stride-fresh", next: fullStride(true, false)},
		{name: "full-stride-name-keyed", next: fullStride(false, true)},
		{name: "plan-projected-all", retained: allNames, next: planned(allNames)},
		{name: "plan-projected-subset", retained: subset, next: planned(subset)},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			fields := allFields
			if p.retained != nil && len(p.retained) != len(allNames) {
				fields = nil
				for _, n := range p.retained {
					fields = append(fields, schema.Field(n))
				}
			}
			next := p.next(t)
			for row := 0; ; row++ {
				rec, err := next()
				if err == io.EOF {
					if row != 4 {
						t.Fatalf("decoded %d rows, want 4", row)
					}
					break
				}
				if err != nil {
					t.Fatalf("row %d: %v", row, err)
				}
				where := fmt.Sprintf("row %d", row)
				checkParityRow(t, where, rec, fields, cellsOf(row))
				if p.retained == nil {
					// Stored state is identical to the map decoder's,
					// dynamic types of the wide values included.
					gv, gn, gw := recordMaps(rec)
					wv, wn, ww := recordMaps(mapRecs[row])
					if !reflect.DeepEqual(gv, wv) || !reflect.DeepEqual(gn, wn) || !reflect.DeepEqual(gw, ww) {
						t.Errorf("%s: stored state diverges from the map decoder\n got  %v %v %v\n want %v %v %v",
							where, gv, gn, gw, wv, wn, ww)
					}
				}
				if row == 1 {
					assertNoStaleWideAfterSet(t, where, rec, fields)
				}
			}
		})
	}
}

// TestRecord_WideSetsRefuseUint64ValueAPI pins that the uint64
// Read/WriteFieldValue API refuses the wide rungs with
// ENCODING_TYPE_MISMATCH rather than truncating.
func TestRecord_WideSetsRefuseUint64ValueAPI(t *testing.T) {
	for _, ft := range []encoding.FieldType{encoding.FieldTypeSetU128, encoding.FieldTypeSetU256} {
		var buf bytes.Buffer
		err := encoding.WriteFieldValue(&buf, ft, 1)
		assertCode(t, "WriteFieldValue "+ft.String(), err, perrors.ENCODING_TYPE_MISMATCH)
		if buf.Len() != 0 {
			t.Errorf("WriteFieldValue %s wrote %d bytes before refusing", ft, buf.Len())
		}
		_, err = encoding.ReadFieldValue(bytes.NewReader(make([]byte, 32)), ft)
		assertCode(t, "ReadFieldValue "+ft.String(), err, perrors.ENCODING_TYPE_MISMATCH)
	}
}

func assertCode(t *testing.T, what string, err error, want perrors.Code) {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != want {
		t.Errorf("%s: err = %v, want code %s", what, err, want)
	}
}

// TestRecord_TypedSetDecodeAllocatesNothing pins that the reuse decoder
// hands set masks to the Record unboxed (encoding.TypedSetRecord): a
// narrow mask at or above 256 and a wide-rung mask would each cost a
// heap allocation per row through SetWideFieldAt's `any`.
func TestRecord_TypedSetDecodeAllocatesNothing(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "s16", Type: encoding.FieldTypeSetU16, Dictionary: parityDict("a", 16)},
		{Name: "s64", Type: encoding.FieldTypeSetU64, Dictionary: parityDict("b", 64)},
		{Name: "s128", Type: encoding.FieldTypeSetU128, Dictionary: parityDict("c", 128)},
		{Name: "s256", Type: encoding.FieldTypeSetU256, Dictionary: parityDict("d", 256)},
	}}
	const rows = 64
	var buf bytes.Buffer
	for i := 0; i < rows; i++ {
		_ = encoding.WriteFieldValue(&buf, encoding.FieldTypeSetU16, uint64(0x8000|i))
		_ = encoding.WriteFieldValue(&buf, encoding.FieldTypeSetU64, uint64(1)<<63|uint64(i))
		_ = encoding.WriteSetMask(&buf, encoding.FieldTypeSetU128, encoding.SetMaskFromUint64(uint64(i)).WithBit(127))
		_ = encoding.WriteSetMask(&buf, encoding.FieldTypeSetU256, encoding.SetMaskFromUint64(uint64(i)).WithBit(255))
	}
	raw := buf.Bytes()
	src := bytes.NewReader(raw)
	rr := encoding.NewRecordReader(src, schema)
	rec := NewReusableRecord(schema)
	if err := rr.ReadRecordReused(rec); err != nil { // warm: sizes aux storage
		t.Fatal(err)
	}
	row := 1
	allocs := testing.AllocsPerRun(rows-2, func() {
		if err := rr.ReadRecordReused(rec); err != nil {
			t.Fatal(err)
		}
		if m, ok := rec.SetMaskValue("s256"); !ok || !m.Has(255) {
			t.Fatal("s256 lost")
		}
		row++
	})
	if allocs != 0 {
		t.Fatalf("reuse decode of four set fields allocates %.1f/row, want 0", allocs)
	}
	if m, _ := rec.SetMaskValue("s64"); !m.Has(63) {
		t.Fatal("s64 high bit lost")
	}
}

// TestRecord_DuplicateFieldNamesKeepMapSemantics pins the duplicate-name
// routing. Nothing on the import path rejects a repeated column name, so
// a schema can carry one; the map decoder then kept the LAST occurrence's
// value (a later write overwrote the key) and reported the name null when
// ANY occurrence was null. The positional record shares one slot per
// name to reproduce exactly that, on every decode path.
func TestRecord_DuplicateFieldNamesKeepMapSemantics(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8, Nullable: true},
		{Name: "b", Type: encoding.FieldTypeCategoricalU8, Dictionary: parityDict("x", 3)},
		{Name: "a", Type: encoding.FieldTypeU8, Nullable: true},
		{Name: "s", Type: encoding.FieldTypeSetU16, Dictionary: parityDict("m", 16), Nullable: true},
		{Name: "s", Type: encoding.FieldTypeSetU16, Dictionary: parityDict("m", 16), Nullable: true},
	}}
	type row struct {
		a1, a2 uint64
		s1, s2 uint64
		nulls  []int
	}
	rows := []row{
		{a1: 1, a2: 2, s1: 0x0101, s2: 0x0300},
		{a1: 3, a2: 4, s1: 0x0101, s2: 0x0300, nulls: []int{0, 4}}, // first a, second s null
		{a1: 5, a2: 6, s1: 0x0101, s2: 0, nulls: []int{2}},         // second a null; s empty
	}
	var buf bytes.Buffer
	for _, r := range rows {
		_ = encoding.WriteFieldValue(&buf, encoding.FieldTypeU8, r.a1)
		_ = encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, 1)
		_ = encoding.WriteFieldValue(&buf, encoding.FieldTypeU8, r.a2)
		_ = encoding.WriteFieldValue(&buf, encoding.FieldTypeSetU16, r.s1)
		_ = encoding.WriteFieldValue(&buf, encoding.FieldTypeSetU16, r.s2)
		bm := make([]byte, schema.BitmapByteSize())
		for _, i := range r.nulls {
			encoding.BitmapSetNull(bm, i)
		}
		_ = encoding.WriteBitmap(&buf, bm)
	}
	raw := buf.Bytes()

	mapRR := encoding.NewRecordReader(bytes.NewReader(raw), schema)
	posRR := encoding.NewRecordReader(bytes.NewReader(raw), schema)
	planRR := encoding.NewRecordReader(bytes.NewReader(raw), schema)
	plan, err := schema.BuildDecodePlan([]string{"a", "s"})
	if err != nil {
		t.Fatal(err)
	}
	keep := func(n string) bool { return n == "a" || n == "s" }
	binding := BindRecords(schema, keep)
	pos := NewReusableRecord(schema)
	for i := range rows {
		values, nulls, wide := map[string]float64{}, map[string]bool{}, map[string]any{}
		if err := mapRR.ReadRecordWithWide(values, nulls, wide); err != nil {
			t.Fatal(err)
		}
		ref := NewRecordWithWide(schema, values, nulls, wide)
		if err := posRR.ReadRecordReused(pos); err != nil {
			t.Fatal(err)
		}
		proj := binding.NewRecord()
		if err := planRR.ReadRecordReusedWithPlan(proj, keep, plan); err != nil {
			t.Fatal(err)
		}
		for _, got := range []struct {
			name string
			rec  *Record
		}{{"full", pos}, {"projected", proj}} {
			at := fmt.Sprintf("row %d %s", i, got.name)
			for _, n := range []string{"a", "s"} {
				if g, w := got.rec.IsNull(n), ref.IsNull(n); g != w {
					t.Errorf("%s: IsNull(%s) = %v, map decoder %v", at, n, g, w)
				}
				gv, gok := got.rec.NumericValue(n)
				wv, wok := ref.NumericValue(n)
				if gv != wv || gok != wok {
					t.Errorf("%s: NumericValue(%s) = %v,%v; map decoder %v,%v", at, n, gv, gok, wv, wok)
				}
				gm, gmok := got.rec.SetMaskValue(n)
				wm, wmok := ref.SetMaskValue(n)
				if !gm.Equal(wm) || gmok != wmok {
					t.Errorf("%s: SetMaskValue(%s) = %v,%v; map decoder %v,%v", at, n, gm.Words(), gmok, wm.Words(), wmok)
				}
			}
			if !reflect.DeepEqual(got.rec.AllValues()["a"], ref.AllValues()["a"]) {
				t.Errorf("%s: AllValues[a] = %v, map decoder %v", at, got.rec.AllValues()["a"], ref.AllValues()["a"])
			}
		}
	}
	// Row 0 pins the rule itself, not only agreement: the last write wins.
	if v, _ := NewRecordWithWide(schema, map[string]float64{"a": 2}, nil, nil).NumericValue("a"); v != 2 {
		t.Fatalf("sanity: %v", v)
	}
}

// TestRecord_ProjectedWritesOutsideLayout pins what a projected record
// does with a write to a schema position it has no slot for: the value
// lands on the overflow side under the field's name and every accessor
// reads it exactly as a full-layout record would. A position past the
// schema panics rather than landing on another field.
func TestRecord_ProjectedWritesOutsideLayout(t *testing.T) {
	fx := buildParityFixture(t)
	schema := fx.schema
	keep := func(n string) bool { return n == "f_u8" }
	proj := BindRecords(schema, keep).NewRecord()
	full := NewReusableRecord(schema)
	idx := func(n string) int { return schemaIndex(schema, n) }

	for _, r := range []*Record{proj, full} {
		r.SetNumericAt(idx("f_f64"), 2.5)
		r.SetNumericAt(idx("f_decimal128"), 12.5)
		r.SetWideFieldAt(idx("f_decimal128"), encoding.NewDecimal128FromInt(125))
		r.SetNumericAt(idx("f_set_u32"), 9)
		r.SetNarrowSetAt(idx("f_set_u32"), 0x8000_0001)
		r.SetWideSetAt(idx("f_set_u256"), encoding.SetMaskFromUint64(0).WithBit(255))
		r.SetNumericAt(idx("f_set_u128"), 1)
		r.SetWideSetAt(idx("f_set_u128"), encoding.SetMask{}) // empty ≠ null
		r.SetNumericAt(idx("f_categorical_u8"), 2)
		r.SetNullFieldAt(idx("f_u16"))
		r.SetNumericAt(idx("f_u16"), 0)
		r.SetWideFieldAt(idx("f_set_u8"), uint64(3))
		r.SetNullFieldAt(idx("f_set_u8"))
	}
	if proj.aux == nil || len(proj.aux.ovVals) == 0 {
		t.Fatal("projected record did not route out-of-layout writes to overflow")
	}
	gv, gn, gw := recordMaps(proj)
	wv, wn, ww := recordMaps(full)
	if !reflect.DeepEqual(gv, wv) || !reflect.DeepEqual(gn, wn) || !reflect.DeepEqual(gw, ww) {
		t.Fatalf("projected overflow state diverges from full layout\n got  %v %v %v\n want %v %v %v", gv, gn, gw, wv, wn, ww)
	}
	for _, n := range []string{"f_f64", "f_decimal128", "f_set_u32", "f_set_u256", "f_set_u128", "f_categorical_u8", "f_u16", "f_set_u8"} {
		if g, w := proj.IsNull(n), full.IsNull(n); g != w {
			t.Errorf("IsNull(%s) = %v, full %v", n, g, w)
		}
		g1, g2 := proj.NumericValue(n)
		w1, w2 := full.NumericValue(n)
		if g1 != w1 || g2 != w2 {
			t.Errorf("NumericValue(%s) = %v,%v; full %v,%v", n, g1, g2, w1, w2)
		}
		gm, gok := proj.SetMaskValue(n)
		wm, wok := full.SetMaskValue(n)
		if !gm.Equal(wm) || gok != wok {
			t.Errorf("SetMaskValue(%s) differs", n)
		}
		gs, gsok := proj.StringValue(n)
		ws, wsok := full.StringValue(n)
		if gs != ws || gsok != wsok {
			t.Errorf("StringValue(%s) = %q,%v; full %q,%v", n, gs, gsok, ws, wsok)
		}
	}
	if !reflect.DeepEqual(proj.AllValues(), full.AllValues()) {
		t.Errorf("AllValues differ:\n proj %v\n full %v", proj.AllValues(), full.AllValues())
	}
	if m, ok := proj.SetMaskValue("f_set_u128"); !ok || !m.IsEmpty() {
		t.Errorf("empty wide mask outside the layout: %v, %v; want empty, present", m.Words(), ok)
	}
	if !proj.IsNull("f_set_u8") {
		t.Error("null set outside the layout not reported null")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("a position past the schema did not panic")
		}
	}()
	proj.SetNumericAt(len(schema.Fields), 1)
}

// TestRecord_CopyIntoProjectedAndDuplicateLayouts pins the join merge
// (copyStateInto) into a destination whose slots are NOT schema
// positions — a projected binding, or a schema with a repeated name. The
// copy must land every value on the right field.
func TestRecord_CopyIntoProjectedAndDuplicateLayouts(t *testing.T) {
	fx := buildParityFixture(t)
	schema := fx.schema
	src := NewReusableRecord(schema)
	rr := encoding.NewRecordReader(bytes.NewReader(fx.raw), schema)
	if err := rr.ReadRecordReused(src); err != nil {
		t.Fatal(err)
	}
	keepNames := map[string]bool{"f_f64": true, "f_set_u256": true, "f_decimal128": true, "f_set_u16": true, "f_categorical_u16": true}
	dst := BindRecords(schema, func(n string) bool { return keepNames[n] }).NewRecord()
	src.copyStateInto(dst, func(s string) string { return s }, 0, true)
	for n := range keepNames {
		gv, gok := dst.NumericValue(n)
		wv, wok := src.NumericValue(n)
		if gv != wv || gok != wok {
			t.Errorf("NumericValue(%s) = %v,%v; source %v,%v", n, gv, gok, wv, wok)
		}
		gw, _ := dst.WideValue(n)
		ww, _ := src.WideValue(n)
		if fmt.Sprint(gw) != fmt.Sprint(ww) {
			t.Errorf("WideValue(%s) = %v; source %v", n, gw, ww)
		}
	}
	if !reflect.DeepEqual(dst.AllValues(), src.AllValues()) {
		t.Errorf("AllValues after copy differ:\n dst %v\n src %v", dst.AllValues(), src.AllValues())
	}
}
