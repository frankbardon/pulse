package processing

import (
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// chainStageOf returns a *types.Response whose host shape matches the
// requested OverlayShape. Used by the dispatch chassis tests to build
// synthetic stage results without going through the buffered Process
// path.
func chainStageOf(shape types.OverlayShape) *types.Response {
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

// ptrInt returns a pointer to i, matching the StageRef Index slot's
// pointer-to-int contract (the zero value is meaningful, so a literal
// int field would collapse "no index supplied" onto "stage 0").
func ptrInt(i int) *int { return &i }

// TestApplyChainOverlays_EmptySpecsShortCircuits asserts the chassis
// returns nil layers / nil warnings / nil error when the spec slice
// is empty — the byte-identity guard against pre-E6-S3 ChainResponse
// shapes (no Overlays slot allocated, no JSON key emitted).
func TestApplyChainOverlays_EmptySpecsShortCircuits(t *testing.T) {
	stages := []*types.Response{chainStageOf(types.OverlayShapeMatrix)}

	layers, warnings, err := ApplyChainOverlays(nil, stages, nil)
	if err != nil {
		t.Fatalf("ApplyChainOverlays(nil specs): %v", err)
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil (short-circuit)", layers)
	}
	if warnings != nil {
		t.Errorf("warnings = %+v, want nil (short-circuit)", warnings)
	}

	layers, _, err = ApplyChainOverlays([]*types.ChainOverlaySpec{}, stages, nil)
	if err != nil {
		t.Fatalf("ApplyChainOverlays(empty specs): %v", err)
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil (short-circuit)", layers)
	}
}

// TestApplyChainOverlays_StubLayerOrderPreserved asserts two specs in
// order produce two layers in matching index order with the right
// Kind echoed verbatim. The stub handler emits exactly one layer per
// spec; the chassis preserves spec-order emission (FR-A2).
func TestApplyChainOverlays_StubLayerOrderPreserved(t *testing.T) {
	stages := []*types.Response{
		chainStageOf(types.OverlayShapeSeries),
		chainStageOf(types.OverlayShapeSeries),
	}
	specs := []*types.ChainOverlaySpec{
		{
			Name:   "first_index",
			Kind:   types.OverlayKindIndexVsStage,
			Scope:  types.OverlayScopeTotal,
			Ref:    types.StageRef{Index: ptrInt(0)},
			Target: types.StageRef{Index: ptrInt(1)},
		},
		{
			Name:   "second_delta",
			Kind:   types.OverlayKindDeltaVsStage,
			Scope:  types.OverlayScopeTotal,
			Ref:    types.StageRef{Index: ptrInt(0)},
			Target: types.StageRef{Index: ptrInt(1)},
		},
	}

	layers, _, err := ApplyChainOverlays(specs, stages, []string{"a", "b"})
	if err != nil {
		t.Fatalf("ApplyChainOverlays: %v", err)
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
	if got, want := layers[0].Name, "first_index"; got != want {
		t.Errorf("layers[0].Name = %q, want %q", got, want)
	}
	if got, want := layers[1].Name, "second_delta"; got != want {
		t.Errorf("layers[1].Name = %q, want %q", got, want)
	}
}

// TestApplyChainOverlays_StageRefByIndexHappyPath asserts the
// resolver consults `StageRef.Index` directly when populated and
// hands the resolved *Response off to the handler. The stub handler
// inherits the target stage's host shape, so the emitted layer's
// Payload.Shape is the canonical proof that the resolver picked the
// right stage.
func TestApplyChainOverlays_StageRefByIndexHappyPath(t *testing.T) {
	// Two distinguishable stages — stage 0 carries a series shape;
	// stage 1 carries a matrix shape. The spec targets stage 1 by
	// Index, so the emitted layer must carry matrix shape.
	stages := []*types.Response{
		chainStageOf(types.OverlayShapeSeries),
		chainStageOf(types.OverlayShapeMatrix),
	}
	specs := []*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Scope:  types.OverlayScopeTotal,
			Ref:    types.StageRef{Index: ptrInt(0)},
			Target: types.StageRef{Index: ptrInt(1)},
		},
	}

	layers, _, err := ApplyChainOverlays(specs, stages, []string{"", ""})
	if err != nil {
		t.Fatalf("ApplyChainOverlays: %v", err)
	}
	if got, want := layers[0].Payload.Shape, types.OverlayShapeMatrix; got != want {
		t.Errorf("layers[0].Payload.Shape = %q, want %q (Target=Index{1} should resolve to matrix-host stage)", got, want)
	}
}

// TestApplyChainOverlays_StageRefByNameHappyPath asserts the
// resolver consults the namedIndex lookup when StageRef.Name is
// populated. The target stage name resolves to stage 1; the resulting
// layer shape matches stage 1's host shape.
func TestApplyChainOverlays_StageRefByNameHappyPath(t *testing.T) {
	stages := []*types.Response{
		chainStageOf(types.OverlayShapeSeries),
		chainStageOf(types.OverlayShapeMatrix),
	}
	specs := []*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindDeltaVsStage,
			Scope:  types.OverlayScopeTotal,
			Ref:    types.StageRef{Name: "first_stage"},
			Target: types.StageRef{Name: "second_stage"},
		},
	}

	layers, _, err := ApplyChainOverlays(specs, stages, []string{"first_stage", "second_stage"})
	if err != nil {
		t.Fatalf("ApplyChainOverlays: %v", err)
	}
	if got, want := layers[0].Payload.Shape, types.OverlayShapeMatrix; got != want {
		t.Errorf("layers[0].Payload.Shape = %q, want %q (Target=Name{second_stage} should resolve to matrix-host stage)", got, want)
	}
}

