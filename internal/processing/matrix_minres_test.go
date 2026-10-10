package processing

import (
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// faMinresCase is one mv_fa_minres.json case: psych::fa(R, nfactors =
// 1, fm = "minres", rotate = "none") on a fixture's correlation matrix
// (scripts/reference/gen_multivariate.R).
type faMinresCase struct {
	Fixture      string      `json:"fixture"`
	Fields       []string    `json:"fields"`
	Reverse      []string    `json:"reverse"`
	Missing      string      `json:"missing"`
	Weight       string      `json:"weight"`
	Correlation  [][]float64 `json:"correlation"`
	SMCStart     []float64   `json:"smc_start"`
	Loadings     []float64   `json:"loadings"`
	Uniquenesses []float64   `json:"uniquenesses"`
	Heywood      bool        `json:"heywood"`
}

func (c faMinresCase) label() string {
	return c.Fixture + "/" + c.Missing + "/" + c.Weight + "/reverse=" + strings.Join(c.Reverse, ",")
}

// minresOracleTol is the documented tolerance of the minres fit against
// psych::fa on a PROPER fit (every ψ > 0): 1e-5 absolute per loading and
// uniqueness. Both minimise the same off-diagonal objective, but psych
// stops its ψ-parametrised L-BFGS-B early — optim's default factr, and a
// gradient (FAgr: λ_k² + ψ_k − 1, the ULS gradient) that is not the
// exact gradient of its minres objective — leaving its loadings up to
// ~7e-6 from the optimum, while Pulse's coordinate descent runs to
// minresConvTol (1e-12). TestMinresOneFactor_MatchesPsychFA therefore
// also asserts Pulse's residual objective is never above psych's: the
// gap is psych's stopping error, not Pulse's. The SMC start is closed
// form and matches to minresStartTol.
//
// A HEYWOOD fit is compared on detection only (the same members with
// ψ ≤ 0), never on values: psych bounds its ψ parameter to [0.005, 1]
// and stops wherever its line search fails against the bound, so its
// improper loadings are an optimiser-path artefact (the heywood
// fixture's free members do not satisfy psych's own fixed point). Pulse
// reports the unconstrained minres optimum — on 3 members the exact fit
// λ₁² = r₁₂·r₁₃ / r₂₃ — and flags it; the operator decides how a Heywood
// fit surfaces.
const (
	minresOracleTol = 1e-5
	minresStartTol  = 1e-12
)

// minresObjective is the off-diagonal residual sum of squares
// Σ_{i≠j} (r_ij − λ_i·λ_j)² both fits minimise.
func minresObjective(r [][]float64, lambda []float64) float64 {
	sum := 0.0
	for i := range r {
		for j := range r {
			if i != j {
				d := r[i][j] - lambda[i]*lambda[j]
				sum += d * d
			}
		}
	}
	return sum
}

// TestMinresOneFactor_MatchesPsychFA pins the one-factor minres fit to
// psych::fa(nfactors = 1, fm = "minres") on attitude and every synthetic
// fixture case: the SMC start; on a proper fit the loadings (sign:
// Σλ > 0) and uniquenesses (1 − λ²) within minresOracleTol; on every
// fit a residual objective no worse than psych's, convergence, and the
// Heywood members (ψ ≤ 0) psych's own uniquenesses show.
func TestMinresOneFactor_MatchesPsychFA(t *testing.T) {
	doc := loadReference[faMinresCase](t, "mv_fa_minres")
	seen := map[string]bool{}
	worst, sweeps := 0.0, 0
	for _, c := range doc.Cases {
		seen[c.Fixture] = true
		fit, err := minresOneFactor(symFrom(t, c.Correlation))
		if err != nil {
			t.Fatalf("%s: %v", c.label(), err)
		}
		if !fit.Converged || fit.Iterations < 1 || fit.Iterations >= minresMaxIter || fit.MaxIterations != minresMaxIter {
			t.Errorf("%s: converged=%v after %d of %d sweeps", c.label(), fit.Converged, fit.Iterations, fit.MaxIterations)
		}
		sweeps = max(sweeps, fit.Iterations)
		if fit.StartFallback {
			t.Errorf("%s: SMC start fell back on an invertible matrix", c.label())
		}
		for k := range c.SMCStart {
			if d := math.Abs(fit.Start[k] - c.SMCStart[k]); !(d <= minresStartTol) {
				t.Errorf("%s: SMC start [%d] = %.17g, psych %.17g", c.label(), k, fit.Start[k], c.SMCStart[k])
			}
		}
		got, psych := minresObjective(c.Correlation, fit.Loadings), minresObjective(c.Correlation, c.Loadings)
		if !(got <= psych+1e-12) {
			t.Errorf("%s: residual objective %.17g above psych's %.17g", c.label(), got, psych)
		}
		// psych flags "heywood" at ψ ≤ 0.005 or |λ| ≥ 1; Pulse reports
		// the strict ψ ≤ 0 members, which psych's uniquenesses show.
		var wantHeywood []int
		for k, psi := range c.Uniquenesses {
			if psi <= 0 {
				wantHeywood = append(wantHeywood, k)
			}
		}
		if len(wantHeywood) > 0 != c.Heywood || !equalInts(fit.Heywood, wantHeywood) {
			t.Errorf("%s: heywood %v, psych ψ≤0 at %v (psych heywood=%v)", c.label(), fit.Heywood, wantHeywood, c.Heywood)
		}
		if w := minresWarnings("m", fit); w != nil {
			t.Errorf("%s: converged fit warned %v", c.label(), w)
		}
		if c.Heywood {
			continue
		}
		for k := range c.Loadings {
			for _, pair := range [][2]float64{{fit.Loadings[k], c.Loadings[k]}, {fit.Uniquenesses[k], c.Uniquenesses[k]}} {
				d := math.Abs(pair[0] - pair[1])
				worst = math.Max(worst, d)
				if !(d <= minresOracleTol) {
					t.Errorf("%s [%d] = %.17g, psych %.17g (|Δ| %.3g)", c.label(), k, pair[0], pair[1], d)
				}
			}
		}
	}
	for _, f := range []string{"attitude", "likert_ties", "reversed_items", "nulls", "heywood"} {
		if !seen[f] {
			t.Errorf("oracle has no %s case", f)
		}
	}
	t.Logf("minres vs psych::fa (proper fits): worst |Δ| %.3g; most sweeps %d of %d", worst, sweeps, minresMaxIter)
}

// TestMinresOneFactor_HeywoodIsExactFit: on the 3-member heywood
// fixture one factor is just identified, so the minres optimum is the
// exact fit λ₁ = √(r₁₂·r₁₃ / r₂₃) > 1 — ψ₁ < 0, reported as Heywood
// member 0 — with a zero residual.
func TestMinresOneFactor_HeywoodIsExactFit(t *testing.T) {
	doc := loadReference[faMinresCase](t, "mv_fa_minres")
	for _, c := range doc.Cases {
		if c.Fixture != "heywood" {
			continue
		}
		r := c.Correlation
		fit, err := minresOneFactor(symFrom(t, r))
		if err != nil {
			t.Fatal(err)
		}
		l1 := math.Sqrt(r[0][1] * r[0][2] / r[1][2])
		want := []float64{l1, r[0][1] / l1, r[0][2] / l1}
		for k := range want {
			if d := math.Abs(fit.Loadings[k] - want[k]); !(d <= 1e-10) {
				t.Errorf("λ[%d] = %.17g, exact %.17g", k, fit.Loadings[k], want[k])
			}
		}
		if !equalInts(fit.Heywood, []int{0}) || !(fit.Uniquenesses[0] < 0) || !(fit.Uniquenesses[1] > 0) {
			t.Errorf("heywood %v ψ %v", fit.Heywood, fit.Uniquenesses)
		}
		if obj := minresObjective(r, fit.Loadings); !(obj <= 1e-18) {
			t.Errorf("residual objective %g, want 0", obj)
		}
		return
	}
	t.Fatal("no heywood case")
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMinresOneFactor_Deterministic: the fit is pure FMA-free arithmetic
// from a fixed start, so two runs are bit-identical, and the Σλ > 0 sign
// rule makes a sign-flipped start irrelevant (reversed items keep their
// negative loadings).
func TestMinresOneFactor_Deterministic(t *testing.T) {
	doc := loadReference[faMinresCase](t, "mv_fa_minres")
	for _, c := range doc.Cases {
		s := symFrom(t, c.Correlation)
		a, _ := minresOneFactor(s)
		b, _ := minresOneFactor(s)
		sum := 0.0
		for k := range a.Loadings {
			if math.Float64bits(a.Loadings[k]) != math.Float64bits(b.Loadings[k]) ||
				math.Float64bits(a.Uniquenesses[k]) != math.Float64bits(b.Uniquenesses[k]) {
				t.Errorf("%s: run differs at [%d]", c.label(), k)
			}
			sum += a.Loadings[k]
		}
		if a.Iterations != b.Iterations || !(sum > 0) {
			t.Errorf("%s: iterations %d vs %d, Σλ = %v", c.label(), a.Iterations, b.Iterations, sum)
		}
	}
}

// TestMinresOneFactor_NotConvergedWarns: a fit stopped at its cap
// reports Converged false and its caller's warning is
// PULSE_MATRIX_NOT_CONVERGED with the cap and tolerance — never a silent
// best effort.
func TestMinresOneFactor_NotConvergedWarns(t *testing.T) {
	doc := loadReference[faMinresCase](t, "mv_fa_minres")
	fit, err := minresSolve(symFrom(t, doc.Cases[0].Correlation), 2)
	if err != nil {
		t.Fatal(err)
	}
	if fit.Converged || fit.Iterations != 2 || fit.MaxIterations != 2 {
		t.Fatalf("capped fit: converged=%v iterations=%d max=%d", fit.Converged, fit.Iterations, fit.MaxIterations)
	}
	ws := minresWarnings("m", fit)
	if len(ws) != 1 || ws[0].Code != string(errors.PULSE_MATRIX_NOT_CONVERGED) {
		t.Fatalf("warnings = %v", ws)
	}
	d := ws[0].Details
	if d["matrix"] != "m" || d["solver"] != "minres_one_factor" || d["iterations"] != 2 || d["max_iterations"] != 2 || d["tolerance"] != minresConvTol {
		t.Errorf("details = %v", d)
	}
}

// TestMinresOneFactor_EdgeInputs: an identity matrix fits λ = 0 (ψ = 1)
// at once; a singular matrix starts from the |r| fallback and still
// converges (two identical members share one loading); a covariance
// input reports ψ on its own scale (s_ii − λ²); undefined input is the
// caller-invariant PROCESSING_INTERNAL.
func TestMinresOneFactor_EdgeInputs(t *testing.T) {
	id := symFrom(t, [][]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}})
	fit, err := minresOneFactor(id)
	if err != nil || !fit.Converged || fit.Iterations != 1 {
		t.Fatalf("identity: %+v %v", fit, err)
	}
	for k := range fit.Loadings {
		if fit.Loadings[k] != 0 || fit.Uniquenesses[k] != 1 {
			t.Errorf("identity [%d]: λ %v ψ %v", k, fit.Loadings[k], fit.Uniquenesses[k])
		}
	}

	dup := symFrom(t, [][]float64{{1, 1, 0.5, 0.4}, {1, 1, 0.5, 0.4}, {0.5, 0.5, 1, 0.3}, {0.4, 0.4, 0.3, 1}})
	fit, err = minresOneFactor(dup)
	if err != nil || !fit.Converged || !fit.StartFallback {
		t.Fatalf("singular: converged=%v fallback=%v err=%v", fit.Converged, fit.StartFallback, err)
	}
	if fit.Start[0] != 1 || fit.Start[2] != 0.5 {
		t.Errorf("fallback start = %v, want the largest |r| per row", fit.Start)
	}
	if math.Abs(fit.Loadings[0]-fit.Loadings[1]) > 1e-9 {
		t.Errorf("identical members load %v vs %v", fit.Loadings[0], fit.Loadings[1])
	}

	// Covariance = D·R·D (D = diag(2, 3, 1)) of a one-factor-exact
	// correlation λ = (0.8, 0.7, 0.6): λ scales by D, ψ = s_ii − λ².
	lam := []float64{0.8, 0.7, 0.6}
	sd := []float64{2, 3, 1}
	cov, _ := linalg.NewSym(3, nil)
	for i := 0; i < 3; i++ {
		for j := i; j < 3; j++ {
			r := lam[i] * lam[j]
			if i == j {
				r = 1
			}
			cov.Set(i, j, sd[i]*r*sd[j])
		}
	}
	fit, err = minresOneFactor(cov)
	if err != nil || !fit.Converged {
		t.Fatalf("covariance: %+v %v", fit, err)
	}
	for k := range lam {
		if math.Abs(fit.Loadings[k]-sd[k]*lam[k]) > 1e-9 || math.Abs(fit.Uniquenesses[k]-sd[k]*sd[k]*(1-lam[k]*lam[k])) > 1e-9 {
			t.Errorf("covariance [%d]: λ %v ψ %v", k, fit.Loadings[k], fit.Uniquenesses[k])
		}
	}

	nan := symFrom(t, [][]float64{{1, math.NaN(), 0}, {math.NaN(), 1, 0}, {0, 0, 1}})
	var ce *errors.CodedError
	if _, err := minresOneFactor(nan); !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_INTERNAL {
		t.Errorf("undefined input: %v", err)
	}
}
