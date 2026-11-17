package regordinal

import (
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/extend"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// score_test.go pins the REG_ORDINAL row scorer (ATTR_REG_PREDICT, U68
// #650) to R: every classical golden case's per-row x'β (polr's $lp) and
// level probabilities (predict(polr, type = "probs")) at the MLE, and
// the outputs derived from them — P(y ≤ level), the expected level
// Σ k·P_k and the class (which.max) — scored from the golden
// coefficients through the extension contract's FittedRegression.

// mapRow is a model.Row over named values.
type mapRow map[string]float64

func (r mapRow) NumericValue(name string) (float64, bool) {
	v, ok := r[name]
	return v, ok
}

// goldenFit is case c's fitted model as a bound fit's extend view: the
// golden coefficients under their result names and the outputs in their
// model-file JSON form.
func goldenFit(c oracleCase) extend.FittedRegression {
	coefs := map[string]float64{}
	p := len(c.Predictors)
	for j, x := range c.Predictors {
		coefs[x] = c.Coefficients[j]
	}
	levels := make([]any, len(c.Levels))
	for k, lv := range c.Levels {
		levels[k] = lv
		if k+1 < len(c.Levels) {
			coefs[lv+ThresholdSeparator+c.Levels[k+1]] = c.Coefficients[p+k]
		}
	}
	return extend.FittedRegression{Type: types.REG_ORDINAL, Target: "y", Predictors: slices.Clone(c.Predictors), Coefficients: coefs,
		Outputs: map[string]any{"link": c.Link, "levels": levels}}
}

func TestOrdinalOracle_RowScores(t *testing.T) {
	t.Parallel()
	worst := map[string]float64{}
	scored := map[string]bool{}
	for _, c := range loadOracle(t) {
		if c.RowProbabilities == nil {
			continue
		}
		scored[c.Link+"/"+c.Weight] = true
		t.Run(c.Name, func(t *testing.T) {
			fit := goldenFit(c)
			k := len(c.Levels)
			mk := func(p pmodel.ScoreParams) extend.RowScorer {
				t.Helper()
				rs, err := ParamScorer(fit, p)
				if err != nil {
					t.Fatalf("ParamScorer(%+v): %v", p, err)
				}
				if got := rs.Columns(); !slices.Equal(got, c.Predictors) {
					t.Fatalf("columns %v, want %v", got, c.Predictors)
				}
				return rs
			}
			link := mk(pmodel.ScoreParams{Scale: pmodel.ScaleLink})
			def := mk(pmodel.ScoreParams{})
			class := mk(pmodel.ScoreParams{Output: pmodel.OutputClass})
			prob, cum := make([]extend.RowScorer, k), make([]extend.RowScorer, k)
			for j, lv := range c.Levels {
				prob[j] = mk(pmodel.ScoreParams{Level: lv})
				cum[j] = mk(pmodel.ScoreParams{Level: lv, Output: pmodel.OutputCumulative})
			}
			check := func(what string, i int, got, want float64) {
				t.Helper()
				e := relErr(got, want)
				if e > worst[what] {
					worst[what] = e
				}
				if e > oracleRel {
					t.Errorf("row %d %s = %.17g, R %.17g (rel %.3g)", i, what, got, want, e)
				}
			}
			for i, r := range c.Rows {
				row := mapRow{}
				for j, x := range c.Predictors {
					row[x] = r[j]
				}
				score := func(rs extend.RowScorer) float64 {
					t.Helper()
					v, ok := rs.Score(row)
					if !ok {
						t.Fatalf("row %d scored null", i)
					}
					return v
				}
				check("eta", i, score(link), c.RowEta[i])
				want := c.RowProbabilities[i]
				expected, cumWant, best := 0.0, 0.0, 0
				for j := range k {
					check("probability", i, score(prob[j]), want[j])
					cumWant += want[j]
					check("cumulative", i, score(cum[j]), cumWant)
					expected += float64(j+1) * want[j]
					if want[j] > want[best] {
						best = j
					}
				}
				check("expected", i, score(def), expected)
				if got := score(class); got != float64(best+1) {
					t.Errorf("row %d class %v, R which.max %d", i, got, best+1)
				}
			}
		})
	}
	for _, want := range []string{"logit/", "probit/", "cloglog/", "logit/frequency", "logit/probability"} {
		if !scored[want] {
			t.Errorf("no golden row scores for %s", want)
		}
	}
	t.Logf("max relative error vs R: %v", worst)
}

// TestRowScorer_FitProbabilitiesShareKernel: a design's fitted
// probabilities and the scorer's per-row vector are one kernel — the
// scorer at the fit's own θ reproduces design.probabilities bit for bit
// on every row.
func TestRowScorer_FitProbabilitiesShareKernel(t *testing.T) {
	t.Parallel()
	for _, c := range loadOracle(t) {
		d, _ := caseDesign(c)
		l := links[c.Link]
		sol, fail := d.newton(l)
		if fail != nil {
			t.Fatalf("%s: fit failed: %+v", c.Name, fail)
		}
		fit := goldenFit(c)
		for j, x := range c.Predictors {
			fit.Coefficients[x] = sol.theta[j]
		}
		for k := 0; k+1 < len(c.Levels); k++ {
			fit.Coefficients[c.Levels[k]+ThresholdSeparator+c.Levels[k+1]] = sol.theta[d.p+k]
		}
		f, err := NewFitted(fit)
		if err != nil {
			t.Fatal(err)
		}
		want, got := make([]float64, d.k), make([]float64, d.k)
		for i := range d.y {
			row := mapRow{}
			for j, x := range c.Predictors {
				row[x] = d.x[i][j]
			}
			d.probabilities(l, sol.theta, i, want)
			if !f.Probabilities(row, got) {
				t.Fatalf("%s row %d scored null", c.Name, i)
			}
			for k := range want {
				if math.Float64bits(got[k]) != math.Float64bits(want[k]) {
					t.Fatalf("%s row %d level %d: scorer %v, fit %v", c.Name, i, k, got[k], want[k])
				}
			}
		}
	}
}

// TestNewFitted_RefusesMalformedFit: a fit missing its link, levels, a
// predictor's or a threshold's coefficient builds no scorer; a null or
// non-finite predictor scores null.
func TestNewFitted_RefusesMalformedFit(t *testing.T) {
	t.Parallel()
	c := loadOracle(t)[0]
	for name, mut := range map[string]func(*extend.FittedRegression){
		"no_link":      func(f *extend.FittedRegression) { delete(f.Outputs, "link") },
		"bad_link":     func(f *extend.FittedRegression) { f.Outputs["link"] = "cauchit" },
		"no_levels":    func(f *extend.FittedRegression) { delete(f.Outputs, "levels") },
		"level_number": func(f *extend.FittedRegression) { f.Outputs["levels"] = []any{1.0, 2.0, 3.0} },
		"no_predictor": func(f *extend.FittedRegression) { delete(f.Coefficients, c.Predictors[0]) },
		"no_threshold": func(f *extend.FittedRegression) { delete(f.Coefficients, c.Levels[0]+ThresholdSeparator+c.Levels[1]) },
	} {
		fit := goldenFit(c)
		mut(&fit)
		if _, err := NewFitted(fit); err == nil {
			t.Errorf("%s: NewFitted built a scorer", name)
		}
	}
	rs, err := Scorer(goldenFit(c))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []mapRow{{c.Predictors[1]: 1}, {c.Predictors[0]: math.NaN(), c.Predictors[1]: 1}, {c.Predictors[0]: math.Inf(1), c.Predictors[1]: 1}} {
		if v, ok := rs.Score(row); ok {
			t.Errorf("row %v scored %v, want null", row, v)
		}
	}
	if _, err := ParamScorer(goldenFit(c), pmodel.ScoreParams{Level: "nope"}); err == nil {
		t.Error("an unknown level built a scorer")
	}
}
