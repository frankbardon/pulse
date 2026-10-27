package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/linalg"
)

// matrix_factor_ml_test.go pins MAT_FACTOR's maximum-likelihood
// extraction against R (mv_factor.json: ml = psych::fa(fm = "ml"),
// ml_exact = the optimum on psych's [0.005, 1] box, reached by
// L-BFGS-B at factr 0 then Newton with stats::optimHess; factanal
// asserted equal in the generator).
//
// Tolerances (matrix-and-vectors/matrix-factor-distance.md, MAT_FACTOR), absolute + relative:
//
//   - faMLExactTol: the solver against the exact optimum — loadings,
//     communalities, the residual and likelihood figures and every
//     rotation but oblimin (faGPAStopTol, the GPA stopping band).
//   - faMLPsychTol: against psych's own L-BFGS-B fit, which stops early
//     (its loadings up to ~2.2e-5 off the optimum); Pulse's objective must
//     never exceed psych's.
//   - faMLStatsTol: the likelihood figures recomputed from psych's own
//     loadings against psych::fa.stats on them — the formula alone.
const (
	faMLExactTol = 1e-9
	faMLPsychTol = 1e-4
	faMLStatsTol = 1e-9
	// faRMSEAIntervalTol: the RMSEA interval against R's bounds (psych's
	// rule at uniroot tol 1e-300). Both sides solve the noncentral χ²
	// root to the last bit of their own CDF, which agree to ~1e-13
	// (TestReferenceOracle_NoncentralChiSquareCDF; R's is 1e-12 absolute
	// above ncp 80), so the bounds agree well inside 1e-9 once the
	// statistic does.
	faRMSEAIntervalTol = 1e-9
)

func faNearNull(t *testing.T, tol float64, where string, got float64, want *float64) {
	t.Helper()
	if want == nil {
		if !math.IsNaN(got) {
			t.Errorf("%s = %v, R null", where, got)
		}
		return
	}
	faNear(t, tol, where, got, *want)
}

// faCheckLikelihood compares the likelihood figures of loadings l with
// one oracle ML entry.
func faCheckLikelihood(t *testing.T, c factorCase, l [][]float64, want faOut, tol float64) {
	t.Helper()
	st := faMLFitIndices(c.sym(t), l, c.NStar, 0.9)
	if st.Insufficient {
		t.Fatalf("insufficient at N* %v", c.NStar)
	}
	faNear(t, tol, "likelihood_chisq", st.ChiSq, want.LikelihoodChisq)
	faNearNull(t, tol, "likelihood_p", st.P, want.LikelihoodP)
	faNearNull(t, tol, "tli", st.TLI, want.TLI)
	faNearNull(t, tol, "rmsea", st.RMSEA, want.RMSEA)
	faNearNull(t, tol, "bic", st.BIC, want.BIC)
	faNearNull(t, faRMSEAIntervalTol, "rmsea_lower", st.RMSEALower, want.RMSEALower)
	faNearNull(t, faRMSEAIntervalTol, "rmsea_upper", st.RMSEAUpper, want.RMSEAUpper)
}

// TestFactorML_MatchesExactOptimum: the projected Newton solve converges
// on every oracle case to the exact ML optimum — loadings (psych's sign
// and descending-variance order), communalities, uniquenesses, the
// residual and likelihood figures and every rotation — and flags the
// floor-bound uniquenesses (the ML Heywood case) exactly where R's
// optimum sits on the 0.005 bound, on more than one case.
func TestFactorML_MatchesExactOptimum(t *testing.T) {
	t.Parallel()

	doc := loadReference[factorCase](t, "mv_factor")
	bounded := 0
	for _, c := range doc.Cases {
		t.Run(c.name(), func(t *testing.T) {
			fit, err := faML(c.sym(t), c.K)
			if err != nil {
				t.Fatal(err)
			}
			if !fit.Converged || fit.Solver != "fa_ml" {
				t.Fatalf("converged %v after %d (solver %s)", fit.Converged, fit.Iterations, fit.Solver)
			}
			want := c.MLExact
			faCheck(t, c, fit, want, faMLExactTol, faGPAStopTol)
			faCheckLikelihood(t, c, fit.Loadings, want, faMLExactTol)
			any := false
			for i, psi := range want.Psi {
				atFloor := psi <= faMLLower
				if fit.Bounded[i] != atFloor {
					t.Errorf("bounded[%d] = %v, R psi %v", i, fit.Bounded[i], psi)
				}
				any = any || atFloor
			}
			if any {
				bounded++
			}
		})
	}
	if bounded < 2 {
		t.Fatalf("%d oracle cases put a uniqueness on the floor, want at least 2", bounded)
	}
}

