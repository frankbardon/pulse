package processing

import (
	stderrors "errors"
	"reflect"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// schemaMatrixSlot returns a *types.Response carrying a Crosstab
// matrix whose RowHeader / ColumnHeader axis types match the
// passed-in (rowKinds, colKinds) tuples. Cells / keys are populated
// to be aligned so the upstream key-set gate (E7-S6) passes — the
// E7-S7 schema-match gate fires only when the key-set gate has
// already accepted the slot pair.
//
// The row/column keys are deterministic placeholders ({"r0"}, ...)
// so two slots constructed with the same (rowCount, colCount) pair
// produce byte-identical key sets regardless of the axis-type
// tuple — that's exactly the surface the E7-S7 schema-match gate
// targets (key-set agrees, structure differs).
func schemaMatrixSlot(rowKinds, colKinds []string, rowCount, colCount int) *types.Response {
	rowKeys := make([]types.AxisKey, rowCount)
	for i := range rowKeys {
		rowKeys[i] = types.AxisKey{rowLabel(i)}
	}
	colKeys := make([]types.AxisKey, colCount)
	for j := range colKeys {
		colKeys[j] = types.AxisKey{colLabel(j)}
	}
	cells := make([][]types.MatrixCell, rowCount)
	for i := range cells {
		row := make([]types.MatrixCell, colCount)
		for j := range row {
			row[j] = types.MatrixCell{Present: true, Value: 0.0}
		}
		cells[i] = row
	}
	return &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: append([]string(nil), rowKinds...)},
				ColumnHeader: types.AxisHeader{Types: append([]string(nil), colKinds...)},
				RowKeys:      rowKeys,
				ColumnKeys:   colKeys,
				Cells:        cells,
			},
		},
	}
}

func rowLabel(i int) string {
	return rowLabels[i%len(rowLabels)]
}

func colLabel(j int) string {
	return colLabels[j%len(colLabels)]
}

var (
	rowLabels = []string{"r0", "r1", "r2", "r3"}
	colLabels = []string{"c0", "c1", "c2", "c3"}
)

