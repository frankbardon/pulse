package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict-time tests for the OVERLAY_YOY validator
// (descriptor.validateOverlayYoY).
//
// E4-S7 scope: contract enforced by the predict gate:
//
//   - Happy path: GROUP scope + Ref.YoY populated + SERIES host
//     (Request.Groups non-empty, Request.Crosstab nil) + first grouper
//     is GROUP_DATE + frequency Param on either OverlaySpec.Params or
//     Groups[0].Params resolves to a supported value (annual | quarterly
//     | monthly | weekly | daily | hourly) passes without overlay errors.
//   - Frequency Param missing on both spec.Params AND
//     Groups[0].Params fires PULSE_OVERLAY_YOY_FREQUENCY_MISSING.
//   - Frequency Param outside the supported set fires
//     PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY.
//   - Non-DATE first grouper fires
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE with Details naming the
//     actual grouper type.

// yoyHostReq returns a minimal grouped Process request whose first
// grouper is GROUP_DATE — the SERIES host the OVERLAY_YOY kind
// targets. Frequency lives on the OverlaySpec.Params (per-test fixture)
// so the host grouper's Params slot is intentionally empty unless the
// test promotes it.
func yoyHostReq() *types.Request {
	return &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_DATE, Field: "order_date"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// TestValidateOverlay_YoYHappyPathAccepted asserts a well-formed
// OVERLAY_YOY overlay riding on a SERIES GROUP_DATE host passes the
// predict gate without surfacing any overlay-specific errors.
func TestValidateOverlay_YoYHappyPathAccepted(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
	}

	env := PredictFromBytes(data, req, nil)
	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
		errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
		errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING,
		errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on OVERLAY_YOY happy-path request", code)
		}
	}
}

// TestValidateOverlay_YoYFrequencyRequired asserts that a YoY spec
// missing the frequency Param on both OverlaySpec.Params and
// Groups[0].Params fires PULSE_OVERLAY_YOY_FREQUENCY_MISSING. The
// story brief named this test explicitly.
func TestValidateOverlay_YoYFrequencyRequired(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	// Intentionally leave Groups[0].Params empty so the gate cannot fall
	// back to the host-grouper authoring slot.
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			// Params intentionally absent.
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING) {
		t.Fatalf("expected PULSE_OVERLAY_YOY_FREQUENCY_MISSING for missing frequency Param")
	}
}

// TestValidateOverlay_YoYFrequencyFromGroupParams pins the orchestrator
// fall-back: when the OverlaySpec.Params slot is empty but the host
// GROUP_DATE grouper carries a frequency Param the gate accepts the
// spec.
func TestValidateOverlay_YoYFrequencyFromGroupParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	req.Groups[0].Params = json.RawMessage(`{"component": "month", "frequency": "monthly"}`)
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			// Params intentionally absent — the gate falls back to
			// Groups[0].Params.
		},
	}

	env := PredictFromBytes(data, req, nil)
	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING,
		errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on YoY frequency-from-group-params request", code)
		}
	}
}

// TestValidateOverlay_YoYIncompatibleFrequencyRejected asserts that a
// frequency value outside the supported set fires
// PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY.
func TestValidateOverlay_YoYIncompatibleFrequencyRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, freq := range []string{"minutely", "yearly", "biennial", "decade"} {
		t.Run(freq, func(t *testing.T) {
			req := yoyHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Name:  "yoy",
					Kind:  types.OverlayKindYoY,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						YoY: &types.OverlayYoYRef{},
					},
					Params: json.RawMessage(`{"frequency": "` + freq + `"}`),
				},
			}

			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY) {
				t.Fatalf("frequency=%s: expected PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY", freq)
			}
		})
	}
}

// TestValidateOverlay_YoYNonDateGrouperRejected asserts that a host
// whose first grouper is not GROUP_DATE fires
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE with Details naming the
// actual grouper type.
func TestValidateOverlay_YoYNonDateGrouperRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	// siblingHostReq uses GROUP_CATEGORY over "region" — not a DATE host.
	req := siblingHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
	}

	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for non-GROUP_DATE host")
	}
}

// TestValidateOverlay_YoYScopeUnsupported asserts every non-GROUP scope
// fires PULSE_OVERLAY_SCOPE_UNSUPPORTED.
func TestValidateOverlay_YoYScopeUnsupported(t *testing.T) {
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
			req := yoyHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindYoY,
					Scope: scope,
					Ref: types.OverlayRef{
						YoY: &types.OverlayYoYRef{},
					},
					Params: json.RawMessage(`{"frequency": "monthly"}`),
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED", scope)
			}
		})
	}
}

// TestValidateOverlay_YoYRefRejected asserts non-YoY Ref pointers
// surface PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_YoYRefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		ref  types.OverlayRef
	}{
		{
			name: "Margin",
			ref:  types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
		},
		{
			name: "Sibling",
			ref:  types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"}},
		},
		{
			name: "Prior",
			ref:  types.OverlayRef{Prior: &types.OverlayPriorRef{}},
		},
		{
			name: "BaselineIndex",
			ref:  types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0}},
		},
		{
			name: "RollingMean",
			ref:  types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := yoyHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:   types.OverlayKindYoY,
					Scope:  types.OverlayScopeGroup,
					Ref:    tc.ref,
					Params: json.RawMessage(`{"frequency": "monthly"}`),
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Fatalf("ref=%s: expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", tc.name)
			}
		})
	}
}

// TestValidateOverlay_YoYMissingRefRejected asserts an empty Ref
// surfaces PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_YoYMissingRefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:   types.OverlayKindYoY,
			Scope:  types.OverlayScopeGroup,
			Ref:    types.OverlayRef{}, // empty
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for empty Ref")
	}
}

// TestValidateOverlay_YoYNoSeriesHostRejected asserts a request with no
// groupers OR an active Crosstab fires
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (no SERIES host).
func TestValidateOverlay_YoYNoSeriesHostRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		req  *types.Request
	}{
		{
			name: "no Groups",
			req: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "value"},
				},
			},
		},
		{
			name: "Crosstab non-nil",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindYoY,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						YoY: &types.OverlayYoYRef{},
					},
					Params: json.RawMessage(`{"frequency": "monthly"}`),
				},
			}
			env := PredictFromBytes(data, tc.req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Fatalf("%s: expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", tc.name)
			}
		})
	}
}

// TestValidateOverlay_YoYLevelWithinRejected asserts non-zero Level or
// Within values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE — the kind is in
// the windowed implicit-margin family.
func TestValidateOverlay_YoYLevelWithinRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name   string
		level  int
		within int
	}{
		{name: "Level=1", level: 1, within: 0},
		{name: "Within=1", level: 0, within: 1},
		{name: "both", level: 1, within: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := yoyHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindYoY,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						YoY: &types.OverlayYoYRef{},
					},
					Params: json.RawMessage(`{"frequency": "monthly"}`),
					Level:  tc.level,
					Within: tc.within,
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
				t.Fatalf("%s: expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE", tc.name)
			}
		})
	}
}
