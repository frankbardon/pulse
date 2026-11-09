package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// overlay_measures.go runs a pairwise overlay over a multi-measure
// crosstab host (types.CrosstabSpec.Cells, U47 #326).
//
// Each measure is a single-measure host (CrosstabHostView.measures)
// holding exactly the payload, components and normalise-denominator base
// floors the single-measure crosstab of that measure produces. The pair
// loop runs per measure against it — so a pair never crosses measures,
// and each measure's n legs read its own margins and bases — and the
// results are laid over the MEASURE-folded payload:
//
//   - COLUMN scope pairs columns; the column keys are shared by every
//     measure, so the pair axis is one list and the opposite (row) axis is
//     the folded rows, measure m of spec row r at r*M+m;
//   - ROW scope pairs rows within one measure; the pair axis is each
//     measure's pairs in declaration order, every label the FOLDED row
//     key (row key + measure label) so pairs of different measures stay
//     distinguishable.
//
// Every per-measure refusal is the single-measure refusal (pairwiseGate),
// raised for the first measure that trips it — except a moment kind's
// measure whose cells carry no Welford triple (U66 #476), refused ALONE:
// its folded rows (COLUMN: r*M+m; ROW: its pairs' rows) are null cells,
// a PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE warning names it
// (overlayhost.PairwiseMeasureRefusedWarning, ahead of the skip
// warnings), and only when every measure is refused does the layer
// refuse, with the first measure's error. The layer shares one
// accumulator, so its summary and warnings describe the whole layer; a
// COLUMN-scope skip reports its folded row index.
func runPairwiseOverlayMeasures(spec *types.OverlaySpec, host *CrosstabHostView, kernel pwKernel, shape pwShape) (types.OverlayLayer, []types.OverlayWarning, error) {
	rowScope := spec.Scope == types.OverlayScopeRow
	M := len(host.measures)
	if M == 0 {
		return types.OverlayLayer{}, nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"pairwise overlay: multi-measure host carries no measure")
	}
	acc := newPairwiseAccumulator()

	pairHeader := types.AxisHeader{Fields: []string{"pair_a", "pair_b"}, Types: []string{"PAIR", "PAIR"}}
	var mx types.MatrixPayload
	mx.CellLabel = "p_value"
	present := 0

	if rowScope {
		mx.RowHeader = pairHeader
		mx.RowKeys = []types.AxisKey{}
		mx.Cells = [][]types.MatrixCell{}
		mx.ColumnHeader = host.Payload().ColumnHeader
		mx.ColumnKeys = copyAxisKeys(host.Payload().ColumnKeys)
	} else {
		mx.RowHeader = host.Payload().RowHeader
		mx.RowKeys = copyAxisKeys(host.Payload().RowKeys)
		mx.ColumnHeader = pairHeader
		mx.Cells = make([][]types.MatrixCell, host.RowCount())
	}

	var colPairs []pairwisePair
	// The pair-cell directions (on mx's grid) and the comparison groups
	// on the folded pair axis: ROW scope pairs within each measure, so a
	// measure's groups map sub-host row i to folded row i*M+m; COLUMN
	// scope shares measure 0's column groups.
	dirCells := [][]types.MatrixCell{}
	if !rowScope {
		dirCells = make([][]types.MatrixCell, host.RowCount())
	}
	var groups [][]int
	var letters *types.PairwiseLetters
	// A measure whose cells carry no Welford triple is refused ALONE on
	// a moment kind (U66 #476, overlayhost.PairwiseMeasureRefusable):
	// its pairs are laid as null cells (absent p, absent direction — so
	// letters and the multiplicity fold skip them) and it warns; the
	// first refusal is the layer's when every measure is refused.
	var firstRefusal error
	var refusals []types.OverlayWarning
	for m, sub := range host.measures {
		g, err := pairwiseGate(spec, sub, shape)
		refused := false
		if err != nil {
			if !pairwiseMeasureRefused(spec, sub, shape, err) {
				return types.OverlayLayer{}, nil, err
			}
			if firstRefusal == nil {
				firstRefusal = err
			}
			// The gate passed the Params decode (layer-wide) before it
			// judged the measure's cells, so this decode succeeds.
			params, derr := types.DecodePairwiseParams(spec.Params)
			if derr != nil {
				return types.OverlayLayer{}, nil, err
			}
			g = pairwiseGateResult{params: params}
			msg, details := overlayhost.PairwiseMeasureRefusedWarning(spec.Kind, m, types.AggregationLabelOf(sub.cell), sub.cellType)
			refusals = append(refusals, types.OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE), Message: msg, Details: details})
			refused = true
		}
		pairs, subGroups, err := buildPairwisePairs(spec, sub, g.params, rowScope)
		if err != nil {
			return types.OverlayLayer{}, nil, err
		}
		letters = g.params.Letters
		if rowScope {
			for _, sg := range subGroups {
				fg := make([]int, len(sg))
				for k, i := range sg {
					fg[k] = i*M + m
				}
				groups = append(groups, fg)
			}
		} else if m == 0 {
			groups = subGroups
		}
		oppIdx := func(o int) int { return o }
		if !rowScope {
			oppIdx = func(o int) int { return o*M + m }
		}
		var pvals, dirs [][]types.MatrixCell
		if refused {
			pvals, dirs = nullPairCells(sub, pairs, rowScope), nullPairCells(sub, pairs, rowScope)
		} else {
			pvals, dirs = acc.evaluate(sub, g, kernel, shape, pairs, rowScope, m, oppIdx)
		}

		if rowScope {
			for p, pr := range pairs {
				mx.RowKeys = append(mx.RowKeys, types.AxisKey{
					stringifyOverlayAxisKey(host.rowKey(pr.i*M + m)),
					stringifyOverlayAxisKey(host.rowKey(pr.j*M + m)),
				})
				mx.Cells = append(mx.Cells, pvals[p])
				dirCells = append(dirCells, dirs[p])
				for _, cell := range pvals[p] {
					if cell.Present {
						present++
					}
				}
			}
			continue
		}
		// COLUMN scope: every measure pairs the same shared column keys.
		if m == 0 {
			colPairs = pairs
		} else if len(pairs) != len(colPairs) {
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"pairwise overlay: measure column pairs diverged", map[string]any{"measure": m})
		}
		for r := 0; r < sub.RowCount(); r++ {
			row := make([]types.MatrixCell, len(pairs))
			drow := make([]types.MatrixCell, len(pairs))
			for p := range pairs {
				row[p] = pvals[p][r]
				drow[p] = dirs[p][r]
				if row[p].Present {
					present++
				}
			}
			mx.Cells[r*M+m] = row
			dirCells[r*M+m] = drow
		}
	}
	if len(refusals) == M {
		return types.OverlayLayer{}, nil, firstRefusal
	}
	if !rowScope {
		mx.ColumnKeys = make([]types.AxisKey, len(colPairs))
		for p, pr := range colPairs {
			mx.ColumnKeys[p] = types.AxisKey{pr.labelI, pr.labelJ}
		}
	}

	count := present
	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: &mx,
		},
		Summary: &types.OverlaySummary{Count: &count},
	}
	acc.legs.stamp(layer.Summary)
	if letters != nil {
		if err := attachPairwiseLetters(&layer, types.PairwiseOverlayParams{Letters: letters}, host.Payload(), dirCells, groups); err != nil {
			return types.OverlayLayer{}, nil, err
		}
	}
	warns := append(refusals, acc.tally.warnings(spec)...)
	layer.Warnings = warns
	return layer, warns, nil
}

