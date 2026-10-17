package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// symmetryLowExpected is the expected count under symmetry below which
// a pair's χ² contribution is flagged: the canonical χ² rule of thumb
// (PULSE_OVERLAY_EXPECTED_LOW), here (n_ij + n_ji) / 2 < 5.
const symmetryLowExpected = 5

// applySymmetry is the OVERLAY_SYMMETRY handler: McNemar's (2 × 2) /
// Bowker's (k × k) test of symmetry on the host crosstab read as a
// from-state (row) × to-state (column) table of PAIRED observations.
//
//	states = the row keys in row order, then the column-only keys in
//	         column order; the columns align to them BY KEY
//	         (overlayhost.MarkovStateName), never by position. A state
//	         on one axis only is a row (or column) of zeros: nobody
//	         started (or ended) there, which the test reads as observed.
//	pair   = (i, j), i < j, over the states in that order
//	s_ij   = n_ij + n_ji     (a pair with s_ij = 0 is dropped)
//	d_ij   = n_ij − n_ji
//	χ²     = Σ d_ij² / s_ij  (k > 2, or correct = false: Bowker / plain
//	         McNemar)
//	χ²     = max(0, |d_12| − 1)² / s_12  (k = 2, correct = true and
//	         d_12 ≠ 0: Edwards' continuity correction, R's default)
//	df     = the number of pairs kept (k(k−1)/2 when none is dropped)
//	p      = P(χ²_df ≥ χ²)   (chiSquareSurvival)
//
// R's mcnemar.test(x, correct) is this exactly, with two documented
// differences: R keeps a dropped pair (0/0, so its statistic is NaN)
// where Pulse drops it and lowers df; and R does not clamp |d| − 1 at 0,
// which only a non-integer table (a Kish-scaled or AGG_SUM host) can
// reach. With no pair kept the statistic, df-based p and Scalar are NaN
// (null on the wire) and df is 0: nobody changed state, so there is
// nothing to test.
//
// The spec: a set Ref is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE and a
// scope other than matrix PULSE_OVERLAY_SCOPE_UNSUPPORTED
// (overlayhost.CrosstabSpecRefusal, raised by the MATRIX dispatcher
// before this handler and by predict alike). The table: absent
// cells are 0 (an undefined NaN cell is absent too — CellAt); a cell
// aggregation other than AGG_COUNT / AGG_SUM or a
// negative / non-finite cell is PULSE_OVERLAY_NONNEGATIVE_REQUIRED; a
// different grouper count per axis is PULSE_OVERLAY_MATRIX_NOT_SQUARE;
// fewer than 2 states or a zero table total is
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Under weight kind
// probability the Σw table is scaled to its Kish n_eff first (the
// OVERLAY_CHISQ_MATRIX rule, weightedLegTally.scale); frequency and
// unweighted hosts read the table as is.
//
// Any kept or dropped pair whose expected count under symmetry,
// s_ij / 2, is below 5 raises ONE PULSE_OVERLAY_EXPECTED_LOW layer
// warning {kind, low_expected_pairs, zero_pairs, expected_min}: the χ²
// approximation weakens there.
//
// Output: SCALAR payload = χ²; Summary.Statistic / PValue and
// Parameters df, states, discordant (Σ s_ij over every pair: the units
// that changed state), pairs_dropped and continuity_corrected (1 when
// the correction moved the statistic, else 0), plus the weighted floor
// (sum_weights, n_eff). Every product is FMA-free (an explicit float64
// conversion around it), so the figures are bit-identical on every
// architecture for the same table.
func applySymmetry(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return applySymmetryAt(spec, host, -1)
}

