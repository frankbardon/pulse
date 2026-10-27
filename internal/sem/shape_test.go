package sem

import (
	"encoding/csv"
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
