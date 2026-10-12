package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/twosample"
	"github.com/frankbardon/pulse/types"
)

// applyZCell is the COMPOSE-host MATRIX-shape runtime handler for
// OVERLAY_Z_CELL. Per-cell two-sample z-test on the means against the
// reference slot's matching cell. Cells are treated as sample means;
// per-side variance and sample size are read from the spec's `Params`
// map under the same rule as `applyTCell` (`variance_target` /
// `variance_ref`, `sample_size_target` / `sample_size_ref`, or the
// symmetric `variance` / `sample_size`) — required on a scalar side,
// no default (twoSampleLegs).
//
// Components-source: when both the target cell and the matching
// reference cell carry a `{mean, variance, n}` triple at
// Response.Components.Crosstab.CellComponents[r][c] — emitted by
// AGG_WELFORD via the MetaAggregator path — the handler reads the
// triple directly from the components map and bypasses the Params
// fallback for that cell pair. Mixed cells (one triple, one scalar)
// fall back to the scalar+Params path on the scalar side and use the
// triple's (variance, n) on the triple side. The universal Components
// surface owns the triple payload.
//
// Math (per cell, post-extraction of mean/variance/n on each side):
//
//	se      = sqrt(var_target/n_target + var_ref/n_ref)
//	z       = (target_mean - ref_mean) / se
//	p_value = 2 * Φ(-|z|)
//
// Reuses `standardNormalCDF` (internal/processing/test_z.go via the canonical
// CDF helper at internal/processing/test_stat.go) so the overlay and
// `TEST_Z_TWO_SAMPLE` produce identical p-values for the same (mean,
// variance, n) inputs. The SE arithmetic mirrors `welchTTest` in
// overlay_compose_handlers.go — same insufficient-n / zero-variance /
// zero-SE rejection rules, only the finaliser swaps to the standard
// normal survival.
//
// Degenerate paths (`n < 2`, both variances == 0, se == 0,
// non-finite z): emit `PULSE_OVERLAY_REF_ZERO` warning + NaN cell.
// Missing reference cells emit the same warning with
// `ref_missing=true`.
func applyZCell(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []types.OverlayWarning, error) {
	refMx := readMatrix(reference)
	target, targetIdx := composeFirstTarget(targets, targetIdxs)
	targetMx := readMatrix(target)
	if refMx == nil || targetMx == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB,
			"overlay "+string(spec.Kind)+" requires MATRIX-shape slots for reference and target",
			map[string]any{
				"kind":         string(spec.Kind),
				"ref_index":    refIdx,
				"target_index": targetIdx,
			})
	}

	refLookup := buildMatrixCellRawLookup(refMx)
	refCoordLookup := buildMatrixCellCoordLookup(refMx)
	legs := newTwoSampleLegs(spec, refIdx, targetIdx)

	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	cells := buildEmptyMatrixLike(rowCount, colCount)

	targetLabel := composeFirstTargetLabel(spec)
	var tally weightedLegTally
	var (
		warnings []types.OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i := 0; i < rowCount; i++ {
		rowKeyStr := axisKeyToString(targetMx.RowKeys[i])
		for j := 0; j < colCount; j++ {
			if i >= len(targetMx.Cells) || j >= len(targetMx.Cells[i]) {
				continue
			}
			cell := targetMx.Cells[i][j]
			if !cell.Present {
				continue
			}
			colKeyStr := axisKeyToString(targetMx.ColumnKeys[j])
			refCell, refPresent := refLookup[matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}]
			if !refPresent {
				warnings = append(warnings, types.OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " reference cell missing for target (row, col) key pair",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"reference":    spec.Reference,
						"target_label": targetLabel,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
						"ref_missing":  true,
					},
				})
				continue
			}
			refCoord, refCoordOK := refCoordLookup[matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}]
			tLeg, okT, err := extractCellWelchInputsFromComponents(target, cell, i, j, legs, twosample.Target)
			if err != nil {
				return types.OverlayLayer{}, nil, err
			}
			var (
				rLeg meanLeg
				okR  bool
			)
			if refCoordOK {
				rLeg, okR, err = extractCellWelchInputsFromComponents(reference, refCell, refCoord.Row, refCoord.Col, legs, twosample.Reference)
			} else {
				rLeg, okR, err = extractCellWelchInputsScalar(refCell, legs, twosample.Reference)
			}
			if err != nil {
				return types.OverlayLayer{}, nil, err
			}
			if !okT || !okR {
				continue
			}
			tally.add(tLeg)
			tally.add(rLeg)
			targetMean, refMean := tLeg.mean, rLeg.mean
			p, ok := welchZTest(targetMean, tLeg.variance, tLeg.nStar, refMean, rLeg.variance, rLeg.nStar)
			if !ok {
				warnings = append(warnings, types.OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " z-test undefined for cell (zero SE OR insufficient n)",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"reference":    spec.Reference,
						"target_label": targetLabel,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
						"target_value": targetMean,
						"ref_value":    refMean,
					},
				})
				cells[i][j] = types.MatrixCell{Value: math.NaN(), Present: true}
				continue
			}
			cells[i][j] = types.MatrixCell{Value: p, Present: true}
			updateMinMaxCount(p, &seen, &minV, &maxV)
		}
	}

	summary := &types.OverlaySummary{}
	if seen > 0 {
		mn, mx, count := minV, maxV, seen
		summary.Min = &mn
		summary.Max = &mx
		summary.Count = &count
	} else {
		zeroCount := 0
		summary.Count = &zeroCount
	}
	tally.stamp(summary)
	layer := composeMatrixOverlayLayer(spec, targetMx, cells, summary)
	return layer, warnings, nil
}