// requireSchemaDivergent asserts the supplied error carries the
// canonical PULSE_OVERLAY_SCHEMA_DIVERGENT code plus the expected
// reference / target labels and canonical schema strings.
func requireSchemaDivergent(t *testing.T, err error, wantReference, wantTargetLabel, wantRefSchema, wantTargetSchema string) {
	t.Helper()
	if err == nil {
		t.Fatal("checkSlotShapeAndSchema: want error, got nil")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got, _ := coded.Details["code"].(string); got != string(pulseerrors.PULSE_OVERLAY_SCHEMA_DIVERGENT) {
		t.Fatalf("Details[code] = %q, want %q", got, pulseerrors.PULSE_OVERLAY_SCHEMA_DIVERGENT)
	}
	if got, _ := coded.Details["reference"].(string); got != wantReference {
		t.Errorf("Details[reference] = %q, want %q", got, wantReference)
	}
	if got, _ := coded.Details["target_label"].(string); got != wantTargetLabel {
		t.Errorf("Details[target_label] = %q, want %q", got, wantTargetLabel)
	}
	if got, _ := coded.Details["reference_schema"].(string); got != wantRefSchema {
		t.Errorf("Details[reference_schema] = %q, want %q", got, wantRefSchema)
	}
	if got, _ := coded.Details["target_schema"].(string); got != wantTargetSchema {
		t.Errorf("Details[target_schema] = %q, want %q", got, wantTargetSchema)
	}
}

// requireSlotShapeDivergent asserts the supplied error carries the
// canonical PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT code plus the
// expected reference / target labels and observed shapes.
func requireSlotShapeDivergent(t *testing.T, err error, wantReference, wantTargetLabel, wantRefShape, wantTargetShape string) {
	t.Helper()
	if err == nil {
		t.Fatal("checkSlotShapeAndSchema: want error, got nil")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got, _ := coded.Details["code"].(string); got != string(pulseerrors.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT) {
		t.Fatalf("Details[code] = %q, want %q", got, pulseerrors.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT)
	}
	if got, _ := coded.Details["reference"].(string); got != wantReference {
		t.Errorf("Details[reference] = %q, want %q", got, wantReference)
	}
	if got, _ := coded.Details["target_label"].(string); got != wantTargetLabel {
		t.Errorf("Details[target_label] = %q, want %q", got, wantTargetLabel)
	}
	if got, _ := coded.Details["reference_shape"].(string); got != wantRefShape {
		t.Errorf("Details[reference_shape] = %q, want %q", got, wantRefShape)
	}
	if got, _ := coded.Details["target_shape"].(string); got != wantTargetShape {
		t.Errorf("Details[target_shape] = %q, want %q", got, wantTargetShape)
	}
}

// TestValidateOverlay_SchemaDivergent_DifferentRowGrouperKind
// exercises the structural-schema arm: reference declares row axis
// kind GROUP_CATEGORY; target declares GROUP_RANGE. Same axis depth,
// same column axis — but row-axis grouper kind differs. The check
// must reject with PULSE_OVERLAY_SCHEMA_DIVERGENT and surface the
// canonical schema strings so renderers can diff the structures.
func TestValidateOverlay_SchemaDivergent_DifferentRowGrouperKind(t *testing.T) {
	ref := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)
	target := schemaMatrixSlot(
		[]string{"GROUP_RANGE"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	err := checkSlotShapeAndSchema(ref, []*types.Response{target}, spec, 0)
	requireSchemaDivergent(t, err,
		"baseline", "treatment",
		"GROUP_CATEGORY/GROUP_CATEGORY",
		"GROUP_RANGE/GROUP_CATEGORY",
	)
}

// TestValidateOverlay_SchemaDivergent_DifferentDepth exercises the
// nested-depth arm: reference has a single row grouper; target has
// two nested row groupers. Same kind at depth 0, different depth.
// The check must reject with PULSE_OVERLAY_SCHEMA_DIVERGENT.
func TestValidateOverlay_SchemaDivergent_DifferentDepth(t *testing.T) {
	ref := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)
	target := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY", "GROUP_DATE"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	err := checkSlotShapeAndSchema(ref, []*types.Response{target}, spec, 0)
	requireSchemaDivergent(t, err,
		"baseline", "treatment",
		"GROUP_CATEGORY/GROUP_CATEGORY",
		"GROUP_CATEGORY|GROUP_DATE/GROUP_CATEGORY",
	)
}

// TestValidateOverlay_SchemaDivergent_FieldNamesAllowedToDiffer is
// the positive companion of the SCHEMA_DIVERGENT arm: two slots
// declaring the same axis kinds at the same depth must pass even
// when the underlying matrix uses different field names. The
// schema-match check is purely structural over Types — field names
// are intentionally NOT compared (per E7-S7 acceptance).
//
// Implementation note: extractSchemaShape reads Matrix.RowHeader.Types
// (not .Fields), so two slots with identical .Types and different
// .Fields produce byte-equal kind tuples and the gate accepts.
func TestValidateOverlay_SchemaDivergent_FieldNamesAllowedToDiffer(t *testing.T) {
	ref := &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Fields: []string{"region"}, Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      []types.AxisKey{{"r0"}},
				ColumnKeys:   []types.AxisKey{{"c0"}},
				Cells:        [][]types.MatrixCell{{{Present: true, Value: 1.0}}},
			},
		},
	}
	target := &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Fields: []string{"label"}, Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Fields: []string{"market"}, Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      []types.AxisKey{{"r0"}},
				ColumnKeys:   []types.AxisKey{{"c0"}},
				Cells:        [][]types.MatrixCell{{{Present: true, Value: 2.0}}},
			},
		},
	}
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	if err := checkSlotShapeAndSchema(ref, []*types.Response{target}, spec, 0); err != nil {
		t.Fatalf("checkSlotShapeAndSchema(same kinds, different field names): want nil, got %v", err)
	}
}

