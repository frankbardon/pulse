package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Runtime-arm coverage for the Welford-input mode selectors.
//
// The refusal added in E2-S1 is PREDICT-ONLY, by decision. Unlike the
// slab partition gate — where a missing runtime twin would let a WRONG
// NUMBER out — n_source and p_source are genuinely inert on the
// Welford-input kinds, so the runtime answer is correct with or without
// them. A runtime twin would convert a pulse.Process call that succeeds
// today into a hard failure and buy no correctness.
//
// The two tests below pin BOTH halves of that decision, so the
// asymmetry is deliberate and observable rather than an oversight:
// a non-distinct selector runs and is ignored; a distinct-key selector
// still hits E1-S2's cell-aggregator admission, which is retained
// because its three modes ship in this same release and can break no
// existing caller.

// pairwiseWelfordHost is the fixture TestOverlayPairwise_WelchT_Welford
// uses, hoisted so both the baseline and the selector-bearing run read
// the same numbers.
func pairwiseWelfordHost() *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"A"}, {"B"}},
		ColumnKeys:   []types.AxisKey{{"x"}},
		Cells: [][]types.MatrixCell{
			{{Value: 10.0, Present: true}},
			{{Value: 12.0, Present: true}},
		},
	}
	comps := &types.CrosstabComponents{
		CellComponents: [][]map[string]any{
			{{"mean": 10.0, "variance": 4.0, "n": 50}},
			{{"mean": 12.0, "variance": 9.0, "n": 60}},
		},
		// Margin counts deliberately DIFFERENT from the Welford n's.
		// If a selector ever started reaching the math, the p-value
		// would move and the equality assertion below would break.
		RowMarginCounts:    []int{7, 9},
		ColumnMarginCounts: []int{11},
	}
	return NewCrosstabHostViewWithComponents(mx, comps)
}

func pairwiseWelfordP(t *testing.T, kind types.OverlayKind, params types.PairwiseOverlayParams) float64 {
	t.Helper()
	specs := []types.OverlaySpec{{
		Kind:   kind,
		Scope:  types.OverlayScopeRow,
		Params: mustParams(t, params),
	}}
	layers, _, err := ApplyOverlays(specs, pairwiseWelfordHost())
	if err != nil {
		t.Fatalf("%s: ApplyOverlays: %v", kind, err)
	}
	cell := layers[0].Payload.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatalf("%s: pair cell absent", kind)
	}
	v, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("%s: cell value %T, want float64", kind, cell.Value)
	}
	return v
}

// TestOverlayPairwise_WelfordSelectorsInertAtRuntime records the
// predict-only decision: the runtime arm neither refuses a non-distinct
// selector nor lets one move the answer.
func TestOverlayPairwise_WelfordSelectorsInertAtRuntime(t *testing.T) {
	for _, kind := range []types.OverlayKind{
		types.OverlayKindPairwiseWelchT,
		types.OverlayKindPairwiseTwoMeansZ,
	} {
		baseline := pairwiseWelfordP(t, kind, types.PairwiseOverlayParams{})
		for _, params := range []types.PairwiseOverlayParams{
			{NSource: types.PairwiseNSourceCellNUnweighted},
			{NSource: types.PairwiseNSourceCellValueWeight},
			{NSource: types.PairwiseNSourceCellWeightSum},
			{NSource: types.PairwiseNSourceRowMarginN},
			{NSource: types.PairwiseNSourceColumnMarginN},
			{NSource: types.PairwiseNSourceNWithin, NWithinDepth: 0},
			{PSource: types.PairwisePSourceCellValue},
			{PSource: types.PairwisePSourceCellValuePct},
			{NSource: types.PairwiseNSourceRowMarginN, PSource: types.PairwisePSourceCellValue},
		} {
			got := pairwiseWelfordP(t, kind, params)
			if math.Abs(got-baseline) > 0 {
				t.Errorf("%s with %+v: p = %v, want the selector-free %v — the selector must stay inert at runtime",
					kind, params, got, baseline)
			}
		}
	}
}

// TestOverlayPairwise_WelfordDistinctSelectorStillRefusedAtRuntime pins
// the OTHER half. E1-S2's admission gate is keyed on
// types.PairwiseNSourceReadsDistinctKeys and fires for every pairwise
// kind including these two, so a distinct-key selector is a runtime
// error here even though the predict refusal is what a caller should
// see first. Retained deliberately: the three distinct modes are new in
// this release, so refusing them at runtime breaks nobody, and the
// AGG_WELFORD cell carries no distinct-key figure to read.
func TestOverlayPairwise_WelfordDistinctSelectorStillRefusedAtRuntime(t *testing.T) {
	for _, kind := range []types.OverlayKind{
		types.OverlayKindPairwiseWelchT,
		types.OverlayKindPairwiseTwoMeansZ,
	} {
		for _, mode := range []string{
			types.PairwiseNSourceNWithinDistinct,
			types.PairwiseNSourceRowMarginDistinct,
			types.PairwiseNSourceColumnMarginDistinct,
		} {
			specs := []types.OverlaySpec{{
				Kind:   kind,
				Scope:  types.OverlayScopeRow,
				Params: mustParams(t, types.PairwiseOverlayParams{NSource: mode}),
			}}
			_, _, err := ApplyOverlays(specs, pairwiseWelfordHost())
			if err == nil {
				t.Fatalf("%s n_source=%s: expected the distinct-key admission refusal, got nil", kind, mode)
			}
			if !pairwiseErrHasCode(err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Errorf("%s n_source=%s: error %v does not carry PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", kind, mode, err)
			}
		}
	}
}
