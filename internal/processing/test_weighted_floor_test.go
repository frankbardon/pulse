package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// U36 E1-S7: under kind frequency every minimum-n guard reads Σw — a
// row of weight w stands for w identical rows, so a frequency-weighted
// test refuses exactly when its expansion does (weighting.Basis.FloorN).
// Each case sits at the edge the raw-row guard used to refuse: fewer raw
// rows than the guard's minimum, Σw at or above it. The weighted run
// must equal the unweighted run on the rep()-expanded rows — the same
// figures (relative 1e-10; ±Inf / NaN by class) and warnings, or the
// same refusal code when the expansion refuses too. runs marks the cases
// whose expansion yields a result: the defect this story fixes is a
// raw-row refusal of exactly those.

// runWeightedRowTestErr is runWeightedRowTest returning Finalize's
// error instead of failing.
func runWeightedRowTestErr(t *testing.T, spec types.Test, rows []wRow, kind types.WeightKind) (*types.TestResult, error) {
	t.Helper()
	schema := weightedTestSchema()
	if kind != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
	}
	rt, err := rowTestRegistry[spec.Type](&spec, schema)
	if err != nil {
		t.Fatalf("%s factory: %v", spec.Type, err)
	}
	for _, r := range rows {
		rec := NewRecord(schema, map[string]float64{"x": r.x, "y": r.y, "w": r.w, "g": float64(dictIDOrAdd(schema, "g", r.g))})
		if err := rt.UpdateRow(rec); err != nil {
			t.Fatal(err)
		}
	}
	return rt.Finalize()
}

// sameFigure compares two figures: equal class for ±Inf / NaN, else
// relative 1e-10.
func sameFigure(got, want float64) bool {
	switch {
	case math.IsNaN(want):
		return math.IsNaN(got)
	case math.IsInf(want, 0):
		return got == want
	}
	return relEq(got, want, 1e-10)
}

// assertFloorParity checks a weighted outcome against its expansion's.
func assertFloorParity(t *testing.T, name string, runs bool, got *types.TestResult, gotErr error, want *types.TestResult, wantErr error) {
	t.Helper()
	if runs && wantErr != nil {
		t.Fatalf("%s: the expansion refuses (%v); the case must sit where it runs", name, wantErr)
	}
	if wantErr != nil {
		var g, w *errors.CodedError
		if !stderrors.As(gotErr, &g) || !stderrors.As(wantErr, &w) || g.Code != w.Code {
			t.Fatalf("%s: weighted err %v, want the expansion's %v", name, gotErr, wantErr)
		}
		return
	}
	if gotErr != nil {
		t.Fatalf("%s: weighted refuses (%v) where the expansion runs", name, gotErr)
	}
	if got.Error != nil || want.Error != nil {
		if got.Error == nil || want.Error == nil || got.Error.Code != want.Error.Code {
			t.Fatalf("%s: slot error %+v, want the expansion's %+v", name, got.Error, want.Error)
		}
		return
	}
	if !sameFigure(got.Statistic, want.Statistic) || !sameFigure(got.DF, want.DF) || !sameFigure(got.PValue, want.PValue) || got.RejectNull != want.RejectNull {
		t.Fatalf("%s: stat/df/p/reject %v %v %v %v, want the expansion's %v %v %v %v",
			name, got.Statistic, got.DF, got.PValue, got.RejectNull, want.Statistic, want.DF, want.PValue, want.RejectNull)
	}
	if !slices.Equal(got.Warnings, want.Warnings) {
		t.Fatalf("%s: warnings %v, want %v", name, got.Warnings, want.Warnings)
	}
	// Figures behind their own floor (TEST_PEARSON_R's interval needs 4).
	for _, k := range []string{"ci_low", "ci_high"} {
		if w, ok := want.Details[k].(float64); ok {
			if g, _ := got.Details[k].(float64); !sameFigure(g, w) {
				t.Fatalf("%s: %s = %v, want the expansion's %v", name, k, g, w)
			}
		}
	}
}

