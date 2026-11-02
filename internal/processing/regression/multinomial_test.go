package regression

import (
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// multinomial_test.go: REG_MULTINOMIAL's engine invariants beside the R
// oracle (multinomial_oracle_test.go) — the reference is a
// reparameterization, frequency weights are the expansion, and the
// data-dependent refusals each carry their code and reason.

// mnRows is a deterministic three-level fixture over x1, x2 (labels a,
// b, c by a noisy latent score), with integer weights f.
func mnRows(n int) (rows []Record, labels []string, f []float64) {
	for i := 0; i < n; i++ {
		fi := float64(i)
		x1 := float64(fi*0.07) - 2
		x2 := math.Sin(float64(0.37*fi) + 0.5)
		s := float64(0.9*x1) - float64(0.6*x2) + float64(1.4*math.Sin(float64(1.9*fi)))
		l := "a"
		switch {
		case s > 0.6:
			l = "c"
		case s > -0.5:
			l = "b"
		}
		w := float64(1 + i%3)
		rows = append(rows, labeledRecord{mockRecord: mockRecord{values: map[string]float64{"x1": x1, "x2": x2, "f": w}}, labels: map[string]string{"y": l}})
		labels = append(labels, l)
		f = append(f, w)
	}
	return rows, labels, f
}

func fitMultinomial(t *testing.T, spec *types.RegressionSpec, rows []Record) (*types.RegressionResult, error) {
	t.Helper()
	return newMultinomial(t, spec, []string{"a", "b", "c"}).FitBuffered(rows)
}

func mnSpec() *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Predictors: []string{"x1", "x2"}}
}

// TestMultinomial_ReferenceReparameterizes: refitting against another
// reference is the same model — β_c|b = β_c|a − β_b|a and β_a|b = −β_b|a
// on every term — and the likelihood does not move.
func TestMultinomial_ReferenceReparameterizes(t *testing.T) {
	t.Parallel()

	rows, _, _ := mnRows(150)
	ra, err := fitMultinomial(t, mnSpec(), rows)
	if err != nil {
		t.Fatal(err)
	}
	sb := mnSpec()
	sb.Reference = "b"
	rb, err := fitMultinomial(t, sb, rows)
	if err != nil {
		t.Fatal(err)
	}
	if ra.Reference != "a" || rb.Reference != "b" || len(rb.Categories) != 3 {
		t.Fatalf("references %q %q, categories %v", ra.Reference, rb.Reference, rb.Categories)
	}
	close := func(what string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-10*math.Max(1, math.Abs(want)) {
			t.Errorf("%s = %.17g, want %.17g", what, got, want)
		}
	}
	for _, term := range []string{InterceptKey, "x1", "x2"} {
		close("a:"+term, rb.Coefficients["a:"+term], -ra.Coefficients["b:"+term])
		close("c:"+term, rb.Coefficients["c:"+term], ra.Coefficients["c:"+term]-ra.Coefficients["b:"+term])
	}
	close("deviance", rb.Deviance, ra.Deviance)
	close("null_deviance", rb.NullDeviance, ra.NullDeviance)
}

