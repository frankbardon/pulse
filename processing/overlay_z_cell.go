package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// applyZCell is the COMPOSE-host MATRIX-shape runtime handler for
// OVERLAY_Z_CELL (E1-S17). Per-cell two-sample z-test on the means
// against the reference slot's matching cell. Cells are treated as
// sample means; per-side variance and sample size are read from the
// spec's `Params` map with the same defaults applied by `applyTCell`
// (`variance_target` / `variance_ref` default 1.0, `sample_size_target`
// / `sample_size_ref` default 2).
//
// Triple-aware: when both the target cell and the matching reference
// cell carry a `WelfordTriple` Rich payload (AGG_WELFORD output), the
// handler reads `(Mean, Variance, N)` directly off the triple and the
// Params fallback is bypassed for that cell pair. Mixed cells (one
// triple, one scalar) fall back to the scalar+Params path on the
// scalar side and use the triple's (Variance, N) on the triple side.
// Triple detection routes through the shared `welfordTripleFromCell`
// helper (processing/welford_triple_extract.go) so the MATRIX-arm and
// the SERIES-arm of the Z parity pair stay symmetric with the
// T parity pair (E1-S9 / E1-S10) and the schema-match gate's
// "welford_triple" cell-shape token.
//
// Math (per cell, post-extraction of mean/variance/n on each side):
//
//	se      = sqrt(var_target/n_target + var_ref/n_ref)
//	z       = (target_mean - ref_mean) / se
//	p_value = 2 * (1 - Φ(|z|))
//
// Reuses `standardNormalCDF` (processing/test_z.go via the canonical
// CDF helper at processing/test_stat.go) so the overlay and
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
func applyZCell(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []OverlayWarning, error) {
	refMx := readMatrix(reference)
	target, targetIdx := composeFirstTarget(targets, targetIdxs)
	targetMx := readMatrix(target)
	if refMx == nil || targetMx == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires MATRIX-shape slots for reference and target",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"kind":         string(spec.Kind),
				"ref_index":    refIdx,
				"target_index": targetIdx,
			})
	}

	refLookup := buildMatrixCellRawLookup(refMx)
	varTargetDefault := varianceFromParams(spec.Params, "variance_target", 1.0)
	varRefDefault := varianceFromParams(spec.Params, "variance_ref", 1.0)
	nTargetDefault := sampleSizeFromParams(spec.Params, "sample_size_target", 2.0)
	nRefDefault := sampleSizeFromParams(spec.Params, "sample_size_ref", 2.0)

	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	cells := buildEmptyMatrixLike(rowCount, colCount)

	targetLabel := composeFirstTargetLabel(spec)
	var (
		warnings []OverlayWarning
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
				warnings = append(warnings, OverlayWarning{
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
			targetMean, targetVar, targetN, okT := extractCellWelchInputs(cell, varTargetDefault, nTargetDefault)
			refMean, refVar, refN, okR := extractCellWelchInputs(refCell, varRefDefault, nRefDefault)
			if !okT || !okR {
				continue
			}
			p, ok := welchZTest(targetMean, targetVar, targetN, refMean, refVar, refN)
			if !ok {
				warnings = append(warnings, OverlayWarning{
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
	layer := composeMatrixOverlayLayer(spec, targetMx, cells, summary)
	return layer, warnings, nil
}

// welchZTest computes the two-sided p-value of a two-sample z-test on
// two independent (mean, variance, n) triples. Reuses the Welch
// standard-error arithmetic the t-test family applies (var_a/n_a +
// var_b/n_b) so the overlay and `TEST_Z_TWO_SAMPLE` produce identical
// p-values for the same (mean, variance, n) inputs. Finalises via
// `standardNormalCDF` (the canonical CDF helper at
// processing/test_stat.go) — the only mathematical divergence from
// `welchTTest`.
//
// Math:
//
//	se      = sqrt(va/na + vb/nb)
//	z       = (meanA - meanB) / se
//	p_value = 2 * (1 - Φ(|z|))
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
	p := 2 * (1 - standardNormalCDF(math.Abs(z)))
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return math.NaN(), false
	}
	return p, true
}

// extractCellWelchInputs reads `(mean, variance, n)` from a MatrixCell.
// A cell carrying a `WelfordTriple` Rich payload returns the triple's
// `(Mean, Variance, N)` and bypasses the per-side `Params` defaults;
// a cell carrying a scalar numeric falls back to the supplied
// `varDefault` + `nDefault`. Absent / non-numeric cells return
// `(0, 0, 0, false)` so the caller can skip the cell.
//
// Triple detection delegates to `welfordTripleFromCell` so the MATRIX
// and SERIES arms of the Z parity pair stay symmetric with the T
// parity pair.
func extractCellWelchInputs(cell types.MatrixCell, varDefault, nDefault float64) (mean, variance, n float64, ok bool) {
	if !cell.Present {
		return 0, 0, 0, false
	}
	if triple, isTriple := welfordTripleFromCell(cell); isTriple {
		return triple.Mean, triple.Variance, float64(triple.N), true
	}
	scalar, scalarOK := scalarFromCell(cell)
	if !scalarOK {
		return 0, 0, 0, false
	}
	return scalar, varDefault, nDefault, true
}
