package service

import (
	stderrors "errors"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestComposeOverlayTwoSampleLegsPresent pins the fold-time invariant: a
// Compose OVERLAY_T_VS_REF scoring a grouped AGG_WELFORD slot whose
// response came back without aggregation Components (or an
// OVERLAY_T_CELL over a moment-carrying crosstab cell whose response
// came back without crosstab Components) fails with
// PROCESSING_INTERNAL instead of folding an empty layer. The gate
// refusal (PULSE_OVERLAY_COMPONENTS_REQUIRED) and the Compose veto
// exist so this never fires on a correct engine.
func TestComposeOverlayTwoSampleLegsPresent(t *testing.T) {
	t.Parallel()

	svc := New(crosstabJoinFixture(t))
	slot := func(label string, agg types.AggregationType) *types.Request {
		return &types.Request{Label: label,
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: agg, Field: "value", Label: "v"}}}
	}
	with := &types.Response{Components: &types.ResponseComponents{Aggregations: []types.AggregationComponents{{}}}}
	without := &types.Response{}
	// Scalar slots carry their params: the gate refusal judged them
	// before the fold, so the invariant only ever sees the components leg.
	overlay := []types.ComposeOverlaySpec{{Name: "t", Kind: types.OverlayKindTVsRef, Scope: types.OverlayScopeGroup,
		Reference: "a", Targets: []string{"b"}, Params: map[string]any{"variance": 1.0, "sample_size": 4.0}}}
	cases := []struct {
		name      string
		agg       types.AggregationType
		responses []*types.Response
		wantErr   bool
	}{
		{"target_missing", types.AGG_WELFORD, []*types.Response{with, without, without}, true},
		{"reference_missing", types.AGG_WELFORD, []*types.Response{without, with, with}, true},
		{"present", types.AGG_WELFORD, []*types.Response{with, with, without}, false},
		{"scalar_slot", types.AGG_AVERAGE, []*types.Response{without, without, without}, false},
		{"failed_slot", types.AGG_WELFORD, []*types.Response{with, nil, without}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := []*types.Request{slot("a", tc.agg), slot("b", tc.agg), slot("c", tc.agg)}
			err := svc.composeOverlayTwoSampleLegsPresent(&types.ComposedRequest{Overlays: overlay}, requests, []string{"a", "b", "c"}, tc.responses)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_INTERNAL || ce.Details["reason"] != "components_disabled" {
				t.Fatalf("want PROCESSING_INTERNAL with the rule's coordinates, got %v", err)
			}
		})
	}
}

// TestComposeOverlayTwoSampleLegsPresent_Crosstab: an OVERLAY_T_CELL
// over moment-carrying crosstab cells (AGG_VARIANCE) needs no params
// while the slots return crosstab Components; a slot that came back
// without them would leave the cell a bare scalar with no variance or
// n — PROCESSING_INTERNAL, never a default.
func TestComposeOverlayTwoSampleLegsPresent_Crosstab(t *testing.T) {
	t.Parallel()

	svc := New(crosstabJoinFixture(t))
	slot := func(label string) *types.Request {
		return &types.Request{Label: label, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell: &types.Aggregation{Type: types.AGG_VARIANCE, Field: "value"}}}
	}
	with := &types.Response{Components: &types.ResponseComponents{Crosstab: &types.CrosstabComponents{}}}
	without := &types.Response{Components: &types.ResponseComponents{}}
	overlay := []types.ComposeOverlaySpec{{Name: "t", Kind: types.OverlayKindTCell, Scope: types.OverlayScopeCell,
		Reference: "a", Targets: []string{"b"}}}
	requests := []*types.Request{slot("a"), slot("b")}
	if err := svc.composeOverlayTwoSampleLegsPresent(&types.ComposedRequest{Overlays: overlay}, requests, []string{"a", "b"},
		[]*types.Response{with, with}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err := svc.composeOverlayTwoSampleLegsPresent(&types.ComposedRequest{Overlays: overlay}, requests, []string{"a", "b"},
		[]*types.Response{with, without})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_INTERNAL || ce.Details["slot"] != "b" || ce.Details["side"] != "target" {
		t.Fatalf("want PROCESSING_INTERNAL naming slot b's target side, got %v", err)
	}
}