// TestFactorML_BeatsPsych: psych's early-stopped fit sits within
// faMLPsychTol of Pulse's loadings, and Pulse's objective is never above
// the one at psych's uniquenesses.
func TestFactorML_BeatsPsych(t *testing.T) {
	t.Parallel()

	doc := loadReference[factorCase](t, "mv_factor")
	for _, c := range doc.Cases {
		t.Run(c.name(), func(t *testing.T) {
			r := c.sym(t)
			fit, err := faML(r, c.K)
			if err != nil {
				t.Fatal(err)
			}
			faNearRows(t, faMLPsychTol, "psych unrotated", fit.Loadings, c.ML.Unrotated)
			ours, err := faMLObjectiveAt(r, fit.Psi, c.K)
			if err != nil {
				t.Fatal(err)
			}
			theirs, err := faMLObjectiveAt(r, c.ML.Psi, c.K)
			if err != nil {
				t.Fatal(err)
			}
			if ours > theirs+1e-14 {
				t.Errorf("objective %.17g above psych's %.17g", ours, theirs)
			}
		})
	}
}

func faMLObjectiveAt(r *linalg.Sym, psi []float64, k int) (float64, error) {
	pt, err := faMLEval(r, append([]float64(nil), psi...), k)
	return pt.f, err
}

// TestFactorMLFitIndices_MatchPsychFaStats: the likelihood chi-square,
// its p, TLI, RMSEA and BIC recomputed from psych's own ML loadings
// equal psych::fa.stats on them (null at df ≤ 0), and the solver's
// objective at psych's uniquenesses is psych's F.
func TestFactorMLFitIndices_MatchPsychFaStats(t *testing.T) {
	t.Parallel()

	doc := loadReference[factorCase](t, "mv_factor")
	nullTLI := 0
	for _, c := range doc.Cases {
		t.Run(c.name(), func(t *testing.T) {
			faCheckLikelihood(t, c, c.ML.Unrotated, c.ML, faMLStatsTol)
		})
		if c.ML.TLI == nil {
			nullTLI++
		}
	}
	if nullTLI == 0 {
		t.Fatal("no oracle case has df ≤ 0 (null p, TLI, RMSEA, BIC)")
	}
}

