package regression

import (
	"math"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/frankbardon/pulse/types"
)

// fitOLS is the property-test convenience wrapper: build an engine,
// fit a dataset, return coefficients keyed by predictor name. Fails
// the test on any error so the property body can focus on the
// invariant under test.
func fitOLS(t *testing.T, predictors []string, records []Record) *types.RegressionResult {
	t.Helper()
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictors}
	eng := newOLS(t, spec)
	res, err := eng.FitBuffered(records)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	return res
}

// genDataset produces a deterministic non-degenerate (x_1, x_2, y)
// triple from a seed. Caller chooses true coefficients; the function
// returns the records plus the true (intercept, β_1, β_2) used to
// generate y so the property test can transform/perturb them.
func genDataset(seed int64, n int, intercept, b1, b2 float64) ([]Record, []float64, []float64, []float64) {
	rng := rand.New(rand.NewSource(seed))
	x1 := make([]float64, n)
	x2 := make([]float64, n)
	y := make([]float64, n)
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		x1[i] = rng.NormFloat64()*3 + 1
		x2[i] = rng.NormFloat64()*1.5 - 2
		noise := rng.NormFloat64() * 0.2
		y[i] = intercept + b1*x1[i] + b2*x2[i] + noise
		records[i] = mockRecord{values: map[string]float64{
			"x1": x1[i], "x2": x2[i], "y": y[i],
		}}
	}
	return records, x1, x2, y
}

// scaleRecords returns a new []Record with the target multiplied by c
// (predictor values unchanged). Used to test the (linear-in-y) scaling
// invariance β(c·y) = c·β(y).
func scaleRecords(records []Record, c float64) []Record {
	out := make([]Record, len(records))
	for i, r := range records {
		mr := r.(mockRecord)
		newValues := make(map[string]float64, len(mr.values))
		for k, v := range mr.values {
			if k == "y" {
				newValues[k] = c * v
			} else {
				newValues[k] = v
			}
		}
		out[i] = mockRecord{values: newValues}
	}
	return out
}

// scalePredictor returns records with predictor j multiplied by c.
// Used to test β_j(c·x_j) = β_j(x_j) / c (other coefficients
// unchanged after de-standardization).
func scalePredictor(records []Record, name string, c float64) []Record {
	out := make([]Record, len(records))
	for i, r := range records {
		mr := r.(mockRecord)
		newValues := make(map[string]float64, len(mr.values))
		for k, v := range mr.values {
			if k == name {
				newValues[k] = c * v
			} else {
				newValues[k] = v
			}
		}
		out[i] = mockRecord{values: newValues}
	}
	return out
}

// shiftTarget returns records with the target offset by a constant.
// Used to test (intercept(y + k), β(y + k)) = (intercept(y) + k, β(y)).
func shiftTarget(records []Record, k float64) []Record {
	out := make([]Record, len(records))
	for i, r := range records {
		mr := r.(mockRecord)
		newValues := make(map[string]float64, len(mr.values))
		for k0, v := range mr.values {
			if k0 == "y" {
				newValues[k0] = v + k
			} else {
				newValues[k0] = v
			}
		}
		out[i] = mockRecord{values: newValues}
	}
	return out
}

// TestRegOLS_Property_ScaleInvariance: for any c ≠ 0, fitting y' = c·y
// must produce (intercept', β') = (c·intercept, c·β). Verified for a
// few representative datasets via testing/quick.
func TestRegOLS_Property_ScaleInvariance(t *testing.T) {
	property := func(seed int64, cBits uint32) bool {
		// Constrain c to a reasonable non-zero range so float64
		// representability doesn't bias the comparison.
		c := float64(int32(cBits%200)) / 13.0
		if c == 0 {
			c = 0.5
		}
		records, _, _, _ := genDataset(seed%1_000_000+1, 60, 0.7, 1.4, -0.9)
		base := fitOLS(t, []string{"x1", "x2"}, records)
		scaled := fitOLS(t, []string{"x1", "x2"}, scaleRecords(records, c))

		tol := 1e-9 * (1 + math.Abs(c))
		if !closef(scaled.Coefficients[InterceptKey], c*base.Coefficients[InterceptKey], tol) {
			t.Logf("intercept: base=%v c=%v scaled=%v want=%v",
				base.Coefficients[InterceptKey], c, scaled.Coefficients[InterceptKey], c*base.Coefficients[InterceptKey])
			return false
		}
		for _, name := range []string{"x1", "x2"} {
			if !closef(scaled.Coefficients[name], c*base.Coefficients[name], tol) {
				t.Logf("β_%s: base=%v c=%v scaled=%v want=%v",
					name, base.Coefficients[name], c, scaled.Coefficients[name], c*base.Coefficients[name])
				return false
			}
		}
		return true
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 30}); err != nil {
		t.Errorf("scale-invariance property failed: %v", err)
	}
}