// TestApplyChainOverlays_TargetDefaultsToLatest asserts that an
// empty Target (both Index nil AND Name empty) defaults to the
// latest stage (len(stages) - 1) per the research doc convention.
// The two-stage fixture's latest stage is stage 1 (matrix shape);
// the stub layer's Payload.Shape must match.
func TestApplyChainOverlays_TargetDefaultsToLatest(t *testing.T) {
	stages := []*types.Response{
		chainStageOf(types.OverlayShapeSeries),
		chainStageOf(types.OverlayShapeMatrix),
	}
	specs := []*types.ChainOverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsStage,
			Scope: types.OverlayScopeTotal,
			Ref:   types.StageRef{Index: ptrInt(0)},
			// Target slot intentionally zero-value — the resolver must
			// default it to the latest stage.
		},
	}

	layers, _, err := ApplyChainOverlays(specs, stages, []string{"", ""})
	if err != nil {
		t.Fatalf("ApplyChainOverlays: %v", err)
	}
	if got, want := layers[0].Payload.Shape, types.OverlayShapeMatrix; got != want {
		t.Errorf("layers[0].Payload.Shape = %q, want %q (default Target should resolve to latest stage)", got, want)
	}
}

// TestApplyChainOverlays_StageRefOutOfRange_CodedError asserts an
// Index that lands outside [0, len(stages)) fires a coded error. The
// canonical PULSE_OVERLAY_TARGET_UNKNOWN code (Target arm) landed with
// E6-S6 along with PULSE_OVERLAY_REFERENCE_UNKNOWN (Ref arm); arms are
// distinguished by the `which` Detail.
func TestApplyChainOverlays_StageRefOutOfRange_CodedError(t *testing.T) {
	stages := []*types.Response{chainStageOf(types.OverlayShapeSeries)}
	specs := []*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Scope:  types.OverlayScopeTotal,
			Ref:    types.StageRef{Index: ptrInt(0)},
			Target: types.StageRef{Index: ptrInt(99)}, // out of range
		},
	}

	_, _, err := ApplyChainOverlays(specs, stages, []string{""})
	if err == nil {
		t.Fatalf("ApplyChainOverlays: want coded error for out-of-range Target")
	}
	ce, ok := err.(*pulseerrors.CodedError)
	if !ok {
		t.Fatalf("err = %T, want *errors.CodedError", err)
	}
	if got, want := ce.Code, pulseerrors.PROCESSING_INTERNAL; got != want {
		t.Errorf("err.Code = %q, want %q", got, want)
	}
	// Details carry the canonical Target-arm code + which arm fired.
	if got := ce.Details["code"]; got != string(pulseerrors.PULSE_OVERLAY_TARGET_UNKNOWN) {
		t.Errorf("Details.code = %v, want %q",
			got, pulseerrors.PULSE_OVERLAY_TARGET_UNKNOWN)
	}
	if got := ce.Details["which"]; got != "target" {
		t.Errorf("Details.which = %v, want \"target\"", got)
	}
}

