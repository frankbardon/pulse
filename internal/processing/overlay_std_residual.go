package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Part names OVERLAY_STD_RESIDUAL writes into OverlayPayload.Parts.
const (
	// StdResidualPartExpected is the expected-count matrix part.
	StdResidualPartExpected = "expected"
	// StdResidualPartP is the two-sided normal p-value matrix part —
	// the kind's multiplicity p-site.
	StdResidualPartP = "p"
)

// contingencyTable is a crosstab host read as a row × column table of
// observed counts: absent cells count 0, the totals are recomputed from
// the cells read (never the host's own margins), and under weight kind
// probability the Σw table — cells, totals, grand — is scaled to its
// Kish n_eff (the OVERLAY_CHISQ_MATRIX rule). table carries the cells'
// weighted floor for Summary.Parameters.
type contingencyTable struct {
	observed  [][]float64
	rowTotals []float64
	colTotals []float64
	grand     float64
	table     weightedLegTally
}

// readContingencyTable reads host as a contingency table for the whole-
// table kind spec. A host smaller than 2 × 2 or with a non-positive
// grand total is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, exactly as
// OVERLAY_CHISQ_MATRIX refuses it.
func readContingencyTable(spec *types.OverlaySpec, host *CrosstabHostView) (*contingencyTable, error) {
	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	if rowCount < 2 || colCount < 2 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires a host contingency of at least 2 rows × 2 columns",
			map[string]any{
				"kind": string(spec.Kind),
				"rows": rowCount,
				"cols": colCount,
			})
	}
	ct := &contingencyTable{
		observed:  make([][]float64, rowCount),
		rowTotals: make([]float64, rowCount),
		colTotals: make([]float64, colCount),
	}
	basis := host.WeightBasis()
	for i := 0; i < rowCount; i++ {
		ct.observed[i] = make([]float64, colCount)
		for j := 0; j < colCount; j++ {
			v, ok := host.CellAt(i, j)
			if !ok {
				continue
			}
			if l, ok := host.CellFloor(i, j, basis); ok {
				ct.table.add(l)
			}
			ct.observed[i][j] = v
			ct.rowTotals[i] += v
			ct.colTotals[j] += v
			ct.grand += v
		}
	}
	if ct.grand <= 0 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires a non-zero grand total; got 0",
			map[string]any{
				"kind": string(spec.Kind),
			})
	}
	if basis == weighting.Probability {
		scaleContingency(ct.table.scale(), ct.observed, ct.rowTotals, ct.colTotals, &ct.grand)
	}
	return ct, nil
}

