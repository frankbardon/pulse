package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// siblingHostReq returns a minimal grouped Process request (the
// SERIES host the sibling-reference family targets). Mirrors
// indexVsTotalSeriesHostReq — region grouper + value AGG_SUM.
func siblingHostReq() *types.Request {
	return &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// TestValidateOverlay_DeltaVsSibling_HappyPath asserts a well-formed
// OVERLAY_DELTA_VS_SIBLING overlay riding on a SERIES host passes the
// predict gate without surfacing any overlay-specific errors.
func TestValidateOverlay_DeltaVsSibling_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := siblingHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "delta_sib",
			Kind:  types.OverlayKindDeltaVsSibling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
		errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on DELTA_VS_SIBLING happy-path request", code)
		}
	}
}

// TestValidateOverlay_DeltaVsSibling_ScopeUnsupported asserts every
// non-GROUP scope is rejected.
func TestValidateOverlay_DeltaVsSibling_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeCell,
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := siblingHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindDeltaVsSibling,
					Scope: scope,
					Ref: types.OverlayRef{
						Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED", scope)
			}
		})
	}
}

// TestValidateOverlay_DeltaVsSibling_RefRejected asserts non-Sibling
// Ref pointers surface PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_DeltaVsSibling_RefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		ref  types.OverlayRef
	}{
		{
			name: "Margin",
			ref:  types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand}},
		},
		{
			name: "BaselineIndex",
			ref:  types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := siblingHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindDeltaVsSibling,
					Scope: types.OverlayScopeGroup,
					Ref:   tc.ref,
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Fatalf("Ref=%s: expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", tc.name)
			}
		})
	}
}

// TestValidateOverlay_DeltaVsSibling_MissingSiblingFieldRejected pins
// the missing-Field rejection: a Sibling pointer with empty Field
// surfaces PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_DeltaVsSibling_MissingSiblingFieldRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := siblingHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindDeltaVsSibling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "", Value: "north"},
			},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on empty Sibling.Field")
	}
}

// TestValidateOverlay_DeltaVsSibling_MissingSiblingValueRejected pins
// the missing-Value rejection.
func TestValidateOverlay_DeltaVsSibling_MissingSiblingValueRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := siblingHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindDeltaVsSibling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: ""},
			},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on empty Sibling.Value")
	}
}

// TestValidateOverlay_DeltaVsSibling_NoSeriesHostRejected pins the
// SERIES-host requirement.
func TestValidateOverlay_DeltaVsSibling_NoSeriesHostRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		req  *types.Request
	}{
		{
			name: "no groupers",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "value"}},
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindDeltaVsSibling,
						Scope: types.OverlayScopeGroup,
						Ref: types.OverlayRef{
							Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
						},
					},
				},
			},
		},
		{
			name: "crosstab host",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{
					{
						Kind:  types.OverlayKindDeltaVsSibling,
						Scope: types.OverlayScopeGroup,
						Ref: types.OverlayRef{
							Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
						},
					},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := PredictFromBytes(data, tc.req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on missing SERIES host")
			}
		})
	}
}

// TestValidateOverlay_DeltaVsSibling_LevelWithinRejected pins the
// Level / Within rejection.
func TestValidateOverlay_DeltaVsSibling_LevelWithinRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name   string
		level  int
		within int
	}{
		{"level=1", 1, 0},
		{"within=1", 0, 1},
		{"both", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := siblingHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:   types.OverlayKindDeltaVsSibling,
					Scope:  types.OverlayScopeGroup,
					Level:  tc.level,
					Within: tc.within,
					Ref: types.OverlayRef{
						Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
				t.Fatalf("level=%d within=%d: expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE", tc.level, tc.within)
			}
		})
	}
}

// TestValidateOverlay_IndexVsSibling_HappyPath asserts a well-formed
// OVERLAY_INDEX_VS_SIBLING overlay passes the predict gate. Mirrors
// the DELTA_VS_SIBLING happy-path test — both kinds share the
// structural contract.
func TestValidateOverlay_IndexVsSibling_HappyPath(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := siblingHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "idx_sib",
			Kind:  types.OverlayKindIndexVsSibling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
		errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on INDEX_VS_SIBLING happy-path request", code)
		}
	}
}

// TestValidateOverlay_IndexVsSibling_ScopeUnsupported pins the scope
// rejection. Mirrors DELTA_VS_SIBLING.
func TestValidateOverlay_IndexVsSibling_ScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeCell,
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := siblingHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindIndexVsSibling,
					Scope: scope,
					Ref: types.OverlayRef{
						Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
					},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED", scope)
			}
		})
	}
}

// TestValidateOverlay_IndexVsSibling_MissingSiblingRejected pins both
// missing-Field and missing-Value paths.
func TestValidateOverlay_IndexVsSibling_MissingSiblingRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		ref  types.OverlayRef
	}{
		{
			name: "missing Sibling pointer",
			ref:  types.OverlayRef{},
		},
		{
			name: "empty Field",
			ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "", Value: "north"},
			},
		},
		{
			name: "empty Value",
			ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: ""},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := siblingHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindIndexVsSibling,
					Scope: types.OverlayScopeGroup,
					Ref:   tc.ref,
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Fatalf("%s: expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", tc.name)
			}
		})
	}
}

// TestValidateOverlay_IndexVsSibling_LevelWithinRejected pins the
// Level / Within rejection. Mirrors DELTA_VS_SIBLING.
func TestValidateOverlay_IndexVsSibling_LevelWithinRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := siblingHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsSibling,
			Scope:  types.OverlayScopeGroup,
			Level:  1,
			Within: 0,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
			},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
		t.Fatalf("expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on non-zero Level")
	}
}
