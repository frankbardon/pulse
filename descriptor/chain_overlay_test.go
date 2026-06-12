package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E6-S7 — predict-time chain-overlay validator coverage.
//
// Bulk runtime tests live in S8; this file owns the gate-coverage
// matrix: kind unknown, StageRef arms, shape divergence detection, the
// happy path, and the FR-I3 OverlaysSchemaDivergence echo. The bigger
// per-handler equivalence matrix rides alongside the runtime handlers
// in processing/.

func intPtr(v int) *int { return &v }

func makeMergeableSumRequest(field, label string) *types.Request {
	return &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: field, Label: label},
		},
	}
}

func makeMergeableSumGroupedRequest(field, label, groupField string) *types.Request {
	return &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: field, Label: label},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: groupField},
		},
	}
}

// twoStageScalarChainRequest returns a ChainRequest carrying two
// mergeable scalar stages. Stage 0 sums "x" → "sx"; stage 1 sums "sx" →
// "n". Both stages have empty Groups so inferChainStageShape returns
// OverlayShapeScalar for either index. The shape happens to match the
// E6-S4 runtime handler's scalar arm.
func twoStageScalarChainRequest(specs []*types.ChainOverlaySpec) *types.ChainRequest {
	return &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Stages: []*types.ChainStage{
			{
				Name:    "sum_x",
				Request: makeMergeableSumRequest("x", "sx"),
			},
			{
				Name:    "sum_sx",
				Request: makeMergeableSumRequest("sx", "n"),
			},
		},
		Overlays: specs,
	}
}

// twoStageSeriesVsScalarChainRequest returns a ChainRequest whose stages
// disagree on shape: stage 0 is SERIES (grouper + aggregator); stage 1
// is SCALAR (aggregator only). Used to exercise the divergence path.
func twoStageSeriesVsScalarChainRequest(specs []*types.ChainOverlaySpec) *types.ChainRequest {
	return &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Stages: []*types.ChainStage{
			{
				Name:    "group_by_tag",
				Request: makeMergeableSumGroupedRequest("x", "sx", "tag"),
			},
			{
				Name:    "sum_sx",
				Request: makeMergeableSumRequest("sx", "n"),
			},
		},
		Overlays: specs,
	}
}

// twoStageMatrixVsSeriesChainRequest returns a ChainRequest whose
// stages disagree on shape: stage 0 is MATRIX (Crosstab set); stage 1
// is SERIES (grouper + aggregator). MATRIX-vs-SERIES is the canonical
// PRD §6 FR-F2 divergence example.
func twoStageMatrixVsSeriesChainRequest(specs []*types.ChainOverlaySpec) *types.ChainRequest {
	return &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Stages: []*types.ChainStage{
			{
				Name: "crosstab_x_by_tag",
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: "x", Label: "sx"},
					},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tag"}},
						Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tag"}},
						Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "x"},
					},
				},
			},
			{
				Name:    "group_by_tag",
				Request: makeMergeableSumGroupedRequest("x", "sx", "tag"),
			},
		},
		Overlays: specs,
	}
}

// TestInferChainStageShape_Matrix locks in the MATRIX precedence: a
// stage Request whose Crosstab slot is non-nil resolves to MATRIX even
// when Aggregations / Groups are also populated.
func TestInferChainStageShape_Matrix(t *testing.T) {
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tag"}},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tag"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tag"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "x"},
		},
	}
	if got := inferChainStageShape(req); got != types.OverlayShapeMatrix {
		t.Errorf("MATRIX precedence: got %q, want %q", got, types.OverlayShapeMatrix)
	}
}

// TestInferChainStageShape_Series locks in the SERIES branch:
// Aggregations + Groups (and no Crosstab) resolves to SERIES.
func TestInferChainStageShape_Series(t *testing.T) {
	req := makeMergeableSumGroupedRequest("x", "sx", "tag")
	if got := inferChainStageShape(req); got != types.OverlayShapeSeries {
		t.Errorf("SERIES branch: got %q, want %q", got, types.OverlayShapeSeries)
	}
}

// TestInferChainStageShape_Scalar locks in the SCALAR branch:
// Aggregations alone (no Groups, no Crosstab) resolves to SCALAR.
func TestInferChainStageShape_Scalar(t *testing.T) {
	req := makeMergeableSumRequest("x", "sx")
	if got := inferChainStageShape(req); got != types.OverlayShapeScalar {
		t.Errorf("SCALAR branch: got %q, want %q", got, types.OverlayShapeScalar)
	}
}

// TestInferChainStageShape_NilFallthrough confirms nil and
// "no aggregations" both fall through to SCALAR — the defensive
// passthrough that keeps the helper total over every *types.Request
// value the chain gate may not have screened.
func TestInferChainStageShape_NilFallthrough(t *testing.T) {
	if got := inferChainStageShape(nil); got != types.OverlayShapeScalar {
		t.Errorf("nil request: got %q, want %q", got, types.OverlayShapeScalar)
	}
	empty := &types.Request{}
	if got := inferChainStageShape(empty); got != types.OverlayShapeScalar {
		t.Errorf("empty request: got %q, want %q", got, types.OverlayShapeScalar)
	}
}

