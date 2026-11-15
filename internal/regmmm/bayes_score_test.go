package regmmm

import (
	"math"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// scoreRow is a scoring row: numbers by name, a categorical geo label.
type scoreRow struct {
	nums  map[string]float64
	label map[string]string
}

func (r scoreRow) NumericValue(name string) (float64, bool) { v, ok := r.nums[name]; return v, ok }
func (r scoreRow) StringValue(name string) (string, bool)   { v, ok := r.label[name]; return v, ok }
func (r scoreRow) Uint64Value(name string) (uint64, bool) {
	v, ok := r.nums[name]
	return uint64(v), ok
}

// drawsFit is a fit whose outputs.draws (JSON form) hold two chains of
// two draws per name, cols[name] its four pooled values.
func drawsFit(predictors []string, cols map[string][4]float64, hierarchy map[string]any) extend.FittedRegression {
	var names []any
	chains := []any{[]any{}, []any{}}
	for name, v := range cols {
		names = append(names, name)
		chains[0] = append(chains[0].([]any), []any{v[0], v[1]})
		chains[1] = append(chains[1].([]any), []any{v[2], v[3]})
	}
	outs := map[string]any{pmodel.MMMDrawsKey: map[string]any{"names": names, "draws": chains, "thin": 1.0, "credible_level": 0.5}}
	if hierarchy != nil {
		outs["hierarchy"] = hierarchy
	}
	return extend.FittedRegression{Type: types.REG_MMM_BAYES, Target: "y", Predictors: predictors, Outputs: outs}
}

// TestBayesScorer_Curves: the curve is read off the coefficient names —
// lam: logistic, k: alone Michaelis–Menten, k: and s: Hill — and the
// score is the mean over the draws of α + γ·z + β·curve(x); the bounds
// are the draws' type-7 quantiles at (1 ∓ credible_level) / 2; a null
// or negative channel scores null; a fit without draws is
// PULSE_MODEL_SCORE_INVALID draws_required.
func TestBayesScorer_Curves(t *testing.T) {
	t.Parallel()

	base := map[string][4]float64{"(intercept)": {10, 11, 12, 13}, "tv": {2, 3, 4, 5}, "price": {-1, -1, -2, -2}}
	with := func(extra map[string][4]float64) map[string][4]float64 {
		out := map[string][4]float64{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	x, z := 30.0, 0.5
	for _, tc := range []struct {
		name  string
		cols  map[string][4]float64
		curve func(d int, c map[string][4]float64) float64
	}{
		{"logistic", with(map[string][4]float64{"lam:tv": {0.01, 0.02, 0.03, 0.04}}), func(d int, c map[string][4]float64) float64 {
			u := c["lam:tv"][d] * x
			return (1 - math.Exp(-u)) / (1 + math.Exp(-u))
		}},
		{"michaelis_menten", with(map[string][4]float64{"k:tv": {20, 25, 30, 35}}), func(d int, c map[string][4]float64) float64 {
			return x / (c["k:tv"][d] + x)
		}},
		{"hill", with(map[string][4]float64{"k:tv": {20, 25, 30, 35}, "s:tv": {1, 1.5, 2, 2.5}}), func(d int, c map[string][4]float64) float64 {
			return 1 / (1 + math.Pow(c["k:tv"][d]/x, c["s:tv"][d]))
		}},
	} {
		fit := drawsFit([]string{"tv", "price"}, tc.cols, nil)
		sc, err := BayesScorer(fit)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		preds := make([]float64, 4)
		var mean float64
		for d := range preds {
			preds[d] = tc.cols["(intercept)"][d] + float64(tc.cols["price"][d]*z) + float64(tc.cols["tv"][d]*tc.curve(d, tc.cols))
			mean += float64(preds[d] / 4)
		}
		row := scoreRow{nums: map[string]float64{"tv": x, "price": z}}
		if got, ok := sc.Score(row); !ok || math.Abs(got-mean) > 1e-12 {
			t.Errorf("%s: score %v (%v), want %v", tc.name, got, ok, mean)
		}
		lo, _ := BayesBoundScorer(fit, pmodel.BoundLower)
		hi, _ := BayesBoundScorer(fit, pmodel.BoundUpper)
		l, _ := lo.Score(row)
		h, _ := hi.Score(row)
		sorted := append([]float64(nil), preds...)
		for i := range sorted {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j] < sorted[i] {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		// level 0.5: p = 0.25 and 0.75 over four draws (h = 0.75, 2.25).
		wantL := sorted[0] + float64(0.75*(sorted[1]-sorted[0]))
		wantH := sorted[2] + float64(0.25*(sorted[3]-sorted[2]))
		if math.Abs(l-wantL) > 1e-12 || math.Abs(h-wantH) > 1e-12 {
			t.Errorf("%s: bounds %v / %v, want %v / %v", tc.name, l, h, wantL, wantH)
		}
		for _, bad := range []scoreRow{{nums: map[string]float64{"price": z}}, {nums: map[string]float64{"tv": -1, "price": z}}} {
			if _, ok := sc.Score(bad); ok {
				t.Errorf("%s: row %v scored", tc.name, bad.nums)
			}
		}
	}
	_, err := BayesScorer(extend.FittedRegression{Type: types.REG_MMM_BAYES, Predictors: []string{"tv"}, Outputs: map[string]any{"seed": 1.0}})
	var ce *perr.CodedError
	if ce, _ = err.(*perr.CodedError); ce == nil || ce.Code != perr.PULSE_MODEL_SCORE_INVALID || ce.Details["reason"] != pmodel.ReasonDrawsRequired {
		t.Errorf("no draws: %v", err)
	}
}

// TestBayesScorer_GeoCodes: on a panel coded by an unsigned integer the
// row's code picks its geo; an unseen code scores from the population β
// and the mean geo intercept and is counted — only when it scores — and
// a null geo scores null.
func TestBayesScorer_GeoCodes(t *testing.T) {
	t.Parallel()

	cols := map[string][4]float64{
		"(intercept)[1]": {10, 10, 10, 10}, "(intercept)[2]": {20, 20, 20, 20},
		"tv": {1, 1, 1, 1}, "tv[1]": {2, 2, 2, 2}, "tv[2]": {3, 3, 3, 3}, "k:tv": {10, 10, 10, 10},
	}
	fit := drawsFit([]string{"tv"}, cols, map[string]any{"geo": "market", "geos": []any{"1", "2"}})
	s, err := BayesScorer(fit)
	if err != nil {
		t.Fatal(err)
	}
	h := 10.0 / (10 + 10) // Michaelis–Menten at x = k
	for _, tc := range []struct {
		code float64
		want float64
	}{{1, 10 + 2*h}, {2, 20 + 3*h}, {7, 15 + 1*h}} {
		got, ok := s.Score(scoreRow{nums: map[string]float64{"tv": 10, "market": tc.code}})
		if !ok || math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("geo %v: %v (%v), want %v", tc.code, got, ok, tc.want)
		}
	}
	if _, ok := s.Score(scoreRow{nums: map[string]float64{"tv": 10}}); ok {
		t.Error("a null geo scored")
	}
	if _, ok := s.Score(scoreRow{nums: map[string]float64{"market": 9}}); ok {
		t.Error("an unseen geo with a null channel scored")
	}
	w := s.(*bayesScorer).GeoTally().Warning("p", nil)
	if w == nil || w.Details["rows"].([]int64)[0] != 1 || w.Details["geos"].([]string)[0] != "7" || len(w.Details["geos"].([]string)) != 1 {
		t.Fatalf("tally warning %+v, want geo 7 counted once (the null row not)", w)
	}
}
