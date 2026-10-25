package embeddersmoke

import (
	"math"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/optimize"
)

// TestOptimizeFlow drives the public optimize package the way an
// embedder would: a budget-split problem (maximise √a + √b with
// a + b = 4, a, b ≥ 0.01) solved with a multiplier read back, then a
// refused input surfacing its coded error.
func TestOptimizeFlow(t *testing.T) {
	f := func(x []float64) float64 { return -(math.Sqrt(x[0]) + math.Sqrt(x[1])) }
	grad := func(x []float64) []float64 {
		return []float64{-0.5 / math.Sqrt(x[0]), -0.5 / math.Sqrt(x[1])}
	}
	res, err := optimize.Minimize(f, grad, []float64{1, 3}, optimize.MinimizeOptions{
		Lower: []float64{0.01, 0.01},
		Constraints: []optimize.Constraint{{
			Type: optimize.Eq,
			Fn:   func(x []float64) float64 { return x[0] + x[1] - 4 },
			Grad: func([]float64) []float64 { return []float64{1, 1} },
		}},
		Acc: 1e-10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.X[0]-2) > 1e-5 || math.Abs(res.X[1]-2) > 1e-5 {
		t.Errorf("x = %v, want (2, 2)", res.X)
	}
	// ∂(−√a − √b)/∂a = −1/(2√2) at the optimum: the budget's shadow price.
	if math.Abs(res.Multipliers[0]+0.5/math.Sqrt2) > 1e-5 {
		t.Errorf("multiplier %v", res.Multipliers[0])
	}
	if _, err := optimize.Minimize(f, grad, nil, optimize.MinimizeOptions{}); !perrors.HasCode(err, perrors.PULSE_OPTIMIZE_INVALID) {
		t.Errorf("empty start: %v", err)
	}
}

// TestOptimizeLeastSquaresFlow fits y = b1·x1 + b2·x2 with b2 ≥ 0 on data
// whose free b2 is negative: the bound holds b2 at exactly 0, b1 is the
// one-predictor fit, and JTJ reads back as XᵀX.
func TestOptimizeLeastSquaresFlow(t *testing.T) {
	x1 := []float64{1, 2, 3, 4}
	x2 := []float64{1, -1, 1, -1}
	y := []float64{1.9, 4.1, 5.9, 8.1} // ≈ 2·x1 − 0.1·x2
	r := make([]float64, len(y))
	res, err := optimize.LeastSquares(
		func(b []float64) []float64 {
			for i := range y {
				r[i] = float64(b[0]*x1[i]) + float64(b[1]*x2[i]) - y[i]
			}
			return r
		},
		func([]float64) [][]float64 {
			return [][]float64{{x1[0], x2[0]}, {x1[1], x2[1]}, {x1[2], x2[2]}, {x1[3], x2[3]}}
		},
		[]float64{1, 1}, []float64{math.Inf(-1), 0}, nil, optimize.LeastSquaresOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.X[1] != 0 || res.Active[1] != optimize.AtLower || !res.Status.Converged() {
		t.Fatalf("b2 = %v (active %v, status %v), want exactly 0 on its bound", res.X[1], res.Active[1], res.Status)
	}
	if want := 60.2 / 30.0; math.Abs(res.X[0]-want) > 1e-9 { // Σx1·y / Σx1²
		t.Errorf("b1 = %v, want %v", res.X[0], want)
	}
	if res.JTJ.At(0, 0) != 30 || res.JTJ.At(1, 1) != 4 {
		t.Errorf("JTJ = %v", res.JTJ.ToRows())
	}
}
