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

// rankCorrelation returns the slot's rank correlation matrix and, when
// withP (params.inference with the auxiliaries rendered), each cell's
// two-sided p-value over the cell's own massed rows (rankCellP,
// matrix_rank_inference.go; NaN where ρ / τ is undefined and on the
// diagonal) — nil otherwise.
func rankCorrelation(in *matrixFinalizeInput, withP bool) (*linalg.Sym, [][]float64, error) {
	rows := in.Rows
	if rows == nil {
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"rank correlation matrix finalized without its row buffer",
			map[string]any{"matrix": in.Plan().Name, "method": in.Plan().Method})
	}
	p := len(in.Members())
	out, err := linalg.NewSym(p, nil)
	if err != nil {
		return nil, nil, err
	}
	kendall := in.Plan().Method == vectors.CorrelationKendall
	var pv [][]float64
	setP := func(i, j int, v float64) {}
	if withP {
		pv = make([][]float64, p)
		for i := range pv {
			pv[i] = make([]float64, p)
			for j := range pv[i] {
				pv[i][j] = math.NaN()
			}
		}
		setP = func(i, j int, v float64) { pv[i][j], pv[j][i] = v, v }
	}

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
		ranks := make([][]float64, p)
		ties := make([][]float64, p)
		var nW float64
		if !kendall || withP {
			for i := range cols {
				ranks[i], ties[i] = weightedMidRanks(cols[i], ws)
			}
			for _, w := range ws {
				nW += w
			}
		}
		for i := 0; i < p; i++ {
			out.Set(i, i, rankDiagonal(cols[i]))
			for j := i + 1; j < p; j++ {
				if kendall {
					c, d, tx, ty := kendallCounts(cols[i], cols[j], ws)
					out.Set(i, j, kendallCellOf(c, d, tx, ty))
					if withP {
						setP(i, j, kendallCellP(out.At(i, j), c, d, nW, ties[i], ties[j]))
					}
				} else {
					out.Set(i, j, spearmanCell(ranks[i], ranks[j], ws, nW))
					if withP {
						setP(i, j, spearmanCellP(out.At(i, j), ranks[i], ranks[j], nW, ties[i], ties[j]))
					}
				}
			}
		}
		return out, pv, nil
	}

	// Pairwise: each cell over its own massed rows.
	for i := 0; i < p; i++ {
		xs, _, _ := massedPair(rows, i, i)
		out.Set(i, i, rankDiagonal(xs))
		for j := i + 1; j < p; j++ {
			xs, ys, ws := massedPair(rows, i, j)
			var nW float64
			for _, w := range ws {
				nW += w
			}
			if kendall {
				c, d, tx, ty := kendallCounts(xs, ys, ws)
				out.Set(i, j, kendallCellOf(c, d, tx, ty))
				if withP {
					_, tiesX := weightedMidRanks(xs, ws)
					_, tiesY := weightedMidRanks(ys, ws)
					setP(i, j, kendallCellP(out.At(i, j), c, d, nW, tiesX, tiesY))
				}
				continue
			}
			rx, tiesX := weightedMidRanks(xs, ws)
			ry, tiesY := weightedMidRanks(ys, ws)
			out.Set(i, j, spearmanCell(rx, ry, ws, nW))
			if withP {
				setP(i, j, spearmanCellP(out.At(i, j), rx, ry, nW, tiesX, tiesY))
			}
		}
	}
	return out, pv, nil
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

// kendallCellOf is τ-b off the Knight counts, NaN when undefined.
func kendallCellOf(c, d, tx, ty float64) float64 {
	tau, ok := kendallTauB(c, d, tx, ty)
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
