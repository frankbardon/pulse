package processing

import (
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// refTables labels "t" a, b, c — the vector v of vecBindSchemaAndVectors
// is c, a, b, so every alignment below is a real reordering.
func refTables() vectors.ReferenceTables {
	return vectors.NewReferenceTables(map[string]types.ReferenceVectorTable{
		"t": {Labels: []string{"a", "b", "c"}, Profiles: map[string][]float64{"k": {10, 20, 30}, "9": {1, 2, 3}}},
	})
}

// TestRef_ProgramAlignment: ref() inside a pair is aligned to the named
// vector's label order — a literal key folded to a constant at build, a
// numeric field key resolved per row — and a null key is the row's null
// input, never a refusal.
func TestRef_ProgramAlignment(t *testing.T) {
	vecs, rec := vecBindSchemaAndVectors(t)
	s := mkSchema("a", "b", "c", "x")
	row := rec(map[string]float64{"a": 1, "b": 2, "c": 6, "x": 9}) // v = [6, 1, 2]
	cases := []struct {
		src  string
		want float64
	}{
		{`dot(v, ref("t", "k"))`, 6*30 + 1*10 + 2*20},
		{`dot(v, ref("t", x))`, 6*3 + 1*1 + 2*2},
		{`dot(vcenter(ref("t", "k")), v)`, 6*10 + 1*-10 + 2*0},
		{`ref("t", "k")[0] + 0 * x`, 10}, // outside a pair: the table's order
	}
	for _, tc := range cases {
		prog, err := newExprProgram(tc.src, "formula", s, vecs, refTables(), setExprOptions())
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		got, nullInput, err := prog.runRecord(row)
		if err != nil || nullInput {
			t.Fatalf("%s: err %v nullInput %v", tc.src, err, nullInput)
		}
		if got != tc.want {
			t.Errorf("%s = %v, want %v", tc.src, got, tc.want)
		}
	}

	// The literal key is folded: the aligned profile is a program
	// constant, so a row reads nothing for it.
	prog, err := newExprProgram(`dot(v, ref("t", "k"))`, "formula", s, vecs, refTables(), setExprOptions())
	if err != nil {
		t.Fatal(err)
	}
	folded := false
	for _, c := range prog.typed.Load().Constants {
		if reflect.DeepEqual(c, []float64{30, 10, 20}) {
			folded = true
		}
	}
	if !folded {
		t.Errorf("constants %v carry no folded [30 10 20]", prog.typed.Load().Constants)
	}

	keyed, err := newExprProgram(`dot(v, ref("t", x)) > 0`, "filter", s, vecs, refTables(), setExprOptions())
	if err != nil {
		t.Fatal(err)
	}
	_, nullInput, err := keyed.runRecord(rec(map[string]float64{"a": 1, "b": 2, "c": 6}, "x"))
	if !nullInput || err == nil || exprRefusal(err) != nil {
		t.Errorf("null key: nullInput %v err %v (refusal %v), want a null-input evaluation error", nullInput, err, exprRefusal(err))
	}
	_, _, err = keyed.runRecord(rec(map[string]float64{"a": 1, "b": 2, "c": 6, "x": 4}))
	if r := exprRefusal(err); r == nil || r.Code != perr.PULSE_VECTOR_REFERENCE_UNKNOWN || r.Details["reason"] != vectors.ReferenceReasonKeyUnknown {
		t.Errorf("missing row key: %v, want PULSE_VECTOR_REFERENCE_UNKNOWN key_unknown", err)
	}
}

// TestRef_BuildRefusals: ref() as the whole expression is a vector
// result, a request cannot supply the internal third argument, and the
// shared reference rules refuse at build.
func TestRef_BuildRefusals(t *testing.T) {
	vecs, _ := vecBindSchemaAndVectors(t)
	s := mkSchema("a", "b", "c", "x")
	cases := []struct {
		src  string
		code perr.Code
	}{
		{`ref("t", "k")`, perr.PROCESSING_CONFIG},
		{`vsum(ref("t", "k", "x"))`, perr.PROCESSING_CONFIG},
		{`vsum(ref("t"))`, perr.PROCESSING_CONFIG},
		{`vsum(ref("u", "k"))`, perr.PULSE_VECTOR_REFERENCE_UNKNOWN},
		{`dot(w, ref("t", "k"))`, perr.PULSE_VECTOR_REFERENCE_MISMATCH},
	}
	for _, tc := range cases {
		_, err := newExprProgram(tc.src, "formula", s, vecs, refTables(), setExprOptions())
		if topCode(err) != tc.code {
			t.Errorf("%s: %v, want %s", tc.src, err, tc.code)
		}
	}
	// Outside an expression program nothing is bound: the unpatched
	// call is refused, never a silent empty vector.
	if _, err := refBuiltin("t", "k"); topCode(err) != perr.PULSE_VECTOR_REFERENCE_UNKNOWN {
		t.Errorf("unpatched ref(): %v, want PULSE_VECTOR_REFERENCE_UNKNOWN", err)
	}
}

// TestRef_OverlayFormula: OVERLAY_FORMULA refuses a ref() call like the
// other vector built-ins, while a Compose host's `ref` VARIABLE still
// binds (an env name wins over a function of the same name).
func TestRef_OverlayFormula(t *testing.T) {
	spec := &types.OverlaySpec{Kind: types.OverlayKindFormula}
	exts := &ExtensionRegistry{ReferenceVectors: refTables()}
	if _, err := compileFormulaProgramWithExtensions(spec, `ref("t", "k")[0]`, map[string]any{"value": 0.0}, exts); topCode(err) != perr.PULSE_OVERLAY_FORMULA_INVALID_IDENT {
		t.Errorf("overlay ref() call: %v, want PULSE_OVERLAY_FORMULA_INVALID_IDENT", err)
	}
	if _, err := compileFormulaProgramWithExtensions(spec, `value - ref`, map[string]any{"value": 0.0, "ref": 0.0}, exts); err != nil {
		t.Errorf("overlay `ref` variable: %v", err)
	}
}