// TestValidateChain_OverlayKindUnknown confirms that any
// OverlayKind outside the whole-chain catalog (today: only
// OVERLAY_INDEX_VS_STAGE / OVERLAY_DELTA_VS_STAGE) on
// ChainOverlaySpec.Kind raises PULSE_OVERLAY_KIND_UNKNOWN.
func TestValidateChain_OverlayKindUnknown(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageScalarChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsMargin, // MATRIX-host kind; not allowed on ChainRequest.Overlays
			Ref:    types.StageRef{Index: intPtr(0)},
			Target: types.StageRef{Index: intPtr(1)},
			Scope:  types.OverlayScopeCell,
		},
	})
	env := ValidateChainFromBytes(data, req)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_KIND_UNKNOWN) {
		t.Fatalf("expected PULSE_OVERLAY_KIND_UNKNOWN; got %+v", env.Errors)
	}
}

// TestValidateChain_OverlayStageRefIndexOutOfRange exercises the
// out-of-range Index arm for both Ref and Target. Two specs share the
// request so we see both arms emit per-spec.
func TestValidateChain_OverlayStageRefIndexOutOfRange(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageScalarChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Ref:    types.StageRef{Index: intPtr(0)},
			Target: types.StageRef{Index: intPtr(99)},
			Scope:  types.OverlayScopeTotal,
		},
		{
			Kind:   types.OverlayKindDeltaVsStage,
			Ref:    types.StageRef{Index: intPtr(-1)},
			Target: types.StageRef{Index: intPtr(0)},
			Scope:  types.OverlayScopeTotal,
		},
	})
	env := ValidateChainFromBytes(data, req)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_TARGET_UNKNOWN) {
		t.Fatalf("expected PULSE_OVERLAY_TARGET_UNKNOWN; got %+v", env.Errors)
	}
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN) {
		t.Fatalf("expected PULSE_OVERLAY_REFERENCE_UNKNOWN; got %+v", env.Errors)
	}
}

// TestValidateChain_OverlayStageRefByNameUnknown confirms StageRef.Name
// resolution: an unmatched Name fires the matching arm's coded error.
func TestValidateChain_OverlayStageRefByNameUnknown(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageScalarChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Ref:    types.StageRef{Name: "does_not_exist"},
			Target: types.StageRef{Index: intPtr(1)},
			Scope:  types.OverlayScopeTotal,
		},
	})
	env := ValidateChainFromBytes(data, req)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN) {
		t.Fatalf("expected PULSE_OVERLAY_REFERENCE_UNKNOWN; got %+v", env.Errors)
	}
}

// TestValidateChain_OverlayStageRefXORViolated confirms the XOR rule:
// both Index AND Name populated is rejected.
func TestValidateChain_OverlayStageRefXORViolated(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageScalarChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Ref:    types.StageRef{Index: intPtr(0), Name: "sum_x"},
			Target: types.StageRef{Index: intPtr(1)},
			Scope:  types.OverlayScopeTotal,
		},
	})
	env := ValidateChainFromBytes(data, req)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN) {
		t.Fatalf("expected PULSE_OVERLAY_REFERENCE_UNKNOWN; got %+v", env.Errors)
	}
}

// TestValidateChain_OverlayShapeDivergent exercises the MATRIX vs SERIES
// divergence path: stage 0 carries Crosstab (MATRIX), stage 1 carries a
// grouper + aggregator (SERIES). The spec's Ref points to stage 0 and
// Target defaults to the latest stage (1), forcing the shape mismatch.
func TestValidateChain_OverlayShapeDivergent(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageMatrixVsSeriesChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Ref:    types.StageRef{Index: intPtr(0)},
			Target: types.StageRef{Index: intPtr(1)},
			Scope:  types.OverlayScopeTotal,
		},
	})
	env := ValidateChainFromBytes(data, req)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT) {
		t.Fatalf("expected PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT; got %+v", env.Errors)
	}
}

// TestValidateChain_OverlayShapeDivergent_SeriesVsScalar exercises the
// SERIES vs SCALAR divergence: stage 0 has groupers, stage 1 is plain
// aggregations.
func TestValidateChain_OverlayShapeDivergent_SeriesVsScalar(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageSeriesVsScalarChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindDeltaVsStage,
			Ref:    types.StageRef{Index: intPtr(0)},
			Target: types.StageRef{Index: intPtr(1)},
			Scope:  types.OverlayScopeTotal,
		},
	})
	env := ValidateChainFromBytes(data, req)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT) {
		t.Fatalf("expected PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT; got %+v", env.Errors)
	}
}

