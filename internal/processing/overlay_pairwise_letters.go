package processing

import (
	"math"
	"slices"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// overlay_pairwise_letters.go is the significance-letters presentation of
// the OVERLAY_PAIRWISE_* family (U47 #327). params.letters adds the
// `letters` part beside the RAW p-value primary, which is never touched:
// on the HOST coordinates, one types.PairwiseLetterCell per cell whose
// pair-axis index sits in a comparison group — the index's letter (A, B,
// … restarting per group), the group, the letters it is HIGHER than (the
// sign of each scored pair's test statistic, pwOutcome.dir) and the subset
// it is significantly ABOVE.
//
// A pair is significant on the layer's adjusted figures when a
// multiplicity correction ran (OverlayPayload.PAdjusted present): at
// significant_adjusted, or p_adjusted < params.letters.alpha when one is
// set (echoed as Summary.Parameters["letters_alpha"]); otherwise raw p <
// alpha (default types.DefaultMultiplicityAlpha). Because the correction
// runs after the layer is built (internal/service's multiplicity fold,
// which may pool several layers), Above is recomputed from the layer
// alone — RefreshPairwiseLetters — once here on the raw p and again by
// the fold after it writes the adjusted twins.

// The part name and summary key of the pairwise letters presentation.
const (
	PairwiseLettersPart     = "letters"
	PairwiseLettersAlphaKey = "letters_alpha"

	pairwiseLettersLabel = "letters"
)

// pairwiseLegs is one pair's two pair-axis indexes (i < j in pair order).
type pairwiseLegs struct{ i, j int }

// pairsFromGroups rebuilds the pair list from the comparison groups —
// every a < b within each group, group by group: buildPairwisePairs'
// order, so position p here is position p on the PAIR axis.
func pairsFromGroups(groups [][]int) []pairwiseLegs {
	var out []pairwiseLegs
	for _, m := range groups {
		for a := 0; a < len(m); a++ {
			for b := a + 1; b < len(m); b++ {
				out = append(out, pairwiseLegs{m[a], m[b]})
			}
		}
	}
	return out
}

// attachPairwiseLetters writes the `letters` part on host's coordinates:
// every index of every comparison group lettered, Higher read off the
// pair-cell directions (dirGrid, on the primary's grid), then Above
// computed off the raw p-values. groups is buildPairwisePairs' pair-order
// group list on the (folded) pair axis.
func attachPairwiseLetters(layer *types.OverlayLayer, params types.PairwiseOverlayParams, host *types.MatrixPayload, dirGrid [][]types.MatrixCell, groups [][]int) error {
	rowScope := layer.Scope == types.OverlayScopeRow
	rows, cols := len(host.RowKeys), len(host.ColumnKeys)
	lm := &types.MatrixPayload{
		RowHeader:    host.RowHeader,
		ColumnHeader: host.ColumnHeader,
		RowKeys:      copyAxisKeys(host.RowKeys),
		ColumnKeys:   copyAxisKeys(host.ColumnKeys),
		CellLabel:    pairwiseLettersLabel,
		Cells:        make([][]types.MatrixCell, rows),
	}
	for r := range lm.Cells {
		lm.Cells[r] = make([]types.MatrixCell, cols)
	}
	opp := cols
	if !rowScope {
		opp = rows
	}
	letter := map[int]string{}
	higher := map[int][][]string{}
	for _, members := range groups {
		for k, idx := range members {
			letter[idx] = PairwiseLetterName(k)
			higher[idx] = make([][]string, opp)
		}
	}
	pairs := pairsFromGroups(groups)
	for p, pr := range pairs {
		for o := 0; o < opp; o++ {
			r, c := pairwiseLegCoord(rowScope, p, o)
			if r >= len(dirGrid) || c >= len(dirGrid[r]) || !dirGrid[r][c].Present {
				continue
			}
			d, _ := dirGrid[r][c].Value.(float64)
			switch {
			case d > 0:
				higher[pr.i][o] = append(higher[pr.i][o], letter[pr.j])
			case d < 0:
				higher[pr.j][o] = append(higher[pr.j][o], letter[pr.i])
			}
		}
	}
	for g, members := range groups {
		for _, idx := range members {
			for o := 0; o < opp; o++ {
				r, c := pairwiseLegCoord(rowScope, idx, o)
				lm.Cells[r][c] = types.MatrixCell{Present: true, Value: types.PairwiseLetterCell{
					Letter: letter[idx], Group: g,
					Higher: append([]string{}, higher[idx][o]...), Above: []string{},
				}}
			}
		}
	}
	if layer.Payload.Parts == nil {
		layer.Payload.Parts = map[string]*types.OverlayPayload{}
	}
	layer.Payload.Parts[PairwiseLettersPart] = &types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: lm}
	if params.Letters != nil && params.Letters.Alpha != nil {
		if layer.Summary == nil {
			layer.Summary = &types.OverlaySummary{}
		}
		if layer.Summary.Parameters == nil {
			layer.Summary.Parameters = map[string]float64{}
		}
		layer.Summary.Parameters[PairwiseLettersAlphaKey] = *params.Letters.Alpha
	}
	return RefreshPairwiseLetters(layer)
}

// PairwiseLetterName is the k-th letter of a group (0-based): A … Z, AA,
// AB, … — the spreadsheet column sequence.
func PairwiseLetterName(k int) string {
	s := ""
	for k >= 0 {
		s = string(rune('A'+k%26)) + s
		k = k/26 - 1
	}
	return s
}

