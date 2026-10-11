package processing

import (
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
)

// matrix_factor_test.go pins MAT_FACTOR's extraction kernels against R
// (mv_factor.json: psych::fa minres and principal axis on the reference
// fixtures' correlations, then the rotation engine's R definitions).

// Factor oracle tolerances (matrix-and-vectors.md, MAT_FACTOR), absolute
// + relative per cell:
//
//   - faPAOracleTol: principal axis replays psych's iteration step for
//     step (same start, same stopping rule, same iteration count), so
//     only eigensolver rounding (gonum vs LAPACK) is left.
//   - faExactOracleTol: minres against psych's EXACT optimum (the
//     principal-axis fixed point at min.err 1e-14); Pulse's Newton solve
//     stops at max|d| ≤ 1e-10.
//   - faPsychMinresTol: minres against psych's own L-BFGS-B fit, which
//     stops early (worst seen ~7e-5, attitude k = 3); Pulse's residual
//     must also never exceed psych's.
//   - faGPAStopTol: oblimin over minres loadings. GPA stops when its
//     projected gradient drops below 1e-5, so an input that moved by
//     1e-11 (the two minres solutions' gap) can stop it one iteration
//     earlier or later: the rotated values then agree only within the
//     criterion's own stopping band (worst seen 4.8e-5, mtcars
//     frequency k = 3). Principal axis feeds it R's input to the last
//     ulps and keeps faPAOracleTol.
const (
	faPAOracleTol    = 1e-9
	faExactOracleTol = 1e-7
	faPsychMinresTol = 2e-4
	faGPAStopTol     = 1e-4
)

type faRotOut struct {
	Loadings [][]float64 `json:"loadings"`
	Phi      [][]float64 `json:"phi"`
	Variance []float64   `json:"variance"`
}

type faOut struct {
	Unrotated      [][]float64         `json:"unrotated"`
	Communalities  []float64           `json:"communalities"`
	Uniquenesses   []float64           `json:"uniquenesses"`
	Variance       []float64           `json:"variance"`
	RMSR           float64             `json:"rmsr"`
	EmpiricalChisq float64             `json:"empirical_chisq"`
	DF             float64             `json:"df"`
	EmpiricalP     *float64            `json:"empirical_p"`
	Objective      float64             `json:"objective"`
	Rotations      map[string]faRotOut `json:"rotations"`
	Iterations     *int                `json:"iterations"`
	Converged      *bool               `json:"converged"`
}

type factorCase struct {
	Fixture       string      `json:"fixture"`
	Fields        []string    `json:"fields"`
	Weight        string      `json:"weight"`
	K             int         `json:"k"`
	NStar         float64     `json:"n_star"`
	Correlation   [][]float64 `json:"correlation"`
	SMC           []float64   `json:"smc"`
	KMO           float64     `json:"kmo"`
	BartlettChisq float64     `json:"bartlett_chisq"`
	BartlettP     float64     `json:"bartlett_p"`
	Minres        faOut       `json:"minres"`
	MinresExact   *faOut      `json:"minres_exact"`
	PrincipalAxis faOut       `json:"principal_axis"`
}

func (c factorCase) name() string { return fmt.Sprintf("%s/%s/k%d", c.Fixture, c.Weight, c.K) }

func (c factorCase) sym(t *testing.T) *linalg.Sym {
	t.Helper()
	s, err := linalg.NewSymFromRows(c.Correlation)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func faNear(t *testing.T, tol float64, where string, got, want float64) {
	t.Helper()
	if !(math.Abs(got-want) <= tol*(1+math.Abs(want))) {
		t.Errorf("%s = %.17g, R = %.17g (|Δ| %.3g)", where, got, want, math.Abs(got-want))
	}
}

func faNearRows(t *testing.T, tol float64, where string, got, want [][]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rows, R %d", where, len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("%s[%d]: %d cols, R %d", where, i, len(got[i]), len(want[i]))
		}
		for j := range want[i] {
			faNear(t, tol, fmt.Sprintf("%s[%d][%d]", where, i, j), got[i][j], want[i][j])
		}
	}
}

// faRotations are the criteria the oracle records, keyed like it.
var faRotations = map[string]rotationSpec{
	"varimax": {Method: vectors.RotationVarimax},
	"promax":  {Method: vectors.RotationPromax, Kappa: 4},
	"oblimin": {Method: vectors.RotationOblimin, Gamma: 0},
}

