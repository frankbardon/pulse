package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// E7-S14 — predict-time validator for ComposedRequest.Overlays.
//
// Coverage matrix mirrors descriptor/chain_overlay_test.go: kind-
// unknown, reference / target arm resolution, slot-shape divergence,
// slot-not-crosstab, schema-divergent, panel-targets-over-cap, the
// happy path, and the FR-I3 OverlaysSchemaDivergence echo. Per-helper
// sync tests pin kindRequiresMatrixCompose + composeDescriptorDefaultLabel
// against their processing/ originals so the duplicated catalog rows
// cannot drift.

// --- fixtures -------------------------------------------------------

// scalarSumRequest is the SCALAR fixture: a single AGG_SUM with no
// grouper. inferComposeSlotShape → OverlayShapeScalar.
func scalarSumRequest(label string, field string) *types.Request {
	return &types.Request{
		Label: label,
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: field, Label: "sum_" + field},
		},
	}
}

// seriesGroupedSumRequest is the SERIES fixture: AGG_SUM + a single
// grouper. inferComposeSlotShape → OverlayShapeSeries.
func seriesGroupedSumRequest(label, field, groupField string) *types.Request {
	return &types.Request{
		Label: label,
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: field, Label: "sum_" + field},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: groupField},
		},
	}
}

// matrixCrosstabRequest is the MATRIX fixture: a Crosstab with one
// row + one column grouper. inferComposeSlotShape → OverlayShapeMatrix.
func matrixCrosstabRequest(label, rowField, colField string) *types.Request {
	return &types.Request{
		Label: label,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: rowField}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: colField}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// matrixCrosstabRangeRowRequest is a MATRIX variant whose row axis
// uses GROUP_RANGE instead of GROUP_CATEGORY. Paired with
// matrixCrosstabRequest to exercise PULSE_OVERLAY_SCHEMA_DIVERGENT
// (matching shape, mismatched row-axis kind tuple).
func matrixCrosstabRangeRowRequest(label, rowField, colField string) *types.Request {
	return &types.Request{
		Label: label,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_RANGE, Field: rowField}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: colField}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// --- helper sync gates ---------------------------------------------

// TestKindRequiresMatrixCompose_MatchesProcessing pins the descriptor
// catalog (kindRequiresMatrixCompose) against the processing original
// (processing.KindRequiresMatrix exposed via the schema match helper).
// Both surfaces must accept exactly the same set of OverlayKinds; a
// new matrix-required kind landing in processing/ without an
// accompanying descriptor row would break the predict-time gate.
func TestKindRequiresMatrixCompose_MatchesProcessing(t *testing.T) {
	for _, kind := range types.AllOverlayKinds() {
		got := kindRequiresMatrixCompose(kind)
		want := processing.KindRequiresMatrix(kind)
		if got != want {
			t.Errorf("kindRequiresMatrixCompose(%q) = %v, want %v (processing parity)", kind, got, want)
		}
	}
}

// TestComposeDescriptorDefaultLabel_MatchesProcessingHelper pins the
// descriptor's slot-label default (`request_<i+1>`) against the
// runtime helper exposed via processing.ComposeDefaultLabel. The two
// implementations are independently maintained under the no-execute
// structural ban; this test catches any drift on either side.
func TestComposeDescriptorDefaultLabel_MatchesProcessingHelper(t *testing.T) {
	for i := 0; i < 32; i++ {
		got := composeDescriptorDefaultLabel(i)
		want := processing.ComposeDefaultLabel(i)
		if got != want {
			t.Errorf("composeDescriptorDefaultLabel(%d) = %q, want %q (processing parity)", i, got, want)
		}
	}
}

// --- inferComposeSlotShape -----------------------------------------

func TestInferComposeSlotShape_Matrix(t *testing.T) {
	req := matrixCrosstabRequest("r", "row", "col")
	got := inferComposeSlotShape(req)
	if got != types.OverlayShapeMatrix {
		t.Errorf("inferComposeSlotShape(matrix) = %q, want matrix", got)
	}
}

