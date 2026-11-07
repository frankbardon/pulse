package overlayhost

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func extensionTestDecl() *ExtensionDecl {
	return &ExtensionDecl{
		Kind:       "OVERLAY_ACME_X",
		HostShapes: []types.OverlayShape{types.OverlayShapeMatrix},
		Shapes:     []types.OverlayShape{types.OverlayShapeMatrix, types.OverlayShapeSeries},
		Scopes:     []types.OverlayScope{types.OverlayScopeCell},
		Refs:       []string{RefMargin},
	}
}

// TestExtensionOutputRefusal pins every reason of the output rule the
// probe and every run share.
func TestExtensionOutputRefusal(t *testing.T) {
	t.Parallel()

	one := 1.0
	yes := true
	m := &types.MatrixPayload{}
	cases := []struct {
		name     string
		payload  types.OverlayPayload
		summary  *types.OverlaySummary
		warnings []types.OverlayWarning
		reason   string
	}{
		{name: "matrix_ok", payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m}},
		{name: "series_ok", payload: types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{}}},
		{name: "undeclared", payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &one}, reason: "undeclared_shape"},
		{name: "empty_shape", payload: types.OverlayPayload{Matrix: m}, reason: "undeclared_shape"},
		{name: "arm_missing", payload: types.OverlayPayload{Shape: types.OverlayShapeSeries}, reason: "shape_arm_mismatch"},
		{name: "two_arms", payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m, Series: &types.SeriesPayload{}}, reason: "shape_arm_mismatch"},
		{name: "parts", payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m,
			Parts: map[string]*types.OverlayPayload{"p": {Shape: types.OverlayShapeScalar, Scalar: &one}}}, reason: "undeclared_parts"},
		{name: "payload_p_adjusted", payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m, SignificantAdjusted: m}, reason: "engine_owned"},
		{name: "summary_p_adjusted", payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m},
			summary: &types.OverlaySummary{SignificantAdjusted: &yes}, reason: "engine_owned"},
		{name: "warning_no_code", payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m},
			warnings: []types.OverlayWarning{{Message: "x"}}, reason: "invalid_warning"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ExtensionOutputRefusal(extensionTestDecl(), &tc.payload, tc.summary, tc.warnings, 3)
			if tc.reason == "" {
				if got != nil {
					t.Fatalf("refusal = %v, want nil", got)
				}
				return
			}
			if got == nil || got.Code != errors.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH || got.Details["reason"] != tc.reason || got.Details["index"] != 3 {
				t.Fatalf("refusal = %v, want reason %s at index 3", got, tc.reason)
			}
		})
	}
	if got := ExtensionOutputRefusal(extensionTestDecl(), &types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &one}, nil, nil, -1); got == nil {
		t.Fatal("probe refusal = nil")
	} else if _, has := got.Details["index"]; has {
		t.Errorf("probe refusal carries index: %v", got.Details)
	}
}

// TestExtensionRequestRefusal_Order: the host shape is judged before
// the Ref, the Ref before the Scope, the Scope before the hook — and
// the hook only on a spec every declarative rule accepted.
func TestExtensionRequestRefusal_Order(t *testing.T) {
	t.Parallel()

	called := 0
	d := extensionTestDecl()
	d.Validate = func(*types.OverlaySpec) error { called++; return stderrors.New("no") }
	margin := types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}}
	cases := []struct {
		name   string
		shape  types.OverlayShape
		spec   types.OverlaySpec
		code   errors.Code
		called int
	}{
		{name: "shape_first", shape: types.OverlayShapeSeries, spec: types.OverlaySpec{Kind: d.Kind, Scope: types.OverlayScopeRow}, code: errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "ref_before_scope", shape: types.OverlayShapeMatrix, spec: types.OverlaySpec{Kind: d.Kind, Scope: types.OverlayScopeRow}, code: errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "scope", shape: types.OverlayShapeMatrix, spec: types.OverlaySpec{Kind: d.Kind, Scope: types.OverlayScopeRow, Ref: margin}, code: errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{name: "hook_last", shape: types.OverlayShapeMatrix, spec: types.OverlaySpec{Kind: d.Kind, Scope: types.OverlayScopeCell, Ref: margin}, code: errors.PULSE_EXTENSION_SPEC_REFUSED, called: 1},
	}
	for _, tc := range cases {
		called = 0
		got := ExtensionRequestRefusal(d, tc.shape, false, &tc.spec, 0)
		if got == nil || got.Code != tc.code || called != tc.called {
			t.Errorf("%s: refusal = %v (hook called %d), want %s (hook called %d)", tc.name, got, called, tc.code, tc.called)
		}
	}
}
