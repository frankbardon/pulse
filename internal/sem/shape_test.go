package sem

import (
	"encoding/csv"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// shape_test.go pins the synthetic same-shape models (sem-contribution
// E2-S5; testdata/shape63_model.txt, shape89_model.txt over
// shape_data.csv, gen_shape_data.py): four outputs on one factor, a
// mediator over constructs, two-level hierarchies, 63 and 89 mostly
// 0/1 indicators, fitted to the z-scored listwise rows
// (fit_semopy_shape.py) — and the 63 model on the first 50 ids
// (n = 47 < p = 67), where semopy substitutes cov_nearest.

// shapeCase is one shape reference: its model, the id cut (0: every
// row) and whether semopy substituted the covariance.
type shapeCase struct {
	name, model string
	rows        int
	nearest     bool
}

var shapeCases = []shapeCase{
	{"shape63", "shape63", 0, false},
	{"shape89", "shape89", 0, false},
	{"shape63_singular", "shape63", 50, true},
}

// readShapeCorr returns the correlation matrix (the covariance of the
// population-SD z-scores) of cols over the rows complete on every one
// of them (listwise) with id < rows (rows 0: all), and the row count.
func readShapeCorr(t *testing.T, cols []string, rows int) (*mat.SymDense, float64) {
	t.Helper()
	f, err := os.Open("testdata/shape_data.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, h := range recs[0] {
		idx[h] = i
	}
	p := len(cols)
	var x [][]float64
rowLoop:
	for _, rec := range recs[1:] {
		id, err := strconv.Atoi(rec[idx["id"]])
		if err != nil {
			t.Fatal(err)
		}
		if rows > 0 && id >= rows {
			continue
		}
		r := make([]float64, p)
		for c, name := range cols {
			if rec[idx[name]] == "" {
				continue rowLoop
			}
			if r[c], err = strconv.ParseFloat(rec[idx[name]], 64); err != nil {
				t.Fatal(err)
			}
		}
		x = append(x, r)
	}
	n := len(x)
	mean := make([]float64, p)
	for _, r := range x {
		for c, v := range r {
			mean[c] += v
		}
	}
	for c := range mean {
		mean[c] /= float64(n)
	}
	s := mat.NewSymDense(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			acc := 0.0
			for _, r := range x {
				acc += (r[i] - mean[i]) * (r[j] - mean[j])
			}
			s.SetSym(i, j, acc/float64(n))
		}
	}
	corr := mat.NewSymDense(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			corr.SetSym(i, j, s.At(i, j)/math.Sqrt(s.At(i, i)*s.At(j, j)))
		}
	}
	return corr, float64(n)
}

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
