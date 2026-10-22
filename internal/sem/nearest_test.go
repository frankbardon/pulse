package sem

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// TestCovNearest_MatchesStatsmodels pins CovNearest to statsmodels'
// cov_nearest(S, threshold=1e-2) on synthetic matrices
// (testdata/gen_nearest.py): rank-deficient, indefinite, already
// positive definite (unclipped), and an exact dependency beside a
// unit-variance uncorrelated member.
func TestCovNearest_MatchesStatsmodels(t *testing.T) {
	b, err := os.ReadFile("testdata/nearest_statsmodels.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref map[string]struct {
		Input   [][]float64 `json:"input"`
		Output  [][]float64 `json:"output"`
		MinEig  float64     `json:"min_eigenvalue"`
		Clipped bool        `json:"clipped"`
	}
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatal(err)
	}
	if len(ref) != 4 {
		t.Fatalf("%d reference cases, want 4", len(ref))
	}
	for name, c := range ref {
		t.Run(name, func(t *testing.T) {
			p := len(c.Input)
			s := mat.NewSymDense(p, nil)
			for i := 0; i < p; i++ {
				for j := i; j < p; j++ {
					s.SetSym(i, j, c.Input[i][j])
				}
			}
			got, err := CovNearest(s, NearestThreshold)
			if err != nil {
				t.Fatal(err)
			}
			if got.Clipped != c.Clipped {
				t.Errorf("clipped %v, statsmodels %v", got.Clipped, c.Clipped)
			}
			if d := math.Abs(got.MinEigenvalue - c.MinEig); d > 1e-12 {
				t.Errorf("min eigenvalue %g, statsmodels %g", got.MinEigenvalue, c.MinEig)
			}
			for i := 0; i < p; i++ {
				for j := 0; j < p; j++ {
					if d := rel(got.Cov.At(i, j), c.Output[i][j]); d > 1e-12 {
						t.Errorf("[%d][%d] = %.17g, statsmodels %.17g (rel %.1e)", i, j, got.Cov.At(i, j), c.Output[i][j], d)
					}
				}
			}
			if c.Clipped && !PositiveDefinite(got.Cov) {
				t.Error("the adjusted matrix does not factor")
			}
		})
	}
}

// TestCovNearest_Refusals: a variance that is zero, negative or not
// finite has no correlation form (statsmodels produces NaN there).
func TestCovNearest_Refusals(t *testing.T) {
	for name, d := range map[string]float64{"zero": 0, "negative": -1, "inf": math.Inf(1), "nan": math.NaN()} {
		s := mat.NewSymDense(2, []float64{1, 0, 0, d})
		if _, err := CovNearest(s, NearestThreshold); err == nil {
			t.Errorf("%s variance: no error", name)
		}
	}
}

// TestFit_NearestMatchesSemopy fits semopy's own adjusted matrix
// (fit_semopy_nearest.py: an n < p battery, and a standardized one with
// a constant item whose zero variance the reference's wrapped
// cov_nearest enters at 1 — semopy itself raises there, and MAT_SEM
// refuses it, so that case pins FitCovNearest alone), the Lambda starts
// off the data's covariance, and pins the fit to semopy's on it — the
// default setup to its default fit, Fisher scoring to its tight fit.
func TestFit_NearestMatchesSemopy(t *testing.T) {
	for _, name := range []string{"singular", "constant"} {
		desc, err := os.ReadFile("testdata/" + name + "_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewModel(string(desc), false)
		if err != nil {
			t.Fatal(err)
		}
		for _, cfg := range []struct {
			name, ref string
			opt       Options
			tol       float64
		}{
			{"default", "default", Options{}, 1e-9},
			{"fisher", "tight", Options{Solver: SolverFisher}, 1e-6},
		} {
			t.Run(name+"/"+cfg.name, func(t *testing.T) {
				path := "testdata/" + name + "_semopy_" + cfg.ref + ".json"
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var aux struct {
					N      float64     `json:"n"`
					Sample [][]float64 `json:"sample_cov"`
					Adj    [][]float64 `json:"adjusted_cov"`
				}
				if err := json.Unmarshal(b, &aux); err != nil {
					t.Fatal(err)
				}
				p := len(aux.Adj)
				s, raw := mat.NewSymDense(p, nil), mat.NewSymDense(p, nil)
				for i := 0; i < p; i++ {
					for j := i; j < p; j++ {
						s.SetSym(i, j, aux.Adj[i][j])
						raw.SetSym(i, j, aux.Sample[i][j])
					}
				}
				if PositiveDefinite(raw) {
					t.Fatal("the sample covariance factors: no substitution to test")
				}
				fit, err := FitCovNearest(m, s, raw, aux.N, cfg.opt)
				if err != nil {
					t.Fatal(err)
				}
				if err := ComputeStats(fit, string(desc)); err != nil {
					t.Fatal(err)
				}
				checkAgainstSemopy(t, fit, loadRef(t, path), cfg.tol)
			})
		}
	}
}

// TestFit_NearSingularRawMatchesSemopy: a near-exact dependency that
// still factors (near_data.csv, gen_near_data.py: its covariance is
// exactly L·Lᵀ with l55 = 2^-24, so every Cholesky step is exact on
// any platform) is fitted raw, as semopy fits it (no cov_nearest), and
// pinned to semopy's fit on that matrix (fit_semopy_exact.py). SLSQP
// to convergence and Fisher scoring meet semopy's tight fit at 1e-6;
// the default ftol 1e-6 early stop is pinned only at 1e-4: on this
// near-singular S the stopping iterate moves at that level with the
// summation order (semopy's own default fit does, between two
// mathematically equal ways of handing it the same matrix).
func TestFit_NearSingularRawMatchesSemopy(t *testing.T) {
	desc, err := os.ReadFile("testdata/near_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModel(string(desc), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []struct {
		name, ref string
		opt       Options
		tol       float64
	}{
		{"default", "default", Options{}, 1e-4},
		{"slsqp_tight", "tight", Options{Ftol: 1e-14, MaxIter: 100000}, 1e-6},
		{"fisher", "tight", Options{Solver: SolverFisher}, 1e-6},
	} {
		t.Run(cfg.name, func(t *testing.T) {
			path := "testdata/near_semopy_" + cfg.ref + ".json"
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var aux struct {
				N      float64     `json:"n"`
				Sample [][]float64 `json:"sample_cov"`
			}
			if err := json.Unmarshal(b, &aux); err != nil {
				t.Fatal(err)
			}
			s, n := readCSVCov(t, "testdata/near_data.csv", m.Observed)
			if n != aux.N {
				t.Fatalf("n %v, semopy %v", n, aux.N)
			}
			for i := range aux.Sample {
				for j := range aux.Sample[i] {
					if s.At(i, j) != aux.Sample[i][j] {
						t.Fatalf("S[%d][%d] = %.17g, the rows' exact covariance %.17g", i, j, s.At(i, j), aux.Sample[i][j])
					}
				}
			}
			if !PositiveDefinite(s) {
				t.Fatal("the near-singular covariance does not factor")
			}
			fit, err := FitCov(m, s, n, cfg.opt)
			if err != nil {
				t.Fatal(err)
			}
			if err := ComputeStats(fit, string(desc)); err != nil {
				t.Fatal(err)
			}
			checkAgainstSemopy(t, fit, loadRef(t, path), cfg.tol)
		})
	}
}