// applyStdResidual is the OVERLAY_STD_RESIDUAL handler: the Haberman
// adjusted standardized residual of every host cell,
//
//	E[i,j] = R_i · C_j / N
//	z[i,j] = (O[i,j] − E[i,j]) / sqrt(E[i,j] · (1 − R_i/N) · (1 − C_j/N))
//	p[i,j] = 2 · Φ(−|z[i,j]|)
//
// — R's chisq.test(x)$stdres. Under independence each z is
// approximately standard normal, so |z| > 1.96 marks a cell that holds
// notably more (z > 0) or fewer (z < 0) cases than the margins predict.
//
// The table is readContingencyTable's: absent cells count 0, totals are
// recomputed from the cells, and a probability-weighted host's Σw table
// is scaled to its Kish n_eff first (as OVERLAY_CHISQ_MATRIX; frequency
// and unweighted hosts never multiply).
//
// Output: a MATRIX payload of residuals on the host's coordinates with
// two parts on the same coordinates — "expected" (E) and "p". Every
// host coordinate is present on all three, absent host cells included
// (an absent cell is an observed 0 the margins still predict counts
// for). A cell whose variance term is not positive — E = 0 (a zero row
// or column total), or a row / column holding the whole table — has an
// undefined residual and p: NaN, written null on the wire. Any expected
// count below 5 (zero included) raises ONE PULSE_OVERLAY_EXPECTED_LOW
// layer warning {kind, low_expected_cells, zero_expected_cells,
// expected_min}: the normal approximation behind z and p weakens there.
//
// Summary: Baseline 0 (residuals centre on 0), Min / Max / Count over
// the defined residuals, plus the weighted floor (sum_weights, n_eff).
func applyStdResidual(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	ct, err := readContingencyTable(spec, host)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}
	rowCount, colCount := len(ct.observed), len(ct.colTotals)
	n := ct.grand
	resid := make([][]types.MatrixCell, rowCount)
	expected := make([][]types.MatrixCell, rowCount)
	pv := make([][]types.MatrixCell, rowCount)
	var (
		minV, maxV          float64
		seen                int
		lowCells, zeroCells int
	)
	expectedMin := math.Inf(1)
	for i := 0; i < rowCount; i++ {
		resid[i] = make([]types.MatrixCell, colCount)
		expected[i] = make([]types.MatrixCell, colCount)
		pv[i] = make([]types.MatrixCell, colCount)
		rowShare := 1 - ct.rowTotals[i]/n
		for j := 0; j < colCount; j++ {
			e := ct.rowTotals[i] * ct.colTotals[j] / n
			expected[i][j] = types.MatrixCell{Value: e, Present: true}
			if e < expectedMin {
				expectedMin = e
			}
			if e < 5 {
				lowCells++
			}
			if e <= 0 {
				zeroCells++
			}
			z, p := math.NaN(), math.NaN()
			if v := e * rowShare * (1 - ct.colTotals[j]/n); e > 0 && v > 0 {
				z = (ct.observed[i][j] - e) / math.Sqrt(v)
				p = normalTwoSidedP(z)
			}
			resid[i][j] = types.MatrixCell{Value: z, Present: true}
			pv[i][j] = types.MatrixCell{Value: p, Present: true}
			if math.IsNaN(z) {
				continue
			}
			if seen == 0 || z < minV {
				minV = z
			}
			if seen == 0 || z > maxV {
				maxV = z
			}
			seen++
		}
	}

	payload := host.Payload()
	onHost := func(cells [][]types.MatrixCell, label string) *types.MatrixPayload {
		return &types.MatrixPayload{
			RowHeader:        payload.RowHeader,
			ColumnHeader:     payload.ColumnHeader,
			RowKeys:          append([]types.AxisKey(nil), payload.RowKeys...),
			ColumnKeys:       append([]types.AxisKey(nil), payload.ColumnKeys...),
			Cells:            cells,
			CellLabel:        label,
			NormalizeApplied: types.CrosstabNormalizeNone,
		}
	}
	name := overlayLayerName(spec)
	layer := types.OverlayLayer{
		Name:  name,
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: onHost(resid, name),
			Parts: map[string]*types.OverlayPayload{
				StdResidualPartExpected: {Shape: types.OverlayShapeMatrix, Matrix: onHost(expected, StdResidualPartExpected)},
				StdResidualPartP:        {Shape: types.OverlayShapeMatrix, Matrix: onHost(pv, StdResidualPartP)},
			},
		},
	}
	baseline := 0.0
	summary := &types.OverlaySummary{Baseline: &baseline}
	ct.table.stamp(summary)
	count := seen
	summary.Count = &count
	if seen > 0 {
		mn, mx := minV, maxV
		summary.Min = &mn
		summary.Max = &mx
	}
	layer.Summary = summary

	var warnings []types.OverlayWarning
	if lowCells > 0 {
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_EXPECTED_LOW),
			Message: "overlay " + string(spec.Kind) +
				" normal approximation may be unreliable: cells with expected count < 5 detected (a zero expected count leaves the residual undefined)",
			Details: map[string]any{
				"kind":                string(spec.Kind),
				"low_expected_cells":  lowCells,
				"zero_expected_cells": zeroCells,
				"expected_min":        expectedMin,
			},
		})
	}
	return layer, warnings, nil
}
