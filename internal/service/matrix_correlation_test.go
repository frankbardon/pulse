package service

import (
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// MAT_CORRELATION end to end through the service: the pinned
// numpy / statsmodels oracle (matrix_reference_values_test.go), parity
// with an N² TEST_PEARSON_R battery over the same rows, and the
// zero-spread member's null row and column.

// corrSpec is a MAT_CORRELATION spec over inline members, weighted by
// the f (frequency) or p (probability) column when weight names one.
func corrSpec(name string, fields []string, weight string) types.MatrixSpec {
	s := types.MatrixSpec{Name: name, Type: types.MAT_CORRELATION, Fields: fields}
	s.Weight = covSpec("", "", 0, weight, "").Weight
	return s
}

// TestMatrixCorrelation_MatchesReference pins MAT_CORRELATION against
// numpy.corrcoef (unweighted) and statsmodels DescrStatsW.corrcoef
// (frequency and probability weights): listwise over null cells, a
// zero-weight row kept.
func TestMatrixCorrelation_MatchesReference(t *testing.T) {
	cfg := writeMatrixCohort(t, "ref.pulse", matrixRefRows)
	for _, c := range matrixRefCorrCases {
		t.Run(fmt.Sprintf("weight=%q", c.weight), func(t *testing.T) {
			req := &types.Request{
				Cohort:   &types.Cohort{Filename: "ref.pulse"},
				Matrices: []types.MatrixSpec{corrSpec("corr", []string{"x1", "x2", "x3"}, c.weight)},
			}
			res := processMatrices(t, cfg, req).Matrices[0]
			if res.Name != "corr" || res.Type != types.MAT_CORRELATION {
				t.Fatalf("result identity = %q %s", res.Name, res.Type)
			}
			for r := 0; r < 3; r++ {
				for col := 0; col < 3; col++ {
					if !closeTo(res.Primary.Values[r][col], c.corr[r][col], 1e-12) {
						t.Errorf("corr[%d][%d] = %.17g, want %.17g", r, col, res.Primary.Values[r][col], c.corr[r][col])
					}
				}
			}
			if det := res.Scalars["determinant"]; !closeTo(det, c.det, 1e-9) {
				t.Errorf("determinant = %.17g, want %.17g", det, c.det)
			}
		})
	}
}

// pearsonParityRows is matrixRows with every null filled in, so listwise (the
// matrix) and per-pair (TEST_PEARSON_R) deletion keep the same rows.
// It keeps matrixRows' mixed magnitudes (x1 ≈ 1e4, x2 ≈ x1/2 − 3e3,
// x3 ≈ 1e6) and its zero-weight rows on both weight columns.
func pearsonParityRows(n int) [][5]float64 {
	rows := matrixRows(n)
	for i := range rows {
		if math.IsNaN(rows[i][0]) {
			rows[i][0] = 1e4 + float64(i%37)
		}
		if math.IsNaN(rows[i][1]) {
			rows[i][1] = 0.5*rows[i][0] - 3e3 + 1e-4*float64(i%13)
		}
	}
	return rows
}

// TestMatrixCorrelation_PearsonParity is the TEST_PEARSON_R parity
// gate: every off-diagonal cell of MAT_CORRELATION equals the r of a
// TEST_PEARSON_R run on that pair over the same rows within
// 1e-12 + 1e-12·|r|, in both field orders, unweighted and under both
// weight kinds. On one merge block of unit-weight rows (serial, ≤ 4096
// rows) the cell is BITWISE the test's r when the test's field is the
// earlier member: the block's co-moment runs the same Welford
// recurrence and Corr divides by the same √(M2_x·M2_y). Past one block
// (the merge tree), under weights (the test's first-row step differs)
// and in the reversed field order (the cross product accumulates the
// other way) the operation order differs, so agreement is to
// tolerance only.
//
// The bitwise leg is asserted on amd64 only (CI's architecture). The
// matrix is FMA-free everywhere (linalg's reference co-moment), but
// TEST_PEARSON_R's recurrence is plain Go, which the compiler fuses
// into multiply-adds on arm64 (and other FMA targets), so there its
// last bits differ; its bits must not change, so the tolerance leg is
// what holds on every architecture.
func TestMatrixCorrelation_PearsonParity(t *testing.T) {
	members := []string{"x1", "x2", "x3"}
	pearsonUnfused := runtime.GOARCH == "amd64"
	if !pearsonUnfused {
		t.Logf("GOARCH=%s fuses TEST_PEARSON_R's recurrence into multiply-adds: bitwise leg skipped, tolerance leg asserted", runtime.GOARCH)
	}
	for _, n := range []int{40, 4096, 4097, 3*4096 + 17} {
		cfg := writeMatrixCohort(t, "parity.pulse", pearsonParityRows(n))
		for _, w := range []string{"", "f", "p"} {
			t.Run(fmt.Sprintf("n=%d/weight=%q", n, w), func(t *testing.T) {
				var tests []*types.Test
				for i, a := range members {
					for j, b := range members {
						if i == j {
							continue
						}
						tests = append(tests, &types.Test{
							Type: types.TEST_PEARSON_R, Field: a, Field2: b,
							Label:  a + "~" + b,
							Weight: covSpec("", "", 0, w, "").Weight,
						})
					}
				}
				req := &types.Request{
					Cohort:   &types.Cohort{Filename: "parity.pulse"},
					Matrices: []types.MatrixSpec{corrSpec("corr", members, w)},
					Tests:    tests,
				}
				resp := processMatrices(t, cfg, req)
				if len(resp.Tests) != len(tests) {
					t.Fatalf("%d test results, want %d", len(resp.Tests), len(tests))
				}
				vals := resp.Matrices[0].Primary.Values
				bitwise := pearsonUnfused && w == "" && n <= 4096
				k := 0
				for i := range members {
					for j := range members {
						if i == j {
							if vals[i][j] != 1 {
								t.Errorf("diagonal [%d][%d] = %v, want exactly 1", i, j, vals[i][j])
							}
							continue
						}
						ref := resp.Tests[k].Statistic
						k++
						got := vals[i][j]
						if !(math.Abs(got-ref) <= 1e-12+1e-12*math.Abs(ref)) {
							t.Errorf("[%d][%d] = %.17g, TEST_PEARSON_R(%s, %s) = %.17g", i, j, got, members[i], members[j], ref)
						}
						if bitwise && i < j && math.Float64bits(got) != math.Float64bits(ref) {
							t.Errorf("[%d][%d] = %.17g (%#x), TEST_PEARSON_R(%s, %s) = %.17g (%#x): want the same bits on one unit-weight block",
								i, j, got, math.Float64bits(got), members[i], members[j], ref, math.Float64bits(ref))
						}
						if got < -1 || got > 1 {
							t.Errorf("[%d][%d] = %v outside [-1, 1]", i, j, got)
						}
					}
				}
			})
		}
	}
}

// TestMatrixCorrelation_ZeroSpreadIsNull: a constant member has no
// defined correlation — its whole row and column, diagonal included,
// are NaN in Go and null on the wire (keys kept), the determinant is
// null, and every other cell is still defined.
func TestMatrixCorrelation_ZeroSpreadIsNull(t *testing.T) {
	rows := pearsonParityRows(60)
	for i := range rows {
		rows[i][2] = 7.25 // x3 constant
	}
	cfg := writeMatrixCohort(t, "flat.pulse", rows)
	req := &types.Request{
		Cohort:   &types.Cohort{Filename: "flat.pulse"},
		Matrices: []types.MatrixSpec{corrSpec("", []string{"x1", "x3", "x2"}, "")},
	}
	res := processMatrices(t, cfg, req).Matrices[0]
	if res.Name != "MAT_CORRELATION" {
		t.Errorf("default name = %q, want MAT_CORRELATION", res.Name)
	}
	v := res.Primary.Values
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			touches := r == 1 || c == 1
			if touches != math.IsNaN(v[r][c]) {
				t.Errorf("[%d][%d] = %v; zero-spread member x3 is axis 1", r, c, v[r][c])
			}
		}
	}
	if v[0][0] != 1 || v[2][2] != 1 {
		t.Errorf("defined diagonal = %v, %v; want 1", v[0][0], v[2][2])
	}
	if !math.IsNaN(res.Scalars["determinant"]) {
		t.Errorf("determinant = %v, want NaN", res.Scalars["determinant"])
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"determinant":null`) || !strings.Contains(string(b), `[null,null,null]`) {
		t.Errorf("wire form lacks the null row / determinant: %s", b)
	}
}
