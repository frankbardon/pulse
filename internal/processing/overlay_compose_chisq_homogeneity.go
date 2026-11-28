package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// applyChiSqHomogeneity is OVERLAY_CHISQ_VS_REF under
// params.reference_kind "sample" (#791): the reference slot is a second
// sample, not a fixed distribution, so the target and reference tables
// are tested for homogeneity — one 2 x |C| contingency table whose rows
// are the two slots and whose columns are the compared cells.
//
// Math:
//
//	C         = cells present on both sides with target + ref > 0
//	T, R      = Σ_C target, Σ_C ref;  N = T + R;  col_j = t_j + r_j
//	E_t,j     = T · col_j / N;  E_r,j = R · col_j / N
//	chisq     = Σ_C (t_j − E_t,j)² / E_t,j + (r_j − E_r,j)² / E_r,j
//	df        = |C| − 1
//
// Equals R's chisq.test(rbind(target[C], ref[C])) (no continuity
// correction: R applies none past 2 x 2, and a 2 x 2 table here means
// a two-cell host). A cell empty on both sides carries no evidence and
// is left out (R would divide by its zero expected); a cell empty on
// ONE side is an ordinary observation, so no PULSE_OVERLAY_REF_ZERO.
//
// Weights: each slot's cells are Σw; under kind probability each slot's
// table is scaled to its OWN Kish n_eff (c = n_eff / Σw over the cells
// read), frequency and unweighted read as is — first-order Kish on both
// rows, not Rao-Scott. summary.parameters' sum_weights / n_eff are the
// union of both slots' floors (OVERLAY_PROP_Z_CELL's rule). Both slots'
// floors are read, so the components-disabled refusal and the Compose
// veto cover the reference too (overlayhost.ChiSqVsRefReadsReference).
func applyChiSqHomogeneity(spec *types.ComposeOverlaySpec, reference *types.Response, refMx *types.MatrixPayload, target *types.Response, targetMx *types.MatrixPayload) (types.OverlayLayer, []types.OverlayWarning, error) {
	type refCell struct {
		v    float64
		i, j int
	}
	refAt := make(map[matrixCellLookupKey]refCell, len(refMx.RowKeys)*len(refMx.ColumnKeys))
	for i := 0; i < len(refMx.RowKeys) && i < len(refMx.Cells); i++ {
		rk := axisKeyToString(refMx.RowKeys[i])
		for j := 0; j < len(refMx.ColumnKeys) && j < len(refMx.Cells[i]); j++ {
			cell := refMx.Cells[i][j]
			if !cell.Present {
				continue
			}
			v, ok := scalarFromCell(cell)
			if !ok {
				continue
			}
			refAt[matrixCellLookupKey{row: rk, col: axisKeyToString(refMx.ColumnKeys[j])}] = refCell{v: v, i: i, j: j}
		}
	}

	tHost, rHost := responseHostView(target), responseHostView(reference)
	tBasis, rBasis := tHost.WeightBasis(), rHost.WeightBasis()
	var tTally, rTally, union weightedLegTally

	type cellPair struct{ t, r float64 }
	pairs := make([]cellPair, 0, len(refAt))
	for i := 0; i < len(targetMx.RowKeys) && i < len(targetMx.Cells); i++ {
		rk := axisKeyToString(targetMx.RowKeys[i])
		for j := 0; j < len(targetMx.ColumnKeys) && j < len(targetMx.Cells[i]); j++ {
			cell := targetMx.Cells[i][j]
			if !cell.Present {
				continue
			}
			tv, ok := scalarFromCell(cell)
			if !ok {
				continue
			}
			rc, ok := refAt[matrixCellLookupKey{row: rk, col: axisKeyToString(targetMx.ColumnKeys[j])}]
			if !ok {
				continue
			}
			if tv+rc.v <= 0 {
				continue // empty on both sides: no evidence, outside C
			}
			pairs = append(pairs, cellPair{t: tv, r: rc.v})
			if l, ok := tHost.CellFloor(i, j, tBasis); ok {
				tTally.add(l)
				union.add(l)
			}
			if l, ok := rHost.CellFloor(rc.i, rc.j, rBasis); ok {
				rTally.add(l)
				union.add(l)
			}
		}
	}
	// Under kind probability each slot's Σw table is scaled to its own
	// Kish n_eff; frequency and unweighted slots are read as is (no
	// multiply, so their operation order is untouched).
	if tBasis == weighting.Probability {
		c := tTally.scale()
		for k := range pairs {
			pairs[k].t *= c
		}
	}
	if rBasis == weighting.Probability {
		c := rTally.scale()
		for k := range pairs {
			pairs[k].r *= c
		}
	}

	var targetN, refN float64
	for _, p := range pairs {
		targetN += p.t
		refN += p.r
	}
	var warnings []types.OverlayWarning
	if targetN <= 0 || refN <= 0 || len(pairs) == 0 {
		warnings = append(warnings, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " cannot compute the homogeneity χ² (empty target or reference)",
			Details: map[string]any{
				"kind":           string(spec.Kind),
				"reference":      spec.Reference,
				"reference_kind": "sample",
				"target_n":       targetN,
				"ref_n":          refN,
			},
		})
		summary := &types.OverlaySummary{
			Statistic:  nanPtr(),
			PValue:     nanPtr(),
			Parameters: map[string]float64{"df": math.NaN()},
		}
		union.stamp(summary)
		return composeScalarOverlayLayer(spec, nil, summary), warnings, nil
	}

	n := targetN + refN
	var chisq float64
	lowExpected := 0
	for _, p := range pairs {
		col := p.t + p.r
		for _, side := range [2]struct{ obs, total float64 }{{p.t, targetN}, {p.r, refN}} {
			expected := float64(side.total*col) / n
			if expected < 5 {
				lowExpected++
			}
			diff := side.obs - expected
			chisq += float64(diff*diff) / expected
		}
	}
	df := float64(len(pairs) - 1)
	pValue := chiSquareSurvival(chisq, df)
	if lowExpected > 0 {
		warnings = append(warnings, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_EXPECTED_LOW),
			Message: "overlay " + string(spec.Kind) + " contingency table has cells with expected < 5",
			Details: map[string]any{
				"kind":               string(spec.Kind),
				"reference":          spec.Reference,
				"reference_kind":     "sample",
				"low_expected_cells": lowExpected,
				"total_cells":        2 * len(pairs),
			},
		})
	}
	stat, pv := chisq, pValue
	summary := &types.OverlaySummary{
		Statistic:  &stat,
		PValue:     &pv,
		Parameters: map[string]float64{"df": df},
	}
	union.stamp(summary)
	scalarValue := pValue
	return composeScalarOverlayLayer(spec, &scalarValue, summary), warnings, nil
}
