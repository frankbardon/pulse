package service

import (
	stderrors "errors"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestComposeOverlayFloorsPresent pins the fold-time invariant: a
// host-floor-scaling Compose overlay that would read a
// probability-weighted slot whose response came back without crosstab
// Components fails with PROCESSING_INTERNAL instead of folding Σw as n
// (silently unweighted p-values). The gate refusal and the Compose veto
// exist so this never fires on a correct engine.
func TestComposeOverlayFloorsPresent(t *testing.T) {
	svc := New(crosstabJoinFixture(t))
	slot := func(label string, weighted bool) *types.Request {
		r := skipCrosstabRequest(false)
		r.Label = label
		if weighted {
			r.Crosstab.Cell.Weight = types.SlotWeightOf(types.WeightSpec{Field: "value", Kind: types.WeightKindProbability})
		}
		return r
	}
	withFloor := &types.Response{Components: &types.ResponseComponents{Crosstab: &types.CrosstabComponents{}}}
	noFloor := &types.Response{}
	overlay := []types.ComposeOverlaySpec{{
		Name: "pz", Kind: types.OverlayKindPropZCell, Scope: types.OverlayScopeCell,
		Reference: "a", Targets: []string{"b"},
	}}
	cases := []struct {
		name      string
		weighted  bool
		responses []*types.Response
		wantErr   bool
	}{
		{name: "weighted_target_missing_floor", weighted: true, responses: []*types.Response{withFloor, noFloor, noFloor}, wantErr: true},
		{name: "weighted_reference_missing_floor", weighted: true, responses: []*types.Response{noFloor, withFloor, withFloor}, wantErr: true},
		{name: "weighted_floors_present", weighted: true, responses: []*types.Response{withFloor, withFloor, noFloor}},
		{name: "unweighted_missing_floor", weighted: false, responses: []*types.Response{noFloor, noFloor, noFloor}},
		{name: "failed_slot_is_not_an_invariant_breach", weighted: true, responses: []*types.Response{withFloor, nil, noFloor}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := []*types.Request{slot("a", tc.weighted), slot("b", tc.weighted), slot("c", tc.weighted)}
			labels := []string{"a", "b", "c"}
			err := svc.composeOverlayFloorsPresent(&types.ComposedRequest{Overlays: overlay}, requests, labels, tc.responses)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_INTERNAL {
				t.Fatalf("want PROCESSING_INTERNAL, got %v", err)
			}
		})
	}
}