// pairwiseMeasureRefused reports whether err — pairwiseGate's refusal of
// one measure sub of a multi-measure host — is the per-measure Welford
// refusal a moment kind lays as null rows (overlayhost.PairwiseMeasureRefusable)
// rather than a layer refusal: the gate's Welford-cell refusal on a
// measure whose cells carry no triple. Every earlier gate refusal is
// layer-wide (Params, letters, components) and keeps refusing the layer.
func pairwiseMeasureRefused(spec *types.OverlaySpec, sub *CrosstabHostView, shape pwShape, err error) bool {
	return overlayhost.PairwiseMeasureRefusable(spec.Kind) && shape == pwShapeWelford &&
		errors.HasCode(err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) &&
		sub.HasComponents() && !sub.HasWelfordCells()
}

// nullPairCells is evaluate's pair-major grid for a refused measure:
// every (pair, opposite index) cell absent — JSON null.
func nullPairCells(sub *CrosstabHostView, pairs []pairwisePair, rowScope bool) [][]types.MatrixCell {
	opp := sub.ColumnCount()
	if !rowScope {
		opp = sub.RowCount()
	}
	out := make([][]types.MatrixCell, len(pairs))
	for p := range out {
		out[p] = make([]types.MatrixCell, opp)
	}
	return out
}