// TestMultinomial_FrequencyWeightIsExpansion: a frequency-weighted fit
// is the fit of its rep()-expanded rows (coefficients, standard errors,
// deviance, two_log_lik), n_obs the raw rows and sum_weights Σf.
func TestMultinomial_FrequencyWeightIsExpansion(t *testing.T) {
	t.Parallel()

	rows, _, f := mnRows(90)
	ws := mnSpec()
	ws.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
	rw, err := fitMultinomial(t, ws, rows)
	if err != nil {
		t.Fatal(err)
	}
	var expanded []Record
	sum := 0.0
	for i, r := range rows {
		for k := 0; k < int(f[i]); k++ {
			expanded = append(expanded, r)
		}
		sum += f[i]
	}
	re, err := fitMultinomial(t, mnSpec(), expanded)
	if err != nil {
		t.Fatal(err)
	}
	if rw.NObs != len(rows) || rw.SumWeights != sum || rw.NEff != 0 {
		t.Fatalf("n_obs %d sum_weights %v n_eff %v", rw.NObs, rw.SumWeights, rw.NEff)
	}
	for k, want := range re.Coefficients {
		if got := rw.Coefficients[k]; math.Abs(got-want) > 1e-10*math.Max(1, math.Abs(want)) {
			t.Errorf("coef %s = %v, expansion %v", k, got, want)
		}
		if got, want := rw.StdErrors[k], re.StdErrors[k]; math.Abs(got-want) > 1e-10*want {
			t.Errorf("se %s = %v, expansion %v", k, got, want)
		}
	}
	if math.Abs(rw.TwoLogLik-re.TwoLogLik) > 1e-10*math.Abs(re.TwoLogLik) || rw.AIC == 0 {
		t.Errorf("two_log_lik %v aic %v, expansion %v", rw.TwoLogLik, rw.AIC, re.TwoLogLik)
	}
	// A probability weight drops the likelihood figures and reports n_eff.
	ps := mnSpec()
	ps.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindProbability})
	rp, err := fitMultinomial(t, ps, rows)
	if err != nil {
		t.Fatal(err)
	}
	if rp.TwoLogLik != 0 || rp.AIC != 0 || !(rp.NEff > 0) || rp.Deviance == 0 {
		t.Errorf("probability: two_log_lik %v aic %v n_eff %v deviance %v", rp.TwoLogLik, rp.AIC, rp.NEff, rp.Deviance)
	}
}

// TestMultinomial_DataRefusals: each data-dependent refusal is its own
// code, with its reason.
func TestMultinomial_DataRefusals(t *testing.T) {
	t.Parallel()

	rows, labels, _ := mnRows(60)
	only := func(keep func(string) bool) []Record {
		var out []Record
		for i, r := range rows {
			if keep(labels[i]) {
				out = append(out, r)
			}
		}
		return out
	}
	// Perfectly separated: the label is the sign of x1.
	var separated []Record
	for i := 0; i < 40; i++ {
		x1 := float64(i) - 19.5
		l := "a"
		if x1 > 0 {
			l = "c"
		}
		separated = append(separated, labeledRecord{mockRecord: mockRecord{values: map[string]float64{"x1": x1, "x2": math.Sin(float64(i))}}, labels: map[string]string{"y": l}})
	}
	collinear := make([]Record, len(rows))
	for i, r := range rows {
		v := r.(labeledRecord).values
		collinear[i] = labeledRecord{mockRecord: mockRecord{values: map[string]float64{"x1": v["x1"], "x2": float64(2 * v["x1"])}}, labels: r.(labeledRecord).labels}
	}
	refC := mnSpec()
	refC.Reference = "c"
	for _, tc := range []struct {
		name   string
		spec   *types.RegressionSpec
		rows   []Record
		code   errors.Code
		reason string
	}{
		{"one category", mnSpec(), only(func(l string) bool { return l == "b" }), errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA, "one_category"},
		{"reference empty", refC, only(func(l string) bool { return l != "c" }), errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA, "reference_empty"},
		{"too few rows", mnSpec(), rows[:2], errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA, ""},
		{"collinear", mnSpec(), collinear, errors.PROCESSING_REGRESSION_RANK_DEFICIENT, ""},
		{"separated", mnSpec(), separated, errors.PROCESSING_REGRESSION_SEPARATION, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := fitMultinomial(t, tc.spec, tc.rows)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("reason %v, want %s", ce.Details["reason"], tc.reason)
			}
		})
	}
}

// TestMultinomial_CategoriesFollowDictionary: the fitted categories
// keep the dictionary's order, not the rows' or the alphabet's; a
// dictionary level no row carries is left out with its coefficients.
func TestMultinomial_CategoriesFollowDictionary(t *testing.T) {
	t.Parallel()

	rows, _, _ := mnRows(120)
	eng := newMultinomial(t, mnSpec(), []string{"c", "unused", "a", "b"})
	res, err := eng.FitBuffered(rows)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"c", "a", "b"}; len(res.Categories) != 3 || res.Categories[0] != want[0] || res.Categories[1] != want[1] || res.Categories[2] != want[2] {
		t.Fatalf("categories %v, want %v", res.Categories, want)
	}
	if res.Reference != "c" || len(res.Coefficients) != 6 {
		t.Fatalf("reference %q, %d coefficients", res.Reference, len(res.Coefficients))
	}
	if _, ok := res.Coefficients["a:x1"]; !ok {
		t.Fatalf("no a:x1 in %v", res.Coefficients)
	}
}