// TestWeightedTests_FrequencyFloorIsExpansion: the univariate and
// bivariate frequency-weighted tests at the raw-row floor's edge.
func TestWeightedTests_FrequencyFloorIsExpansion(t *testing.T) {
	// one: a group of ONE raw row standing for w rows; the others are
	// ordinary multi-row groups.
	one := func(g string, x, w float64) wRow { return wRow{x: x, y: 0.5*x + 1, g: g, w: w} }
	twoGroups := []wRow{
		one("a", 5.0, 3),
		{x: 4.1, y: 2.0, g: "b", w: 1}, {x: 6.3, y: 4.4, g: "b", w: 2}, {x: 7.0, y: 3.1, g: "b", w: 1}, {x: 8.2, y: 5.0, g: "b", w: 1},
	}
	threeGroups := append(append([]wRow(nil), twoGroups...),
		wRow{x: 3.0, y: 1.0, g: "c", w: 1}, wRow{x: 5.5, y: 3.3, g: "c", w: 2}, wRow{x: 4.4, y: 2.9, g: "c", w: 1})
	// kw: three groups of one raw row each and Σw ≥ 2 per group (raw
	// N = 4 < 2k = 6, Σw = 7).
	kw := []wRow{one("a", 2.0, 2), one("b", 5.0, 3), {x: 3.5, g: "c", w: 1}, {x: 9.0, g: "c", w: 1}}
	// pairs: two raw rows, Σw = 3 — the bivariate floor is 3.
	pairs := []wRow{{x: 1.0, y: 3.0, w: 2}, {x: 2.0, y: 5.5, w: 1}}
	// wilcoxon: three non-zero differences, distinct |d|, Σw = 6.
	wilcoxon := []wRow{{x: 3.0, y: 1.0, w: 2}, {x: 1.5, y: 4.5, w: 2}, {x: 7.0, y: 2.0, w: 2}}
	for _, c := range []struct {
		name string
		spec types.Test
		rows []wRow
		runs bool
	}{
		{"brown_forsythe", types.Test{Type: types.TEST_BROWN_FORSYTHE, Field: "x", SplitBy: "g"}, threeGroups, true},
		{"ks", types.Test{Type: types.TEST_KS, Field: "x", SplitBy: "g"}, twoGroups, true},
		{"mann_whitney", types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "x", SplitBy: "g"}, twoGroups, true},
		{"kruskal", types.Test{Type: types.TEST_KRUSKAL_WALLIS, Field: "x", SplitBy: "g"}, kw, true},
		{"wilcoxon_sr", types.Test{Type: types.TEST_WILCOXON_SR, Field: "x", Field2: "y"}, wilcoxon, true},
		{"spearman", types.Test{Type: types.TEST_SPEARMAN_R, Field: "x", Field2: "y"}, pairs, true},
		{"kendall", types.Test{Type: types.TEST_KENDALL_TAU, Field: "x", Field2: "y"}, pairs, true},
		{"pearson", types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y"}, pairs, true},
		// Three raw rows, Σw = 4: the Fisher-z interval's floor of 4.
		{"pearson_ci", types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y"}, append(append([]wRow(nil), pairs...), wRow{x: 4.0, y: 6.0, w: 1}), true},
		{"t_split", types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g"}, twoGroups, true},
		{"welch", types.Test{Type: types.TEST_WELCH, Field: "x", SplitBy: "g"}, twoGroups, true},
		{"z", types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "x", SplitBy: "g"}, twoGroups, true},
		// A single raw row (or pair) is constant however heavy, and with
		// every group a single row the within-group spread is zero: the
		// expansion refuses on that, so the weighted run must refuse
		// with the SAME code (no longer the raw-row floor's).
		{"t_one_sample", types.Test{Type: types.TEST_T, Field: "x", Params: json.RawMessage(`{"mu":4}`)}, []wRow{{x: 5.0, w: 3}}, false},
		{"paired", types.Test{Type: types.TEST_PAIRED_T, Field: "x", Field2: "y"}, []wRow{{x: 5.0, y: 3.0, w: 3}}, false},
		{"anova_f", types.Test{Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "g"}, []wRow{one("a", 2.0, 2), one("b", 5.0, 3)}, false},
		{"anova_welch", types.Test{Type: types.TEST_ANOVA_WELCH, Field: "x", SplitBy: "g"}, threeGroups, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, gotErr := runWeightedRowTestErr(t, c.spec, c.rows, types.WeightKindFrequency)
			want, wantErr := runWeightedRowTestErr(t, c.spec, expandRows(c.rows), "")
			assertFloorParity(t, c.name, c.runs, got, gotErr, want, wantErr)
			// Probability weights keep the raw floor: their Σw counts
			// nothing (a low n_eff warns instead). Rank / exact tests
			// are frequency-only, so only the moment tests have this arm.
			// (pearson_ci's three raw rows already pass the raw p floor.)
			if c.runs && isAwareMomentTest(c.spec.Type) && c.name != "pearson_ci" {
				if _, err := runWeightedRowTestErr(t, c.spec, c.rows, types.WeightKindProbability); !isCode(err, errors.PULSE_TEST_INSUFFICIENT_N) {
					t.Errorf("%s probability: err %v, want the raw-row floor's PULSE_TEST_INSUFFICIENT_N", c.name, err)
				}
			}
		})
	}
}

