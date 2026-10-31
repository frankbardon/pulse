package synth

import (
	"bytes"
	mrand "math/rand/v2"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// numericChainSchema declares a categorical and three numerics in a
// fixed order: x (driven by g), z (unrelated to anything) and then y,
// which is driven by x. The order is the point — the generation-order
// rule admits a numeric predictor only into a target declared after it.
func numericChainSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	g := encoding.NewDictionary()
	for _, v := range []string{"a", "b"} {
		if _, err := g.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: g},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "z", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
}

// numericChainRows generates
//
//	x = 50 + 4·[g=b] + N(0, 3²)
//	z = 10 + N(0, 1)
//	y = 5 + 2·x + N(0, 2²)
//
// so x carries most of y's variance (r² ≈ 0.86), z carries none, and x
// is strictly positive — a numeric the indicator constancy test would
// read as "identically one".
func numericChainRows(n int, seed uint64) ([]map[string]any, []map[string]bool) {
	rng := mrand.New(mrand.NewPCG(seed, seed*2654435761+1))
	rows := make([]map[string]any, n)
	nulls := make([]map[string]bool, n)
	for i := 0; i < n; i++ {
		g, bump := "a", 0.0
		if rng.IntN(2) == 1 {
			g, bump = "b", 1
		}
		x := 50 + float64(4*bump) + float64(3*rng.NormFloat64())
		z := 10 + rng.NormFloat64()
		y := 5 + float64(2*x) + float64(2*rng.NormFloat64())
		rows[i] = map[string]any{"g": g, "x": x, "z": z, "y": y}
		nulls[i] = map[string]bool{}
	}
	return rows, nulls
}

func numericChainProfile(t *testing.T) *Profile {
	t.Helper()
	schema := numericChainSchema(t)
	rows, nulls := numericChainRows(3000, 9)
	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	return prof
}

func numericPredictorsOf(m FieldModel) []string {
	var out []string
	for _, p := range m.Predictors {
		if p.Kind == ModelPredictorNumeric {
			out = append(out, p.Field)
		}
	}
	return out
}

// TestModelSelection_NumericPredictorEntersLaterTargetsOnly pins both
// halves of numeric predictor selection: a numeric that explains a
// later-declared numeric enters its model (as kind "numeric", with an
// empty level and the slope as its coefficient), a numeric explaining
// nothing does not, and the symmetric edge — y predicting x — is never
// emitted, because x is declared first and a numeric is drawn before
// the fields it predicts.
func TestModelSelection_NumericPredictorEntersLaterTargetsOnly(t *testing.T) {
	t.Parallel()

	prof := numericChainProfile(t)
	y, ok := modelByField(prof.Models, "y")
	if !ok {
		t.Fatalf("no model for y; warnings=%v", prof.Warnings)
	}
	if got := numericPredictorsOf(y); len(got) != 1 || got[0] != "x" {
		t.Fatalf("y's numeric predictors = %v, want exactly [x]; predictors=%+v", got, y.Predictors)
	}
	for _, p := range y.Predictors {
		if p.Field != "x" {
			continue
		}
		if p.Level != "" {
			t.Errorf("numeric predictor carries level %q, want empty", p.Level)
		}
		if p.Coefficient < 1.9 || p.Coefficient > 2.1 {
			t.Errorf("x coefficient = %v, want ~2 (the planted slope)", p.Coefficient)
		}
	}
	x, ok := modelByField(prof.Models, "x")
	if !ok {
		t.Fatalf("no model for x; warnings=%v", prof.Warnings)
	}
	if got := numericPredictorsOf(x); len(got) != 0 {
		t.Fatalf("x took numeric predictors %v; a numeric may only predict fields declared after it", got)
	}

	// The term survives translation onto the Spec, so generation draws
	// it rather than dropping y's whole model as unsupported.
	spec, warnings := SpecFromProfile(prof, 10)
	carried := false
	for _, m := range spec.Models {
		if m.Field != "y" {
			continue
		}
		for _, p := range m.Predictors {
			if p.Kind == ModelPredictorNumeric && p.Field == "x" {
				carried = true
			}
		}
	}
	if !carried {
		t.Fatalf("y's numeric predictor did not reach the Spec; models=%+v warnings=%v", spec.Models, warnings)
	}
}

// TestModelSelection_NumericOnlyCohortFitsWhenNumericsRelate is the
// behaviour change on a cohort with no categorical or set_* field:
// before numeric candidates it was skipped wholesale as having nothing
// to regress on; now a related pair fits, and the first-declared
// numeric gets a zero-predictor model rather than a skip.
func TestModelSelection_NumericOnlyCohortFitsWhenNumericsRelate(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
	full, nulls := numericChainRows(2000, 4)
	rows := make([]map[string]any, len(full))
	for i, r := range full {
		rows[i] = map[string]any{"x": r["x"], "y": r["y"]}
	}
	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	y, ok := modelByField(prof.Models, "y")
	if !ok || len(numericPredictorsOf(y)) != 1 {
		t.Fatalf("y did not take x as a numeric predictor: models=%+v warnings=%v", prof.Models, prof.Warnings)
	}
	for _, w := range prof.Warnings {
		if strings.Contains(w, "no categorical or set_* candidate predictors") {
			t.Errorf("a numeric-only cohort with a related pair still warned the no-candidate skip: %q", w)
		}
	}
}

// TestModelColumns_NumericColumnPrunedOnlyWhenConstant pins prune's
// numeric arm directly: a numeric column non-zero on every admitted row
// survives (it is not an intercept duplicate unless it is CONSTANT),
// and a constant one is dropped.
func TestModelColumns_NumericColumnPrunedOnlyWhenConstant(t *testing.T) {
	t.Parallel()

	fit := &fieldFit{columns: []dummyColumn{
		{Name: "x", Kind: dummyNumeric, Field: "x"},
		{Name: "k", Kind: dummyNumeric, Field: "k"},
	}}
	fit.prune([]int{10, 10}, []int{-1, -1}, nil, []bool{true, false}, 10)
	if len(fit.columns) != 1 || fit.columns[0].Name != "x" {
		t.Fatalf("surviving columns = %+v, want only the varying numeric x", fit.columns)
	}
}
