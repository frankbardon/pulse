package synth

import (
	"math"
	"math/rand"
	"testing"
)

// probitDesign draws n rows of (1, x) with x uniform on [-2, 2] and
// y ~ Bernoulli(Φ(a + b·x)). Products are rounded explicitly so the
// inputs are the same bits on every architecture.
func probitDesign(n int, a, b float64, seed int64) (x, y []float64) {
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < n; i++ {
		xi := float64(4*rng.Float64()) - 2
		x = append(x, 1, xi)
		if rng.Float64() < phi(a+float64(b*xi)) {
			y = append(y, 1)
		} else {
			y = append(y, 0)
		}
	}
	return x, y
}

// TestFitProbit_RecoversKnownCoefficients checks the Fisher-scoring fit
// against the probit that generated the data.
func TestFitProbit_RecoversKnownCoefficients(t *testing.T) {
	t.Parallel()

	x, y := probitDesign(8000, -0.5, 0.8, 1)
	res, ok := fitProbit(x, y, 2, 0)
	if !ok {
		t.Fatal("fitProbit refused a well-posed design")
	}
	if math.Abs(res.beta[0]+0.5) > 0.06 || math.Abs(res.beta[1]-0.8) > 0.06 {
		t.Errorf("beta = %v, want ~[-0.5 0.8]", res.beta)
	}
	if !(res.loglik > res.loglik0) || res.loglik0 >= 0 {
		t.Errorf("loglik %v must beat the intercept-only %v", res.loglik, res.loglik0)
	}

	// The ridge shrinks the slope toward 0 and never the intercept's
	// role of matching the prevalence.
	shrunk, ok := fitProbit(x, y, 2, 2000)
	if !ok || !(math.Abs(shrunk.beta[1]) < math.Abs(res.beta[1])) {
		t.Errorf("ridge slope %v must be smaller than the free %v", shrunk.beta, res.beta)
	}
}

// TestFitProbit_RefusesWhatItCannotFit covers the three refusals the
// capture falls back from.
func TestFitProbit_RefusesWhatItCannotFit(t *testing.T) {
	t.Parallel()

	// Perfect separation: y = 1 exactly when x > 0. The MLE does not
	// exist and the slope runs away.
	var x, y []float64
	for i := 0; i < 400; i++ {
		xi := float64(i-200) + 0.5
		x = append(x, 1, xi)
		if xi > 0 {
			y = append(y, 1)
		} else {
			y = append(y, 0)
		}
	}
	if _, ok := fitProbit(x, y, 2, 0); ok {
		t.Error("a separated design must be refused, not shipped with a runaway slope")
	}
	// A constant target.
	if _, ok := fitProbit([]float64{1, 0, 1, 1}, []float64{1, 1}, 2, 0); ok {
		t.Error("a constant target must be refused")
	}
	// A duplicated column: singular information matrix.
	dx, dy := probitDesign(500, 0, 1, 2)
	var dup []float64
	for i := 0; i < len(dy); i++ {
		dup = append(dup, dx[2*i], dx[2*i+1], dx[2*i+1])
	}
	if _, ok := fitProbit(dup, dy, 3, 0); ok {
		t.Error("a singular design must be refused")
	}
}

// TestCholeskySolve_SolvesSPDSystem pins the FMA-free solver on a small
// system with a known answer.
func TestCholeskySolve_SolvesSPDSystem(t *testing.T) {
	t.Parallel()

	a := []float64{4, 2, 2, 3}
	got, ok := choleskySolve(a, []float64{10, 8}, 2)
	if !ok || math.Abs(got[0]-1.75) > 1e-12 || math.Abs(got[1]-1.5) > 1e-12 {
		t.Errorf("solve = %v (ok %v), want [1.75 1.5]", got, ok)
	}
	if _, ok := choleskySolve([]float64{1, 2, 2, 1}, []float64{1, 1}, 2); ok {
		t.Error("an indefinite matrix must be refused")
	}
}

// TestProbitSpecScale_DrawIsTheCapturedProbit checks the translation
// algebraically: through the bernoulli draw's own moments and threshold,
// the translated model fires exactly when a + b·x + z > 0.
func TestProbitSpecScale_DrawIsTheCapturedProbit(t *testing.T) {
	t.Parallel()

	const a, b, p = -0.3, 0.7, 0.32
	scale, intercept, residualStd, ok := probitSpecScale(a, p)
	if !ok {
		t.Fatal("probitSpecScale refused a valid prevalence")
	}
	std := math.Sqrt(p * (1 - p))
	threshold := phiInv(1 - p)
	for _, x := range []float64{-2, -0.5, 0, 1.3} {
		for _, z := range []float64{-1.1, 0, 0.4, 2} {
			pred := intercept + scale*b*x
			u := (pred-p)/std + (residualStd/std)*z
			if fires, want := u > threshold, a+b*x+z > 0; fires != want && math.Abs(a+b*x+z) > 1e-9 {
				t.Errorf("x=%v z=%v: translated draw fires %v, probit says %v", x, z, fires, want)
			}
		}
	}
	for _, bad := range []float64{0, 1} {
		if _, _, _, ok := probitSpecScale(a, bad); ok {
			t.Errorf("prevalence %v has no probit threshold and must be refused", bad)
		}
	}
}