// TestFactorMLFitIndices_Guards: n_m = (N* − 1) − (2p + 5)/6 − 2k/3 ≤ 0
// is insufficient (every figure null), a non-finite N* nulls every figure
// without the flag, the chi-square is never negative, and a zero-factor
// model reduces to Bartlett's statistic.
func TestFactorMLFitIndices_Guards(t *testing.T) {
	t.Parallel()

	doc := loadReference[factorCase](t, "mv_factor")
	c := doc.Cases[1]
	r := c.sym(t)
	p := len(c.Fields)
	l := c.MLExact.Unrotated
	minN := float64(2*p+4*c.K+11) / 6
	st := faMLFitIndices(r, l, minN, 0.9)
	if !st.Insufficient || !math.IsNaN(st.ChiSq) || !math.IsNaN(st.TLI) || st.MinNStar != minN {
		t.Errorf("at the minimum N*: %+v", st)
	}
	if st := faMLFitIndices(r, l, minN+0.5, 0.9); st.Insufficient || math.IsNaN(st.ChiSq) {
		t.Errorf("above the minimum N*: %+v", st)
	}
	if st := faMLFitIndices(r, l, math.Inf(1), 0.9); st.Insufficient || !math.IsNaN(st.ChiSq) {
		t.Errorf("infinite N*: %+v", st)
	}
	zero := make([][]float64, p)
	for i := range zero {
		zero[i] = []float64{}
	}
	st = faMLFitIndices(r, zero, c.NStar, 0.9)
	b := bartlettSphericity(r, c.NStar)
	faNear(t, 1e-10, "zero-factor chisq", st.ChiSq, b.ChiSq)
	faNear(t, 1e-10, "zero-factor p", st.P, b.PValue)
	faNear(t, 1e-12, "zero-factor tli", st.TLI, 0)
	// A perfect fit's rounding-level negative objective clamps to 0.
	perfect := faMLFitIndices(r, faPerfectLoadings(c, t), c.NStar, 0.9)
	if perfect.ChiSq < 0 {
		t.Errorf("chisq %v below 0", perfect.ChiSq)
	}
}

// faPerfectLoadings is a one-factor model reproducing its own implied
// correlation, so the ML objective against it is 0 up to rounding.
func faPerfectLoadings(c factorCase, t *testing.T) [][]float64 {
	t.Helper()
	l := c.MLExact.Unrotated
	out := make([][]float64, len(l))
	for i := range l {
		out[i] = []float64{l[i][0]}
	}
	return out
}

// TestFactorMLHessian_MatchesFiniteDifference: the exact θ-Hessian
// equals a central difference of the analytic gradient, at the start
// and away from it, so the Newton step is the true one.
func TestFactorMLHessian_MatchesFiniteDifference(t *testing.T) {
	t.Parallel()

	doc := loadReference[factorCase](t, "mv_factor")
	for _, c := range doc.Cases[:7] {
		t.Run(c.name(), func(t *testing.T) {
			r := c.sym(t)
			p := len(c.Fields)
			h0, _ := minresStart(r)
			psi := make([]float64, p)
			for i := range psi {
				psi[i] = math.Min(0.95, math.Max(0.05, 1-h0[i])) * (1 + 0.03*float64(i%3))
			}
			pt, err := faMLEval(r, psi, c.K)
			if err != nil {
				t.Fatal(err)
			}
			hess := faMLHessian(pt, c.K)
			const step = 1e-6
			for j := 0; j < p; j++ {
				up := append([]float64(nil), psi...)
				dn := append([]float64(nil), psi...)
				up[j] *= math.Exp(step)
				dn[j] *= math.Exp(-step)
				pu, err := faMLEval(r, up, c.K)
				if err != nil {
					t.Fatal(err)
				}
				pd, err := faMLEval(r, dn, c.K)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < p; i++ {
					fd := (pu.g[i] - pd.g[i]) / (2 * step)
					if math.Abs(fd-hess[i][j]) > 1e-6*(1+math.Abs(fd)) {
						t.Errorf("H[%d][%d] = %.12g, finite difference %.12g", i, j, hess[i][j], fd)
					}
				}
			}
			// The gradient is the objective's: a central difference of F.
			for i := 0; i < p; i++ {
				up := append([]float64(nil), psi...)
				dn := append([]float64(nil), psi...)
				up[i] *= math.Exp(step)
				dn[i] *= math.Exp(-step)
				fu, _ := faMLObjectiveAt(r, up, c.K)
				fdn, _ := faMLObjectiveAt(r, dn, c.K)
				fd := (fu - fdn) / (2 * step)
				if math.Abs(fd-pt.g[i]) > 1e-6*(1+math.Abs(fd)) {
					t.Errorf("g[%d] = %.12g, finite difference %.12g", i, pt.g[i], fd)
				}
			}
		})
	}
}

