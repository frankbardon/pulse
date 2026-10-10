package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
)

// matrix_rank.go is MAT_CORRELATION's rank methods (params.method
// "spearman" / "kendall") over a BUFFERED slot's rows. Each cell is the
// same computation as the matching two-field test over the same rows —
// rankPearson (TEST_SPEARMAN_R) or kendallCounts + kendallTauB
// (TEST_KENDALL_TAU) — so a cell equals the test's statistic bit for
// bit when both see the same rows.
//
// Rows: the buffer admits exactly the rows the co-moment counts
// (weight-0 rows included, so the floor n matches the Pearson matrix).
// A weight-0 row carries no mass and does not exist on the frequency
// expansion, so the rank computation leaves it out, as the tests do
// (testWeight.rowWeight skips w = 0). Under listwise every cell reads
// the complete rows, ranked once per member; under pairwise each cell
// re-ranks the pair over that pair's own rows (R
// cor(use = "pairwise.complete.obs", method = …)).
//
// Undefined figures are NaN: a pair whose rows leave either member a
// single distinct value (fewer than two massed rows included). The
// diagonal is 1 for a member whose own rows have spread and NaN
// otherwise — the Pearson matrix's zero-spread rule, so a flat member's
// whole row and column are null.

// rankCorrelation returns the slot's rank correlation matrix.
func rankCorrelation(in *matrixFinalizeInput) (*linalg.Sym, error) {
	rows := in.Rows
	if rows == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"rank correlation matrix finalized without its row buffer",
			map[string]any{"matrix": in.Plan().Name, "method": in.Plan().Method})
	}
	p := len(in.Members())
	out, err := linalg.NewSym(p, nil)
	if err != nil {
		return nil, err
	}
	kendall := in.Plan().Method == vectors.CorrelationKendall

	if !rows.pairwise {
		// Listwise: one row set for every cell.
		cols := make([][]float64, p)
		var ws []float64
		for r := 0; r < rows.Len(); r++ {
			w := rows.Weight(r)
			if w == 0 {
				continue
			}
			row := rows.Row(r)
			for i := 0; i < p; i++ {
				cols[i] = append(cols[i], row[i])
			}
			ws = append(ws, w)
		}
		var ranks [][]float64
		var nW float64
		if !kendall {
			ranks = make([][]float64, p)
			for i := range cols {
				ranks[i], _ = weightedMidRanks(cols[i], ws)
			}
			for _, w := range ws {
				nW += w
			}
		}
		for i := 0; i < p; i++ {
			out.Set(i, i, rankDiagonal(cols[i]))
			for j := i + 1; j < p; j++ {
				if kendall {
					out.Set(i, j, kendallCell(cols[i], cols[j], ws))
				} else {
					out.Set(i, j, spearmanCell(ranks[i], ranks[j], ws, nW))
				}
			}
		}
		return out, nil
	}

	// Pairwise: each cell over its own massed rows.
	for i := 0; i < p; i++ {
		xs, _, _ := massedPair(rows, i, i)
		out.Set(i, i, rankDiagonal(xs))
		for j := i + 1; j < p; j++ {
			xs, ys, ws := massedPair(rows, i, j)
			if kendall {
				out.Set(i, j, kendallCell(xs, ys, ws))
				continue
			}
			rx, _ := weightedMidRanks(xs, ws)
			ry, _ := weightedMidRanks(ys, ws)
			var nW float64
			for _, w := range ws {
				nW += w
			}
			out.Set(i, j, spearmanCell(rx, ry, ws, nW))
		}
	}
	return out, nil
}

// massedPair is matrixRows.Pair without the weight-0 rows.
func massedPair(rows *matrixRows, i, j int) (xs, ys, ws []float64) {
	ax, ay, aw := rows.Pair(i, j)
	for k, w := range aw {
		if w == 0 {
			continue
		}
		xs = append(xs, ax[k])
		ys = append(ys, ay[k])
		ws = append(ws, w)
	}
	return xs, ys, ws
}

// spearmanCell is ρ over two mid-rank columns, NaN when undefined.
func spearmanCell(rx, ry, ws []float64, nW float64) float64 {
	rho, ok := rankPearson(rx, ry, ws, nW)
	if !ok {
		return math.NaN()
	}
	return rho
}

// kendallCell is τ-b over two value columns, NaN when undefined.
func kendallCell(xs, ys, ws []float64) float64 {
	tau, ok := kendallTauB(kendallCounts(xs, ys, ws))
	if !ok {
		return math.NaN()
	}
	return tau
}

// rankDiagonal is 1 when xs holds at least two distinct values (the
// member has spread on its massed rows) and NaN otherwise.
func rankDiagonal(xs []float64) float64 {
	for _, v := range xs {
		if v != xs[0] {
			return 1
		}
	}
	return math.NaN()
}
