package regadapt

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// weightRow is a one-row extend.Record carrying target y, predictor x
// and weight w; only NumericValue is read by RegressionDriver.admit.
type weightRow struct {
	extend.Record
	vals map[string]float64
}

func (r weightRow) NumericValue(field string) (float64, bool) {
	v, ok := r.vals[field]
	return v, ok
}

// TestRegressionDriver_SumWSqBarriered: the weighted admission folds
// Σw² (Kish n_eff's denominator) with an explicit float64(w*w) barrier,
// so the arm64 compiler cannot fuse it into an FMA and the n_eff of a
// weighted extension regression is the same bits on arm64 and amd64.
// The weights are chosen so a fused fold differs from the rounded one
// at some row (checked per row: the two can re-converge by chance).
func TestRegressionDriver_SumWSqBarriered(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Target: "y", Predictors: []string{"x"}}
	d := &RegressionDriver{spec: spec, weight: &types.WeightSpec{Field: "w"}}
	var want, fused float64
	separated := 0
	for i := 1; i <= 200; i++ {
		w := 0.1 + float64(i)/3
		if _, ok := d.admit(weightRow{vals: map[string]float64{"y": 1, "x": 2, "w": w}}); !ok {
			t.Fatalf("row %d not admitted", i)
		}
		want += float64(w * w)
		fused = math.FMA(w, w, fused)
		if fused != want {
			separated++
		}
		if math.Float64bits(d.sumWSq) != math.Float64bits(want) {
			t.Fatalf("row %d: sumWSq = %v (%#x), want the barriered fold %v (%#x)", i, d.sumWSq, math.Float64bits(d.sumWSq), want, math.Float64bits(want))
		}
	}
	if separated == 0 {
		t.Fatal("weights never separate a fused fold from the barriered one; pick others")
	}
	if d.n != 200 {
		t.Fatalf("n = %d, want 200", d.n)
	}
}
