package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestChiSqVsRef_ReferenceKindMatchesPredict: OVERLAY_CHISQ_VS_REF's
// params.reference_kind (#791) is judged by one rule on both sides — a
// value other than "fixed" / "sample" is PULSE_OVERLAY_PARAM_MISSING
// with the same message and details from PredictCompose and Compose;
// both accepted values predict clean and run, and "sample" answers a
// different statistic than the default over the same slots.
func TestChiSqVsRef_ReferenceKindMatchesPredict(t *testing.T) {
	t.Parallel()

	p, _, cohort := acceptanceCohort(t)
	ctx := context.Background()
	mk := func(params map[string]any) *types.ComposedRequest {
		total := floorCrosstab(cohort, nil, types.SlotWeight{}, nil)
		total.Label = "total"
		sub := floorCrosstab(cohort, nil, types.SlotWeight{}, nil)
		sub.Label = "sub"
		sub.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x >= 10"}}
		return &types.ComposedRequest{
			Requests: []*types.Request{total, sub},
			Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: types.OverlayKindChiSqVsRef, Scope: types.OverlayScopeMatrix,
				Reference: "total", Targets: []string{"sub"}, Params: params}},
		}
	}
	for _, bad := range []any{"samples", 1.0, true} {
		req := mk(map[string]any{"reference_kind": bad})
		env, err := p.PredictCompose(ctx, req)
		pe := firstPredictError(t, env, err)
		_, err = p.Compose(ctx, req)
		sameRefusal(t, "reference_kind", pe, err, errors.PULSE_OVERLAY_PARAM_MISSING)
		if pe.Details["param"] != "reference_kind" || pe.Details["index"] != 0 {
			t.Fatalf("details %v", pe.Details)
		}
	}
	stat := map[string]float64{}
	for _, kind := range []string{"", "fixed", "sample"} {
		var params map[string]any
		if kind != "" {
			params = map[string]any{"reference_kind": kind}
		}
		req := mk(params)
		env, err := p.PredictCompose(ctx, req)
		if pe := firstPredictError(t, env, err); pe != nil {
			t.Fatalf("%q: predict %+v", kind, pe)
		}
		resp, err := p.Compose(ctx, req)
		if err != nil {
			t.Fatalf("%q: %v", kind, err)
		}
		s := resp.Overlays[0].Summary
		if s == nil || s.Statistic == nil {
			t.Fatalf("%q: summary %+v", kind, s)
		}
		stat[kind] = *s.Statistic
	}
	if stat[""] != stat["fixed"] || stat["sample"] == stat["fixed"] {
		t.Fatalf("statistics %v: want default == fixed != sample", stat)
	}
}