// applySymmetryAt is applySymmetry for the spec at index of
// Request.Overlays (-1 unknown): the MATRIX dispatcher passes it so the
// params, cell and arity refusals carry the same details — index
// included — as predict's.
func applySymmetryAt(spec *types.OverlaySpec, host *CrosstabHostView, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	params, cerr := overlayhost.ParseSymmetryParams(spec, index)
	if cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	if cerr := overlayhost.NonnegativeCellRefusal(spec.Kind, host.cellType, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	payload := host.Payload()
	if cerr := overlayhost.MarkovArityRefusal(spec.Kind, overlayhost.HeaderGrouperCount(payload.RowHeader), overlayhost.HeaderGrouperCount(payload.ColumnHeader), index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}

	// The state set: row keys, then column-only keys; stateOfRow /
	// stateOfCol map a host coordinate to its state.
	hostRows, hostCols := host.RowCount(), host.ColumnCount()
	stateIndex := make(map[string]int, hostRows+hostCols)
	stateOfRow := make([]int, hostRows)
	for i := 0; i < hostRows; i++ {
		name := overlayhost.MarkovStateName(payload.RowKeys[i])
		if s, ok := stateIndex[name]; ok {
			stateOfRow[i] = s
			continue
		}
		stateIndex[name] = len(stateIndex)
		stateOfRow[i] = stateIndex[name]
	}
	stateOfCol := make([]int, hostCols)
	for j := 0; j < hostCols; j++ {
		name := overlayhost.MarkovStateName(payload.ColumnKeys[j])
		if s, ok := stateIndex[name]; ok {
			stateOfCol[j] = s
			continue
		}
		stateIndex[name] = len(stateIndex)
		stateOfCol[j] = stateIndex[name]
	}
	k := len(stateIndex)

	// The k × k table (absent cell = 0) and its weighted floor.
	observed := make([][]float64, k)
	for s := range observed {
		observed[s] = make([]float64, k)
	}
	rowTotals := make([]float64, k)
	colTotals := make([]float64, k)
	var grand float64
	basis := host.WeightBasis()
	var table weightedLegTally
	for i := 0; i < hostRows; i++ {
		for j := 0; j < hostCols; j++ {
			if i >= len(payload.Cells) || j >= len(payload.Cells[i]) || !payload.Cells[i][j].Present {
				continue // absent cell = 0
			}
			v, ok := host.CellAt(i, j)
			if !ok {
				continue // an undefined (NaN) cell is absent (E4-S6)
			}
			if cerr := overlayhost.NonnegativeValueRefusal(spec.Kind, payload.RowKeys[i], payload.ColumnKeys[j], v); cerr != nil {
				return types.OverlayLayer{}, nil, cerr
			}
			if l, ok := host.CellFloor(i, j, basis); ok {
				table.add(l)
			}
			r, c := stateOfRow[i], stateOfCol[j]
			observed[r][c] += v
			rowTotals[r] += v
			colTotals[c] += v
			grand += v
		}
	}
	if k < 2 || !(grand > 0) {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires at least 2 states and a non-zero table total",
			map[string]any{"kind": string(spec.Kind), "states": k, "total": grand})
	}
	if basis == weighting.Probability {
		scaleContingency(table.scale(), observed, rowTotals, colTotals, &grand)
	}

	var (
		stat, discordant          float64
		kept, dropped, lowPairs   int
		corrected                 bool
		expectedMin               = math.Inf(1)
		correctionApplies         = params.Correct && k == 2
		statistic, pValue, scalar float64
	)
	for i := 0; i < k; i++ {
		for j := i + 1; j < k; j++ {
			a, b := observed[i][j], observed[j][i]
			s := a + b
			discordant += s
			e := s / 2
			if e < expectedMin {
				expectedMin = e
			}
			if e < symmetryLowExpected {
				lowPairs++
			}
			if !(s > 0) {
				dropped++
				continue
			}
			kept++
			d := math.Abs(a - b)
			if correctionApplies && d != 0 {
				d = math.Max(0, d-1)
				corrected = true
			}
			stat += float64(d*d) / s
		}
	}
	df := float64(kept)
	if kept == 0 {
		statistic, pValue = math.NaN(), math.NaN()
	} else {
		statistic, pValue = stat, chiSquareSurvival(stat, df)
	}
	scalar = statistic

	var warnings []types.OverlayWarning
	if lowPairs > 0 {
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_EXPECTED_LOW),
			Message: "overlay " + string(spec.Kind) +
				" χ² approximation may be unreliable: state pairs with expected count < 5 under symmetry ((n_ij + n_ji) / 2) detected",
			Details: map[string]any{
				"kind":               string(spec.Kind),
				"low_expected_pairs": lowPairs,
				"zero_pairs":         dropped,
				"expected_min":       expectedMin,
			},
		})
	}

	correctedFlag := 0.0
	if corrected {
		correctedFlag = 1
	}
	summary := &types.OverlaySummary{
		Statistic: &statistic,
		PValue:    &pValue,
		Parameters: map[string]float64{
			"df":                   df,
			"states":               float64(k),
			"discordant":           discordant,
			"pairs_dropped":        float64(dropped),
			"continuity_corrected": correctedFlag,
		},
	}
	table.stamp(summary)

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeScalar,
			Scalar: &scalar,
		},
		Summary: summary,
	}
	return layer, warnings, nil
}
