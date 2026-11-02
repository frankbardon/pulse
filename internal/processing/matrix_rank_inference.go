package processing

import (
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_rank_inference.go is params.inference on MAT_CORRELATION's rank
// methods (params.method "spearman" / "kendall"): each cell's two-sided
// p-value is R cor.test(method = …)'s default (exact = NULL,
// continuity = FALSE) over the cell's own massed rows — the frequency
// expansion under a frequency weight, where n = Σw and a weight above 1
// is a tie — and its interval is a Fisher-z interval with the method's
// own standard error, which cor.test does not report.
//
// p: kendallCorTestP / spearmanCorTestP (rank_cortest.go), the helpers
// TEST_KENDALL_TAU / TEST_SPEARMAN_R call, so a cell's p equals the
// test's over the same rows.
//
// Interval: tanh(atanh(r) ∓ z_{α/2}·se), se² = (1 + ρ²/2)/(n − 3) for
// Spearman (Bonett & Wright 2000) and 0.437/(n − 4) for Kendall's τ
// (Fieller, Hartley & Pearson 1957); |r| = 1 gives [r, r]. Floors: p
// needs 3 rows and n − 2 > 0 (the shared rule); the interval 4 rows and
// n − 3 > 0 (Spearman) or 5 rows and n − 4 > 0 (Kendall). N* is the
// shared pairSize (unweighted rows, Σw under frequency; a rank method
// takes no probability weight), so the thin-cell nulls and warnings are
// inferenceAux's.

// rankInference is MAT_CORRELATION's params.inference under a rank
// method: pv holds each cell's p off rankCorrelation.
func (in *matrixFinalizeInput) rankInference(r *linalg.Sym, pv [][]float64) (map[string]*types.MatrixValues, []*types.ResponseWarning) {
	basis := weighting.BasisOf(in.slot.weight)
	rule := inferenceRule{
		p:      func(a, b int) float64 { return pv[a][b] },
		zDF:    spearmanZDF,
		ciRows: 1,
		ciNeed: "4 rows and N* − 3 > 0 for the interval (Bonett–Wright se)",
	}
	if in.Plan().Method == "kendall" {
		rule.zDF, rule.ciRows = kendallZDF, 2
		rule.ciNeed = "5 rows and N* − 4 > 0 for the interval (Fieller se)"
	}
	return in.inferenceAux(r.At, 0, func(a, b int) pairInference {
		return pairSize(in.CM, basis, a, b)
	}, in.Plan().Pairwise, nil, rule)
}

// spearmanZDF is 1/se² of atanh(ρ), Bonett & Wright (2000):
// se² = (1 + ρ²/2)/(n − 3).
func spearmanZDF(rho, n float64) float64 { return (n - 3) / (1 + float64(rho*rho/2)) }

// kendallZDF is 1/se² of atanh(τ), Fieller, Hartley & Pearson (1957):
// se² = 0.437/(n − 4).
func kendallZDF(_, n float64) float64 { return (n - 4) / 0.437 }
