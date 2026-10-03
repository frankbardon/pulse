package regression

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Reference oracle for this package's Student-t primitives. The R
// goldens live beside the processing package's copy
// (../testdata/reference/, written by scripts/reference/gen_reference.R
// via `make reference`); their golden-hash footers are checked by
// processing's TestGoldensNotHandEdited. Tolerance mirrors that suite:
// relative 1e-10 with an absolute floor only at the representable edge.

const (
	oracleRelTol           = 1e-10
	oracleProbAbsFloor     = 1e-310
	oracleQuantileAbsFloor = 1e-14
)

func loadSharedReference[C any](t *testing.T, name string) []C {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", name+".json"))
	if err != nil {
		t.Fatalf("read reference %s: %v (regenerate with `make reference`)", name, err)
	}
	body, _ := splitHash(raw)
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

func TestReferenceOracle_RegressionStudentT(t *testing.T) {
	cases := loadSharedReference[struct {
		T         float64 `json:"t"`
		DF        float64 `json:"df"`
		PTwoSided float64 `json:"p_two_sided"`
		CDFNeg    float64 `json:"cdf_neg"`
		CDFPos    float64 `json:"cdf_pos"`
	}](t, "student_t_p")
	fails := 0
	report := func(format string, args ...any) {
		t.Helper()
		if fails++; fails <= 25 {
			t.Errorf(format, args...)
		}
	}
	for _, c := range cases {
		if got := studentTTwoSidedP(c.T, c.DF); !oracleClose(got, c.PTwoSided, oracleRelTol, oracleProbAbsFloor) {
			report("studentTTwoSidedP(t=%g, df=%g) = %.17g, want %.17g", c.T, c.DF, got, c.PTwoSided)
		}
		if got := studentTCDF(-c.T, c.DF); !oracleClose(got, c.CDFNeg, oracleRelTol, oracleProbAbsFloor) {
			report("studentTCDF(t=%g, df=%g) = %.17g, want %.17g", -c.T, c.DF, got, c.CDFNeg)
		}
		if got := studentTCDF(c.T, c.DF); !oracleClose(got, c.CDFPos, oracleRelTol, oracleProbAbsFloor) {
			report("studentTCDF(t=%g, df=%g) = %.17g, want %.17g", c.T, c.DF, got, c.CDFPos)
		}
	}
	if fails > 25 {
		t.Errorf("%d more failures suppressed", fails-25)
	}
}

func TestReferenceOracle_RegressionStudentTQuantile(t *testing.T) {
	cases := loadSharedReference[struct {
		P  float64 `json:"p"`
		DF float64 `json:"df"`
		Q  float64 `json:"q"`
	}](t, "student_t_quantile")
	fails := 0
	for _, c := range cases {
		got := studentTQuantile(c.P, c.DF)
		if oracleClose(got, c.Q, oracleRelTol, oracleQuantileAbsFloor) {
			continue
		}
		if fails++; fails <= 25 {
			t.Errorf("studentTQuantile(p=%g, df=%g) = %.17g, want %.17g (rel err %.3g)",
				c.P, c.DF, got, c.Q, math.Abs(got-c.Q)/math.Abs(c.Q))
		}
	}
	if fails > 25 {
		t.Errorf("%d more failures suppressed", fails-25)
	}
}
