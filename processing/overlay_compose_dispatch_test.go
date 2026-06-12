package processing

import (
	"errors"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// composeSlotOf returns a *types.Response whose host shape matches the
// requested OverlayShape. Used by the COMPOSE-host dispatch chassis
// tests to build synthetic per-slot results without going through the
// buffered Process path.
func composeSlotOf(shape types.OverlayShape) *types.Response {
	switch shape {
	case types.OverlayShapeMatrix:
		return &types.Response{
			Crosstab: &types.CrosstabResult{
				Matrix: &types.MatrixPayload{
					RowKeys:    []types.AxisKey{{"a"}},
					ColumnKeys: []types.AxisKey{{"x"}},
					Cells:      [][]types.MatrixCell{{{Present: true, Value: 1.0}}},
				},
			},
		}
	case types.OverlayShapeSeries:
		return &types.Response{
			Data: []map[string]any{
				{"region": "us", "sum": 1.0},
			},
		}
	default:
		return &types.Response{}
	}
}

// TestApplyComposeOverlays_EmptySpecsShortCircuits asserts the chassis
// returns nil layers / nil warnings / nil error when the spec slice
// is empty — the byte-identity guard against pre-E7-S4
// ComposedResponse shapes (no Overlays slot allocated, no JSON key
// emitted).
func TestApplyComposeOverlays_EmptySpecsShortCircuits(t *testing.T) {
	responses := []*types.Response{composeSlotOf(types.OverlayShapeScalar)}
	labels := []string{"request_1"}

	layers, warnings, err := ApplyComposeOverlays(nil, responses, labels)
	if err != nil {
		t.Fatalf("ApplyComposeOverlays(nil specs): %v", err)
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil (short-circuit)", layers)
	}
	if warnings != nil {
		t.Errorf("warnings = %+v, want nil (short-circuit)", warnings)
	}

	layers, _, err = ApplyComposeOverlays([]types.ComposeOverlaySpec{}, responses, labels)
	if err != nil {
		t.Fatalf("ApplyComposeOverlays(empty specs): %v", err)
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil (short-circuit)", layers)
	}
}

// TestApplyComposeOverlays_StubLayerOrderPreserved asserts two specs
// in order produce two layers in matching index order with the right
// Kind echoed verbatim. The chassis stub handler emits exactly one
// layer per spec; the chassis preserves spec-order emission (FR-A2
// equivalent for the COMPOSE-host path).
func TestApplyComposeOverlays_StubLayerOrderPreserved(t *testing.T) {
	responses := []*types.Response{
		composeSlotOf(types.OverlayShapeSeries),
		composeSlotOf(types.OverlayShapeSeries),
	}
	labels := []string{"baseline", "treatment"}
	// E7-S4 ships the chassis without per-kind handlers, so the
	// dispatch table is empty and every spec falls through to
	// applyComposeStub. The Kind values here are placeholders chosen
	// from the universal OverlayKind enum to exercise the spec-order
	// echo — the per-kind catalog (E7-S9..S12 + E7-S15+) will
	// register real handlers and replace the stub.
	specs := []types.ComposeOverlaySpec{
		{
			Name:      "first",
			Kind:      types.OverlayKindIndexVsStage,
			Scope:     types.OverlayScopeTotal,
			Reference: "baseline",
			Targets:   []string{"treatment"},
		},
		{
			Name:      "second",
			Kind:      types.OverlayKindDeltaVsStage,
			Scope:     types.OverlayScopeTotal,
			Reference: "baseline",
			Targets:   []string{"treatment"},
		},
	}

	layers, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err != nil {
		t.Fatalf("ApplyComposeOverlays: %v", err)
	}
	if got, want := len(layers), 2; got != want {
		t.Fatalf("len(layers) = %d, want %d", got, want)
	}
	if got, want := layers[0].Kind, types.OverlayKindIndexVsStage; got != want {
		t.Errorf("layers[0].Kind = %q, want %q", got, want)
	}
	if got, want := layers[1].Kind, types.OverlayKindDeltaVsStage; got != want {
		t.Errorf("layers[1].Kind = %q, want %q", got, want)
	}
	if got, want := layers[0].Name, "first"; got != want {
		t.Errorf("layers[0].Name = %q, want %q", got, want)
	}
	if got, want := layers[1].Name, "second"; got != want {
		t.Errorf("layers[1].Name = %q, want %q", got, want)
	}
}

// TestApplyComposeOverlays_StubShapeInheritsReference asserts the stub
// handler's inferred OverlayShape walks the reference slot's
// top-level host shape — matrix when reference carries a Crosstab,
// series when it carries grouped Process Data, scalar otherwise. The
// reference (not the target) is the canonical anchor for Compose-only
// kinds.
func TestApplyComposeOverlays_StubShapeInheritsReference(t *testing.T) {
	cases := []struct {
		name     string
		refShape types.OverlayShape
		want     types.OverlayShape
	}{
		{"matrix_reference_inherits_matrix", types.OverlayShapeMatrix, types.OverlayShapeMatrix},
		{"series_reference_inherits_series", types.OverlayShapeSeries, types.OverlayShapeSeries},
		{"scalar_reference_inherits_scalar", types.OverlayShapeScalar, types.OverlayShapeScalar},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responses := []*types.Response{
				composeSlotOf(tc.refShape),
				composeSlotOf(types.OverlayShapeScalar), // target shape varies; the chassis must read REFERENCE
			}
			labels := []string{"ref_slot", "target_slot"}
			specs := []types.ComposeOverlaySpec{{
				Name:      "shape_check",
				Kind:      types.OverlayKindIndexVsStage,
				Scope:     types.OverlayScopeTotal,
				Reference: "ref_slot",
				Targets:   []string{"target_slot"},
			}}
			layers, _, err := ApplyComposeOverlays(specs, responses, labels)
			if err != nil {
				t.Fatalf("ApplyComposeOverlays: %v", err)
			}
			if got, want := layers[0].Payload.Shape, tc.want; got != want {
				t.Errorf("Payload.Shape = %q, want %q", got, want)
			}
		})
	}
}

