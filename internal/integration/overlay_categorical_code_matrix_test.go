package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E4-S17. The categorical-code rule (E4-S14) reaches the two edges it
// left open:
//
//   - a MATRIX overlay over a crosstab whose CELL aggregation is a code
//     (AGG_MODE over a categorical / packed_bool field writes the modal
//     dictionary index / truth value into every cell and margin) — every
//     kind that reads cell values is refused; the moment pairwise kinds,
//     which read only the cell's component moments, are not judged;
//   - a chain stage >= 1 whose AGG_MODE reads a column an upstream
//     code-emitting aggregation wrote (f64 in the synthesised schema, a
//     code all the same) — code-ness propagates through every stage, for
//     the whole-chain overlays and a stage's own overlays alike.
//
// Every parity case runs PREDICT FIRST on a fresh request.

// ccMatrixSpec is a valid spec of a MATRIX kind over a 3x2+ crosstab.
func ccMatrixSpec(kind types.OverlayKind) types.OverlaySpec {
	spec := types.OverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeCell}
	switch kind {
	case types.OverlayKindChiSqMatrix:
		spec.Scope = types.OverlayScopeMatrix
	case types.OverlayKindChiSqRow, types.OverlayKindPairwisePropZ, types.OverlayKindPairwiseProbitT,
		types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ, types.OverlayKindPairwiseWeightedTwoMeansZ:
		spec.Scope = types.OverlayScopeRow
	case types.OverlayKindChiSqCol:
		spec.Scope = types.OverlayScopeColumn
	case types.OverlayKindIndexVsMargin, types.OverlayKindDeltaVsMargin, types.OverlayKindZScoreVsMargin, types.OverlayKindShareOfRow:
		spec.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
	case types.OverlayKindShareOfCol:
		spec.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}}
	case types.OverlayKindShareOfTotal:
		spec.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand}}
	case types.OverlayKindFormula:
		spec.Params = json.RawMessage(`{"formula":"cell / margin_grand"}`)
	}
	if kind == types.OverlayKindPairwiseWeightedTwoMeansZ {
		spec.Params = json.RawMessage(`{"n_basis":"kish"}`)
	}
	return spec
}

// ccValueKinds are the MATRIX kinds that read cell values.
var ccValueKinds = []types.OverlayKind{
	types.OverlayKindChiSqCol, types.OverlayKindChiSqMatrix, types.OverlayKindChiSqRow,
	types.OverlayKindDeltaVsMargin, types.OverlayKindFisherExactCell, types.OverlayKindFormula,
	types.OverlayKindIndexVsMargin, types.OverlayKindPairwiseProbitT, types.OverlayKindPairwisePropZ,
	types.OverlayKindShareOfCol, types.OverlayKindShareOfRow, types.OverlayKindShareOfTotal,
	types.OverlayKindZScoreVsMargin,
}

// ccCrosstab: rows region, columns col, cell typ over field, overlays.
func ccCrosstab(col string, typ types.AggregationType, field string, specs ...types.OverlaySpec) *pulse.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "cc.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: col}},
			Cell:    &types.Aggregation{Type: typ, Field: field, Label: "c"},
		},
		Overlays: specs}
}

