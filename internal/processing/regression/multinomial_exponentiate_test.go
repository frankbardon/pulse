package regression

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// multinomial_exponentiate_test.go pins RegressionSpec.Exponentiate on
// REG_MULTINOMIAL (U68 E3-S2): the relative-risk ratios exp(β), their
// delta-method standard errors exp(β)·SE and the exponentiated Wald
// interval, against gen_multinomial.R's exp(coef(multinom)) block —
// classical and under each robust covariance — through the buffered
// fit entry point the processor calls (FitEachBufferedWith), so the
// wiring is what is tested, not the leaf alone.

// fitMultinomialEach fits spec over recs the way the processor does.
func fitMultinomialEach(t *testing.T, spec *types.RegressionSpec, levels []string, recs []Record) *types.RegressionResult {
	t.Helper()
	res, errs, err := FitEachBufferedWith([]*types.RegressionSpec{spec}, categoricalSchema("y", levels, "x1", "x2", "w", "g"), recs, nil)
	if err != nil || errs[0] != nil {
		t.Fatalf("FitEachBufferedWith: %v / %v", err, errs[0])
	}
	return res[0]
}

// TestReferenceOracle_MultinomialExponentiatedMatchesR: every case, on
// the classical standard errors and under hc0, hc1 and cluster, reports
// R's exp(b), exp(b)·SE, exp(b − z·SE) and exp(b + z·SE) on every key at
// 1e-10 relative, and its raw figures are the fit without exponentiate
// byte for byte.
func TestReferenceOracle_MultinomialExponentiatedMatchesR(t *testing.T) {
	t.Parallel()

	maxErr := 0.0
	for _, c := range loadMultinomial(t) {
		recs := c.records()
		for i, rec := range recs {
			rec.(labeledRecord).values["g"] = c.Robust.Clusters[i]
		}
		for _, typ := range []struct {
			cov  types.CovarianceType
			want multinomialExpo
		}{
			{"", c.Exponentiated},
			{types.CovarianceHC0, c.Robust.HC0.Exponentiated},
			{types.CovarianceHC1, c.Robust.HC1.Exponentiated},
			{types.CovarianceCluster, c.Robust.Cluster.Exponentiated},
		} {
			name := string(typ.cov)
			if name == "" {
				name = "classical"
			}
			t.Run(c.Name+"/"+name, func(t *testing.T) {
				spec := func(expo bool) *types.RegressionSpec {
					s := c.spec()
					s.Exponentiate = expo
					if typ.cov != "" {
						s.Covariance = &types.RegressionCovariance{Type: typ.cov}
						if typ.cov == types.CovarianceCluster {
							s.Covariance.Cluster = "g"
						}
					}
					return s
				}
				res := fitMultinomialEach(t, spec(true), c.Levels, recs)
				e := res.Exponentiated
				if e == nil || len(e.Estimates) != len(c.Keys) || len(e.StdErrors) != len(c.Keys) || len(e.CILower) != len(c.Keys) || len(e.CIUpper) != len(c.Keys) {
					t.Fatalf("exponentiated block %+v, want every one of %d keys", e, len(c.Keys))
				}
				near := func(what string, got, want float64) {
					t.Helper()
					err := math.Abs(got-want) / math.Max(math.Abs(want), 1e-14/multinomialTol)
					maxErr = math.Max(maxErr, err)
					if err > multinomialTol {
						t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want, err)
					}
				}
				for j, k := range c.Keys {
					near("estimate["+k+"]", e.Estimates[k], typ.want.Estimates[j])
					near("se["+k+"]", e.StdErrors[k], typ.want.StdErrors[j])
					near("ci_lower["+k+"]", e.CILower[k], typ.want.CILower[j])
					near("ci_upper["+k+"]", e.CIUpper[k], typ.want.CIUpper[j])
				}
				// Raw figures never move: the result minus the block is
				// the plain fit's, byte for byte.
				plain := fitMultinomialEach(t, spec(false), c.Levels, recs)
				res.Exponentiated = nil
				a, _ := json.Marshal(res)
				b, _ := json.Marshal(plain)
				if string(a) != string(b) {
					t.Errorf("raw figures moved:\n%s\n%s", a, b)
				}
			})
		}
	}
	t.Logf("max relative error: %v", maxErr)
}

// TestMultinomial_ExponentiatePenalizedAndResampled: a penalized fit
// reports exp(β) on every key with a null standard error and interval
// (its SEs are null); under a jackknife the standard errors are exp(β)
// times the replicate SEs and the bounds exp of the resampled Wald
// interval's.
func TestMultinomial_ExponentiatePenalizedAndResampled(t *testing.T) {
	t.Parallel()

	c := loadMultinomial(t)[0]
	recs := c.records()

	pen := c.spec()
	pen.Vcov, pen.Penalty, pen.Alpha, pen.Exponentiate = false, "l2", 0.05, true
	res := fitMultinomialEach(t, pen, c.Levels, recs)
	e := res.Exponentiated
	if e == nil || len(e.Estimates) != len(res.Coefficients) || len(res.Coefficients) == 0 {
		t.Fatalf("penalized block %+v over %d coefficients", e, len(res.Coefficients))
	}
	for k, b := range res.Coefficients {
		if got := e.Estimates[k]; math.Abs(got-math.Exp(b)) > 1e-14*math.Exp(b) {
			t.Errorf("estimate %s = %v, want exp(%v)", k, got, b)
		}
		if !math.IsNaN(e.StdErrors[k]) || !math.IsNaN(e.CILower[k]) || !math.IsNaN(e.CIUpper[k]) {
			t.Errorf("%s: se %v ci [%v, %v], want null", k, e.StdErrors[k], e.CILower[k], e.CIUpper[k])
		}
	}

	jk := c.spec()
	jk.Vcov, jk.Resample, jk.Exponentiate = false, "jackknife", true
	res = fitMultinomialEach(t, jk, c.Levels, recs)
	e = res.Exponentiated
	if e == nil || len(e.StdErrors) != len(c.Keys) {
		t.Fatalf("jackknife block %+v", e)
	}
	for _, k := range c.Keys {
		b, se := res.Coefficients[k], res.StdErrors[k]
		if got, want := e.StdErrors[k], math.Exp(b)*se; math.Abs(got-want) > 1e-13*want {
			t.Errorf("%s: se %v, want exp(b)·SE_jackknife %v", k, got, want)
		}
		if got, want := e.CILower[k], math.Exp(res.CILower[k]); math.Abs(got-want) > 1e-13*want {
			t.Errorf("%s: ci_lower %v, want %v", k, got, want)
		}
	}
}
