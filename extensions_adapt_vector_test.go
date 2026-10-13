package pulse

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// extVecRecordSchema: emb a native vec_f64 dim 3, nemb a null native
// vector, x a scalar, "x[2]" a real column whose name looks like an
// element member, raw a plain (non-vector) f64.
func extVecRecord() (*processing.Record, *processing.Record) {
	s := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "emb", Type: encoding.FieldTypeVecF64, Nullable: true},
			{Name: "nemb", Type: encoding.FieldTypeVecF64, Nullable: true},
			{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
			{Name: "x[2]", Type: encoding.FieldTypeF64, Nullable: true},
			{Name: "raw", Type: encoding.FieldTypeF64, Nullable: true},
		},
		Vectors: []encoding.VectorField{{FieldIndex: 0, Dim: 3}, {FieldIndex: 1, Dim: 3}},
	}
	rec := processing.NewRecordWithWide(s,
		map[string]float64{"x": 1.5, "x[2]": 7, "raw": 2},
		map[string]bool{"nemb": true},
		map[string]any{"emb": []float64{10, math.NaN(), 30}})
	nullCol := processing.NewRecordWithWide(s, map[string]float64{"x": 1.5},
		map[string]bool{"x[2]": true, "nemb": true},
		map[string]any{"emb": []float64{10, math.NaN(), 30}})
	return rec, nullCol
}

// TestExtRecord_NativeElementMembers: the extend view of an engine row
// resolves "f[k]" (1-based, canonical) on a native vector field; a NaN
// element and a null vector are a null member (the shared fill's rule);
// every other name keeps the plain lookup.
func TestExtRecord_NativeElementMembers(t *testing.T) {
	rec, nullCol := extVecRecord()
	var r extend.Record = extRecord{rec}
	cases := []struct {
		name   string
		want   float64
		wantOK bool
	}{
		{"emb[1]", 10, true},
		{"emb[3]", 30, true},
		{"emb[2]", 0, false},  // NaN element → null member
		{"emb[0]", 0, false},  // out of range (1-based)
		{"emb[4]", 0, false},  // out of range (> dim)
		{"emb[01]", 0, false}, // non-canonical spelling
		{"emb[+1]", 0, false},
		{"emb", 0, false},     // the vector itself stays NumericValue-false
		{"nemb[1]", 0, false}, // null vector → every element null
		{"x", 1.5, true},
		{"x[2]", 7, true},     // a real column named like a member: plain lookup
		{"raw[1]", 0, false},  // not a vector: no element
		{"nope[1]", 0, false}, // missing field
	}
	for _, c := range cases {
		got, ok := r.NumericValue(c.name)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("NumericValue(%q) = %v, %v; want %v, %v", c.name, got, ok, c.want, c.wantOK)
		}
		// IsNull agrees with NumericValue — except on the vector field
		// itself, which holds a (non-numeric) value.
		if null := r.IsNull(c.name); null == c.wantOK && c.name != "emb" {
			t.Errorf("IsNull(%q) = %v; want %v", c.name, null, !c.wantOK)
		}
		// The engine row itself is unchanged for every name but the
		// element members.
		if _, eok := rec.NumericValue(c.name); eok != ok && c.name != "emb[1]" && c.name != "emb[3]" {
			t.Errorf("engine NumericValue(%q) ok = %v, view %v", c.name, eok, ok)
		}
	}
	// A null real column "x[2]" stays null: the element fallback reads
	// a vector named "x", which does not exist.
	nr := extRecord{nullCol}
	if _, ok := nr.NumericValue("x[2]"); ok || !nr.IsNull("x[2]") {
		t.Error(`null column "x[2]" reads as present`)
	}
}

// TestExtRecord_ElementReadAllocatesNothing: an element read through
// the extend view — including wrapping the row into the interface — is
// zero-alloc on the per-row hot path.
func TestExtRecord_ElementReadAllocatesNothing(t *testing.T) {
	rec, _ := extVecRecord()
	rows := recordRows{rec}
	var x float64
	var ok bool
	if allocs := testing.AllocsPerRun(100, func() {
		r := rows.At(0)
		x, ok = r.NumericValue("emb[3]")
		_ = r.IsNull("emb[1]")
	}); allocs != 0 {
		t.Errorf("element read allocates %v per row, want 0", allocs)
	}
	if !ok || x != 30 {
		t.Errorf("NumericValue(emb[3]) = %v, %v; want 30, true", x, ok)
	}
}

// extVecProbe is every row-reading extend sibling at once; each call
// records whether the element member "emb[1]" read as present.
type extVecProbe struct{ seen []bool }

