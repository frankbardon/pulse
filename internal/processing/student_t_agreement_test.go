package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/processing/regression"
)

// TestStudentTCopiesAgree pins the two Student-t implementations — this
// package's (TEST_*, overlays) and regression's (REG_* coefficient
// p-values, Bayes credible intervals) — to each other BIT FOR BIT over
// the R reference grids, and the shared value to R itself. Exact
// equality (math.Float64bits), never a tolerance: the copies run the
// same algorithm with the same function boundaries, so any drift is a
// divergence, not rounding.
func TestStudentTCopiesAgree(t *testing.T) {
	same := func(a, b float64) bool {
		return math.Float64bits(a) == math.Float64bits(b) || (math.IsNaN(a) && math.IsNaN(b))
	}
	fails := 0
	report := func(format string, args ...any) {
		t.Helper()
		if fails++; fails <= 25 {
			t.Errorf(format, args...)
		}
	}

	pDoc := loadReference[struct {
		T         float64 `json:"t"`
		DF        float64 `json:"df"`
		PTwoSided float64 `json:"p_two_sided"`
	}](t, "student_t_p")
	oracle := newOracleCheck(t, "studentTTwoSidedP (agreed)", oracleRelTol, oracleProbAbsFloor)
	for _, k := range pDoc.Cases {
		for _, tv := range []float64{k.T, -k.T} {
			got, reg := studentTTwoSidedP(tv, k.DF), regression.StudentTTwoSidedPForAgreement(tv, k.DF)
			if !same(got, reg) {
				report("studentTTwoSidedP(t=%g, df=%g): processing %.17g != regression %.17g", tv, k.DF, got, reg)
			}
			oracle.check(got, k.PTwoSided, "t=%g, df=%g", tv, k.DF)
		}
	}
	oracle.done(len(pDoc.Cases))

	// Two-sided critical value vs the lower-tail quantile: by symmetry
	// studentTInverseTwoSided(alpha) == -studentTQuantile(alpha/2), and
	// alpha/2 → 2·(alpha/2) round-trips exactly, so both copies solve the
	// identical root-finding problem.
	invDoc := loadReference[struct {
		Alpha float64 `json:"alpha"`
		DF    float64 `json:"df"`
		Q     float64 `json:"q"`
	}](t, "student_t_inverse_two_sided")
	invOracle := newOracleCheck(t, "studentTInverseTwoSided (agreed)", oracleRelTol, oracleQuantileAbsFloor)
	for _, k := range invDoc.Cases {
		got, reg := studentTInverseTwoSided(k.Alpha, k.DF), -regression.StudentTQuantileForAgreement(k.Alpha/2, k.DF)
		if !same(got, reg) {
			report("critical value (alpha=%g, df=%g): processing %.17g != regression %.17g", k.Alpha, k.DF, got, reg)
		}
		invOracle.check(got, k.Q, "alpha=%g, df=%g", k.Alpha, k.DF)
	}
	invOracle.done(len(invDoc.Cases))

	qDoc := loadReference[struct {
		P  float64 `json:"p"`
		DF float64 `json:"df"`
		Q  float64 `json:"q"`
	}](t, "student_t_quantile")
	for _, k := range qDoc.Cases {
		if k.P == 0.5 {
			continue // exact-zero symmetry branch; processing has no lower-tail form
		}
		var want float64
		if k.P < 0.5 {
			want = -studentTInverseTwoSided(2*k.P, k.DF)
		} else {
			want = studentTInverseTwoSided(2*(1-k.P), k.DF)
		}
		if reg := regression.StudentTQuantileForAgreement(k.P, k.DF); !same(want, reg) {
			report("quantile (p=%g, df=%g): processing %.17g != regression %.17g", k.P, k.DF, want, reg)
		}
	}
	if fails > 25 {
		t.Errorf("%d more disagreements suppressed", fails-25)
	}
}
