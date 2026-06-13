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

// matrixCrosstabDateColRequest is a MATRIX variant whose column axis
// uses GROUP_DATE instead of GROUP_CATEGORY. Paired with
// matrixCrosstabRequest to exercise PULSE_OVERLAY_SCHEMA_DIVERGENT
// on the COLUMN axis (E10-S2 col-grouper-kind divergence dimension).
func matrixCrosstabDateColRequest(label, rowField, colField string) *types.Request {
	return &types.Request{
		Label: label,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: rowField}},
			Columns: []*types.Group{{Type: types.GROUP_DATE, Field: colField}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// matrixCrosstabNestedRowRequest is a MATRIX variant whose row axis
// is nested two-deep (two row groupers). Paired with the standard
// single-row matrixCrosstabRequest to exercise the nested-depth
// dimension of PULSE_OVERLAY_SCHEMA_DIVERGENT (E10-S2).
func matrixCrosstabNestedRowRequest(label, rowFieldA, rowFieldB, colField string) *types.Request {
	return &types.Request{
		Label: label,
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: rowFieldA},
				{Type: types.GROUP_CATEGORY, Field: rowFieldB},
			},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: colField}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// matrixCrosstabRenamedFieldsRequest is a MATRIX variant whose row
// and column groupers carry DIFFERENT Field names from the baseline
// (matrixCrosstabRequest) but identical grouper-Type tuples. Used to
// pin the E10-S2 "field-name divergence NOT reported" rule —
// composeRequestAxisTuples MUST drop Field and key only on Type.
func matrixCrosstabRenamedFieldsRequest(label, rowField, colField string) *types.Request {
	return &types.Request{
		Label: label,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: rowField}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: colField}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// seriesRangeGroupedRequest is the SERIES fixture with GROUP_RANGE
// instead of GROUP_CATEGORY. Paired with seriesGroupedSumRequest to
// exercise the SERIES-arm grouper-Type divergence dimension
// (E10-S2 "type divergence" — distinct grouper Type values on
// otherwise-identical SERIES slots).
func seriesRangeGroupedRequest(label, field, groupField string) *types.Request {
	return &types.Request{
		Label: label,
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: field, Label: "sum_" + field},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_RANGE, Field: groupField, Interval: 10},
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

