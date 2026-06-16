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

func TestApplyComposeOverlays_MultiLayerDispatch_PanelIndexVsRef(t *testing.T) {
	// Three slots: baseline (reference) + t0 + t1 + t2 (3 targets).
	// Every slot carries the same canonical 3×3 matrix shape so the
	// chassis schema-match + key-alignment gates accept; the per-target
	// math is target / ref * 100 = 200 across the grid.
	ref := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	t0 := makeMatrixFromValues([3][3]float64{
		{20, 40, 60},
		{80, 100, 120},
		{140, 160, 180},
	})
	t1 := makeMatrixFromValues([3][3]float64{
		{20, 40, 60},
		{80, 100, 120},
		{140, 160, 180},
	})
	t2 := makeMatrixFromValues([3][3]float64{
		{20, 40, 60},
		{80, 100, 120},
		{140, 160, 180},
	})
	responses := []*types.Response{ref, t0, t1, t2}
	labels := []string{"baseline", "t0", "t1", "t2"}
	specs := []types.ComposeOverlaySpec{{
		Name:      "panel",
		Kind:      types.OverlayKindPanelIndexVsRef,
		Scope:     types.OverlayScopeCell,
		Reference: "baseline",
		Targets:   []string{"t0", "t1", "t2"},
	}}
	layers, warnings, err := ApplyComposeOverlays(specs, responses, labels)
	if err != nil {
		t.Fatalf("ApplyComposeOverlays: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	// One spec, three targets ⇒ three layers in the parent layers slice.
	if got, want := len(layers), 3; got != want {
		t.Fatalf("len(layers) = %d, want %d (multi-layer chassis dispatch)", got, want)
	}
	// Every emitted layer carries the parent kind, not the underlying
	// single-target sibling (INDEX_VS_REF).
	for i, layer := range layers {
		if layer.Kind != types.OverlayKindPanelIndexVsRef {
			t.Errorf("layers[%d].Kind = %q, want %q", i, layer.Kind, types.OverlayKindPanelIndexVsRef)
		}
	}
	// Naming convention.
	wantNames := []string{"panel__t0", "panel__t1", "panel__t2"}
	for i, want := range wantNames {
		if layers[i].Name != want {
			t.Errorf("layers[%d].Name = %q, want %q", i, layers[i].Name, want)
		}
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
				composeSlotOf(tc.refShape),
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
