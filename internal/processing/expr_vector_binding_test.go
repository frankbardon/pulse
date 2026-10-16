package processing

import (
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// Named-vector binding (U26 E2-S1): a Request.Vectors entry an
// expression names binds as a []float64 of its members.

func vecBindSchemaAndVectors(t *testing.T) ([]vectors.Resolved, func(vals map[string]float64, nulls ...string) *Record) {
	t.Helper()
	s := mkSchema("a", "b", "c", "x")
	vecs, err := vectors.Resolve([]types.VectorSpec{
		{Name: "v", Fields: []string{"c", "a", "b"}}, // caller order is axis order
		{Name: "w", Fields: []string{"x"}},
	}, s)
	if err != nil {
		t.Fatal(err)
	}
	rec := func(vals map[string]float64, nulls ...string) *Record {
		n := map[string]bool{}
		for _, name := range nulls {
			n[name] = true
		}
		return NewRecordWithNulls(s, vals, n)
	}
	return vecs, rec
}

// TestExprVectors_BindingSemantics: member order, a null member as a
// NaN element, a whole-row-null vector as all-NaN (never nil, never a
// null-input row), and each consumer reading it.
func TestExprVectors_BindingSemantics(t *testing.T) {
	vecs, rec := vecBindSchemaAndVectors(t)
	s := mkSchema("a", "b", "c", "x")
	full := rec(map[string]float64{"a": 1, "b": 2, "c": 6, "x": 9})
	part := rec(map[string]float64{"a": 1, "c": 6, "x": 9}, "b")
	none := rec(map[string]float64{"x": 9}, "a", "b", "c")

	cases := []struct {
		src             string
		full, part, non any
	}{
		{"v[0]", 6.0, 6.0, math.NaN()}, // axis order: c, a, b
		{"len(v)", 3, 3, 3},
		{"vcount(v)", 3, 2, 0},
		{"vsum(v)", 9.0, 7.0, math.NaN()},
		{"v[2] != v[2]", false, true, true}, // a null member is NaN
		{"v == nil", false, false, false},   // never nil
		{"vcount(v) >= 3 ? vmean(v) : nil", 3.0, nil, nil},
	}
	for _, tc := range cases {
		prog, err := newExprProgram(tc.src, "formula", s, vecs, nil, exprenv.Options())
		if err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		if len(prog.vecs) != 1 || prog.vecs[0].name != "v" {
			t.Fatalf("%q binds %+v, want only v (w is unreferenced)", tc.src, prog.vecs)
		}
		for i, c := range []struct {
			r    *Record
			want any
		}{{full, tc.full}, {part, tc.part}, {none, tc.non}} {
			got, nullInput, err := prog.runRecord(c.r)
			if err != nil || nullInput {
				t.Fatalf("%q row %d: err %v nullInput %v", tc.src, i, err, nullInput)
			}
			if !sameExprValue(got, c.want) {
				t.Errorf("%q row %d = %#v, want %#v", tc.src, i, got, c.want)
			}
		}
	}
}

func sameExprValue(got, want any) bool {
	if w, ok := want.(float64); ok && math.IsNaN(w) {
		g, ok := got.(float64)
		return ok && math.IsNaN(g)
	}
	return got == want
}

// TestExprVectors_UnreferencedBindsNothing: no vector named, no binding
// and no frame work — the no-vector path is the pre-binding program.
func TestExprVectors_UnreferencedBindsNothing(t *testing.T) {
	vecs, rec := vecBindSchemaAndVectors(t)
	prog, err := newExprProgram("a + b", "formula", mkSchema("a", "b", "c", "x"), vecs, nil, exprenv.Options())
	if err != nil {
		t.Fatal(err)
	}
	if prog.vecs != nil || prog.frames.New != nil {
		t.Fatalf("an unreferenced vector bound: %+v", prog.vecs)
	}
	r := rec(map[string]float64{"a": 1, "b": 2, "c": 3, "x": 4})
	if out, _, err := prog.runRecord(r); err != nil || out != 3.0 {
		t.Fatalf("a + b = %v, %v", out, err)
	}
}

// TestExprVectors_FillAllocationFree: after warm-up, binding a row's
// vectors reuses the pooled frame — no allocation per row.
func TestExprVectors_FillAllocationFree(t *testing.T) {
	vecs, rec := vecBindSchemaAndVectors(t)
	prog, err := newExprProgram("vsum(v) + vsum(w)", "formula", mkSchema("a", "b", "c", "x"), vecs, nil, exprenv.Options())
	if err != nil {
		t.Fatal(err)
	}
	r := rec(map[string]float64{"a": 1, "c": 3, "x": 4}, "b")
	env := map[string]any{}
	prog.releaseVectors(prog.fillVectors(env, r))
	allocs := testing.AllocsPerRun(200, func() {
		prog.releaseVectors(prog.fillVectors(env, r))
	})
	if allocs != 0 {
		t.Fatalf("fillVectors allocates %v per row, want 0", allocs)
	}
}

// TestExprVectors_TopLevelVectorRefused: a vector as the whole
// expression is refused at build under the vector-result rule.
func TestExprVectors_TopLevelVectorRefused(t *testing.T) {
	vecs, _ := vecBindSchemaAndVectors(t)
	for _, what := range []string{"formula", "filter", "formula total() argument"} {
		_, err := newExprProgram("v", what, mkSchema("a", "b", "c", "x"), vecs, nil, exprenv.Options())
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["expression_site"] != what {
			t.Errorf("%s: err %v, want the PROCESSING_CONFIG vector-result refusal", what, err)
		}
	}
}

// TestFormula_NilResultIsNull: a nil formula result is AttributeNull —
// distinct from an ordinary NaN — which every write arm records as a
// null; an ordinary NaN stays a value unless the computer EmitsNull.
func TestFormula_NilResultIsNull(t *testing.T) {
	s := mkSchema("a", "b")
	r := NewRecordWithNulls(s, map[string]float64{"b": 0}, map[string]bool{"a": true})
	for _, tc := range []struct {
		src      string
		wantNull bool
	}{
		{"a", true},                   // a bare null field
		{"a ?? nil", true},            // an explicit nil
		{"b > 0 ? 1 : nil", true},     // the guard's nil arm
		{"b / b", false},              // 0/0: an ordinary NaN, a value
		{"a == nil ? 2 : nil", false}, // the number arm
	} {
		comp, err := newFormulaAttribute(&types.Attribute{Type: types.ATTR_FORMULA, Expression: tc.src, Label: "f"}, s)
		if err != nil {
			t.Fatal(err)
		}
		if err := BindAttribute(comp, nil, nil); err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		got, err := comp.(*formulaAttribute).Row(r, "")
		if err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		if IsAttributeNull(got) != tc.wantNull {
			t.Errorf("%q = %v, IsAttributeNull %v want %v", tc.src, got, IsAttributeNull(got), tc.wantNull)
		}
		out := NewRecordWithNulls(s, map[string]float64{"b": 0}, nil)
		SetAttributeOutput(out, "f", got, EmitsNull(comp))
		if out.IsNull("f") != tc.wantNull {
			t.Errorf("%q written null = %v, want %v", tc.src, out.IsNull("f"), tc.wantNull)
		}
	}
	if IsAttributeNull(math.NaN()) {
		t.Fatal("an ordinary NaN reads as AttributeNull")
	}
}

// TestAppendExprIdents_ExpandsVectors: an identifier naming a request
// vector projects its members (and only the known ones); a field keeps
// its own name. Without the expansion the members would decode null.
func TestAppendExprIdents_ExpandsVectors(t *testing.T) {
	s := mkSchema("a", "b", "c", "x", "unused")
	known := schemaFieldSet(s)
	vecs := map[string][]string{"v": {"c", "a"}, "w": {"b"}}
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{"vmean(v) > x", []string{"a", "c", "x"}},
		{"total(vsum(v))", []string{"a", "c"}},
		{"x > 0 ? (vcount(v) > 1 ? vmax(v) : nil) : vsum(w)", []string{"a", "b", "c", "x"}},
		{"x", []string{"x"}},
	} {
		out := NewFieldSet(4)
		if !appendExprIdents(out, tc.src, known, vecs) {
			t.Fatalf("%q did not parse", tc.src)
		}
		if got := sortedFields(out); !equalStrings(got, tc.want) {
			t.Errorf("%q projects %v, want %v", tc.src, got, tc.want)
		}
	}
}