// TestValidateCompose_SchemaDivergent_ColGrouperMismatch is the
// E10-S2 column-axis divergence dimension: both slots are MATRIX with
// identical row-axis grouper-Type tuples, but the column axis kinds
// disagree (GROUP_CATEGORY vs GROUP_DATE). PULSE_OVERLAY_SCHEMA_DIVERGENT
// must fire and the SlotPair must echo (a, b) with Reason="schema-divergent".
func TestValidateCompose_SchemaDivergent_ColGrouperMismatch(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			matrixCrosstabRequest("a", "row", "col"),
			matrixCrosstabDateColRequest("b", "row", "col"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "mixed_col_kind",
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
		t.Errorf("expected PULSE_OVERLAY_SCHEMA_DIVERGENT for col-grouper divergence, got %+v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	foundPair := false
	for _, p := range result.OverlaysSchemaDivergence {
		if p.Reason == "schema-divergent" && p.ReferenceLabel == "a" && p.TargetLabel == "b" {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Errorf("expected schema-divergent SlotPair(a,b) for col-grouper divergence, got %+v", result.OverlaysSchemaDivergence)
	}
}

// TestValidateCompose_SchemaDivergent_NestedDepthMismatch is the
// E10-S2 nested-depth dimension: both slots are MATRIX with identical
// per-grouper Types but the row axis is single-deep on the reference
// and two-deep on the target. PULSE_OVERLAY_SCHEMA_DIVERGENT must
// fire — the structural match treats grouper depth as part of the
// schema (composeRequestAxisTuples returns different-length tuples).
func TestValidateCompose_SchemaDivergent_NestedDepthMismatch(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			matrixCrosstabRequest("a", "row", "col"),
			matrixCrosstabNestedRowRequest("b", "row", "row2", "col"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "depth_mismatch",
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
		t.Errorf("expected PULSE_OVERLAY_SCHEMA_DIVERGENT for nested-depth divergence, got %+v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	foundPair := false
	for _, p := range result.OverlaysSchemaDivergence {
		if p.Reason == "schema-divergent" && p.ReferenceLabel == "a" && p.TargetLabel == "b" {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Errorf("expected schema-divergent SlotPair(a,b) for nested-depth divergence, got %+v", result.OverlaysSchemaDivergence)
	}
}

// TestValidateCompose_SchemaDivergent_SeriesTypeMismatch is the
// E10-S2 SERIES-arm grouper-Type dimension: both slots are SERIES
// with a single grouper each but the Types disagree (GROUP_CATEGORY
// vs GROUP_RANGE). PULSE_OVERLAY_SCHEMA_DIVERGENT must fire —
// composeRequestAxisTuples reads Group.Type for the series arm so
// distinct grouper kinds produce distinct row tuples even with
// matching Fields.
func TestValidateCompose_SchemaDivergent_SeriesTypeMismatch(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),  // GROUP_CATEGORY
			seriesRangeGroupedRequest("b", "y", "tag"), // GROUP_RANGE
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "series_type_mismatch",
				Kind:      types.OverlayKindDeltaVsRef,
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
		t.Errorf("expected PULSE_OVERLAY_SCHEMA_DIVERGENT for series type divergence, got %+v", env.Errors)
	}
	result := env.Data.(*ComposeValidationResult)
	foundPair := false
	for _, p := range result.OverlaysSchemaDivergence {
		if p.Reason == "schema-divergent" && p.ReferenceLabel == "a" && p.TargetLabel == "b" {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Errorf("expected schema-divergent SlotPair(a,b) for series type divergence, got %+v", result.OverlaysSchemaDivergence)
	}
}

// TestValidateCompose_FieldNameDivergence_NotReported pins the
// E10-S2 "Field-name divergence NOT reported" rule: two MATRIX slots
// with structurally identical grouper Types but DIFFERENT per-grouper
// Field values must not surface PULSE_OVERLAY_SCHEMA_DIVERGENT.
// composeRequestAxisTuples drops Field by design — only Type
// participates in the structural match, so column renames are silent.
func TestValidateCompose_FieldNameDivergence_NotReported(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			matrixCrosstabRequest("a", "row_orig", "col_orig"),
			matrixCrosstabRenamedFieldsRequest("b", "row_renamed", "col_renamed"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "field_rename_ok",
				Kind:      types.OverlayKindIndexVsRef,
				Reference: "a",
				Targets:   []string{"b"},
			},
		},
	}
	env := ValidateCompose(req)
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT) {
			t.Errorf("PULSE_OVERLAY_SCHEMA_DIVERGENT must NOT fire on field-name-only divergence; got %+v", e)
		}
	}
	result := env.Data.(*ComposeValidationResult)
	for _, p := range result.OverlaysSchemaDivergence {
		if p.Reason == "schema-divergent" {
			t.Errorf("schema-divergent SlotPair must NOT be emitted on field-name-only divergence; got %+v", p)
		}
	}
}

// TestValidateCompose_OverlaysSchemaDivergence_NeverNil pins the
// PRD §I-FR-I3 invariant: ComposeValidationResult.OverlaysSchemaDivergence
// is always a non-nil slice (empty array in JSON renderers when no
// divergence fires). Mirrors the same contract on PredictResult.
func TestValidateCompose_OverlaysSchemaDivergence_NeverNil(t *testing.T) {
	// No overlays at all — early-return path.
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
		},
	}
	env := ValidateCompose(req)
	result := env.Data.(*ComposeValidationResult)
	if result.OverlaysSchemaDivergence == nil {
		t.Error("OverlaysSchemaDivergence is nil; must be non-nil empty slice for JSON contract")
	}
	if len(result.OverlaysSchemaDivergence) != 0 {
		t.Errorf("expected empty slice, got %+v", result.OverlaysSchemaDivergence)
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

// TestValidateCompose_OverlayCost_SingleTargetBaseline pins the per-spec
// OverlayCost emission for a single-target ComposeOverlaySpec — the cost
// dispatcher must surface the bare kind cost (no multi-target scaling)
// for every non-panel kind. E10-S3 establishes the cost map contract on
// ComposeValidationResult sibling to PredictResult.OverlayCost /
// FacetValidationResult.OverlayCost.
func TestValidateCompose_OverlayCost_SingleTargetBaseline(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
			seriesGroupedSumRequest("b", "y", "tag"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "delta_single",
				Kind:      types.OverlayKindDeltaVsRef,
				Reference: "a",
				Targets:   []string{"b"},
			},
		},
	}
	env := ValidateCompose(req)
	result := env.Data.(*ComposeValidationResult)
	if result.OverlayCost == nil {
		t.Fatalf("OverlayCost = nil; expected non-nil map")
	}
	cost, ok := result.OverlayCost["delta_single"]
	if !ok {
		t.Fatalf("OverlayCost missing key %q; keys: %v", "delta_single", composeCostKeys(result.OverlayCost))
	}
	// DELTA_VS_REF is streamable via its SERIES handler.
	want := overlayCostForKind(types.OverlayKindDeltaVsRef)
	if cost != want {
		t.Errorf("OverlayCost[delta_single] = %v, want %v (single-target DELTA_VS_REF, no panel scaling)",
			cost, want)
	}
}

