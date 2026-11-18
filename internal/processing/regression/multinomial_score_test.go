package regression

import (
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// multinomial_score_test.go pins REG_MULTINOMIAL's ATTR_REG_PREDICT
// columns (newMultinomialScorer through BuiltinScorer): P(y = category)
// equals the probability vector's entry, the class is the most probable
// category's 0-based index among the fit's categories, the link scale
// is the category's log-odds against the reference (0 for the reference
// itself), a null predictor scores null, and a fit saved to a model
// file and reloaded scores every column bit-identically.

// multinomialGoldenFit fits the named golden case and binds it.
func multinomialGoldenFit(t *testing.T, name string) (multinomialCase, *types.RegressionSpec, *types.RegressionResult, *model.Bound) {
	t.Helper()
	for _, c := range loadMultinomial(t) {
		if c.Name != name {
			continue
		}
		spec := c.spec()
		res, err := newMultinomial(t, spec, c.Levels).FitBuffered(c.records())
		if err != nil {
			t.Fatal(err)
		}
		b, err := model.Bind("m", spec, res)
		if err != nil {
			t.Fatal(err)
		}
		return c, spec, res, b
	}
	t.Fatalf("no golden case %s", name)
	return multinomialCase{}, nil, nil, nil
}

func multinomialColumn(t *testing.T, b *model.Bound, params string) model.RowScorer {
	t.Helper()
	p, cerr := model.DecodeScoreParams(json.RawMessage(params))
	if cerr != nil {
		t.Fatal(cerr)
	}
	rs, err := NewScorer(b, p)
	if err != nil {
		t.Fatalf("%s: %v", params, err)
	}
	return rs
}

func TestMultinomialScorer_Columns(t *testing.T) {
	t.Parallel()
	// The four-level case: a non-alphabetical dictionary, reference high.
	c, _, res, b := multinomialGoldenFit(t, "four_levels_reference")
	f, err := NewMultinomialFitted(b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.Columns(), []string{"x1", "x2"}) || !slices.Equal(f.Outcomes(), res.Categories) {
		t.Fatalf("columns %v outcomes %v", f.Columns(), f.Outcomes())
	}
	ref := slices.Index(res.Categories, res.Reference)
	pi := make([]float64, len(res.Categories))
	sawClass := map[float64]bool{}
	for i, r := range c.ScoreRows {
		row := mockRecord{values: map[string]float64{"x1": r[0], "x2": r[1]}}
		if !f.Probabilities(row, pi) {
			t.Fatalf("row %d null", i)
		}
		best := 0
		for k := range pi {
			if pi[k] > pi[best] {
				best = k
			}
		}
		class, ok := multinomialColumn(t, b, `{"fit":"m","output":"class"}`).Score(row)
		if !ok || class != float64(best) {
			t.Errorf("row %d class %v, want the 0-based index %d of %v", i, class, best, pi)
		}
		sawClass[class] = true
		for k, cat := range res.Categories {
			// Probability (the default output) is the vector's entry.
			for _, params := range []string{`{"fit":"m","category":"` + cat + `"}`, `{"fit":"m","category":"` + cat + `","output":"probability"}`} {
				got, ok := multinomialColumn(t, b, params).Score(row)
				if !ok || got != pi[k] {
					t.Errorf("row %d %s = %v, vector %v", i, params, got, pi[k])
				}
			}
			// The link scale: log(π_k / π_ref), 0 on the reference.
			lo, ok := multinomialColumn(t, b, `{"fit":"m","category":"`+cat+`","scale":"link"}`).Score(row)
			if !ok || math.Abs(lo-math.Log(pi[k]/pi[ref])) > 1e-12 || (k == ref && lo != 0) {
				t.Errorf("row %d log-odds %s = %v, log(π/π_ref) %v", i, cat, lo, math.Log(pi[k]/pi[ref]))
			}
		}
	}
	if len(sawClass) < 2 {
		t.Errorf("the grid's classes %v never differ: the class test is vacuous", sawClass)
	}
	// A null predictor scores null in every column.
	null := mockRecord{values: map[string]float64{"x1": 1}}
	for _, params := range []string{`{"fit":"m","category":"low"}`, `{"fit":"m","output":"class"}`, `{"fit":"m","category":"low","scale":"link"}`} {
		if _, ok := multinomialColumn(t, b, params).Score(null); ok {
			t.Errorf("%s scored a row missing x2", params)
		}
	}
}

// TestMultinomialScorer_ModelFileRoundTrip: the model file keeps the
// categories and reference (feature categories), and the reloaded fit
// scores every column bit-identically.
func TestMultinomialScorer_ModelFileRoundTrip(t *testing.T) {
	t.Parallel()
	c, spec, res, b := multinomialGoldenFit(t, "empty_level")
	f, err := model.NewFile("m", spec, res, model.Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.Features, []string{model.FeatureCategories}) || !slices.Equal(f.Categories, res.Categories) || f.Reference != res.Reference {
		t.Fatalf("file features %v categories %v reference %q", f.Features, f.Categories, f.Reference)
	}
	raw, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := model.DecodeFile("m.pulse-model.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	lb, err := back.Bind("m")
	if err != nil {
		t.Fatal(err)
	}
	params := []string{`{"fit":"m","output":"class"}`}
	for _, cat := range res.Categories {
		params = append(params, `{"fit":"m","category":"`+cat+`"}`, `{"fit":"m","category":"`+cat+`","scale":"link"}`)
	}
	for _, p := range params {
		fit, file := multinomialColumn(t, b, p), multinomialColumn(t, lb, p)
		for i, rec := range c.records() {
			a, aok := fit.Score(rec)
			z, zok := file.Score(rec)
			if aok != zok || math.Float64bits(a) != math.Float64bits(z) {
				t.Fatalf("%s row %d: fit %v, reloaded %v", p, i, a, z)
			}
		}
	}
}