// TestFaRMSEAInterval_Rule: psych::fa.stats' interval rule on hand-built
// statistics — each bound sits where the noncentral χ² CDF at the
// statistic equals its tail, a wider coverage widens both sides, a
// statistic below the central (1 − tail) quantile has lower bound 0 (and
// below the tail quantile upper 0 too), a root beyond psych's bracket
// [0, max(N*, χ²) + 2N*] is null, a bound on the wrong side of the point
// estimate is null, and df ≤ 0, N* ≤ 1 or a coverage outside (0, 1)
// nulls both.
func TestFaRMSEAInterval_Rule(t *testing.T) {
	t.Parallel()

	const chi, df, n = 40.0, 12.0, 150.0
	rmsea := math.Sqrt(math.Max(chi/(df*n)-1/(n-1), 0))
	lo, hi := faRMSEAInterval(chi, df, n, rmsea, 0.9)
	if !(lo > 0 && lo < rmsea && hi > rmsea) {
		t.Fatalf("interval [%v, %v] around %v", lo, hi, rmsea)
	}
	for _, b := range []struct {
		bound, tail float64
	}{{lo, 0.95}, {hi, 0.05}} {
		lambda := b.bound * b.bound * (n - 1) * df
		if got := noncentralChiSquareCDF(chi, df, lambda); math.Abs(got-b.tail) > 1e-12 {
			t.Errorf("CDF at the bound λ %v = %.17g, want %v", lambda, got, b.tail)
		}
	}
	lo95, hi95 := faRMSEAInterval(chi, df, n, rmsea, 0.95)
	if !(lo95 < lo && hi95 > hi) {
		t.Errorf("95%% [%v, %v] does not contain 90%% [%v, %v]", lo95, hi95, lo, hi)
	}
	// χ² = 12 on 12 df: below the central 95th percentile, above the 5th.
	if lo, hi := faRMSEAInterval(12, df, n, 0, 0.9); lo != 0 || !(hi > 0) {
		t.Errorf("χ² at df: [%v, %v], want [0, >0]", lo, hi)
	}
	// χ² = 3 on 12 df: below the central 5th percentile, so both 0.
	if lo, hi := faRMSEAInterval(3, df, n, 0, 0.9); lo != 0 || hi != 0 {
		t.Errorf("tiny χ²: [%v, %v], want [0, 0]", lo, hi)
	}
	// N* = 2, χ² = 100 on 1 df: psych's bracket stops at 104, short of
	// the upper root (null), while the lower root's √(λ/((N* − 1)·df))
	// exceeds the point estimate √(χ²/(df·N*) − 1/(N* − 1)) (null).
	pt := math.Sqrt(100.0/2 - 1)
	if lo, hi := faRMSEAInterval(100, 1, 2, pt, 0.9); !math.IsNaN(lo) || !math.IsNaN(hi) {
		t.Errorf("out-of-bracket / wrong-side: [%v, %v], want [NaN, NaN]", lo, hi)
	}
	// The same out-of-bracket upper root with a zero point estimate is 0.
	if _, hi := faRMSEAInterval(100, 1, 2, 0, 0.9); hi != 0 {
		t.Errorf("out-of-bracket upper at rmsea 0 = %v, want 0", hi)
	}
	for _, c := range []struct{ chi, df, n, conf float64 }{
		{chi, 0, n, 0.9}, {chi, -1, n, 0.9}, {chi, df, 1, 0.9}, {chi, df, n, 0}, {chi, df, n, 1}, {math.NaN(), df, n, 0.9},
	} {
		if lo, hi := faRMSEAInterval(c.chi, c.df, c.n, rmsea, c.conf); !math.IsNaN(lo) || !math.IsNaN(hi) {
			t.Errorf("faRMSEAInterval(%v, %v, %v, conf %v) = [%v, %v], want null", c.chi, c.df, c.n, c.conf, lo, hi)
		}
	}
}