// TestValidateCompose_OverlayCost_MultiRefScaling locks the multi-ref
// cost-scaling rule. The two multi-reference COMPOSE kinds
// (OVERLAY_PROP_Z_PANEL, OVERLAY_PANEL_INDEX_VS_REF) scale the per-spec
// cost by len(Targets) so renderers see a per-panel fan-out budget that
// reflects the actual emission shape (one OverlayLayer per target for
// PANEL_INDEX_VS_REF; one pairwise-p value slice for PROP_Z_PANEL).
// Single-target kinds ignore the slice and surface the raw kind cost.
//
// The test exercises both multi-ref kinds (catalog-driven so a future
// multi-ref kind landing here without a matching cost-dispatch entry
// fails closed) and includes the under-cap baseline, the at-cap edge,
// and the over-cap clamp.
func TestValidateCompose_OverlayCost_MultiRefScaling(t *testing.T) {
	// Build a 5-slot fixture: 1 reference + 4 targets. PROP_Z_PANEL
	// requires MATRIX hosts (kindRequiresMatrixCompose), PANEL_INDEX_VS_REF
	// supports both MATRIX and SERIES hosts; both tests use MATRIX fixtures
	// to land on the same fixture surface.
	requests := []*types.Request{
		matrixCrosstabRequest("ref", "row", "col"),
		matrixCrosstabRequest("t1", "row", "col"),
		matrixCrosstabRequest("t2", "row", "col"),
		matrixCrosstabRequest("t3", "row", "col"),
		matrixCrosstabRequest("t4", "row", "col"),
	}

	cases := []struct {
		name        string
		kind        types.OverlayKind
		targets     []string
		options     *types.OverlayOptions
		wantTargets int
	}{
		{
			name:        "PropZPanel_UnderCap_TwoTargets",
			kind:        types.OverlayKindPropZPanel,
			targets:     []string{"t1", "t2"},
			wantTargets: 2,
		},
		{
			name:        "PanelIndexVsRef_UnderCap_ThreeTargets",
			kind:        types.OverlayKindPanelIndexVsRef,
			targets:     []string{"t1", "t2", "t3"},
			wantTargets: 3,
		},
		{
			name:        "PropZPanel_AtCustomCap_FourTargets",
			kind:        types.OverlayKindPropZPanel,
			targets:     []string{"t1", "t2", "t3", "t4"},
			options:     &types.OverlayOptions{MaxPanelTargets: 4},
			wantTargets: 4,
		},
		{
			name:    "PropZPanel_OverCustomCap_FourTargetsCapTwo",
			kind:    types.OverlayKindPropZPanel,
			targets: []string{"t1", "t2", "t3", "t4"},
			options: &types.OverlayOptions{MaxPanelTargets: 2},
			// Clamp at cap: 4 targets but cap is 2 → cost reflects 2.
			wantTargets: 2,
		},
		{
			name:    "PanelIndexVsRef_OverCustomCap_ThreeTargetsCapOne",
			kind:    types.OverlayKindPanelIndexVsRef,
			targets: []string{"t1", "t2", "t3"},
			options: &types.OverlayOptions{MaxPanelTargets: 1},
			// Clamp at cap: 3 targets but cap is 1 → cost reflects 1.
			wantTargets: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &types.ComposedRequest{
				Requests: requests,
				Overlays: []types.ComposeOverlaySpec{
					{
						Name:      "panel",
						Kind:      tc.kind,
						Reference: "ref",
						Targets:   tc.targets,
						Options:   tc.options,
					},
				},
			}
			env := ValidateCompose(req)
			result := env.Data.(*ComposeValidationResult)
			cost, ok := result.OverlayCost["panel"]
			if !ok {
				t.Fatalf("OverlayCost missing key %q; keys: %v", "panel", composeCostKeys(result.OverlayCost))
			}
			base := overlayCostForKind(tc.kind)
			want := base * float64(tc.wantTargets)
			if cost != want {
				t.Errorf("OverlayCost[panel] = %v, want %v (kind %q, %d targets, capped at %d)",
					cost, want, tc.kind, len(tc.targets), tc.wantTargets)
			}
		})
	}
}