// faCheck compares a fit (and its rotations) with one oracle extraction.
func faCheck(t *testing.T, c factorCase, fit faFit, want faOut, tol, gpaTol float64) {
	t.Helper()
	faNearRows(t, tol, "unrotated", fit.Loadings, want.Unrotated)
	for i := range want.Communalities {
		faNear(t, tol, fmt.Sprintf("communality[%d]", i), fit.Communalities[i], want.Communalities[i])
		faNear(t, tol, fmt.Sprintf("uniqueness[%d]", i), 1-fit.Communalities[i], want.Uniquenesses[i])
	}
	rmsr, chisq, df, p := faResidualStats(c.sym(t), fit.Loadings, c.NStar)
	faNear(t, tol, "rmsr", rmsr, want.RMSR)
	faNear(t, tol, "empirical_chisq", chisq, want.EmpiricalChisq)
	if df != want.DF {
		t.Errorf("df = %v, R %v", df, want.DF)
	}
	if want.EmpiricalP == nil {
		if !math.IsNaN(p) {
			t.Errorf("empirical_p = %v, R null", p)
		}
	} else {
		faNear(t, tol, "empirical_p", p, *want.EmpiricalP)
	}
	for name, w := range want.Rotations {
		got, err := rotateLoadings(fit.Loadings, faRotations[name])
		if err != nil {
			t.Fatal(err)
		}
		rt := tol
		if name == "oblimin" {
			rt = gpaTol
		}
		faNearRows(t, rt, name+".loadings", got.Loadings, w.Loadings)
		faNearRows(t, rt, name+".phi", got.Phi, w.Phi)
		for i := range w.Variance {
			faNear(t, rt, fmt.Sprintf("%s.variance[%d]", name, i), got.Variance[i], w.Variance[i])
		}
	}
}

// TestFactorPrincipalAxis_MatchesPsych: principal axis replays
// psych::fa(fm = "pa") — the SMC start, the iteration count, the
// convergence flag (two cases stop at the 50-iteration cap), the
// loadings, communalities, residual figures and every rotation of the
// loadings within faPAOracleTol.
func TestFactorPrincipalAxis_MatchesPsych(t *testing.T) {
	doc := loadReference[factorCase](t, "mv_factor")
	capped := 0
	for _, c := range doc.Cases {
		t.Run(c.name(), func(t *testing.T) {
			r := c.sym(t)
			start, _ := minresStart(r)
			for i, w := range c.SMC {
				faNear(t, 1e-12, fmt.Sprintf("smc[%d]", i), start[i], w)
			}
			fit, err := faPrincipalAxis(r, c.K)
			if err != nil {
				t.Fatal(err)
			}
			want := c.PrincipalAxis
			if fit.Iterations != *want.Iterations || fit.Converged != *want.Converged {
				t.Fatalf("iterations %d converged %v, psych %d %v", fit.Iterations, fit.Converged, *want.Iterations, *want.Converged)
			}
			faCheck(t, c, fit, want, faPAOracleTol, faPAOracleTol)
		})
		if !*c.PrincipalAxis.Converged {
			capped++
		}
	}
	if capped == 0 {
		t.Fatal("no oracle case reaches the principal-axis cap")
	}
}

