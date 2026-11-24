package pulse

import (
	"context"
	"math"
	"slices"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// compare_resample_test.go covers the Wald nested-model comparison over
// a resampled fit (U69 #642): the statistic reads the full fit's
// jackknife / bootstrap replicate covariance — the matrix its own
// `vcov` reports, whose √diag are its standard errors — on χ² over q,
// on every comparable type (REG_OLS, REG_GLM, REG_MULTINOMIAL and the
// declared REG_ORDINAL); the covariance is formed whether or not the
// full fit asks for `vcov`; predict names the distribution the run
// reports. The figures are pinned to R by internal/service's
// TestReferenceOracle_CompareResampleMatchesR.

// waldOver is b_Sᵀ V_SS⁻¹ b_S over keys of the fit's own reported vcov
// (a small Gauss-Jordan solve; q ≤ 4 here).
func waldOver(t *testing.T, res *types.RegressionResult, keys []string) float64 {
	t.Helper()
	if res.Vcov == nil {
		t.Fatal("full fit carries no vcov")
	}
	q := len(keys)
	a := make([][]float64, q)
	b := make([]float64, q)
	for i, ki := range keys {
		ri := slices.Index(res.Vcov.RowKeys, ki)
		if ri < 0 {
			t.Fatalf("vcov has no key %s (%v)", ki, res.Vcov.RowKeys)
		}
		b[i] = res.Coefficients[ki]
		a[i] = make([]float64, q+1)
		for j, kj := range keys {
			a[i][j] = res.Vcov.Values[ri][slices.Index(res.Vcov.ColumnKeys, kj)]
		}
		a[i][q] = b[i]
	}
	for c := 0; c < q; c++ {
		for r := 0; r < q; r++ {
			if r == c {
				continue
			}
			f := a[r][c] / a[c][c]
			for k := c; k <= q; k++ {
				a[r][k] -= f * a[c][k]
			}
		}
	}
	w := 0.0
	for i := 0; i < q; i++ {
		w += b[i] * a[i][q] / a[i][i]
	}
	return w
}

// TestCompare_ResampledWaldReadsReplicateCovariance: per type and
// resample kind, the Wald comparison of two resampled fits equals
// b_Sᵀ V_SS⁻¹ b_S over the full fit's own replicate vcov on χ²(q) (no
// df_residual), differs from the unresampled test, is byte-identical
// (statistic and standard errors) whether or not the full fit asks for
// vcov at rng_seed 0 (U69 E2-S4), and predict's summary
// names the run's test, distribution and terms.
func TestCompare_ResampledWaldReadsReplicateCovariance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	mnSingle, _ := mnModes(t)
	mp := mnSingle[0].p
	jack := func(s *types.RegressionSpec) *types.RegressionSpec { s.Resample = "jackknife"; return s }
	jackG := func(g int) func(*types.RegressionSpec) *types.RegressionSpec {
		return func(s *types.RegressionSpec) *types.RegressionSpec {
			// rng_seed 0: the derived group assignment ignores vcov
			// (U69 E2-S4), so the with / without comparison below holds.
			s.Resample, s.JackknifeGroups = "jackknife", g
			return s
		}
	}
	boot := func(s *types.RegressionSpec) *types.RegressionSpec {
		s.Resample, s.BootstrapIters = "bootstrap", 60 // rng_seed 0, vcov-neutral
		return s
	}
	mn := func(preds ...string) *types.RegressionSpec {
		s := cmpMultinomial("", preds...)
		s.TermTests = false // term_tests reads the analytical covariance: refused beside resample
		return s
	}
	for _, tc := range []struct {
		name   string
		p      *Pulse
		cohort string
		mod    func(*types.RegressionSpec) *types.RegressionSpec
		r0, r1 func() *types.RegressionSpec
		keys   []string
	}{
		{"ols jackknife", p, marginsCohort, jack, func() *types.RegressionSpec { return cmpOLS("x1") }, func() *types.RegressionSpec { return cmpOLS("x1", "x2") }, []string{"x2"}},
		{"ols bootstrap", p, marginsCohort, boot, func() *types.RegressionSpec { return cmpOLS("x1") }, func() *types.RegressionSpec { return cmpOLS("x1", "x2", "c") }, []string{"x2", "c"}},
		{"logit grouped jackknife", p, marginsCohort, jackG(6), func() *types.RegressionSpec { return cmpGLM("binomial", "b", "x1") },
			func() *types.RegressionSpec { return cmpGLM("binomial", "b", "x1", "x2") }, []string{"x2"}},
		{"poisson bootstrap", p, marginsCohort, boot, func() *types.RegressionSpec { return cmpGLM("poisson", "c", "x2") },
			func() *types.RegressionSpec { return cmpGLM("poisson", "c", "x1", "x2") }, []string{"x1"}},
		{"ordinal grouped jackknife", p, marginsCohort, jackG(4), func() *types.RegressionSpec { return cmpOrdinal("", "x1") },
			func() *types.RegressionSpec { return cmpOrdinal("", "x1", "x2") }, []string{"x2"}},
		{"multinomial grouped jackknife", mp, mnCohort, jackG(5), func() *types.RegressionSpec { return mn("x1") },
			func() *types.RegressionSpec { return mn("x1", "x2") }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(r0, r1 *types.RegressionSpec) (*ComposedResponse, *ComposedRequest) {
				t.Helper()
				c := cmpBatch(tc.cohort, map[string]*types.RegressionSpec{"m0": r0, "m1": r1},
					types.FitComparisonSpec{Name: "w", Restricted: "m0", Full: "m1", Test: types.ComparisonTestWald})
				resp, err := tc.p.Compose(ctx, c)
				if err != nil {
					t.Fatal(err)
				}
				comparisonsJSON(t, resp) // fails on a failed comparison
				return resp, c
			}
			withVcov := tc.mod(tc.r1())
			withVcov.Vcov = true
			resp, req := run(tc.mod(tc.r0()), withVcov)
			got, full := resp.Comparisons[0], resp.Fits["m1"]
			keys := tc.keys
			if keys == nil {
				// Every "<level>:x2" key of the multinomial fit.
				for _, k := range full.Vcov.RowKeys {
					if len(k) > 3 && k[len(k)-3:] == ":x2" {
						keys = append(keys, k)
					}
				}
			}
			want := waldOver(t, full.RegressionResult, keys)
			q := float64(len(keys))
			if full.Resample == "" || got.Distribution != types.ComparisonDistributionChiSquare || got.DF != q || got.DFResidual != 0 {
				t.Fatalf("comparison %+v (resample %q), want χ² on %v", got, full.Resample, q)
			}
			if math.Abs(got.Statistic-want) > 1e-9*want || got.PValue != statdist.ChiSquareSurvival(got.Statistic, q) {
				t.Fatalf("statistic %.15g p %v, want b'V⁻¹b = %.15g over the replicate vcov", got.Statistic, got.PValue, want)
			}
			// No vcov asked: the same replicate covariance, formed for
			// the comparison alone.
			bare, _ := run(tc.mod(tc.r0()), tc.mod(tc.r1()))
			if comparisonsJSON(t, bare) != comparisonsJSON(t, resp) {
				t.Fatalf("without vcov %s, with %s", comparisonsJSON(t, bare), comparisonsJSON(t, resp))
			}
			// The same replicates (rng_seed 0 ignores vcov): the full
			// fit's standard errors are bit-identical too.
			for k, se := range full.StdErrors {
				if math.Float64bits(bare.Fits["m1"].StdErrors[k]) != math.Float64bits(se) {
					t.Fatalf("SE %s: %v without vcov, %v with", k, bare.Fits["m1"].StdErrors[k], se)
				}
			}
			// The unresampled test reads the analytical covariance.
			plain, _ := run(tc.r0(), tc.r1())
			if plain.Comparisons[0].Statistic == got.Statistic {
				t.Fatalf("resampled statistic %v equals the unresampled one", got.Statistic)
			}
			// Predict names the run's test, distribution and terms.
			env, err := tc.p.PredictCompose(ctx, req)
			if err != nil || len(env.Errors) > 0 {
				t.Fatalf("predict: %v %+v", err, env.Errors)
			}
			s := env.Data.(*descx.ComposeValidationResult).Compare[0]
			if s.Test != got.Test || s.Distribution != got.Distribution || !slices.Equal(s.Terms, got.Terms) {
				t.Fatalf("predict %+v, run %s %s %v", s, got.Test, got.Distribution, got.Terms)
			}
		})
	}
}
