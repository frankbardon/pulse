//go:build heavy

package sem

import (
	"os"
	"testing"
)

// TestFit_MatchesSemopy runs every setup against the semopy reference
// it reproduces: the default (the SLSQP port) semopy's default fit,
// early stop included, to rounding; a converging setup the tight fit.
func TestFit_MatchesSemopy(t *testing.T) {
	t.Parallel()

	configs := []struct {
		name string
		opt  Options
		ref  string
		tol  float64
	}{
		{"default", Options{}, "default", 1e-9},
		{"slsqp_rank2", Options{Information: InformationRank2}, "default", 1e-9},
		{"slsqp_tight", Options{Ftol: 1e-14, MaxIter: 100000}, "tight", 1e-6},
		{"fisher_dense", Options{Solver: SolverFisher}, "tight", 1e-6},
		{"fisher_rank2", Options{Solver: SolverFisher, Information: InformationRank2}, "tight", 1e-6},
	}
	for _, cfg := range configs {
		for _, tc := range parityCases {
			t.Run(cfg.name+"/"+tc.name, func(t *testing.T) {
				desc, err := os.ReadFile("testdata/" + tc.name + "_model.txt")
				if err != nil {
					t.Fatal(err)
				}
				m, err := NewModel(string(desc), false)
				if err != nil {
					t.Fatal(err)
				}
				s, n := readCSVCov(t, "testdata/"+tc.data+"_data.csv", m.Observed)
				fit, err := FitCov(m, s, n, cfg.opt)
				if err != nil {
					t.Fatal(err)
				}
				if err := ComputeStats(fit, string(desc)); err != nil {
					t.Fatal(err)
				}
				checkAgainstSemopy(t, fit, loadRef(t, "testdata/"+tc.name+"_semopy_"+cfg.ref+".json"), cfg.tol)
			})
		}
	}
}
