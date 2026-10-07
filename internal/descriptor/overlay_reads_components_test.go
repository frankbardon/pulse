package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// OverlayReadsHostComponents is the rule a `return` skip obeys (the
// service's compute-plan veto): the pairwise family reads the host's
// components under every basis; every ScalesByHostFloor kind and
// OVERLAY_FISHER_EXACT_CELL read the weighted floor only over a weighted
// cell (frequency: the stamped sum_weights; probability: the Kish n_eff
// too), so an unweighted host skips them; a payload-only kind never
// reads them. validateOverlayHiddenFloor's refusal set (a floor kind
// over a probability cell) stays inside the flagged set.
func TestOverlayReadsHostComponents(t *testing.T) {
	weighted := []weighting.Basis{weighting.Frequency, weighting.Probability}
	for _, kind := range types.AllOverlayKinds() {
		floor := weighting.ScalesByHostFloor(kind) || kind == types.OverlayKindFisherExactCell
		if types.IsPairwiseOverlayKind(kind) {
			for _, b := range append([]weighting.Basis{weighting.Unweighted}, weighted...) {
				if !OverlayReadsHostComponents(kind, b) {
					t.Errorf("%s (basis %v): pairwise kind reads per-cell floors, yet not flagged", kind, b)
				}
			}
			continue
		}
		for _, b := range weighted {
			if floor && !OverlayReadsHostComponents(kind, b) {
				t.Errorf("%s (basis %v): reads the weighted floor, yet not flagged", kind, b)
			}
		}
		if OverlayReadsHostComponents(kind, weighting.Unweighted) {
			t.Errorf("%s: flagged over an unweighted cell, where it reads the MatrixPayload only", kind)
		}
		if !floor && OverlayReadsHostComponents(kind, weighting.Probability) {
			t.Errorf("%s: payload-only kind flagged", kind)
		}
	}
	if !OverlayReadsHostComponents(types.OverlayKindFisherExactCell, weighting.Frequency) {
		t.Error("OVERLAY_FISHER_EXACT_CELL stamps the frequency floor's sum_weights, yet not flagged")
	}
}

// CrosstabCellWeightBasis resolves the cell as the runtime stamps it:
// the request weight, the cell's own slot weight, the instance default
// (alone) — and `weight: null` on the cell opts out of all three.
func TestCrosstabCellWeightBasis(t *testing.T) {
	mk := func() *types.Request {
		return &types.Request{Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
		}}
	}
	freq := &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	prob := &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	if got := CrosstabCellWeightBasis(mk(), nil, nil); got != weighting.Unweighted {
		t.Errorf("no weight: %v; want unweighted", got)
	}
	if got := CrosstabCellWeightBasis(nil, prob, nil); got != weighting.Unweighted {
		t.Errorf("nil request: %v; want unweighted", got)
	}
	r := mk()
	r.Weight = freq
	if got := CrosstabCellWeightBasis(r, nil, nil); got != weighting.Frequency {
		t.Errorf("request frequency weight: %v; want frequency", got)
	}
	if got := CrosstabCellWeightBasis(mk(), prob, nil); got != weighting.Probability {
		t.Errorf("default probability weight alone: %v; want probability", got)
	}
	r = mk()
	r.Crosstab.Cell.Weight = types.SlotWeightOf(*freq)
	if got := CrosstabCellWeightBasis(r, nil, nil); got != weighting.Frequency {
		t.Errorf("cell slot weight: %v; want frequency", got)
	}
	r = mk()
	if err := json.Unmarshal([]byte(`{"type":"AGG_COUNT","field":"x","weight":null}`), r.Crosstab.Cell); err != nil {
		t.Fatal(err)
	}
	if got := CrosstabCellWeightBasis(r, prob, nil); got != weighting.Unweighted {
		t.Errorf("cell weight null under a default: %v; want unweighted", got)
	}
}
