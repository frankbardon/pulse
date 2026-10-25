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
