package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// RakePartFactors names the OVERLAY_RAKE part carrying the per-cell
// adjustment factors (fitted / observed).
const RakePartFactors = "factors"

// applyRake is the OVERLAY_RAKE handler: iterative proportional fitting
// (IPF) of the host crosstab to target margins.
//
//	r_i, c_j  = the row / column targets in host order; targets_as
//	            "shares" normalizes each axis's targets by their sum and
//	            scales them to the host grand total N, "counts" takes
//	            them as given (their totals agree — ParseRakeParams)
//	m⁰        = the observed table (absent cells 0)
//	sweep     = every row with a positive total × r_i / row total,
//	            then every column with a positive total × c_j / column
//	            total
//	residual  = max over both axes of |margin − target| / target
//	            (|margin| / Σ r for a zero target)
//
// Sweeps run until residual ≤ params.tolerance or params.max_iter
// sweeps have run. Every fitted cell is m⁰_ij · a_i · b_j, so a zero
// cell (a structural zero) stays zero, and a row or column whose cells
// are all zero cannot reach a positive target: the fit stops short with
// a PULSE_OVERLAY_RAKE_NOT_CONVERGED warning {kind, iterations,
// residual, tolerance} and still ships, converged 0.
//
// Primary MATRIX: the fitted table on the host's own keys. Part
// factors: MATRIX fitted / observed, NaN (null) where the observed cell
// is 0. Summary.Parameters: iterations, residual, converged (1 / 0).
//
// The table: a cell aggregation other than AGG_COUNT / AGG_SUM or a
// negative / non-finite cell is PULSE_OVERLAY_NONNEGATIVE_REQUIRED; a
// target key the host axis lacks or a host key the targets lack is
// PULSE_OVERLAY_RAKE_TARGET_MISMATCH (row axis first); a zero grand
// total is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. The host payload
// is read, never written.
//
// Each rescale is one rounded product per cell and each margin a plain
// sum, so the figures are bit-identical on every architecture.
func applyRake(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return applyRakeAt(spec, host, -1)
}

// applyRakeAt is applyRake for the spec at index of Request.Overlays
// (-1 unknown): the MATRIX dispatcher passes it so the params, cell and
// target refusals carry the same details — index included — as
// predict's.
func applyRakeAt(spec *types.OverlaySpec, host *CrosstabHostView, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	params, cerr := overlayhost.ParseRakeParams(spec, index)
	if cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	if cerr := overlayhost.NonnegativeCellRefusal(spec.Kind, host.cellType, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	payload := host.Payload()
	nr, nc := host.RowCount(), host.ColumnCount()

	rowRaw, cerr := rakeAxisTargets(spec.Kind, overlayhost.RakeAxisRow, payload.RowKeys[:nr], params.RowTargets, index)
	if cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	colRaw, cerr := rakeAxisTargets(spec.Kind, overlayhost.RakeAxisColumn, payload.ColumnKeys[:nc], params.ColTargets, index)
	if cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}

	// The observed table.
	obs := make([]float64, nr*nc)
	var grand float64
	for i := 0; i < nr; i++ {
		for j := 0; j < nc; j++ {
			if i >= len(payload.Cells) || j >= len(payload.Cells[i]) || !payload.Cells[i][j].Present {
				continue // absent cell = 0
			}
			v, ok := host.CellAt(i, j)
			if !ok {
				v = math.NaN()
			}
			if cerr := overlayhost.NonnegativeValueRefusal(spec.Kind, payload.RowKeys[i], payload.ColumnKeys[j], v); cerr != nil {
				return types.OverlayLayer{}, nil, cerr
			}
			obs[i*nc+j] = v
			grand += v
		}
	}
	if !(grand > 0) {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires a non-zero table total",
			map[string]any{"kind": string(spec.Kind), "total": grand})
	}

	rowTargets, colTargets := rowRaw, colRaw
	if params.TargetsAs == overlayhost.RakeTargetsShares {
		rowTargets = rakeScale(rowRaw, overlayhost.RakeTargetSum(params.RowTargets), grand)
		colTargets = rakeScale(colRaw, overlayhost.RakeTargetSum(params.ColTargets), grand)
	}

	fitted, iterations, residual := rakeIPF(obs, nr, nc, rowTargets, colTargets, params.MaxIter, params.Tolerance)
	converged := residual <= params.Tolerance

	var warnings []types.OverlayWarning
	if !converged {
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_RAKE_NOT_CONVERGED),
			Message: "overlay " + string(spec.Kind) + " stopped after " + strconv.Itoa(iterations) +
				" sweeps with a margin still off its target by more than the tolerance; the fitted table is approximate",
			Details: map[string]any{
				"kind":       string(spec.Kind),
				"iterations": iterations,
				"residual":   residual,
				"tolerance":  params.Tolerance,
			},
		})
	}

	factors := make([]float64, nr*nc)
	for k, o := range obs {
		if o > 0 {
			factors[k] = fitted[k] / o
		} else {
			factors[k] = math.NaN()
		}
	}
	table := func(values []float64, label string) *types.MatrixPayload {
		cells := make([][]types.MatrixCell, nr)
		for i := range cells {
			cells[i] = make([]types.MatrixCell, nc)
			for j := range cells[i] {
				cells[i][j] = types.MatrixCell{Value: values[i*nc+j], Present: true}
			}
		}
		return &types.MatrixPayload{
			RowHeader:        payload.RowHeader,
			ColumnHeader:     payload.ColumnHeader,
			RowKeys:          append([]types.AxisKey(nil), payload.RowKeys[:nr]...),
			ColumnKeys:       append([]types.AxisKey(nil), payload.ColumnKeys[:nc]...),
			Cells:            cells,
			CellLabel:        label,
			NormalizeApplied: types.CrosstabNormalizeNone,
		}
	}

	convergedFlag := 0.0
	if converged {
		convergedFlag = 1
	}
	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: table(fitted, "fitted"),
			Parts: map[string]*types.OverlayPayload{
				RakePartFactors: {Shape: types.OverlayShapeMatrix, Matrix: table(factors, RakePartFactors)},
			},
		},
		Summary: &types.OverlaySummary{Parameters: map[string]float64{
			"iterations": float64(iterations),
			"residual":   residual,
			"converged":  convergedFlag,
		}},
	}
	return layer, warnings, nil
}

