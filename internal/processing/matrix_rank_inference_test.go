package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// matrix_rank_inference_test.go pins the rank methods' p-value rule
// (matrix_rank_inference.go) against R: the null distributions R's
// cor.test calls (C_pRho = AS 89, C_pKendall) over full grids, and
// cor.test itself on single pairs through the whole cell rule — every
// branch: exact / approximate, tied / untied, n on both sides of 50
// (Kendall) and 1290 (Spearman), |r| = 1. The matrix end to end is
// internal/service TestMatrixInference_MatchesROracle.

type rankInferenceOracle struct {
	RankRho []struct {
		N     int      `json:"n"`
		Q     float64  `json:"q"`
		Lower bool     `json:"lower"`
		P     *float64 `json:"p"`
	} `json:"rank_rho"`
	RankKendall []struct {
		N int      `json:"n"`
		Q float64  `json:"q"`
		P *float64 `json:"p"`
	} `json:"rank_kendall"`
	RankPairs []struct {
		Method string    `json:"method"`
		Why    string    `json:"why"`
		X      []float64 `json:"x"`
		Y      []float64 `json:"y"`
		R      *float64  `json:"r"`
		P      *float64  `json:"p"`
	} `json:"rank_pairs"`
}

func loadRankInferenceOracle(t *testing.T) rankInferenceOracle {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(referenceDir, "matrix_inference.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := splitReferenceHash(raw)
	var doc rankInferenceOracle
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.RankRho) < 500 || len(doc.RankKendall) < 200 || len(doc.RankPairs) < 80 {
		t.Fatalf("oracle too small: %d rho, %d kendall, %d pairs", len(doc.RankRho), len(doc.RankKendall), len(doc.RankPairs))
	}
	return doc
}

// rankPTol: 1e-10 absolute + relative. Sums of the same counts in the
// same order as R; only exp / erfc differ in the last bits.
const rankPTol = 1e-10

func rankPClose(got, want float64) bool {
	return math.Abs(got-want) <= rankPTol*(1+math.Abs(want))
}

// TestPRho_MatchesR: AS 89 equals R's C_pRho over every statistic for
// n ≤ 9 (the exact permutation counts) and a grid to n = 1290 (the
// Edgeworth series, clamped), both tails.
func TestPRho_MatchesR(t *testing.T) {
	doc := loadRankInferenceOracle(t)
	for _, c := range doc.RankRho {
		got := pRho(c.Q, c.N, c.Lower)
		if !rankPClose(got, *c.P) {
			t.Errorf("pRho(q=%g, n=%d, lower=%v) = %.17g, R = %.17g", c.Q, c.N, c.Lower, got, *c.P)
		}
	}
}

// TestPKendall_MatchesR: the exact Kendall distribution equals R's
// C_pKendall over every q (and one past each end) for n ≤ 10 and a
// grid to n = 49.
func TestPKendall_MatchesR(t *testing.T) {
	doc := loadRankInferenceOracle(t)
	for _, c := range doc.RankKendall {
		got := pKendall(c.Q, c.N)
		if !rankPClose(got, *c.P) {
			t.Errorf("pKendall(q=%g, n=%d) = %.17g, R = %.17g", c.Q, c.N, got, *c.P)
		}
	}
}

// TestRankCellP_MatchesCorTest: one pair through the cell rule —
// mid-ranks, the Knight counts, ties, the exact / approximate switch —
// equals cor.test(method = …)'s p (and estimate).
func TestRankCellP_MatchesCorTest(t *testing.T) {
	doc := loadRankInferenceOracle(t)
	for i, c := range doc.RankPairs {
		t.Run(fmt.Sprintf("%d/%s/%s/n=%d", i, c.Method, c.Why, len(c.X)), func(t *testing.T) {
			ws := make([]float64, len(c.X))
			for k := range ws {
				ws[k] = 1
			}
			n := float64(len(c.X))
			rx, tx := weightedMidRanks(c.X, ws)
			ry, ty := weightedMidRanks(c.Y, ws)
			var r, p float64
			if c.Method == "kendall" {
				cc, d, tx2, ty2 := kendallCounts(c.X, c.Y, ws)
				r = kendallCellOf(cc, d, tx2, ty2)
				p = kendallCellP(r, cc, d, n, tx, ty)
			} else {
				r = spearmanCell(rx, ry, ws, n)
				p = spearmanCellP(r, rx, ry, n, tx, ty)
			}
			if !rankPClose(r, *c.R) {
				t.Errorf("estimate = %.17g, R = %.17g", r, *c.R)
			}
			if !rankPClose(p, *c.P) {
				t.Errorf("p = %.17g, cor.test = %.17g", p, *c.P)
			}
		})
	}
}

// TestKendallVarS_IsTheTestsVariance: TEST_KENDALL_TAU's var_s detail
// is kendallVarS (one tie-corrected variance for the test and the
// matrix).
func TestKendallVarS_IsTheTestsVariance(t *testing.T) {
	cases := []struct {
		name   string
		tx, ty []float64
		n      float64
		want   float64
	}{
		// No ties: n(n−1)(2n+5)/18.
		{"untied", nil, nil, 10, 10 * 9 * 25 / 18.0},
		// One x tie of 2, one y tie of 3 at n = 6:
		// (6·5·17 − 2·1·9 − 3·2·11)/18 + (2·6)/(2·6·5) + 0.
		{"tied", []float64{2}, []float64{3}, 6, (6*5*17-2*1*9-3*2*11)/18.0 + 2*6/(2*6*5.0)},
	}
	for _, c := range cases {
		if got := kendallVarS(c.n, c.tx, c.ty); math.Abs(got-c.want) > 1e-12*c.want {
			t.Errorf("%s: kendallVarS = %v, want %v", c.name, got, c.want)
		}
	}
}
