package statdist

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reference oracle for the Student-t primitives. The R goldens live
// beside the processing package's oracle (../processing/testdata/
// reference/, written by scripts/reference/gen_reference.R via
// `make reference`); their golden-hash footers are checked by
// processing's TestGoldensNotHandEdited. Tolerance mirrors that suite:
// relative 1e-10 with an absolute floor only at the representable edge.
// Comparisons are relative, never bit-exact, so amd64 FMA contraction
// (vs arm64 local runs) cannot flip a case.

const (
	oracleRelTol           = 1e-10
	oracleProbAbsFloor     = 1e-310
	oracleQuantileAbsFloor = 1e-14
)

var referenceDir = filepath.Join("..", "processing", "testdata", "reference")

func loadReference[C any](t *testing.T, name string) []C {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(referenceDir, name+".json"))
	if err != nil {
		t.Fatalf("read reference %s: %v (regenerate with `make reference`)", name, err)
	}
	body := raw
	if idx := strings.LastIndex(string(raw), "\n// golden-hash: "); idx >= 0 {
		body = raw[:idx]
	}
	var doc struct {
		RVersion string `json:"r_version"`
		Cases    []C    `json:"cases"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode reference %s: %v", name, err)
	}
	if doc.RVersion == "" || len(doc.Cases) == 0 {
		t.Fatalf("reference %s is missing its R version or cases", name)
	}
	return doc.Cases
}

func oracleClose(got, want, rel, absFloor float64) bool {
	return !math.IsNaN(got) && math.Abs(got-want) <= math.Max(rel*math.Abs(want), absFloor)
}

// failReporter caps the error flood at 25 lines per test.
type failReporter struct {
	t     *testing.T
	fails int
}

func (r *failReporter) errorf(format string, args ...any) {
	r.t.Helper()
	if r.fails++; r.fails <= 25 {
		r.t.Errorf(format, args...)
	}
}

func (r *failReporter) done() {
	r.t.Helper()
	if r.fails > 25 {
		r.t.Errorf("%d more failures suppressed", r.fails-25)
	}
}

func TestReferenceOracle_StudentTTwoSidedPAndCDF(t *testing.T) {
	cases := loadReference[struct {
		T         float64 `json:"t"`
		DF        float64 `json:"df"`
		PTwoSided float64 `json:"p_two_sided"`
		CDFNeg    float64 `json:"cdf_neg"`
		CDFPos    float64 `json:"cdf_pos"`
	}](t, "student_t_p")
	r := &failReporter{t: t}
	for _, c := range cases {
		for _, tv := range []float64{c.T, -c.T} { // symmetric in t
			if got := StudentTTwoSidedP(tv, c.DF); !oracleClose(got, c.PTwoSided, oracleRelTol, oracleProbAbsFloor) {
				r.errorf("StudentTTwoSidedP(t=%g, df=%g) = %.17g, want %.17g", tv, c.DF, got, c.PTwoSided)
			}
		}
		if got := StudentTCDF(-c.T, c.DF); !oracleClose(got, c.CDFNeg, oracleRelTol, oracleProbAbsFloor) {
			r.errorf("StudentTCDF(t=%g, df=%g) = %.17g, want %.17g", -c.T, c.DF, got, c.CDFNeg)
		}
		if got := StudentTCDF(c.T, c.DF); !oracleClose(got, c.CDFPos, oracleRelTol, oracleProbAbsFloor) {
			r.errorf("StudentTCDF(t=%g, df=%g) = %.17g, want %.17g", c.T, c.DF, got, c.CDFPos)
		}
	}
	r.done()
}

func TestReferenceOracle_StudentTInverseTwoSided(t *testing.T) {
	cases := loadReference[struct {
		Alpha float64 `json:"alpha"`
		DF    float64 `json:"df"`
		Q     float64 `json:"q"`
	}](t, "student_t_inverse_two_sided")
	r := &failReporter{t: t}
	for _, c := range cases {
		if got := StudentTInverseTwoSided(c.Alpha, c.DF); !oracleClose(got, c.Q, oracleRelTol, oracleQuantileAbsFloor) {
			r.errorf("StudentTInverseTwoSided(alpha=%g, df=%g) = %.17g, want %.17g", c.Alpha, c.DF, got, c.Q)
		}
	}
	r.done()
}

func TestReferenceOracle_StudentTQuantile(t *testing.T) {
	cases := loadReference[struct {
		P  float64 `json:"p"`
		DF float64 `json:"df"`
		Q  float64 `json:"q"`
	}](t, "student_t_quantile")
	r := &failReporter{t: t}
	for _, c := range cases {
		if got := StudentTQuantile(c.P, c.DF); !oracleClose(got, c.Q, oracleRelTol, oracleQuantileAbsFloor) {
			r.errorf("StudentTQuantile(p=%g, df=%g) = %.17g, want %.17g (rel err %.3g)",
				c.P, c.DF, got, c.Q, math.Abs(got-c.Q)/math.Abs(c.Q))
		}
	}
	r.done()
}