func TestInferComposeSlotShape_Series(t *testing.T) {
	req := seriesGroupedSumRequest("s", "x", "tag")
	got := inferComposeSlotShape(req)
	if got != types.OverlayShapeSeries {
		t.Errorf("inferComposeSlotShape(series) = %q, want series", got)
	}
}

func TestInferComposeSlotShape_Scalar(t *testing.T) {
	req := scalarSumRequest("c", "x")
	got := inferComposeSlotShape(req)
	if got != types.OverlayShapeScalar {
		t.Errorf("inferComposeSlotShape(scalar) = %q, want scalar", got)
	}
}

func TestInferComposeSlotShape_NilReturnsScalar(t *testing.T) {
	got := inferComposeSlotShape(nil)
	if got != types.OverlayShapeScalar {
		t.Errorf("inferComposeSlotShape(nil) = %q, want scalar", got)
	}
}

// --- ValidateCompose gates -----------------------------------------

// TestValidateCompose_HappyPath_NoDivergence exercises the
// always-green path: three SERIES slots with identical schema and a
// well-formed overlay against the first slot. Envelope should carry no
// errors and the divergence slice should be empty.
func TestValidateCompose_HappyPath_NoDivergence(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
			seriesGroupedSumRequest("b", "y", "tag"),
			seriesGroupedSumRequest("c", "z", "tag"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "delta_ab",
				Kind:      types.OverlayKindDeltaVsRef,
				Reference: "a",
				Targets:   []string{"b", "c"},
			},
		},
	}
	env := ValidateCompose(req)
	if len(env.Errors) != 0 {
		t.Errorf("happy path produced errors: %v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	if len(result.OverlaysSchemaDivergence) != 0 {
		t.Errorf("happy path produced divergence: %+v", result.OverlaysSchemaDivergence)
	}
	if !result.Valid {
		t.Errorf("happy path result.Valid = false")
	}
}

func TestValidateCompose_UnknownReference_Coded(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
			seriesGroupedSumRequest("b", "y", "tag"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "bad_ref",
				Kind:      types.OverlayKindDeltaVsRef,
				Reference: "ghost",
				Targets:   []string{"a"},
			},
		},
	}
	env := ValidateCompose(req)
	if len(env.Errors) == 0 {
		t.Fatal("expected error for unknown reference")
	}
	if env.Errors[0].Code != string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN) {
		t.Errorf("Errors[0].Code = %q, want %q", env.Errors[0].Code, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN)
	}
	result := env.Data.(*ComposeValidationResult)
	if len(result.OverlaysSchemaDivergence) != 1 {
		t.Fatalf("OverlaysSchemaDivergence len = %d, want 1", len(result.OverlaysSchemaDivergence))
	}
	if result.OverlaysSchemaDivergence[0].Reason != "reference-unknown" {
		t.Errorf("Reason = %q, want reference-unknown", result.OverlaysSchemaDivergence[0].Reason)
	}
	if result.OverlaysSchemaDivergence[0].ReferenceLabel != "ghost" {
		t.Errorf("ReferenceLabel = %q, want ghost", result.OverlaysSchemaDivergence[0].ReferenceLabel)
	}
}

func TestValidateCompose_UnknownTarget_Coded(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
			seriesGroupedSumRequest("b", "y", "tag"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "bad_target",
				Kind:      types.OverlayKindDeltaVsRef,
				Reference: "a",
				Targets:   []string{"b", "phantom"},
			},
		},
	}
	env := ValidateCompose(req)
	foundUnknownTarget := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_TARGET_UNKNOWN) {
			foundUnknownTarget = true
			break
		}
	}
	if !foundUnknownTarget {
		t.Errorf("expected PULSE_OVERLAY_TARGET_UNKNOWN, got %+v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	foundPair := false
	for _, p := range result.OverlaysSchemaDivergence {
		if p.Reason == "target-unknown" && p.TargetLabel == "phantom" {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Errorf("expected target-unknown SlotPair with TargetLabel=phantom, got %+v", result.OverlaysSchemaDivergence)
	}
}

func TestValidateCompose_PanelTargetsOverCap(t *testing.T) {
	// 18 slots → over default cap of 16.
	requests := make([]*types.Request, 0, 18)
	requests = append(requests, matrixCrosstabRequest("ref", "row", "col"))
	targets := make([]string, 0, 17)
	for i := 1; i <= 17; i++ {
		label := "t" + composeDescriptorItoa(i)
		requests = append(requests, matrixCrosstabRequest(label, "row", "col"))
		targets = append(targets, label)
	}
	req := &types.ComposedRequest{
		Requests: requests,
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "panel",
				Kind:      types.OverlayKindPropZPanel,
				Reference: "ref",
				Targets:   targets,
			},
		},
	}
	env := ValidateCompose(req)
	found := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP, got %+v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	if len(result.OverlaysSchemaDivergence) == 0 || result.OverlaysSchemaDivergence[0].Reason != "panel-targets-over-cap" {
		t.Errorf("expected panel-targets-over-cap SlotPair, got %+v", result.OverlaysSchemaDivergence)
	}
}

