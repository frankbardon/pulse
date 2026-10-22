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
// a constant item), the Lambda starts off the data's covariance, and
// pins the fit to semopy's on it — the default setup to its default
// fit, Fisher scoring to its tight fit.
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