func (p *extVecProbe) note(r extend.Record) {
	_, ok := r.NumericValue("emb[1]")
	p.seen = append(p.seen, ok && !r.IsNull("emb[1]"))
}
func (p *extVecProbe) rows(rows extend.Rows) {
	for i := 0; i < rows.Len(); i++ {
		p.note(rows.At(i))
	}
}
func (p *extVecProbe) Aggregate(rows extend.Rows, _ string) (float64, error) {
	p.rows(rows)
	return 0, nil
}
func (p *extVecProbe) UpdateRow(r extend.Record, _ string) error { p.note(r); return nil }
func (p *extVecProbe) Finalize() (float64, error)                { return 0, nil }
func (p *extVecProbe) KeyForRow(r extend.Record, _ string) (string, bool, error) {
	p.note(r)
	return "k", true, nil
}
func (p *extVecProbe) KeysForRow(r extend.Record, _ string) ([]string, bool, error) {
	p.note(r)
	return []string{"k"}, true, nil
}
func (p *extVecProbe) Group(rows extend.Rows, _ string) (map[string][]int, error) {
	p.rows(rows)
	return nil, nil
}
func (p *extVecProbe) Row(r extend.Record, _ string) (float64, error) { p.note(r); return 0, nil }
func (p *extVecProbe) PrePass(r extend.Record, _ string) error        { p.note(r); return nil }
func (p *extVecProbe) Compute(rows extend.Rows, _ string) ([]float64, error) {
	p.rows(rows)
	return make([]float64, rows.Len()), nil
}
func (p *extVecProbe) EmitRow(r extend.Record, _ string) (map[string]extend.FeatureOutput, error) {
	p.note(r)
	return nil, nil
}

type extVecTestProbe struct{ p *extVecProbe }

func (t extVecTestProbe) UpdateRow(r extend.Record) error { t.p.note(r); return nil }
func (t extVecTestProbe) Finalize() (*types.TestResult, error) {
	return nil, nil
}

// TestExtRecord_EveryHandoffResolvesElements: every adapter that hands
// an engine row to an extend operator — aggregators (buffered, online,
// weighted), groupers (buffered, streaming, multi-key), filterers,
// attributes (buffered, row-local, two-pass; weighted rows with a valid
// AND an invalid weight), row tests and features — passes the extRecord
// view, so a native element member reads present on every one.
func TestExtRecord_EveryHandoffResolvesElements(t *testing.T) {
	rec, _ := extVecRecord()
	records := []*processing.Record{rec}
	// x = 1.5: a valid probability weight, an invalid frequency one.
	valid := &slotWeighting{field: "x", kind: types.WeightKindProbability}
	invalid := &slotWeighting{field: "x", kind: types.WeightKindFrequency}
	calls := map[string]func(p *extVecProbe){
		"agg buffered":          func(p *extVecProbe) { _, _ = aggCore{inner: p}.Aggregate(records, "") },
		"agg buffered weighted": func(p *extVecProbe) { _, _ = aggCore{inner: p, wt: valid}.Aggregate(records, "") },
		"agg online":            func(p *extVecProbe) { _ = aggOnline{online: p}.UpdateRow(rec, "") },
		"agg online weighted":   func(p *extVecProbe) { _ = aggOnline{online: p, view: &rowView{s: valid}}.UpdateRow(rec, "") },
		"grp buffered":          func(p *extVecProbe) { _, _ = grpCore{inner: p}.Group(records, "") },
		"grp streaming":         func(p *extVecProbe) { _, _, _ = grpStreaming{streaming: p}.KeyForRow(rec, "f") },
		"grp multi":             func(p *extVecProbe) { _, _, _ = grpMulti{multi: p}.KeysForRow(rec, "f") },
		"filter": func(p *extVecProbe) {
			fn, _ := fltCore{inner: extVecFilter{p}}.Build(&types.Filterer{}, nil)
			_, _ = fn(rec)
		},
		"attr buffered":                func(p *extVecProbe) { _, _ = attrCore{inner: p}.Compute(records, "") },
		"attr buffered valid weight":   func(p *extVecProbe) { _, _ = attrCore{inner: p, wt: valid}.Compute(records, "") },
		"attr buffered invalid weight": func(p *extVecProbe) { _, _ = attrCore{inner: p, wt: invalid}.Compute(records, "") },
		"attr row":                     func(p *extVecProbe) { _, _ = attrRow{row: p}.Row(rec, "") },
		"attr row valid weight":        func(p *extVecProbe) { _, _ = attrRow{row: p, view: &rowView{s: valid}}.Row(rec, "") },
		"attr row invalid weight":      func(p *extVecProbe) { _, _ = attrRow{row: p, view: &rowView{s: invalid}}.Row(rec, "") },
		"attr prepass":                 func(p *extVecProbe) { _ = attrTwoPass{two: extVecTwoPass{p}}.PrePass(rec, "") },
		"row test":                     func(p *extVecProbe) { _ = (&rowTestAdapted{inner: extVecTestProbe{p}}).UpdateRow(rec) },
		"row test weighted": func(p *extVecProbe) {
			_ = (&rowTestAdapted{inner: extVecTestProbe{p}, view: &rowView{s: valid}}).UpdateRow(rec)
		},
		"feature rows":      func(p *extVecProbe) { p.rows(featureRows{rec}) },
		"feature streaming": func(p *extVecProbe) { _ = featStreaming{streaming: extVecTwoPass{p}}.PrePass(rec, "") },
	}
	for name, call := range calls {
		p := &extVecProbe{}
		call(p)
		if len(p.seen) != 1 || !p.seen[0] {
			t.Errorf("%s: emb[1] present per call = %v, want [true]", name, p.seen)
		}
	}
}

type extVecFilter struct{ p *extVecProbe }

func (f extVecFilter) Build(*types.Filterer, *encoding.Schema) (extend.FilterFunc, error) {
	return func(r extend.Record) (bool, error) { f.p.note(r); return true, nil }, nil
}

type extVecTwoPass struct{ *extVecProbe }

func (extVecTwoPass) Finalize() error { return nil }
