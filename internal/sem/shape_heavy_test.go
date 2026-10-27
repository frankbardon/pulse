//go:build heavy

package sem

import (
	"encoding/json"
	"os"
	"testing"
)

// TestFit_ShapeMatchesSemopy fits every shape case from the rows' own
// S and pins the default setup to semopy's default fit: the same
// iteration count, every figure at 1e-8. The tolerance is this shape's,
// set explicitly: its objective is flat along a first-order factor's
// scale (the expected information's smallest eigenvalue ~11 against
// ~1e4; ~0.7 against ~1.7e3 at n < p — matrix-sem.md, Same-shape
// parity), and the default stop, ~1e-3 short of the optimum along it,
// is reproduced to 2e-10. The tight fits (resolved along that direction
// only to ~1e-6) are pinned through the engine, in one request, by the
// root package's TestSEMShape_OneRequestMatchesSemopy, which keeps this
// package's run short.
func TestFit_ShapeMatchesSemopy(t *testing.T) {
	t.Parallel()

	for _, tc := range shapeCases {
		desc, err := os.ReadFile("testdata/" + tc.model + "_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewModel(string(desc), false)
		if err != nil {
			t.Fatal(err)
		}
		s, n := readShapeCorr(t, m.Observed, tc.rows)
		if PositiveDefinite(s) == tc.nearest {
			t.Fatalf("%s: Cholesky factors %v, semopy substituted %v", tc.name, !tc.nearest, tc.nearest)
		}
		t.Run(tc.name, func(t *testing.T) {
			var fit *Fit
			if tc.nearest {
				adj, err := CovNearest(s, NearestThreshold)
				if err != nil {
					t.Fatal(err)
				}
				if fit, err = FitCovNearest(m, adj.Cov, s, n, Options{}); err != nil {
					t.Fatal(err)
				}
			} else if fit, err = FitCov(m, s, n, Options{}); err != nil {
				t.Fatal(err)
			}
			if err := ComputeStats(fit, string(desc)); err != nil {
				t.Fatal(err)
			}
			path := "testdata/" + tc.name + "_semopy_default.json"
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var aux struct {
				N     float64 `json:"n"`
				NIter int     `json:"n_iter"`
			}
			if err := json.Unmarshal(b, &aux); err != nil {
				t.Fatal(err)
			}
			if n != aux.N {
				t.Fatalf("n %v listwise rows, semopy %v", n, aux.N)
			}
			if fit.Iterations != aux.NIter {
				t.Errorf("iterations %d, semopy's default stop %d", fit.Iterations, aux.NIter)
			}
			checkAgainstSemopy(t, fit, loadRef(t, path), 1e-8)
		})
	}
}
