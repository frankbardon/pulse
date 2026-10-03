package statdist

import (
	"math"
	"testing"
)

func sameBits(a, b float64) bool {
	return math.Float64bits(a) == math.Float64bits(b) || (math.IsNaN(a) && math.IsNaN(b))
}

// TestStudentTFormsAgree is the successor of the cross-package
// TestStudentTCopiesAgree: the TEST_* operators reach the Student-t
// family through StudentTTwoSidedP / StudentTInverseTwoSided and the
// regression engine through StudentTCDF / StudentTQuantile, so the
// paired forms must agree BIT FOR BIT over the R grids — exact
// math.Float64bits equality, never a tolerance.
func TestStudentTFormsAgree(t *testing.T) {
	r := &failReporter{t: t}
	pCases := loadReference[struct {
		T  float64 `json:"t"`
		DF float64 `json:"df"`
	}](t, "student_t_p")
	for _, c := range pCases {
		tv := -math.Abs(c.T)
		if tv == 0 {
			continue
		}
		if cdf, half := StudentTCDF(tv, c.DF), 0.5*StudentTTwoSidedP(tv, c.DF); !sameBits(cdf, half) {
			r.errorf("StudentTCDF(%g, df=%g) = %.17g != ½·StudentTTwoSidedP = %.17g", tv, c.DF, cdf, half)
		}
	}
	invCases := loadReference[struct {
		Alpha float64 `json:"alpha"`
		DF    float64 `json:"df"`
	}](t, "student_t_inverse_two_sided")
	for _, c := range invCases {
		// alpha/2 → 2·(alpha/2) round-trips exactly.
		if inv, q := StudentTInverseTwoSided(c.Alpha, c.DF), -StudentTQuantile(c.Alpha/2, c.DF); !sameBits(inv, q) {
			r.errorf("StudentTInverseTwoSided(%g, df=%g) = %.17g != −StudentTQuantile(alpha/2) = %.17g", c.Alpha, c.DF, inv, q)
		}
	}
	r.done()
}

// TestStudentTQuantile_SanityValues spot-checks StudentTQuantile
// against commonly-tabulated t-table values (Engineering Statistics
// Handbook). Tolerance 1e-3 — a table-transcription guard, not an
// accuracy claim (that is the R oracle's job).
func TestStudentTQuantile_SanityValues(t *testing.T) {
	cases := []struct{ df, p, want float64 }{
		{1, 0.975, 12.7062},
		{10, 0.975, 2.2281},
		{30, 0.975, 2.0423},
		{60, 0.975, 2.0003},
		{100, 0.975, 1.9840},
		{10000, 0.975, 1.9600},
		// Symmetry: q(0.025; df) = -q(0.975; df).
		{10, 0.025, -2.2281},
	}
	for _, c := range cases {
		if got := StudentTQuantile(c.p, c.df); math.Abs(got-c.want) > 1e-3 {
			t.Errorf("StudentTQuantile(p=%v, df=%v) = %v, want %v ±1e-3", c.p, c.df, got, c.want)
		}
	}
}

// TestStudentT_DegenerateInputs pins the documented edge returns.
func TestStudentT_DegenerateInputs(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	cases := []struct {
		name      string
		got, want float64
	}{
		{"P df=0", StudentTTwoSidedP(1, 0), nan},
		{"P t=NaN", StudentTTwoSidedP(nan, 3), nan},
		{"P df=NaN", StudentTTwoSidedP(1, nan), nan},
		{"P t=+Inf", StudentTTwoSidedP(inf, 3), 0},
		{"P t=-Inf", StudentTTwoSidedP(-inf, 3), 0},
		{"P t=0", StudentTTwoSidedP(0, 3), 1},
		{"CDF df=-1", StudentTCDF(1, -1), nan},
		{"CDF t=NaN", StudentTCDF(nan, 3), nan},
		{"CDF t=+Inf", StudentTCDF(inf, 3), 1},
		{"CDF t=-Inf", StudentTCDF(-inf, 3), 0},
		{"CDF t=0", StudentTCDF(0, 3), 0.5},
		{"Q df=0", StudentTQuantile(0.9, 0), nan},
		{"Q p=NaN", StudentTQuantile(nan, 3), nan},
		{"Q p=0", StudentTQuantile(0, 3), -inf},
		{"Q p=1", StudentTQuantile(1, 3), inf},
		{"Q p=0.5", StudentTQuantile(0.5, 3), 0},
		{"Inv alpha=0", StudentTInverseTwoSided(0, 3), nan},
		{"Inv alpha=1", StudentTInverseTwoSided(1, 3), nan},
		{"Inv df=0", StudentTInverseTwoSided(0.05, 0), nan},
		{"Inv alpha=NaN", StudentTInverseTwoSided(nan, 3), nan},
		{"BetaXY x=0", RegularizedIncompleteBetaXY(2, 3, 0, 1), 0},
		{"BetaXY y=0", RegularizedIncompleteBetaXY(2, 3, 1, 0), 1},
	}
	for _, c := range cases {
		if !sameBits(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestRegularizedIncompleteBetaXY_ClosedForms checks I_x(a, b) against
// closed forms on both sides of the continued-fraction switch point:
// I_x(a, 1) = x^a and I_x(1, b) = 1 − (1 − x)^b.
func TestRegularizedIncompleteBetaXY_ClosedForms(t *testing.T) {
	for _, x := range []float64{1e-6, 0.1, 0.3, 0.5, 0.7, 0.9, 0.999} {
		y := 1 - x
		for _, a := range []float64{0.5, 2, 7.5, 30} {
			if got, want := RegularizedIncompleteBetaXY(a, 1, x, y), math.Pow(x, a); math.Abs(got-want) > 1e-13*math.Max(want, 1e-300) {
				t.Errorf("I_%g(%g, 1) = %.17g, want x^a = %.17g", x, a, got, want)
			}
			if got, want := RegularizedIncompleteBetaXY(1, a, x, y), -math.Expm1(a*math.Log1p(-x)); math.Abs(got-want) > 1e-13*want {
				t.Errorf("I_%g(1, %g) = %.17g, want 1−(1−x)^b = %.17g", x, a, got, want)
			}
		}
	}
}

// TestLgammaCorrection matches the Stirling remainder against
// math.Lgamma where the subtraction is still well conditioned.
func TestLgammaCorrection(t *testing.T) {
	for _, x := range []float64{10, 12.5, 20, 50, 100} {
		lg, _ := math.Lgamma(x)
		want := lg - ((x-0.5)*math.Log(x) - x + 0.918938533204672741780329736406)
		if got := LgammaCorrection(x); math.Abs(got-want) > 1e-12 {
			t.Errorf("LgammaCorrection(%g) = %.17g, want %.17g", x, got, want)
		}
	}
}