func TestValidateCompose_PanelTargetsOverCap_RespectsOptionsOverride(t *testing.T) {
	// 5 slots with MaxPanelTargets=3 → over caller-set cap.
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			matrixCrosstabRequest("ref", "row", "col"),
			matrixCrosstabRequest("t1", "row", "col"),
			matrixCrosstabRequest("t2", "row", "col"),
			matrixCrosstabRequest("t3", "row", "col"),
			matrixCrosstabRequest("t4", "row", "col"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "panel_strict",
				Kind:      types.OverlayKindPropZPanel,
				Reference: "ref",
				Targets:   []string{"t1", "t2", "t3", "t4"},
				Options:   &types.OverlayOptions{MaxPanelTargets: 3},
			},
		},
	}
	env := ValidateCompose(req)
	found := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP with custom cap, got %+v", env.Errors)
	}
}

func TestValidateCompose_SlotShapeDivergent(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"), // SERIES
			scalarSumRequest("b", "y"),               // SCALAR
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "mixed_shape",
				Kind:      types.OverlayKindDeltaVsRef,
				Reference: "a",
				Targets:   []string{"b"},
			},
		},
	}
	env := ValidateCompose(req)
	found := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT, got %+v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	foundPair := false
	for _, p := range result.OverlaysSchemaDivergence {
		if p.Reason == "slot-shape-divergent" && p.ReferenceLabel == "a" && p.TargetLabel == "b" {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Errorf("expected slot-shape-divergent SlotPair(a,b), got %+v", result.OverlaysSchemaDivergence)
	}
}

func TestValidateCompose_SlotNotCrosstab(t *testing.T) {
	// OVERLAY_RANK requires MATRIX; the reference is a SERIES slot.
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
			seriesGroupedSumRequest("b", "y", "tag"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "rank_not_matrix",
				Kind:      types.OverlayKindRank,
				Reference: "a",
				Targets:   []string{"b"},
			},
		},
	}
	env := ValidateCompose(req)
	found := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PULSE_OVERLAY_SLOT_NOT_CROSSTAB, got %+v", env.Errors)
	}
}

func TestValidateCompose_SchemaDivergent_RowGrouperMismatch(t *testing.T) {
	// Both MATRIX, but slot a uses GROUP_CATEGORY on row axis and
	// slot b uses GROUP_RANGE — schema diverges, slot shapes agree.
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			matrixCrosstabRequest("a", "row", "col"),
			matrixCrosstabRangeRowRequest("b", "row", "col"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "mixed_row_kind",
				Kind:      types.OverlayKindIndexVsRef,
				Reference: "a",
				Targets:   []string{"b"},
			},
		},
	}
	env := ValidateCompose(req)
	found := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PULSE_OVERLAY_SCHEMA_DIVERGENT, got %+v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	foundPair := false
	for _, p := range result.OverlaysSchemaDivergence {
		if p.Reason == "schema-divergent" {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Errorf("expected schema-divergent SlotPair, got %+v", result.OverlaysSchemaDivergence)
	}
}