// TestValidateCompose_OverlayCost_DefaultCapApplies pins the
// composeDefaultPanelCap (16) clamp when the caller omits
// Options.MaxPanelTargets. A 20-target spec on PROP_Z_PANEL must surface
// a cost reflecting cap=16, not the raw 20.
func TestValidateCompose_OverlayCost_DefaultCapApplies(t *testing.T) {
	// 21 slots: 1 reference + 20 targets. Over the default cap of 16.
	requests := make([]*types.Request, 0, 21)
	requests = append(requests, matrixCrosstabRequest("ref", "row", "col"))
	targets := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
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
				// Options omitted — defaults to composeDefaultPanelCap (16).
			},
		},
	}
	env := ValidateCompose(req)
	result := env.Data.(*ComposeValidationResult)
	cost, ok := result.OverlayCost["panel"]
	if !ok {
		t.Fatalf("OverlayCost missing key %q; keys: %v", "panel", composeCostKeys(result.OverlayCost))
	}
	base := overlayCostForKind(types.OverlayKindPropZPanel)
	want := base * float64(composeDefaultPanelCap)
	if cost != want {
		t.Errorf("OverlayCost[panel] = %v, want %v (20 targets clamped at default cap of %d)",
			cost, want, composeDefaultPanelCap)
	}
}

// TestValidateCompose_OverlayCost_EmptyWhenNoOverlays pins the empty-
// not-nil contract. A ComposedRequest without Overlays must still emit
// OverlayCost as an empty (non-nil) map so the JSON envelope output
// stays byte-identical regardless of whether the caller asked for
// overlays. Sibling contract to
// TestPredict_FacetOverlaysApplied_EmptyWhenNoOverlays.
func TestValidateCompose_OverlayCost_EmptyWhenNoOverlays(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			scalarSumRequest("a", "x"),
			scalarSumRequest("b", "y"),
		},
	}
	env := ValidateCompose(req)
	result := env.Data.(*ComposeValidationResult)
	if result.OverlayCost == nil {
		t.Errorf("OverlayCost = nil; expected empty (non-nil) map")
	}
	if len(result.OverlayCost) != 0 {
		t.Errorf("OverlayCost = %+v; expected empty", result.OverlayCost)
	}
}

// TestValidateCompose_OverlayCost_NoTargetsFallback pins the no-target
// fallback rule on multi-ref kinds. A spec without Targets carries the
// single-target equivalent cost — the per-spec validator surfaces the
// missing-target failure separately. The cost map stays well-formed.
func TestValidateCompose_OverlayCost_NoTargetsFallback(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			matrixCrosstabRequest("ref", "row", "col"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "empty_panel",
				Kind:      types.OverlayKindPropZPanel,
				Reference: "ref",
				// Targets intentionally omitted — multi-ref kind with
				// no targets falls back to single-target cost.
			},
		},
	}
	env := ValidateCompose(req)
	result := env.Data.(*ComposeValidationResult)
	cost, ok := result.OverlayCost["empty_panel"]
	if !ok {
		t.Fatalf("OverlayCost missing key %q; keys: %v", "empty_panel", composeCostKeys(result.OverlayCost))
	}
	want := overlayCostForKind(types.OverlayKindPropZPanel)
	if cost != want {
		t.Errorf("OverlayCost[empty_panel] = %v, want %v (no-target multi-ref fallback to base cost)",
			cost, want)
	}
}

// TestValidateCompose_OverlayCost_NameFallbackToKind exercises the
// composeOverlayDescriptorName synthesized-default fallback: an empty
// Name resolves to the on-wire Kind string so the cost-map key matches
// the runtime composeOverlayLayerName resolution. Mirrors the
// PredictResult / FacetValidationResult name-synthesis contract.
func TestValidateCompose_OverlayCost_NameFallbackToKind(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
			seriesGroupedSumRequest("b", "y", "tag"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				// Name intentionally omitted.
				Kind:      types.OverlayKindDeltaVsRef,
				Reference: "a",
				Targets:   []string{"b"},
			},
		},
	}
	env := ValidateCompose(req)
	result := env.Data.(*ComposeValidationResult)
	// The synthesized key matches the on-wire Kind string per the
	// composeOverlayDescriptorName fallback rule.
	want := string(types.OverlayKindDeltaVsRef)
	if _, ok := result.OverlayCost[want]; !ok {
		t.Errorf("OverlayCost missing synthesised key %q; keys: %v",
			want, composeCostKeys(result.OverlayCost))
	}
}

// composeCostKeys returns a stable-ordered slice of OverlayCost map
// keys for failure messages. Sibling to costKeys in
// predict_overlay_test.go but lives here to keep the compose test file
// self-contained.
func composeCostKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