// isAwareMomentTest: the moment tests weighted under both kinds.
func isAwareMomentTest(tt types.TestType) bool {
	switch tt {
	case types.TEST_T, types.TEST_WELCH, types.TEST_Z_TWO_SAMPLE, types.TEST_PEARSON_R:
		return true
	}
	return false
}

func isCode(err error, code errors.Code) bool {
	var ce *errors.CodedError
	return stderrors.As(err, &ce) && ce.Code == code
}

// runMVTest runs one multivariate test request through the processor
// and returns its result (a failed slot keeps its Error).
func runMVTest(t *testing.T, schema *encoding.Schema, recs []*Record, req *types.Request) *types.TestResult {
	t.Helper()
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("%s: %v", req.Tests[0].Type, err)
	}
	if len(resp.Tests) != 1 || resp.Tests[0] == nil {
		t.Fatalf("tests = %+v", resp.Tests)
	}
	return resp.Tests[0]
}

// TestWeightedMVTests_FrequencyFloorIsExpansion: the vector tests at the
// raw-row floor's edge. Below p + 1 distinct rows a covariance is
// singular however the rows are weighted, so most of these expansions
// answer with the singular-covariance result (null figures and a
// PULSE_MATRIX_SINGULAR warning) — the weighted run must answer the
// same, not refuse on raw rows. Hotelling's unequal-covariance form with
// one constant level and Mardia at p = 1 run outright.
func TestWeightedMVTests_FrequencyFloorIsExpansion(t *testing.T) {
	type row struct {
		x1, x2 float64
		g      string
		w      float64
	}
	build := func(schema *encoding.Schema, rows []row, expand bool) []*Record {
		var recs []*Record
		for _, r := range rows {
			copies, w := 1, r.w
			if expand {
				copies, w = int(r.w), 1
			}
			for range copies {
				recs = append(recs, NewRecord(schema, map[string]float64{
					"x1": r.x1, "x2": r.x2, "x3": 0, "w": w, "g": float64(dictIDOrAdd(schema, "g", r.g)),
				}))
			}
		}
		return recs
	}
	split := []row{{1.0, 2.0, "a", 3}, {2.5, 1.0, "b", 1}, {4.0, 3.5, "b", 2}, {3.2, 0.5, "b", 1}}
	for _, c := range []struct {
		name string
		spec types.Test
		rows []row
		runs bool
	}{
		{"hotelling_one_sample", types.Test{Type: types.TEST_HOTELLING_T2, Fields: []string{"x1", "x2"}}, []row{{1.0, 2.0, "a", 2}, {2.5, 1.0, "a", 2}}, true},
		{"hotelling_two_sample", types.Test{Type: types.TEST_HOTELLING_T2, Fields: []string{"x1", "x2"}, SplitBy: "g"}, split[:3], true},
		{"hotelling_unequal", types.Test{Type: types.TEST_HOTELLING_T2, Fields: []string{"x1"}, SplitBy: "g", Params: json.RawMessage(`{"variance":"unequal"}`)}, split, true},
		{"manova", types.Test{Type: types.TEST_MANOVA, Fields: []string{"x1", "x2"}, SplitBy: "g"}, split[:3], true},
		{"box_m", types.Test{Type: types.TEST_BOX_M, Fields: []string{"x1", "x2"}, SplitBy: "g"}, split, true},
		{"bartlett_sphericity", types.Test{Type: types.TEST_BARTLETT_SPHERICITY, Fields: []string{"x1", "x2"}}, []row{{1.0, 2.0, "a", 3}}, false},
		{"mardia_p1", types.Test{Type: types.TEST_MARDIA, Fields: []string{"x1"}}, []row{{1.0, 2.0, "a", 2}, {2.5, 1.0, "a", 1}}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := func(weighted bool) *types.Request {
				spec := c.spec
				r := &types.Request{Tests: []*types.Test{&spec}}
				if weighted {
					r.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
				}
				return r
			}
			schema := hotellingSchema()
			got := runMVTest(t, schema, build(schema, c.rows, false), req(true))
			schema = hotellingSchema()
			want := runMVTest(t, schema, build(schema, c.rows, true), req(false))
			if c.runs && want.Error != nil {
				t.Fatalf("%s: the expansion refuses (%+v); the case must sit where it runs", c.name, want.Error)
			}
			assertFloorParity(t, c.name, c.runs, got, nil, want, nil)
		})
	}
}