// TestProcessMatrixOverlay_CategoricalCodeCellRefused: every MATRIX kind
// that reads cell values runs over a count cell, and is refused over a
// code cell — PROCESSING_CONFIG, the E4-S14 Details shape with slot
// "crosstab.cell" — on predict (first) and runtime alike, messages
// equal. AGG_MODE over the numeric x still runs.
func TestProcessMatrixOverlay_CategoricalCodeCellRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, data := ccFixture(t, pulse.Options{})
	for _, kind := range ccValueKinds {
		t.Run(string(kind)+"/count_cell_runs", func(t *testing.T) {
			req := func() *pulse.Request { return ccCrosstab("flag", types.AGG_COUNT, "x", ccMatrixSpec(kind)) }
			if pe := predictCodedError(t, p, data, req()); pe != nil {
				t.Fatalf("Predict = %s %v, want clean", pe.Code, pe.Details)
			}
			out, err := p.Process(ctx, req())
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(out.Overlays) != 1 {
				t.Fatalf("overlays = %s, want one layer", nsJSON(t, out.Overlays))
			}
		})
		t.Run(string(kind)+"/mode_numeric_cell_runs", func(t *testing.T) {
			req := func() *pulse.Request { return ccCrosstab("flag", types.AGG_MODE, "x", ccMatrixSpec(kind)) }
			if pe := predictCodedError(t, p, data, req()); pe != nil && pe.Details["reason"] == "categorical_code" {
				t.Fatalf("Predict = %s %v, want no categorical_code refusal", pe.Code, pe.Details)
			}
			if _, err := p.Process(ctx, req()); err != nil && codeOf(t, err).Details["reason"] == "categorical_code" {
				t.Fatalf("Process: %v, want no categorical_code refusal", err)
			}
		})
		for _, c := range []struct{ col, field, ft string }{{"flag", "color", "categorical_u8"}, {"color", "flag", "packed_bool"}} {
			t.Run(fmt.Sprintf("%s/mode_%s_cell_refused", kind, c.ft), func(t *testing.T) {
				// A count-cell overlay first: only the value-reading
				// spec at index 1 is named.
				req := func() *pulse.Request {
					return ccCrosstab(c.col, types.AGG_MODE, c.field, ccMatrixSpec(types.OverlayKindPairwiseWelchT), ccMatrixSpec(kind))
				}
				wantDetails := map[string]any{"aggregation": "AGG_MODE", "label": "c", "slot": "crosstab.cell",
					"reason": "categorical_code", "field": c.field, "field_type": c.ft}
				pe := predictCodedError(t, p, data, req())
				if pe == nil || pe.Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(pe.Details, wantDetails) {
					t.Fatalf("Predict = %+v, want PROCESSING_CONFIG %v first", pe, wantDetails)
				}
				_, err := p.Process(ctx, req())
				ce := codeOf(t, err)
				if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
					t.Fatalf("Process = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
				}
				if ce.Message != pe.Message {
					t.Errorf("message drift: runtime %q, predict %q", ce.Message, pe.Message)
				}
				if want := "overlays[1] (" + string(kind) + ") reads the crosstab cell values"; len(ce.Message) < len(want) || ce.Message[:len(want)] != want {
					t.Errorf("message = %q, want it to lead with %q", ce.Message, want)
				}
			})
		}
	}
	// The moment pairwise kinds read the cell's component moments, never
	// its value: a code cell is not theirs to judge (their own cell rules
	// refuse a non-moment cell).
	for _, kind := range []types.OverlayKind{types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ, types.OverlayKindPairwiseWeightedTwoMeansZ} {
		t.Run(string(kind)+"/not_judged", func(t *testing.T) {
			req := func() *pulse.Request { return ccCrosstab("flag", types.AGG_MODE, "color", ccMatrixSpec(kind)) }
			if pe := predictCodedError(t, p, data, req()); pe != nil && pe.Details["reason"] == "categorical_code" {
				t.Fatalf("Predict = %s %v, want no categorical_code refusal", pe.Code, pe.Details)
			}
			if _, err := p.Process(ctx, req()); err != nil && codeOf(t, err).Details["reason"] == "categorical_code" {
				t.Fatalf("Process: %v, want no categorical_code refusal", err)
			}
		})
	}
	// No overlay, no judgement: the code crosstab itself still runs.
	if _, err := p.Process(ctx, ccCrosstab("flag", types.AGG_MODE, "color")); err != nil {
		t.Fatalf("overlay-free code crosstab: %v", err)
	}
}

// ccStage is a later chain stage grouping by the forwarded region key.
func ccStage(overlays []types.OverlaySpec, aggs ...*types.Aggregation) *types.Request {
	return &types.Request{Groups: ccGroups(), Aggregations: aggs, Overlays: overlays}
}