// TestRegOLS_Property_PredictorScale: for any c ≠ 0, scaling predictor
// x_j by c must scale β_j by 1/c while leaving the other coefficients
// and the intercept unchanged.
func TestRegOLS_Property_PredictorScale(t *testing.T) {
	property := func(seed int64, cBits uint32) bool {
		c := float64(int32(cBits%200)+1) / 11.0 // strictly positive non-zero
		records, _, _, _ := genDataset(seed%1_000_000+1, 80, 1.1, 2.0, -0.6)
		base := fitOLS(t, []string{"x1", "x2"}, records)
		scaled := fitOLS(t, []string{"x1", "x2"}, scalePredictor(records, "x1", c))

		// β_x1 should change by factor 1/c; β_x2 and intercept unchanged.
		tol := 1e-8
		if !closef(scaled.Coefficients["x1"], base.Coefficients["x1"]/c, tol*math.Max(1, math.Abs(base.Coefficients["x1"]/c))) {
			t.Logf("β_x1: base=%v c=%v scaled=%v want=%v",
				base.Coefficients["x1"], c, scaled.Coefficients["x1"], base.Coefficients["x1"]/c)
			return false
		}
		if !closef(scaled.Coefficients["x2"], base.Coefficients["x2"], tol) {
			t.Logf("β_x2 shouldn't change: base=%v scaled=%v", base.Coefficients["x2"], scaled.Coefficients["x2"])
			return false
		}
		if !closef(scaled.Coefficients[InterceptKey], base.Coefficients[InterceptKey], tol) {
			t.Logf("intercept shouldn't change: base=%v scaled=%v", base.Coefficients[InterceptKey], scaled.Coefficients[InterceptKey])
			return false
		}
		return true
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 30}); err != nil {
		t.Errorf("predictor-scale property failed: %v", err)
	}
}

// TestRegOLS_Property_InterceptShift: adding a constant k to every
// target value shifts the intercept by k and leaves all slopes
// unchanged.
func TestRegOLS_Property_InterceptShift(t *testing.T) {
	property := func(seed int64, kBits uint64) bool {
		// Constrain k to a reasonable range.
		k := float64(int64(kBits)%500) / 7.0
		records, _, _, _ := genDataset(seed%1_000_000+1, 70, -0.3, 1.7, 0.4)
		base := fitOLS(t, []string{"x1", "x2"}, records)
		shifted := fitOLS(t, []string{"x1", "x2"}, shiftTarget(records, k))

		tol := 1e-9 * (1 + math.Abs(k))
		if !closef(shifted.Coefficients[InterceptKey], base.Coefficients[InterceptKey]+k, tol) {
			t.Logf("intercept: base=%v k=%v shifted=%v want=%v",
				base.Coefficients[InterceptKey], k, shifted.Coefficients[InterceptKey],
				base.Coefficients[InterceptKey]+k)
			return false
		}
		for _, name := range []string{"x1", "x2"} {
			if !closef(shifted.Coefficients[name], base.Coefficients[name], 1e-10) {
				t.Logf("β_%s shouldn't change: base=%v shifted=%v", name, base.Coefficients[name], shifted.Coefficients[name])
				return false
			}
		}
		return true
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 30}); err != nil {
		t.Errorf("intercept-shift property failed: %v", err)
	}
}