// TestValidateOverlay_SlotShapeDivergent_MatrixVsSeries exercises
// the shape-gate arm: reference is MATRIX (Crosstab), target is
// SERIES (Data). Mixed shapes have no shared coordinate grid; the
// check rejects with PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT.
func TestValidateOverlay_SlotShapeDivergent_MatrixVsSeries(t *testing.T) {
	ref := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)
	target := composeSeriesSlot("region", []string{"us", "eu"})
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	err := checkSlotShapeAndSchema(ref, []*types.Response{target}, spec, 0)
	requireSlotShapeDivergent(t, err,
		"baseline", "treatment",
		"matrix", "series",
	)
}

// TestValidateOverlay_SlotShapeDivergent_MatrixVsScalar exercises
// the scalar arm: reference is MATRIX, target is SCALAR (empty
// *Response). Mixed shapes ⇒ PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT.
func TestValidateOverlay_SlotShapeDivergent_MatrixVsScalar(t *testing.T) {
	ref := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY"},
		[]string{"GROUP_CATEGORY"},
		1, 1,
	)
	target := &types.Response{}
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	err := checkSlotShapeAndSchema(ref, []*types.Response{target}, spec, 0)
	requireSlotShapeDivergent(t, err,
		"baseline", "treatment",
		"matrix", "scalar",
	)
}

