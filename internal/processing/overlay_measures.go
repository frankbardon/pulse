package processing

import (
	"github.com/frankbardon/pulse/errors"
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
// raised for the first measure that trips it. The layer shares one
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
	for m, sub := range host.measures {
		g, err := pairwiseGate(spec, sub, shape)
		if err != nil {
			return types.OverlayLayer{}, nil, err
		}
		pairs, err := buildPairwisePairs(spec, sub, g.params, rowScope)
		if err != nil {
			return types.OverlayLayer{}, nil, err
		}
		oppIdx := func(o int) int { return o }
		if !rowScope {
			oppIdx = func(o int) int { return o*M + m }
		}
		pvals := acc.evaluate(sub, g, kernel, shape, pairs, rowScope, m, oppIdx)

		if rowScope {
			for p, pr := range pairs {
				mx.RowKeys = append(mx.RowKeys, types.AxisKey{
					stringifyOverlayAxisKey(host.rowKey(pr.i*M + m)),
					stringifyOverlayAxisKey(host.rowKey(pr.j*M + m)),
				})
				mx.Cells = append(mx.Cells, pvals[p])
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
			for p := range pairs {
				row[p] = pvals[p][r]
				if row[p].Present {
					present++
				}
			}
			mx.Cells[r*M+m] = row
		}
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
	warns := acc.tally.warnings(spec)
	layer.Warnings = warns
	return layer, warns, nil
}
