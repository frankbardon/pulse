package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestComposeOverlayTwoSampleInputsRefusal: every route the rule maps
// (slot shape × scored aggregation × components × params), nil
// instance (hide nothing). Root TestComposeOverlay_WelfordComponentsDisabledRefused
// and TestComposeOverlay_TwoSampleInputsRefused are the predict =
// runtime parity half.
func TestComposeOverlayTwoSampleInputsRefusal(t *testing.T) {
	slot := func(label string, aggs ...types.AggregationType) *types.Request {
		r := &types.Request{Label: label, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}}
		for i, a := range aggs {
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: a, Field: "x", Label: string(rune('a' + i))})
		}
		return r
	}
	cross := func(label string, cell types.AggregationType) *types.Request {
		return &types.Request{Label: label, Crosstab: &types.CrosstabSpec{Cell: &types.Aggregation{Type: cell, Field: "x", Label: "c"}}}
	}
	both := map[string]any{"variance_target": 4.0, "variance_ref": 4.0, "sample_size_target": 9.0, "sample_size_ref": 9.0}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range both {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	spec := func(kind types.OverlayKind, params map[string]any) []types.ComposeOverlaySpec {
		return []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Reference: "r", Targets: []string{"t"}, Params: params}}
	}
	all := func(*types.Request) bool { return true }
	none := func(*types.Request) bool { return false }
	welf := func() []*types.Request {
		return []*types.Request{slot("r", types.AGG_WELFORD), slot("t", types.AGG_WELFORD), slot("u", types.AGG_WELFORD)}
	}
	scalar := func() []*types.Request {
		return []*types.Request{slot("r", types.AGG_AVERAGE), slot("t", types.AGG_AVERAGE), slot("u", types.AGG_AVERAGE)}
	}
	crosstab := func(cell types.AggregationType) []*types.Request {
		return []*types.Request{cross("r", cell), cross("t", cell), cross("u", cell)}
	}
	labels := []string{"r", "t", "u"}
	type want struct {
		code    errors.Code
		host    string
		reason  string
		side    string
		missing []string
	}
	welfOff := &want{code: errors.PULSE_OVERLAY_COMPONENTS_REQUIRED, host: "requests[0].aggregations[0]", reason: "components_disabled"}
	refParams := &want{code: errors.PULSE_OVERLAY_PARAM_MISSING, host: "requests[0].aggregations[0]", reason: "params_missing",
		side: "reference", missing: []string{"variance_ref", "sample_size_ref"}}
	cases := []struct {
		name     string
		overlays []types.ComposeOverlaySpec
		requests []*types.Request
		disabled func(*types.Request) bool
		want     *want
	}{
		// Grouped AGG_WELFORD: the components leg (E4-S13).
		{"t_vs_ref welford off", spec(types.OverlayKindTVsRef, nil), welf(), all, welfOff},
		{"z_vs_ref welford off", spec(types.OverlayKindZVsRef, nil), welf(), all, welfOff},
		{"welford off, params do not substitute", spec(types.OverlayKindTVsRef, both), welf(), all, welfOff},
		{"field scores welford", spec(types.OverlayKindTVsRef, map[string]any{"field": "b"}),
			[]*types.Request{slot("r", types.AGG_SUM, types.AGG_WELFORD), slot("t", types.AGG_SUM, types.AGG_WELFORD)}, all,
			&want{code: errors.PULSE_OVERLAY_COMPONENTS_REQUIRED, host: "requests[0].aggregations[1]", reason: "components_disabled"}},
		{"welford on, no params", spec(types.OverlayKindTVsRef, nil), welf(), none, nil},
		{"unknown field left to the field rule", spec(types.OverlayKindTVsRef, map[string]any{"field": "zz"}), welf(), all, nil},
		{"kind without two-sample inputs", spec(types.OverlayKindIndexVsRef, nil), welf(), all, nil},

		// Grouped scalar: the params leg (E4-S16).
		{"scalar, no params", spec(types.OverlayKindTVsRef, nil), scalar(), none, refParams},
		{"z scalar, no params", spec(types.OverlayKindZVsRef, nil), scalar(), none, refParams},
		{"scalar, target side only", spec(types.OverlayKindTVsRef, map[string]any{"variance_target": 1.0, "sample_size_target": 4.0}), scalar(), none, refParams},
		{"scalar, reference side only", spec(types.OverlayKindTVsRef, map[string]any{"variance_ref": 1.0, "sample_size_ref": 4.0}), scalar(), none,
			&want{code: errors.PULSE_OVERLAY_PARAM_MISSING, host: "requests[1].aggregations[0]", reason: "params_missing",
				side: "target", missing: []string{"variance_target", "sample_size_target"}}},
		{"scalar, one key short", spec(types.OverlayKindTVsRef, with(map[string]any{"sample_size_ref": 0.0})), scalar(), none,
			&want{code: errors.PULSE_OVERLAY_PARAM_MISSING, host: "requests[0].aggregations[0]", reason: "params_missing",
				side: "reference", missing: []string{"sample_size_ref"}}},
		{"scalar, per-side params", spec(types.OverlayKindTVsRef, both), scalar(), none, nil},
		{"scalar, symmetric params", spec(types.OverlayKindZVsRef, map[string]any{"variance": 2.0, "sample_size": 5.0}), scalar(), all, nil},
		{"field scores scalar", spec(types.OverlayKindTVsRef, map[string]any{"field": "a"}),
			[]*types.Request{slot("r", types.AGG_SUM, types.AGG_WELFORD), slot("t", types.AGG_SUM, types.AGG_WELFORD)}, all, refParams},
		{"no targets: params left to the target rule", []types.ComposeOverlaySpec{{Name: "o", Kind: types.OverlayKindTVsRef, Reference: "r"}}, scalar(), none, nil},
		{"unread slot not judged", spec(types.OverlayKindTVsRef, both),
			[]*types.Request{slot("r", types.AGG_AVERAGE), slot("t", types.AGG_AVERAGE), slot("u", types.AGG_WELFORD)}, all, nil},

		// vs-ref over a crosstab slot: no rows to read.
		{"t_vs_ref over crosstab", spec(types.OverlayKindTVsRef, both), crosstab(types.AGG_WELFORD), none,
			&want{code: errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE}},

		// Cell kinds over crosstab slots.
		{"t_cell welford on", spec(types.OverlayKindTCell, nil), crosstab(types.AGG_WELFORD), none, nil},
		{"z_cell variance on", spec(types.OverlayKindZCell, nil), crosstab(types.AGG_VARIANCE), none, nil},
		{"t_cell stddev on", spec(types.OverlayKindTCell, nil), crosstab(types.AGG_STDDEV), none, nil},
		{"t_cell welford off", spec(types.OverlayKindTCell, nil), crosstab(types.AGG_WELFORD), all,
			&want{code: errors.PULSE_OVERLAY_PARAM_MISSING, host: "requests[0].crosstab.cell", reason: "params_missing",
				side: "reference", missing: []string{"variance_ref", "sample_size_ref"}}},
		{"t_cell welford off with params", spec(types.OverlayKindTCell, both), crosstab(types.AGG_WELFORD), all, nil},
		{"z_cell average on", spec(types.OverlayKindZCell, nil), crosstab(types.AGG_AVERAGE), none,
			&want{code: errors.PULSE_OVERLAY_PARAM_MISSING, host: "requests[0].crosstab.cell", reason: "params_missing",
				side: "reference", missing: []string{"variance_ref", "sample_size_ref"}}},
		{"z_cell average with params", spec(types.OverlayKindZCell, both), crosstab(types.AGG_AVERAGE), none, nil},
		{"cell kind over grouped slots left to SLOT_NOT_CROSSTAB", spec(types.OverlayKindZCell, nil), welf(), all, nil},

		// A structurally malformed spec is left to the structural rule.
		{"unknown target left to the target rule", []types.ComposeOverlaySpec{{Name: "o", Kind: types.OverlayKindTVsRef, Reference: "r", Targets: []string{"zz"}}}, scalar(), none, nil},
		{"mixed shapes left to the shape rule", spec(types.OverlayKindTVsRef, nil),
			[]*types.Request{cross("r", types.AGG_AVERAGE), slot("t", types.AGG_AVERAGE)}, none, nil},
		{"grouped vs ungrouped left to the shape rule", spec(types.OverlayKindZVsRef, nil),
			[]*types.Request{slot("r", types.AGG_AVERAGE), {Label: "t", Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "x", Label: "a"}}}}, none, nil},
		{"field unknown on one target left to the field rule", spec(types.OverlayKindTVsRef, map[string]any{"field": "b"}),
			[]*types.Request{slot("r", types.AGG_SUM, types.AGG_SUM), slot("t", types.AGG_SUM)}, none, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ComposeOverlayTwoSampleInputsRefusal(tc.overlays, tc.requests, labels, nil, tc.disabled)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				return
			}
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != tc.want.code {
				t.Fatalf("got %v, want %s", err, tc.want.code)
			}
			if tc.want.code == errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {
				if ce.Details["slot"] != "r" || ce.Details["shape"] != "matrix" || !reflect.DeepEqual(ce.Details["shapes"], []string{"series"}) {
					t.Fatalf("details = %v", ce.Details)
				}
				return
			}
			if ce.Details["host"] != tc.want.host || ce.Details["reason"] != tc.want.reason {
				t.Fatalf("details = %v, want host %s reason %s", ce.Details, tc.want.host, tc.want.reason)
			}
			if tc.want.side != "" && (ce.Details["side"] != tc.want.side || !reflect.DeepEqual(ce.Details["params"], tc.want.missing)) {
				t.Fatalf("details = %v, want side %s params %v", ce.Details, tc.want.side, tc.want.missing)
			}
		})
	}
}