// RefreshPairwiseLetters recomputes every Above of a pairwise layer's
// `letters` part from the layer alone: the letters, groups and Higher
// lists it already carries and the significance of each pair-cell (the
// adjusted figures when a correction ran on the layer, else the raw p —
// see the file comment). A layer without a letters part is left
// untouched. The pairs are rebuilt from the groups (pairsFromGroups); a
// count that disagrees with the primary's PAIR axis is an invariant
// violation (PROCESSING_INTERNAL), never a silent mislabel. The
// multiplicity fold calls it after writing the adjusted twins.
func RefreshPairwiseLetters(layer *types.OverlayLayer) error {
	if layer == nil || !types.IsPairwiseOverlayKind(layer.Kind) {
		return nil
	}
	lp := layer.Payload.Parts[PairwiseLettersPart]
	if lp == nil {
		return nil
	}
	pm := layer.Payload.Matrix
	if lp.Matrix == nil || pm == nil {
		return lettersInternal(layer, "the letters or p-value matrix is missing")
	}
	rowScope := layer.Scope == types.OverlayScopeRow
	lm := lp.Matrix
	nIdx, nOpp := len(lm.ColumnKeys), len(lm.RowKeys)
	nPairs := len(pm.ColumnKeys)
	if rowScope {
		nIdx, nOpp = len(lm.RowKeys), len(lm.ColumnKeys)
		nPairs = len(pm.RowKeys)
	}
	if nOpp == 0 {
		return nil
	}
	cellAt := func(idx, o int) (*types.MatrixCell, types.PairwiseLetterCell, bool) {
		r, c := pairwiseLegCoord(rowScope, idx, o)
		cell := &lm.Cells[r][c]
		v, ok := cell.Value.(types.PairwiseLetterCell)
		return cell, v, ok
	}
	// Each lettered index's letter and group, off its first cell (every
	// cell along a lettered index is present).
	letter := make([]string, nIdx)
	byGroup := map[int][]int{}
	for idx := 0; idx < nIdx; idx++ {
		cell, v, ok := cellAt(idx, 0)
		if !cell.Present {
			continue
		}
		if !ok {
			return lettersInternal(layer, "a letters cell does not hold a PairwiseLetterCell")
		}
		letter[idx] = v.Letter
		byGroup[v.Group] = append(byGroup[v.Group], idx)
	}
	ids := make([]int, 0, len(byGroup))
	for g := range byGroup {
		ids = append(ids, g)
	}
	sort.Ints(ids)
	groups := make([][]int, len(ids))
	for k, g := range ids {
		groups[k] = byGroup[g]
	}
	pairs := pairsFromGroups(groups)
	if len(pairs) != nPairs {
		return lettersInternal(layer, "the comparison groups do not rebuild the PAIR axis")
	}
	sig := pairwiseSignificance(layer)
	above := make([][][]string, nIdx)
	for idx := range above {
		above[idx] = make([][]string, nOpp)
	}
	for p, pr := range pairs {
		for o := 0; o < nOpp; o++ {
			if r, c := pairwiseLegCoord(rowScope, p, o); !sig(r, c) {
				continue
			}
			// The higher leg, off its Higher list (the kernel's sign).
			if _, vi, _ := cellAt(pr.i, o); slices.Contains(vi.Higher, letter[pr.j]) {
				above[pr.i][o] = append(above[pr.i][o], letter[pr.j])
			} else if _, vj, _ := cellAt(pr.j, o); slices.Contains(vj.Higher, letter[pr.i]) {
				above[pr.j][o] = append(above[pr.j][o], letter[pr.i])
			}
		}
	}
	for idx := 0; idx < nIdx; idx++ {
		if letter[idx] == "" {
			continue
		}
		for o := 0; o < nOpp; o++ {
			cell, v, ok := cellAt(idx, o)
			if !ok {
				return lettersInternal(layer, "a letters cell does not hold a PairwiseLetterCell")
			}
			// Losers are appended in pair order — ascending index within
			// the group — so Above comes out in letter order.
			v.Above = append([]string{}, above[idx][o]...)
			cell.Value = v
		}
	}
	return nil
}

// pairwiseSignificance is the per-pair-cell significance test the letters
// read, on the primary's grid.
func pairwiseSignificance(layer *types.OverlayLayer) func(r, c int) bool {
	alpha, explicit := 0.0, false
	if layer.Summary != nil {
		alpha, explicit = layer.Summary.Parameters[PairwiseLettersAlphaKey]
	}
	if !explicit {
		alpha = types.DefaultMultiplicityAlpha
	}
	below := func(mx *types.MatrixPayload, r, c int) bool {
		if mx == nil || r >= len(mx.Cells) || c >= len(mx.Cells[r]) || !mx.Cells[r][c].Present {
			return false
		}
		v, ok := mx.Cells[r][c].Value.(float64)
		return ok && !math.IsNaN(v) && v < alpha
	}
	switch {
	case layer.Payload.PAdjusted != nil && !explicit:
		sa := layer.Payload.SignificantAdjusted
		return func(r, c int) bool {
			if sa == nil || r >= len(sa.Cells) || c >= len(sa.Cells[r]) || !sa.Cells[r][c].Present {
				return false
			}
			b, ok := sa.Cells[r][c].Value.(bool)
			return ok && b
		}
	case layer.Payload.PAdjusted != nil:
		return func(r, c int) bool { return below(layer.Payload.PAdjusted, r, c) }
	}
	return func(r, c int) bool { return below(layer.Payload.Matrix, r, c) }
}

func lettersInternal(layer *types.OverlayLayer, reason string) error {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		"overlay "+string(layer.Kind)+" significance letters: "+reason,
		map[string]any{"kind": string(layer.Kind), "name": layer.Name})
}