// TestApplyComposeOverlays_ReferenceUnknown_CodedError asserts that a
// spec whose Reference does not match any label in `labels` surfaces
// a coded PROCESSING_INTERNAL error whose Details carry the canonical
// PULSE_OVERLAY_REFERENCE_UNKNOWN code (lifted by E7-S5 from the
// pre-E7-S5 PULSE_OVERLAY_REF_UNKNOWN fallback).
func TestApplyComposeOverlays_ReferenceUnknown_CodedError(t *testing.T) {
	responses := []*types.Response{
		composeSlotOf(types.OverlayShapeScalar),
		composeSlotOf(types.OverlayShapeScalar),
	}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{{
		Name:      "bad_ref",
		Kind:      types.OverlayKindIndexVsStage,
		Scope:     types.OverlayScopeTotal,
		Reference: "does_not_exist",
		Targets:   []string{"treatment"},
	}}
	_, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err == nil {
		t.Fatal("ApplyComposeOverlays(unknown reference): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN)
}

// TestApplyComposeOverlays_TargetUnknown_CodedError asserts that a
// spec whose Targets list contains a label that does not match any
// entry in `labels` surfaces a coded PROCESSING_INTERNAL error whose
// Details carry the canonical PULSE_OVERLAY_TARGET_UNKNOWN code
// (lifted by E7-S5 from the pre-E7-S5 PULSE_OVERLAY_REF_UNKNOWN
// fallback).
func TestApplyComposeOverlays_TargetUnknown_CodedError(t *testing.T) {
	responses := []*types.Response{
		composeSlotOf(types.OverlayShapeScalar),
		composeSlotOf(types.OverlayShapeScalar),
	}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{{
		Name:      "bad_target",
		Kind:      types.OverlayKindIndexVsStage,
		Scope:     types.OverlayScopeTotal,
		Reference: "baseline",
		Targets:   []string{"does_not_exist"},
	}}
	_, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err == nil {
		t.Fatal("ApplyComposeOverlays(unknown target): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_TARGET_UNKNOWN)
}

// requireDetailCode asserts the error's CodedError.Details carry a
// `code` key equal to the supplied canonical code string. Used by the
// E7-S5 chassis tests to lock the canonical-code dispatch lifted from
// the legacy PULSE_OVERLAY_REF_UNKNOWN fallback.
func requireDetailCode(t *testing.T, err error, want pulseerrors.Code) {
	t.Helper()
	var coded *pulseerrors.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	got, ok := coded.Details["code"].(string)
	if !ok {
		t.Fatalf("CodedError.Details[code] is not a string: %T (%v)", coded.Details["code"], coded.Details)
	}
	if got != string(want) {
		t.Fatalf("CodedError.Details[code] = %q, want %q", got, string(want))
	}
}

// TestApplyComposeOverlays_TargetsEmpty_CodedError asserts that a spec
// whose Targets slice is empty surfaces a coded error — every
// Compose-only kind requires at least one target slot.
func TestApplyComposeOverlays_TargetsEmpty_CodedError(t *testing.T) {
	responses := []*types.Response{composeSlotOf(types.OverlayShapeScalar)}
	labels := []string{"baseline"}
	specs := []types.ComposeOverlaySpec{{
		Name:      "empty_targets",
		Kind:      types.OverlayKindIndexVsStage,
		Scope:     types.OverlayScopeTotal,
		Reference: "baseline",
		Targets:   nil,
	}}
	_, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err == nil {
		t.Fatal("ApplyComposeOverlays(empty targets): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_TARGET_UNKNOWN)
}

// TestApplyComposeOverlays_NilSlot_CodedError asserts that resolving
// a reference / target label whose corresponding *Response slot is
// nil (the FailFast=false failed-slot hole) surfaces a coded error
// rather than dereferencing the nil downstream.
func TestApplyComposeOverlays_NilSlot_CodedError(t *testing.T) {
	responses := []*types.Response{
		composeSlotOf(types.OverlayShapeScalar),
		nil, // slot 1 failed under FailFast=false
	}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{{
		Name:      "nil_target",
		Kind:      types.OverlayKindIndexVsStage,
		Scope:     types.OverlayScopeTotal,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}}
	_, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err == nil {
		t.Fatal("ApplyComposeOverlays(nil target slot): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_TARGET_UNKNOWN)
}