// TestValidateOverlay_SlotNotCrosstab_MatrixRequiredKindAgainstSeriesTarget
// exercises the SLOT_NOT_CROSSTAB gate against a real matrix-required
// COMPOSE kind. The E7-S9 matrix-required catalog flipped the six
// crosstab-shape kinds to require MATRIX; E7-S10 lifted INDEX_VS_REF
// and DELTA_VS_REF out of the matrix-required set (they now accept
// dual-shape hosts). The remaining matrix-required kinds (T_CELL /
// PROP_Z_CELL / CHISQ_VS_REF / RANK) still fire PULSE_OVERLAY_SLOT_
// NOT_CROSSTAB when paired with a non-MATRIX slot. The error carries
// {target_label, required_shape="MATRIX", observed_shape}.
func TestValidateOverlay_SlotNotCrosstab_MatrixRequiredKindAgainstSeriesTarget(t *testing.T) {
	if !kindRequiresMatrix(types.OverlayKindTCell) {
		t.Fatal("E7-S9 invariant violated: kindRequiresMatrix(OverlayKindTCell) must return true after the matrix-required catalog flip")
	}
	// MATRIX reference + SERIES target → the gate fires on the
	// shape-divergent gate FIRST (gate 1). The SLOT_NOT_CROSSTAB gate
	// runs against the reference arm first; both slots MATRIX
	// satisfies it, then the per-target loop catches a non-MATRIX
	// target via gate 1 (shape divergent) before gate 2 fires. To
	// exercise gate 2 directly we use a SERIES reference + SERIES
	// target so the shape-divergent gate is a no-op and the
	// matrix-required gate fires against the reference.
	ref := &types.Response{Data: []map[string]any{
		{"region": "us", "sum": 1.0},
	}}
	target := &types.Response{Data: []map[string]any{
		{"region": "us", "sum": 2.0},
	}}
	spec := types.ComposeOverlaySpec{
		Kind:      types.OverlayKindTCell,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	err := checkSlotShapeAndSchema(ref, []*types.Response{target}, spec, 0)
	if err == nil {
		t.Fatal("checkSlotShapeAndSchema: want SLOT_NOT_CROSSTAB error against series reference, got nil")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got, _ := coded.Details["code"].(string); got != string(pulseerrors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB) {
		t.Fatalf("Details[code] = %q, want %q", got, pulseerrors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB)
	}
	if got, _ := coded.Details["kind"].(string); got != string(types.OverlayKindTCell) {
		t.Errorf("Details[kind] = %q, want %q", got, types.OverlayKindTCell)
	}
	if got, _ := coded.Details["required_shape"].(string); got != "MATRIX" {
		t.Errorf("Details[required_shape] = %q, want %q", got, "MATRIX")
	}
	if got, _ := coded.Details["target_label"].(string); got != "reference" {
		t.Errorf("Details[target_label] = %q, want %q (reference arm)", got, "reference")
	}
	if got, _ := coded.Details["observed_shape"].(string); got != string(types.OverlayShapeSeries) {
		t.Errorf("Details[observed_shape] = %q, want %q", got, types.OverlayShapeSeries)
	}
}

// TestValidateOverlay_SchemaMatch_HappyPath exercises the positive
// arm of the structural-schema gate: matching shape (MATRIX),
// matching row + column axis kind tuples, matching depth — even
// when field names differ. The gate must return nil and the per-
// kind handler is free to dispatch.
func TestValidateOverlay_SchemaMatch_HappyPath(t *testing.T) {
	ref := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY", "GROUP_DATE"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)
	target := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY", "GROUP_DATE"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	if err := checkSlotShapeAndSchema(ref, []*types.Response{target}, spec, 0); err != nil {
		t.Fatalf("checkSlotShapeAndSchema(identical schemas): want nil, got %v", err)
	}
}

// TestValidateOverlay_SchemaMatch_SeriesHappyPath exercises the
// SERIES arm of the structural-schema gate: matching shape
// (SERIES), matching row-axis fields (the non-value column names).
// The gate must return nil even when the value column ("sum") is
// the same and the underlying aggregator values differ — values
// never participate in the structural axis.
func TestValidateOverlay_SchemaMatch_SeriesHappyPath(t *testing.T) {
	ref := &types.Response{Data: []map[string]any{
		{"region": "us", "sum": 1.0},
		{"region": "eu", "sum": 2.0},
	}}
	target := &types.Response{Data: []map[string]any{
		{"region": "us", "sum": 7.0}, // different value, same axis schema
		{"region": "eu", "sum": 8.0},
	}}
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	if err := checkSlotShapeAndSchema(ref, []*types.Response{target}, spec, 0); err != nil {
		t.Fatalf("checkSlotShapeAndSchema(identical series schemas): want nil, got %v", err)
	}
}

// TestValidateOverlay_SchemaMatch_ScalarHappyPath exercises the
// degenerate SCALAR / SCALAR arm: both slots have no host shape
// (empty *Response). The gate degenerates to a no-op and returns
// nil — there is no coordinate grid OR structural axis to compare.
func TestValidateOverlay_SchemaMatch_ScalarHappyPath(t *testing.T) {
	ref := &types.Response{}
	target := &types.Response{}
	spec := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	if err := checkSlotShapeAndSchema(ref, []*types.Response{target}, spec, 0); err != nil {
		t.Fatalf("checkSlotShapeAndSchema(scalar / scalar): want nil, got %v", err)
	}
}

// TestValidateOverlay_SchemaMatch_MultiTargetMixedDivergence
// asserts the "first divergence wins" rule across two targets:
// the matching target walks past without erroring; the diverging
// target surfaces the SCHEMA_DIVERGENT error with its own label.
// The check stops at the first failing target — multi-target
// reporting is a future surface.
func TestValidateOverlay_SchemaMatch_MultiTargetMixedDivergence(t *testing.T) {
	ref := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)
	matchingTarget := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)
	divergingTarget := schemaMatrixSlot(
		[]string{"GROUP_RANGE"},
		[]string{"GROUP_CATEGORY"},
		2, 2,
	)

	specMatchingFirst := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"matching", "diverging"},
	}
	err := checkSlotShapeAndSchema(ref,
		[]*types.Response{matchingTarget, divergingTarget},
		specMatchingFirst, 0)
	requireSchemaDivergent(t, err,
		"baseline", "diverging",
		"GROUP_CATEGORY/GROUP_CATEGORY",
		"GROUP_RANGE/GROUP_CATEGORY",
	)

	// Diverging-first: must surface immediately with the diverging
	// target's label.
	specDivergingFirst := types.ComposeOverlaySpec{
		Reference: "baseline",
		Targets:   []string{"diverging", "matching"},
	}
	err = checkSlotShapeAndSchema(ref,
		[]*types.Response{divergingTarget, matchingTarget},
		specDivergingFirst, 0)
	requireSchemaDivergent(t, err,
		"baseline", "diverging",
		"GROUP_CATEGORY/GROUP_CATEGORY",
		"GROUP_RANGE/GROUP_CATEGORY",
	)
}