// TestApplyChainOverlays_StageRefUnknownName_CodedError asserts that
// a Name that does not match any ChainStage.Name fires the same
// coded error shape. Ref arm vs Target arm is distinguished via the
// `which` Detail; the Ref arm carries PULSE_OVERLAY_REFERENCE_UNKNOWN
// (canonical chain-overlay missing-reference code, landed with E6-S6).
func TestApplyChainOverlays_StageRefUnknownName_CodedError(t *testing.T) {
	stages := []*types.Response{chainStageOf(types.OverlayShapeSeries)}
	specs := []*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Scope:  types.OverlayScopeTotal,
			Ref:    types.StageRef{Name: "does_not_exist"},
			Target: types.StageRef{Index: ptrInt(0)},
		},
	}

	_, _, err := ApplyChainOverlays(specs, stages, []string{"some_other_name"})
	if err == nil {
		t.Fatalf("ApplyChainOverlays: want coded error for unknown Ref Name")
	}
	ce, ok := err.(*pulseerrors.CodedError)
	if !ok {
		t.Fatalf("err = %T, want *errors.CodedError", err)
	}
	if got := ce.Details["which"]; got != "ref" {
		t.Errorf("Details.which = %v, want \"ref\"", got)
	}
	if got := ce.Details["code"]; got != string(pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN) {
		t.Errorf("Details.code = %v, want %q",
			got, pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN)
	}
	if got := ce.Details["stage_name"]; got != "does_not_exist" {
		t.Errorf("Details.stage_name = %v, want \"does_not_exist\"", got)
	}
}

// TestApplyChainOverlays_UnknownKind_CodedError asserts that a spec
// naming an OverlayKind without a CHAIN-host handler entry fires the
// PROCESSING_INTERNAL + PULSE_OVERLAY_KIND_UNKNOWN coded shape (same
// envelope shape the MATRIX / SERIES / FACET dispatchers emit).
func TestApplyChainOverlays_UnknownKind_CodedError(t *testing.T) {
	stages := []*types.Response{chainStageOf(types.OverlayShapeSeries)}
	specs := []*types.ChainOverlaySpec{
		{
			// OVERLAY_INDEX_VS_MARGIN is a MATRIX-host kind — it has
			// no entry in the chainOverlayHandlers table. Defense in
			// depth: validates the dispatcher's unknown-kind branch.
			Kind:   types.OverlayKindIndexVsMargin,
			Scope:  types.OverlayScopeTotal,
			Ref:    types.StageRef{Index: ptrInt(0)},
			Target: types.StageRef{Index: ptrInt(0)},
		},
	}

	_, _, err := ApplyChainOverlays(specs, stages, []string{""})
	if err == nil {
		t.Fatalf("ApplyChainOverlays: want coded error for unknown CHAIN-host kind")
	}
	ce, ok := err.(*pulseerrors.CodedError)
	if !ok {
		t.Fatalf("err = %T, want *errors.CodedError", err)
	}
	if got, want := ce.Code, pulseerrors.PROCESSING_INTERNAL; got != want {
		t.Errorf("err.Code = %q, want %q", got, want)
	}
	if got := ce.Details["code"]; got != string(pulseerrors.PULSE_OVERLAY_KIND_UNKNOWN) {
		t.Errorf("Details.code = %v, want %q",
			got, pulseerrors.PULSE_OVERLAY_KIND_UNKNOWN)
	}
	if got := ce.Details["host"]; got != "chain" {
		t.Errorf("Details.host = %v, want \"chain\"", got)
	}
}