func TestValidateCompose_KindUnknown(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "garbage",
				Kind:      types.OverlayKind("OVERLAY_DOES_NOT_EXIST"),
				Reference: "a",
				Targets:   []string{"a"},
			},
		},
	}
	env := ValidateCompose(req)
	if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_OVERLAY_KIND_UNKNOWN) {
		t.Errorf("expected PULSE_OVERLAY_KIND_UNKNOWN, got %+v", env.Errors)
	}
}

// TestPredict_OverlaysSchemaDivergence_Populated_3Slot is the story
// acceptance gate: a 3-slot Compose where slot[1] schema diverges
// from slot[0]; overlay refs slot[0] and targets slot[1] + slot[2].
// Asserts:
//   - PULSE_OVERLAY_SCHEMA_DIVERGENT fires for the (slot[0], slot[1])
//     pair.
//   - The divergence slice carries exactly one entry (slot[2] is the
//     same shape as slot[0] so it passes).
//   - ReferenceLabel + TargetLabel echo the slot labels and Reason is
//     the stable identifier.
func TestPredict_OverlaysSchemaDivergence_Populated_3Slot(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			matrixCrosstabRequest("a", "row", "col"),         // baseline
			matrixCrosstabRangeRowRequest("b", "row", "col"), // diverges on row axis
			matrixCrosstabRequest("c", "row", "col"),         // matches baseline
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "fan_out",
				Kind:      types.OverlayKindIndexVsRef,
				Reference: "a",
				Targets:   []string{"b", "c"},
			},
		},
	}
	env := ValidateCompose(req)
	if len(env.Errors) == 0 {
		t.Fatal("expected schema-divergent error")
	}
	codeFound := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT) {
			codeFound = true
			break
		}
	}
	if !codeFound {
		t.Errorf("expected PULSE_OVERLAY_SCHEMA_DIVERGENT, got %+v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	if len(result.OverlaysSchemaDivergence) != 1 {
		t.Fatalf("OverlaysSchemaDivergence len = %d, want 1: %+v",
			len(result.OverlaysSchemaDivergence), result.OverlaysSchemaDivergence)
	}
	pair := result.OverlaysSchemaDivergence[0]
	if pair.ReferenceLabel != "a" {
		t.Errorf("ReferenceLabel = %q, want a", pair.ReferenceLabel)
	}
	if pair.TargetLabel != "b" {
		t.Errorf("TargetLabel = %q, want b", pair.TargetLabel)
	}
	if pair.Reason != "schema-divergent" {
		t.Errorf("Reason = %q, want schema-divergent", pair.Reason)
	}
}

// TestValidateCompose_EmptyOverlays_NoError exercises the short-
// circuit path — a ComposedRequest with no overlays returns a clean
// envelope. Mirrors the chain validator's no-op contract.
func TestValidateCompose_EmptyOverlays_NoError(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
		},
	}
	env := ValidateCompose(req)
	if len(env.Errors) != 0 {
		t.Errorf("expected no errors for empty overlays, got %+v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	if !result.Valid {
		t.Errorf("Valid = false on empty overlays")
	}
}

// TestValidateCompose_NilRequest_Coded exercises the defensive nil
// guard.
func TestValidateCompose_NilRequest_Coded(t *testing.T) {
	env := ValidateCompose(nil)
	if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION on nil request, got %+v", env.Errors)
	}
}

// TestValidateCompose_DefaultLabelResolution exercises the
// auto-default `request_<i+1>` rule. The overlay references the slot
// at index 0 via its synthesized label; ValidateCompose must resolve
// it the same way the runtime would.
func TestValidateCompose_DefaultLabelResolution(t *testing.T) {
	// Both slots leave Label empty → labels become request_1, request_2.
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("", "x", "tag"),
			seriesGroupedSumRequest("", "y", "tag"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "auto_labels",
				Kind:      types.OverlayKindDeltaVsRef,
				Reference: "request_1",
				Targets:   []string{"request_2"},
			},
		},
	}
	env := ValidateCompose(req)
	if len(env.Errors) != 0 {
		t.Errorf("expected no errors with auto-default labels, got %+v", env.Errors)
	}
}