// TestExtractSchemaShape_Canonical_Matrix locks the canonical
// schema-string format the Details payload exposes. Matrix shape
// renders as "<rowKinds>/<colKinds>" with kinds joined "|".
func TestExtractSchemaShape_Canonical_Matrix(t *testing.T) {
	resp := schemaMatrixSlot(
		[]string{"GROUP_CATEGORY", "GROUP_DATE"},
		[]string{"GROUP_RANGE"},
		1, 1,
	)
	got := extractSchemaShape(resp).canonical()
	want := "GROUP_CATEGORY|GROUP_DATE/GROUP_RANGE"
	if got != want {
		t.Errorf("canonical(matrix multi-row) = %q, want %q", got, want)
	}
}

// TestExtractSchemaShape_Canonical_Series locks the series-arm
// canonical string: row axis is the sorted non-value column names,
// column axis is empty (no col axis on a SERIES host). Output:
// "<rowFields>/".
func TestExtractSchemaShape_Canonical_Series(t *testing.T) {
	resp := &types.Response{Data: []map[string]any{
		{"region": "us", "segment": "north", "sum": 1.0},
	}}
	got := extractSchemaShape(resp).canonical()
	// "sum" is the FIRST sorted numeric column (the value),
	// dropped. "region" + "segment" remain in sorted order.
	want := "region|segment/"
	if got != want {
		t.Errorf("canonical(series) = %q, want %q", got, want)
	}
}

// TestExtractSchemaShape_Canonical_Scalar locks the degenerate
// scalar render: "/" with both axes empty.
func TestExtractSchemaShape_Canonical_Scalar(t *testing.T) {
	got := extractSchemaShape(&types.Response{}).canonical()
	if got != "/" {
		t.Errorf("canonical(scalar) = %q, want %q", got, "/")
	}
}

// TestKindRequiresMatrix_E7S10Registry locks the E7-S10 matrix-required
// catalog: the remaining four matrix-only crosstab-shape Compose kinds
// (PROP_Z_CELL, T_CELL, CHISQ_VS_REF, RANK) all return true; every
// other kind returns false. E7-S9 originally registered six entries;
// E7-S10 lifted INDEX_VS_REF and DELTA_VS_REF out of the matrix-
// required set because they now accept dual-shape hosts (MATRIX or
// SERIES). T_VS_REF (E7-S10 series-shape sibling of T_CELL) stays
// false here — series-shape and panel-shape Compose kinds use the
// more general PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT path for cross-slot
// shape gating.
func TestKindRequiresMatrix_E7S10Registry(t *testing.T) {
	matrixRequired := map[types.OverlayKind]bool{
		types.OverlayKindPropZCell:  true,
		types.OverlayKindTCell:      true,
		types.OverlayKindChiSqVsRef: true,
		types.OverlayKindRank:       true,
	}
	for _, kind := range types.AllOverlayKinds() {
		want := matrixRequired[kind]
		got := kindRequiresMatrix(kind)
		if got != want {
			t.Errorf("kindRequiresMatrix(%s) = %v, want %v", kind, got, want)
		}
	}
}

// TestAxisKindsEqual_NilEmptyEquivalent locks the gate-side
// short-circuit: nil and empty kind slices compare equal. Two
// slots whose row axis is missing entirely (degenerate SCALAR)
// must not trip the structural-divergence gate against another
// degenerate slot.
func TestAxisKindsEqual_NilEmptyEquivalent(t *testing.T) {
	if !axisKindsEqual(nil, []string{}) {
		t.Error("axisKindsEqual(nil, []) = false, want true")
	}
	if !axisKindsEqual([]string{}, nil) {
		t.Error("axisKindsEqual([], nil) = false, want true")
	}
	if !axisKindsEqual(nil, nil) {
		t.Error("axisKindsEqual(nil, nil) = false, want true")
	}
}