// TestValidateChain_OverlayShapeMatch_HappyPath confirms that when both
// referenced stages share a shape the validator emits no overlay-
// related error.
func TestValidateChain_OverlayShapeMatch_HappyPath(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageScalarChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Ref:    types.StageRef{Index: intPtr(0)},
			Target: types.StageRef{Index: intPtr(1)},
			Scope:  types.OverlayScopeTotal,
		},
		{
			Kind:   types.OverlayKindDeltaVsStage,
			Ref:    types.StageRef{Name: "sum_x"},
			Target: types.StageRef{Name: "sum_sx"},
			Scope:  types.OverlayScopeTotal,
		},
	})
	env := ValidateChainFromBytes(data, req)
	// The per-stage gate may add SERVICE_VALIDATION (e.g. "x" not in
	// stage 1's synthesised schema), but no overlay-related code should
	// appear.
	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REFERENCE_UNKNOWN,
		errors.PULSE_OVERLAY_TARGET_UNKNOWN,
		errors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected %s in happy-path envelope: %+v", code, env.Errors)
		}
	}
	res, ok := env.Data.(*ChainValidationResult)
	if !ok || res == nil {
		t.Fatalf("expected ChainValidationResult; got %T", env.Data)
	}
	if len(res.OverlaysSchemaDivergence) != 0 {
		t.Errorf("expected OverlaysSchemaDivergence empty; got %+v", res.OverlaysSchemaDivergence)
	}
}

// TestValidateChain_OverlaysSchemaDivergencePopulated confirms FR-I3:
// every rejected (Ref, Target) pair lands in
// ChainValidationResult.OverlaysSchemaDivergence with the resolved
// indices + shapes, alongside the matching envelope error.
func TestValidateChain_OverlaysSchemaDivergencePopulated(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageMatrixVsSeriesChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Ref:    types.StageRef{Index: intPtr(0)},
			Target: types.StageRef{Index: intPtr(1)},
			Scope:  types.OverlayScopeTotal,
		},
	})
	env := ValidateChainFromBytes(data, req)
	res, ok := env.Data.(*ChainValidationResult)
	if !ok || res == nil {
		t.Fatalf("expected ChainValidationResult; got %T", env.Data)
	}
	if len(res.OverlaysSchemaDivergence) != 1 {
		t.Fatalf("expected 1 divergence entry; got %d (%+v)",
			len(res.OverlaysSchemaDivergence), res.OverlaysSchemaDivergence)
	}
	d := res.OverlaysSchemaDivergence[0]
	if d.Index != 0 {
		t.Errorf("divergence.Index = %d, want 0", d.Index)
	}
	if d.Kind != types.OverlayKindIndexVsStage {
		t.Errorf("divergence.Kind = %q, want %q", d.Kind, types.OverlayKindIndexVsStage)
	}
	if d.RefIndex != 0 {
		t.Errorf("divergence.RefIndex = %d, want 0", d.RefIndex)
	}
	if d.TargetIndex != 1 {
		t.Errorf("divergence.TargetIndex = %d, want 1", d.TargetIndex)
	}
	if d.RefShape != types.OverlayShapeMatrix {
		t.Errorf("divergence.RefShape = %q, want %q", d.RefShape, types.OverlayShapeMatrix)
	}
	if d.TargetShape != types.OverlayShapeSeries {
		t.Errorf("divergence.TargetShape = %q, want %q", d.TargetShape, types.OverlayShapeSeries)
	}
}

// TestValidateChain_OverlayTargetDefaultsToLatestStage confirms the
// runtime resolver contract: when both Target slots are empty the
// validator resolves Target to len(stages)-1.
func TestValidateChain_OverlayTargetDefaultsToLatestStage(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageScalarChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Ref:    types.StageRef{Index: intPtr(0)},
			Target: types.StageRef{}, // default to latest stage (1)
			Scope:  types.OverlayScopeTotal,
		},
	})
	env := ValidateChainFromBytes(data, req)
	// No overlay error should fire (shapes match — both scalar).
	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_TARGET_UNKNOWN,
		errors.PULSE_OVERLAY_REFERENCE_UNKNOWN,
		errors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT,
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected %s when Target defaults: %+v", code, env.Errors)
		}
	}
}

// TestValidateChain_OverlayRefMustBeExplicit confirms the runtime rule:
// an empty Ref (both slots nil/empty) is rejected — only Target carries
// a "latest stage" default.
func TestValidateChain_OverlayRefMustBeExplicit(t *testing.T) {
	data := buildSimplePulseBytes(t)
	req := twoStageScalarChainRequest([]*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Ref:    types.StageRef{}, // empty — must fire
			Target: types.StageRef{Index: intPtr(1)},
			Scope:  types.OverlayScopeTotal,
		},
	})
	env := ValidateChainFromBytes(data, req)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN) {
		t.Fatalf("expected PULSE_OVERLAY_REFERENCE_UNKNOWN; got %+v", env.Errors)
	}
}