// rakeAxisTargets aligns one axis's target map to the host keys (host
// order) and refuses a mismatch either way
// (overlayhost.RakeKeyRefusal).
func rakeAxisTargets(kind types.OverlayKind, axis string, keys []types.AxisKey, targets map[string]float64, index int) ([]float64, *errors.CodedError) {
	out := make([]float64, len(keys))
	seen := make(map[string]bool, len(keys))
	var hostOnly, targetOnly []string
	for i, k := range keys {
		name := overlayhost.RakeKeyName(k)
		seen[name] = true
		v, ok := targets[name]
		if !ok {
			hostOnly = append(hostOnly, name)
			continue
		}
		out[i] = v
	}
	for name := range targets {
		if !seen[name] {
			targetOnly = append(targetOnly, name)
		}
	}
	if cerr := overlayhost.RakeKeyRefusal(kind, axis, targetOnly, hostOnly, index); cerr != nil {
		return nil, cerr
	}
	return out, nil
}

// rakeScale turns raw shares (summing to sum) into targets summing to
// total.
func rakeScale(raw []float64, sum, total float64) []float64 {
	out := make([]float64, len(raw))
	for i, v := range raw {
		out[i] = v / sum * total
	}
	return out
}

// rakeIPF runs up to maxIter row-then-column sweeps over a copy of obs
// (nr × nc, row-major) and returns the fitted table, the sweeps run and
// the residual after the last one; it stops early once the residual is
// within tol.
func rakeIPF(obs []float64, nr, nc int, rowTargets, colTargets []float64, maxIter int, tol float64) ([]float64, int, float64) {
	m := append([]float64(nil), obs...)
	var targetTotal float64
	for _, t := range rowTargets {
		targetTotal += t
	}
	iterations := 0
	residual := rakeResidual(m, nr, nc, rowTargets, colTargets, targetTotal)
	for iterations < maxIter {
		iterations++
		for i := 0; i < nr; i++ {
			var s float64
			for j := 0; j < nc; j++ {
				s += m[i*nc+j]
			}
			if s > 0 {
				f := rowTargets[i] / s
				for j := 0; j < nc; j++ {
					m[i*nc+j] *= f
				}
			}
		}
		for j := 0; j < nc; j++ {
			var s float64
			for i := 0; i < nr; i++ {
				s += m[i*nc+j]
			}
			if s > 0 {
				f := colTargets[j] / s
				for i := 0; i < nr; i++ {
					m[i*nc+j] *= f
				}
			}
		}
		residual = rakeResidual(m, nr, nc, rowTargets, colTargets, targetTotal)
		if residual <= tol {
			break
		}
	}
	return m, iterations, residual
}

// rakeResidual is the largest relative margin error over both axes:
// |margin − target| / target, or |margin| / targetTotal for a zero
// target.
func rakeResidual(m []float64, nr, nc int, rowTargets, colTargets []float64, targetTotal float64) float64 {
	rel := func(margin, target float64) float64 {
		if target > 0 {
			return math.Abs(margin-target) / target
		}
		return math.Abs(margin) / targetTotal
	}
	var worst float64
	for i := 0; i < nr; i++ {
		var s float64
		for j := 0; j < nc; j++ {
			s += m[i*nc+j]
		}
		worst = math.Max(worst, rel(s, rowTargets[i]))
	}
	for j := 0; j < nc; j++ {
		var s float64
		for i := 0; i < nr; i++ {
			s += m[i*nc+j]
		}
		worst = math.Max(worst, rel(s, colTargets[j]))
	}
	return worst
}