// TestAxisKindsEqual_OrderSensitive locks the structural-depth
// contract: nested-grouper-depth is part of the structural schema
// AND order matters within each axis tuple. (GROUP_CATEGORY,
// GROUP_DATE) is structurally distinct from (GROUP_DATE,
// GROUP_CATEGORY).
func TestAxisKindsEqual_OrderSensitive(t *testing.T) {
	a := []string{"GROUP_CATEGORY", "GROUP_DATE"}
	b := []string{"GROUP_DATE", "GROUP_CATEGORY"}
	if axisKindsEqual(a, b) {
		t.Errorf("axisKindsEqual(%v, %v) = true, want false (order matters)", a, b)
	}
}

// TestExtractSeriesAxisFields_DropFirstNumeric is a unit guard on
// the series-arm row-axis extraction. Confirms that the FIRST
// sorted numeric column is dropped (the "value" column per
// encodeSeriesRow), subsequent numeric columns stay in the axis,
// and the result is sorted lexicographically.
func TestExtractSeriesAxisFields_DropFirstNumeric(t *testing.T) {
	rows := []map[string]any{
		{
			"alpha":  "x",       // non-numeric, stays
			"value":  1.0,       // FIRST sorted numeric column → dropped
			"weight": 2.0,       // numeric but not first → stays
			"zeta":   "y",       // non-numeric, stays
		},
	}
	got := extractSeriesAxisFields(rows)
	want := []string{"alpha", "weight", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractSeriesAxisFields = %v, want %v", got, want)
	}
}

// TestApplyComposeOverlays_SchemaDivergenceFailsLoud is the
// service-level integration: the chassis rejects a spec whose
// reference + target produce structurally divergent schemas with
// the canonical coded error BEFORE the per-kind dispatch. Asserts
// the `code` Detail surfaces verbatim so the orchestrator can
// branch on it the same way it branches on LookupReference /
// LookupTarget failures.
func TestApplyComposeOverlays_SchemaDivergenceFailsLoud(t *testing.T) {
	responses := []*types.Response{
		schemaMatrixSlot(
			[]string{"GROUP_CATEGORY"},
			[]string{"GROUP_CATEGORY"},
			2, 2,
		),
		schemaMatrixSlot(
			[]string{"GROUP_RANGE"},
			[]string{"GROUP_CATEGORY"},
			2, 2,
		),
	}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{{
		Name:      "diverging_schema",
		Kind:      types.OverlayKindIndexVsStage,
		Scope:     types.OverlayScopeTotal,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}}
	layers, warnings, err := ApplyComposeOverlays(specs, responses, labels)
	if err == nil {
		t.Fatal("ApplyComposeOverlays(schema divergence): want error, got nil")
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil under coded error", layers)
	}
	if warnings != nil {
		t.Errorf("warnings = %+v, want nil under coded error", warnings)
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_SCHEMA_DIVERGENT)
}

// TestApplyComposeOverlays_SlotShapeDivergenceFailsLoud is the
// integration companion for the SHAPE_DIVERGENT gate. Uses a
// SCALAR target (empty *Response) against a MATRIX reference —
// the upstream key-set gate intentionally skips scalar targets
// when the reference is non-scalar so the SHAPE_DIVERGENT gate
// fires next (per its E7-S7 ordering contract).
func TestApplyComposeOverlays_SlotShapeDivergenceFailsLoud(t *testing.T) {
	responses := []*types.Response{
		schemaMatrixSlot(
			[]string{"GROUP_CATEGORY"},
			[]string{"GROUP_CATEGORY"},
			1, 1,
		),
		&types.Response{},
	}
	labels := []string{"baseline", "treatment"}
	specs := []types.ComposeOverlaySpec{{
		Name:      "diverging_shape",
		Kind:      types.OverlayKindIndexVsStage,
		Scope:     types.OverlayScopeTotal,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}}
	_, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err == nil {
		t.Fatal("ApplyComposeOverlays(shape divergence): want error, got nil")
	}
	requireDetailCode(t, err, pulseerrors.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT)
}