// TestChainOverlay_ForwardedCodePropagates: a forwarded code column
// stays a code through every stage, for the whole-chain overlays (the
// scored aggregation of the target stage) and for a stage's own
// Request.Overlays (located details.stage), predict first; a forwarded
// magnitude (AGG_MODE over the numeric x) still scores.
func TestChainOverlay_ForwardedCodePropagates(t *testing.T) {
	t.Parallel()

	p, _ := ccFixture(t, pulse.Options{})
	kind := types.OverlayKindDeltaVsStage
	s0 := func() []*types.Aggregation {
		return []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}, {Type: types.AGG_MODE, Field: "color", Label: "mc"},
			{Type: types.AGG_MODE, Field: "flag", Label: "mf"}, {Type: types.AGG_MODE, Field: "x", Label: "mx"}}
	}
	mode := func(field, label string) *types.Aggregation {
		return &types.Aggregation{Type: types.AGG_MODE, Field: field, Label: label}
	}
	sum := func(field, label string) *types.Aggregation {
		return &types.Aggregation{Type: types.AGG_SUM, Field: field, Label: label}
	}
	for _, c := range []struct{ fwd, ft string }{{"mc", "categorical_u8"}, {"mf", "packed_bool"}} {
		t.Run("whole_chain/stage1/"+c.fwd, func(t *testing.T) {
			ccChainRefused(t, p, func() *pulse.ChainRequest {
				return ccChain(s0(), []*types.Request{ccStage(nil, mode(c.fwd, "s"))}, kind, 1, nil)
			}, map[string]any{"overlay_index": 0, "kind": string(kind), "role": "target", "stage_index": 1, "stage_name": "s1",
				"aggregation_index": 0, "aggregation": "AGG_MODE", "label": "s", "reason": "categorical_code",
				"field": c.fwd, "field_type": c.ft})
		})
		// Two hops: stage 1's mode over the forwarded code is a code
		// again; stage 2's mode over it is refused.
		t.Run("whole_chain/stage2/"+c.fwd, func(t *testing.T) {
			ccChainRefused(t, p, func() *pulse.ChainRequest {
				return ccChain(s0(), []*types.Request{ccStage(nil, sum("s", "s"), mode(c.fwd, "m2")), ccStage(nil, mode("m2", "s"))}, kind, 2, nil)
			}, map[string]any{"overlay_index": 0, "kind": string(kind), "role": "target", "stage_index": 2, "stage_name": "s2",
				"aggregation_index": 0, "aggregation": "AGG_MODE", "label": "s", "reason": "categorical_code",
				"field": "m2", "field_type": c.ft})
		})
		// A stage's own overlays: a series overlay on stage 1 scoring
		// AGG_MODE over the forwarded code (the Process-host rule, on
		// the stage's synthesised input plus its code columns).
		t.Run("stage_overlay/stage1/"+c.fwd, func(t *testing.T) {
			req := func() *pulse.ChainRequest {
				r := ccChain(s0(), []*types.Request{ccStage([]types.OverlaySpec{{Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup}},
					mode(c.fwd, "m"))}, kind, 0, "s")
				return r
			}
			ccChainRefused(t, p, req, map[string]any{"aggregation": "AGG_MODE", "label": "m", "slot": "aggregations[0]",
				"reason": "categorical_code", "field": c.fwd, "field_type": c.ft, "stage": 1})
		})
	}
	// Magnitudes and figures still score: AGG_MODE over a forwarded
	// numeric mode, and a figure (AGG_SUM) over a forwarded code is not a
	// code (a pick only).
	t.Run("whole_chain/stage1/mode_over_forwarded_numeric_scores", func(t *testing.T) {
		ccChainRuns(t, p, func() *pulse.ChainRequest {
			return ccChain(s0(), []*types.Request{ccStage(nil, mode("mx", "s"))}, kind, 1, nil)
		})
	})
	t.Run("whole_chain/stage2/sum_breaks_the_code", func(t *testing.T) {
		ccChainRuns(t, p, func() *pulse.ChainRequest {
			return ccChain(s0(), []*types.Request{ccStage(nil, sum("mc", "m2")), ccStage(nil, mode("m2", "s"))}, kind, 2, nil)
		})
	})
	t.Run("stage_overlay/stage1/mode_over_forwarded_numeric_scores", func(t *testing.T) {
		ccChainRuns(t, p, func() *pulse.ChainRequest {
			return ccChain(s0(), []*types.Request{ccStage([]types.OverlaySpec{{Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup}},
				mode("mx", "m"))}, kind, 0, "s")
		})
	})
}