// TestExprVectors_DeferredAndCachedPaths: a vector beside a non-schema
// name (an earlier attribute's label: the compile defers) and on a
// record whose AllValues map is already built both bind the vector —
// into a pooled copy, never into the record's shared cache.
func TestExprVectors_DeferredAndCachedPaths(t *testing.T) {
	vecs, rec := vecBindSchemaAndVectors(t)
	prog, err := newExprProgram("vsum(v) + extra", "formula", mkSchema("a", "b", "c", "x"), vecs, nil, exprenv.Options())
	if err != nil {
		t.Fatal(err)
	}
	if prog.typed.Load() != nil {
		t.Fatal("a non-schema name must defer the compile")
	}
	for i := range 2 { // first row compiles, second runs the cached program
		r := rec(map[string]float64{"a": 1, "c": 3, "x": 4}, "b")
		r.injectValue("extra", 10)
		out, nullInput, err := prog.runRecord(r)
		if err != nil || nullInput || out != 14.0 {
			t.Fatalf("row %d: %v, %v, %v; want 14", i, out, nullInput, err)
		}
		if _, leaked := r.AllValues()["v"]; leaked {
			t.Fatalf("row %d: the vector leaked into the record's AllValues cache", i)
		}
	}
}

// TestExprVectors_FilterNotPrecomputed: a filter naming a vector is not
// a pure predicate of one group's member fields, so it is never moved
// onto the per-entry precompute path (which would not bind it).
func TestExprVectors_FilterNotPrecomputed(t *testing.T) {
	inputs, ok := exprPureInputs("vmean(battery) > 1", nil)
	if !ok || len(inputs) != 1 || inputs[0] != "battery" {
		t.Fatalf("exprPureInputs = %v, %v", inputs, ok)
	}
	if g, _ := precomputeGroup(mkSchema("b1", "b2"), inputs); g >= 0 {
		t.Fatalf("a vector name resolved to precompute group %d", g)
	}
}

// BenchmarkVectorFormula8 is informational (no gate): the per-row cost
// of an 8-member battery formula — bind the named vector, count its
// valid members and take their mean — beside the same mean written
// out over the eight fields.
func BenchmarkVectorFormula8(b *testing.B) {
	names := []string{"q1", "q2", "q3", "q4", "q5", "q6", "q7", "q8"}
	s := mkSchema(names...)
	vecs, verr := vectors.Resolve([]types.VectorSpec{{Name: "battery", Fields: names}}, s)
	if verr != nil {
		b.Fatal(verr)
	}
	vals := map[string]float64{}
	for i, n := range names {
		vals[n] = float64(i%5 + 1)
	}
	r := NewRecordWithNulls(s, vals, map[string]bool{"q3": true})
	for _, bc := range []struct{ name, src string }{
		{"vector", "vcount(battery) >= 6 ? vmean(battery) : nil"},
		{"scalar", "(q1 + q2 + q4 + q5 + q6 + q7 + q8) / 7"},
	} {
		b.Run(bc.name, func(b *testing.B) {
			prog, err := newExprProgram(bc.src, "formula", s, vecs, nil, exprenv.Options())
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := prog.runRecord(r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