// welchZTest computes the two-sided p-value of a two-sample z-test on
// two independent (mean, variance, n) triples. Reuses the Welch
// standard-error arithmetic the t-test family applies (var_a/n_a +
// var_b/n_b) so the overlay and `TEST_Z_TWO_SAMPLE` produce identical
// p-values for the same (mean, variance, n) inputs. Finalises via
// `standardNormalCDF` (the canonical CDF helper at
// internal/processing/test_stat.go) — the only mathematical divergence from
// `welchTTest`.
//
// Math:
//
//	se      = sqrt(va/na + vb/nb)
//	z       = (meanA - meanB) / se
//	p_value = 2 * Φ(-|z|)
//
// Returns (NaN, false) when:
//   - either sample size < 2
//   - both variances are 0 (degenerate SE)
//   - SE itself rounds to 0
//   - the computation produces a non-finite z or p
func welchZTest(meanA, varA, nA, meanB, varB, nB float64) (float64, bool) {
	if nA < 2 || nB < 2 {
		return math.NaN(), false
	}
	if varA <= 0 && varB <= 0 {
		return math.NaN(), false
	}
	se := math.Sqrt(varA/nA + varB/nB)
	if se == 0 {
		return math.NaN(), false
	}
	z := (meanA - meanB) / se
	if math.IsNaN(z) || math.IsInf(z, 0) {
		return math.NaN(), false
	}
	p := normalTwoSidedP(z)
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return math.NaN(), false
	}
	return p, true
}

// extractCellWelchInputsFromComponents reads the cell's two-sample
// inputs from the per-cell Components map at Response.Components.
// Crosstab.CellComponents[r][c] when the operator emitted a `{mean,
// variance, n}` triple — through the weighted-host rule
// (extractCellComponentsTriple → readMeanLeg), so a weighted cell
// answers (mean, variance on w*, N*). A cell without the triple but
// with a scalar value is the scalar route: side's explicit params
// (extractCellWelchInputsScalar).
//
// Absent / non-numeric cells with no components triple return ok=false
// so the caller can skip the cell. The universal Components surface is
// the only triple source.
func extractCellWelchInputsFromComponents(resp *types.Response, cell types.MatrixCell, r, c int, legs *twoSampleLegs, side twosample.Side) (meanLeg, bool, error) {
	if !cell.Present {
		return meanLeg{}, false, nil
	}
	if leg, tripleOK := extractCellComponentsTriple(resp, r, c); tripleOK {
		return leg, true, nil
	}
	return extractCellWelchInputsScalar(cell, legs, side)
}

// extractCellWelchInputsScalar is the scalar route. Treats a scalar
// MatrixCell.Value as the cell mean with side's explicit (variance, n)
// from params — no default: PULSE_OVERLAY_PARAM_MISSING when absent
// (twoSampleLegs.scalar).
func extractCellWelchInputsScalar(cell types.MatrixCell, legs *twoSampleLegs, side twosample.Side) (meanLeg, bool, error) {
	if !cell.Present {
		return meanLeg{}, false, nil
	}
	scalar, scalarOK := scalarFromCell(cell)
	if !scalarOK {
		return meanLeg{}, false, nil
	}
	v, n, err := legs.scalar(side)
	if err != nil {
		return meanLeg{}, false, err
	}
	return meanLeg{mean: scalar, variance: v, nStar: n}, true, nil
}
