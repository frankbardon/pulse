package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// Per-entry and layer Summary.Parameters keys OVERLAY_RETENTION writes.
const (
	retentionParamSwitch    = "switch"
	retentionParamChurn     = "churn"
	retentionParamFromTotal = "from_total"
	retentionParamStay      = "stay"
	retentionParamStates    = "states"
	retentionParamTotal     = "total"
)

// applyRetention is the OVERLAY_RETENTION handler: the host crosstab
// read as a from-state (row) × to-state (column) table, reported per
// from-state as the shares of its units that stayed, switched and (with
// params.absorbing) churned.
//
//	R_i     = Σ_j n_ij over every host column (row i's total)
//	stay_i  = n_ii / R_i  — the column whose key names row i's state,
//	          aligned BY KEY (overlayhost.MarkovStateName), never by
//	          position; no such column ⇒ 0
//	churn_i = Σ_{j ∈ absorbing} n_ij / R_i  (only with params.absorbing)
//	switch_i = Σ_{every other column} n_ij / R_i
//
// so stay + switch + churn = 1 (up to rounding: each is its own sum
// over R_i, never 1 minus the others). Without params.absorbing there
// is no churn split and switch = every move out. A from-state named in
// params.absorbing is not reported — churn is the way INTO it, and its
// own row (a reactivation, a stay in the absorbing state) is not a
// retention rate. A row with R_i = 0 reports null rates (an undefined
// figure, never 0) with one PULSE_OVERLAY_REF_ZERO {kind, axis: row,
// states, reason: zero_row}.
//
// The spec: a set Ref is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE and a
// scope other than row PULSE_OVERLAY_SCOPE_UNSUPPORTED
// (overlayhost.RetentionSpecRefusal, predict's twin). Params:
// overlayhost.ParseRetentionParams; an absorbing name that is neither a
// row nor a column key is PULSE_OVERLAY_PARAM_MISSING
// (overlayhost.MarkovStateRefusal, runtime only — predict reads no
// record). The table: absent cells are 0 (an undefined NaN cell is
// absent too — CellAt, E4-S6); a cell aggregation other than AGG_COUNT /
// AGG_SUM or a negative / non-finite cell is
// PULSE_OVERLAY_NONNEGATIVE_REQUIRED; a different grouper count per axis
// is PULSE_OVERLAY_MATRIX_NOT_SQUARE (no row key could name a column).
//
// Output: SERIES payload, one entry per reported from-state in host row
// order — Summary.Statistic = stay, Summary.Parameters switch,
// from_total (R_i) and, with absorbing, churn. The layer's
// Summary.Parameters pool the same rates over the reported rows (each
// pooled rate = the summed numerators over the summed totals): stay,
// switch, churn (with absorbing), states (the reported entries) and
// total. Every rate is a ratio of host cells, so a weighted host's Σw
// table is read as is under either weight kind: the rates equal the
// unweighted rates of the row-replicated table (scale-invariant; no
// weight class, no Kish scaling). No product is taken, so the figures
// are bit-identical on every architecture for the same table.
func applyRetention(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return applyRetentionAt(spec, host, -1)
}

// applyRetentionAt is applyRetention for the spec at index of
// Request.Overlays (-1 unknown): the MATRIX dispatcher passes it so the
// spec, params, cell, arity and state refusals carry the same details —
// index included — as predict's.
func applyRetentionAt(spec *types.OverlaySpec, host *CrosstabHostView, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	if cerr := overlayhost.RetentionSpecRefusal(spec, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	params, cerr := overlayhost.ParseRetentionParams(spec, index)
	if cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	if cerr := overlayhost.NonnegativeCellRefusal(spec.Kind, host.cellType, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	payload := host.Payload()
	if cerr := overlayhost.MarkovArityRefusal(spec.Kind, len(payload.RowHeader.Fields), len(payload.ColumnHeader.Fields), index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}

	// State names: the rows, then the column-only keys (the state list a
	// refusal reports).
	hostRows, hostCols := host.RowCount(), host.ColumnCount()
	rowName := make([]string, hostRows)
	colName := make([]string, hostCols)
	known := make(map[string]bool, hostRows+hostCols)
	var states []string
	for i := 0; i < hostRows; i++ {
		rowName[i] = overlayhost.MarkovStateName(payload.RowKeys[i])
		if !known[rowName[i]] {
			known[rowName[i]] = true
			states = append(states, rowName[i])
		}
	}
	for j := 0; j < hostCols; j++ {
		colName[j] = overlayhost.MarkovStateName(payload.ColumnKeys[j])
		if !known[colName[j]] {
			known[colName[j]] = true
			states = append(states, colName[j])
		}
	}
	absorbing := make(map[string]bool, len(params.Absorbing))
	var unknown []string
	for _, n := range params.Absorbing {
		if !known[n] {
			unknown = append(unknown, n)
		}
		absorbing[n] = true
	}
	if cerr := overlayhost.MarkovStateRefusal(spec.Kind, "absorbing", unknown, states, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	withChurn := params.Absorbing != nil

	var (
		entries                               = []types.SeriesEntry{}
		zeroKeys                              []types.AxisKey
		sumStay, sumSwitch, sumChurn, sumFrom float64
	)
	for i := 0; i < hostRows; i++ {
		var stay, churn, moved, total float64
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
			total += v
			switch {
			case colName[j] == rowName[i]:
				stay += v
			case absorbing[colName[j]]:
				churn += v
			default:
				moved += v
			}
		}
		if absorbing[rowName[i]] {
			continue // churn leads into it; its own row is not a retention rate
		}
		stayRate, switchRate, churnRate := math.NaN(), math.NaN(), math.NaN()
		if total > 0 {
			stayRate, switchRate, churnRate = stay/total, moved/total, churn/total
		} else {
			zeroKeys = append(zeroKeys, payload.RowKeys[i])
		}
		sumStay += stay
		sumSwitch += moved
		sumChurn += churn
		sumFrom += total
		entryParams := map[string]float64{
			retentionParamSwitch:    switchRate,
			retentionParamFromTotal: total,
		}
		if withChurn {
			entryParams[retentionParamChurn] = churnRate
		}
		entries = append(entries, types.SeriesEntry{
			Key:     payload.RowKeys[i],
			Summary: types.OverlaySummary{Statistic: &stayRate, Parameters: entryParams},
		})
	}

	var warnings []types.OverlayWarning
	if len(zeroKeys) > 0 {
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " found from-states with a zero row total: " +
				"their stay, switch and churn rates are undefined (null)",
			Details: map[string]any{
				"kind":   string(spec.Kind),
				"axis":   "row",
				"states": zeroKeys,
				"reason": "zero_row",
			},
		})
	}

	pooledStay, pooledSwitch, pooledChurn := math.NaN(), math.NaN(), math.NaN()
	if sumFrom > 0 {
		pooledStay, pooledSwitch, pooledChurn = sumStay/sumFrom, sumSwitch/sumFrom, sumChurn/sumFrom
	}
	layerParams := map[string]float64{
		retentionParamStay:   pooledStay,
		retentionParamSwitch: pooledSwitch,
		retentionParamStates: float64(len(entries)),
		retentionParamTotal:  sumFrom,
	}
	if withChurn {
		layerParams[retentionParamChurn] = pooledChurn
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeSeries,
			Series: &types.SeriesPayload{Entries: entries},
		},
		Summary: &types.OverlaySummary{Parameters: layerParams},
	}
	return layer, warnings, nil
}