// TestFactorMinres_MatchesPsych: minres reaches psych's exact optimum
// (the principal-axis fixed point) within faExactOracleTol — loadings,
// communalities, residual figures and every rotation — and psych's own
// early-stopped fit within faPsychMinresTol with a residual never above
// psych's. An improper optimum (a uniqueness ≤ 0: psych bounds it to
// [0.005, 1], Pulse does not) is compared with psych on detection only —
// both fits put a uniqueness at or below 0; the Heywood fixture has no
// exact optimum at all (its principal-axis iteration never settles), so
// detection is all it checks.
func TestFactorMinres_MatchesPsych(t *testing.T) {
	doc := loadReference[factorCase](t, "mv_factor")
	improper := 0
	for _, c := range doc.Cases {
		t.Run(c.name(), func(t *testing.T) {
			fit, err := faMinres(c.sym(t), c.K)
			if err != nil {
				t.Fatal(err)
			}
			if !fit.Converged {
				t.Fatalf("not converged after %d iterations", fit.Iterations)
			}
			if c.MinresExact == nil {
				if minOf(c.Minres.Uniquenesses) > 0 || 1-maxOf(fit.Communalities) > 0 {
					t.Errorf("Heywood detection: psych min uniqueness %v, Pulse %v", minOf(c.Minres.Uniquenesses), 1-maxOf(fit.Communalities))
				}
				return
			}
			faCheck(t, c, fit, *c.MinresExact, faExactOracleTol, faGPAStopTol)
			if minOf(c.MinresExact.Uniquenesses) <= 0 {
				improper++
				if minOf(c.Minres.Uniquenesses) > 0 {
					t.Errorf("improper optimum: psych min uniqueness %v", minOf(c.Minres.Uniquenesses))
				}
				return
			}
			faNearRows(t, faPsychMinresTol, "psych unrotated", fit.Loadings, c.Minres.Unrotated)
			rmsr, _, _, _ := faResidualStats(c.sym(t), fit.Loadings, c.NStar)
			obj := float64(2 * rmsr * rmsr * float64(len(c.Fields)*(len(c.Fields)-1)))
			if obj > c.Minres.Objective+1e-12 {
				t.Errorf("residual %.17g above psych's %.17g", obj, c.Minres.Objective)
			}
		})
	}
	if improper == 0 {
		t.Fatal("no oracle case has an improper minres optimum")
	}
}

// TestFactorSolvers_NotConvergedAtCap: a solver stopped at its cap
// returns its last iterate with Converged false and its own solver name
// (the finalizer turns that into PULSE_MATRIX_NOT_CONVERGED), and the
// uncapped fit converges.
func TestFactorSolvers_NotConvergedAtCap(t *testing.T) {
	doc := loadReference[factorCase](t, "mv_factor")
	c := doc.Cases[1] // attitude k = 2: minres needs > 1 Newton step, pa > 1 iteration
	r := c.sym(t)
	for _, s := range []struct {
		solver string
		run    func(maxIter int) (faFit, error)
	}{
		{"fa_minres", func(n int) (faFit, error) { return faMinresSolve(r, c.K, n) }},
		{"fa_pa", func(n int) (faFit, error) { return faPrincipalAxisSolve(r, c.K, n) }},
	} {
		capped, err := s.run(1)
		if err != nil {
			t.Fatal(err)
		}
		if capped.Converged || capped.Iterations != 1 || capped.MaxIterations != 1 || capped.Solver != s.solver {
			t.Errorf("%s capped: %+v", s.solver, capped)
		}
		full, err := s.run(1000)
		if err != nil {
			t.Fatal(err)
		}
		if !full.Converged || full.Solver != s.solver {
			t.Errorf("%s uncapped: converged %v after %d", s.solver, full.Converged, full.Iterations)
		}
	}
}

// TestFactorMinres_DispatchAndShape: the extraction dispatch names both
// methods, and a fit's loadings are p × k with every column summing
// positive and communalities = row sums of squares.
func TestFactorMinres_DispatchAndShape(t *testing.T) {
	for _, name := range vectors.FactorExtractions() {
		if faExtractors[name] == nil {
			t.Errorf("extraction %q has no solver", name)
		}
	}
	doc := loadReference[factorCase](t, "mv_factor")
	for _, c := range doc.Cases {
		for name, ex := range faExtractors {
			fit, err := ex(c.sym(t), c.K)
			if err != nil {
				t.Fatal(err)
			}
			if len(fit.Loadings) != len(c.Fields) || len(fit.Loadings[0]) != c.K {
				t.Fatalf("%s %s: shape %dx%d", c.name(), name, len(fit.Loadings), len(fit.Loadings[0]))
			}
			for col := 0; col < c.K; col++ {
				sum := 0.0
				for i := range fit.Loadings {
					sum += fit.Loadings[i][col]
				}
				if sum < 0 {
					t.Errorf("%s %s: column %d sums %v", c.name(), name, col, sum)
				}
			}
			h := rowSquares(fit.Loadings)
			for i := range h {
				if h[i] != fit.Communalities[i] {
					t.Errorf("%s %s: communality[%d] %v, row sum %v", c.name(), name, i, fit.Communalities[i], h[i])
				}
			}
		}
	}
}

func minOf(v []float64) float64 {
	out := math.Inf(1)
	for _, x := range v {
		out = math.Min(out, x)
	}
	return out
}

func maxOf(v []float64) float64 {
	out := math.Inf(-1)
	for _, x := range v {
		out = math.Max(out, x)
	}
	return out
}
