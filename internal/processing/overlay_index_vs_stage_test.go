package processing

import (
	"math"
	"reflect"
	"runtime"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// matrixResponse builds a Response carrying a Crosstab.Matrix payload
// for the matrix-arm tests. RowKeys / ColumnKeys are passed in canonical
// order; the cells slice is row-major aligned. A nil cells[r][c] is
// rendered as an absent MatrixCell so tests can express "missing"
// reference / target slots ergonomically.
func matrixResponse(rowKeys, colKeys []types.AxisKey, cells [][]*float64) *types.Response {
	mxCells := make([][]types.MatrixCell, len(rowKeys))
	for i := range rowKeys {
		mxCells[i] = make([]types.MatrixCell, len(colKeys))
		for j := range colKeys {
			if i < len(cells) && j < len(cells[i]) && cells[i][j] != nil {
				mxCells[i][j] = types.MatrixCell{Value: *cells[i][j], Present: true}
			}
		}
	}
	return &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowKeys:    rowKeys,
				ColumnKeys: colKeys,
				Cells:      mxCells,
			},
		},
	}
}

func fpr(v float64) *float64 { return &v }

// TestApplyIndexVsStage_MatrixHappyPath drives the matrix arm with a
// 2×2 target × 2×2 reference. Indices verify per-cell against the
// hand-computed `target / ref * 100`. The layer carries one MATRIX
// payload with the matching shape inherited from the target stage.
func TestApplyIndexVsStage_MatrixHappyPath(t *testing.T) {
	rowKeys := []types.AxisKey{{"r0"}, {"r1"}}
	colKeys := []types.AxisKey{{"c0"}, {"c1"}}
	target := matrixResponse(rowKeys, colKeys, [][]*float64{
		{fpr(120), fpr(60)},
		{fpr(150), fpr(75)},
	})
	ref := matrixResponse(rowKeys, colKeys, [][]*float64{
		{fpr(100), fpr(50)},
		{fpr(100), fpr(50)},
	})
	spec := &types.ChainOverlaySpec{
		Kind:  types.OverlayKindIndexVsStage,
		Scope: types.OverlayScopeTotal,
	}

	layer, warnings, err := applyIndexVsStage(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyIndexVsStage: %v", err)
	}
	if got := layer.Payload.Shape; got != types.OverlayShapeMatrix {
		t.Fatalf("layer.Payload.Shape = %q, want %q", got, types.OverlayShapeMatrix)
	}
	if len(warnings) != 0 {
		t.Errorf("happy path warnings = %+v, want none", warnings)
	}
	wantCells := [][]float64{
		{120.0, 120.0},
		{150.0, 150.0},
	}
	for i := range wantCells {
		for j := range wantCells[i] {
			got := layer.Payload.Matrix.Cells[i][j]
			if !got.Present {
				t.Errorf("cell(%d,%d) not present", i, j)
				continue
			}
			gotV, _ := got.Value.(float64)
			if math.Abs(gotV-wantCells[i][j]) > 1e-9 {
				t.Errorf("cell(%d,%d) = %g, want %g", i, j, gotV, wantCells[i][j])
			}
		}
	}
}

// TestApplyIndexVsStage_MatrixZeroRefEmitsWarningAndNaN exercises the
// zero-reference defence on the matrix arm. The (0,0) cell has a
// non-zero target but a zero reference — the cell surfaces NaN +
// a PULSE_OVERLAY_REF_ZERO warning per the acceptance "reference == 0
// → emit PULSE_OVERLAY_REF_ZERO warning and substitute NaN".
func TestApplyIndexVsStage_MatrixZeroRefEmitsWarningAndNaN(t *testing.T) {
	rowKeys := []types.AxisKey{{"r0"}}
	colKeys := []types.AxisKey{{"c0"}, {"c1"}}
	target := matrixResponse(rowKeys, colKeys, [][]*float64{{fpr(50), fpr(80)}})
	ref := matrixResponse(rowKeys, colKeys, [][]*float64{{fpr(0), fpr(80)}})
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyIndexVsStage(spec, target, ref, 0, 0)
	if err != nil {
		t.Fatalf("applyIndexVsStage: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly one zero-ref warning", warnings)
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning Code = %q, want %q", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	cell := layer.Payload.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatalf("zero-ref cell expected to be present with NaN, got absent")
	}
	gotV, _ := cell.Value.(float64)
	if !math.IsNaN(gotV) {
		t.Errorf("zero-ref cell value = %g, want NaN", gotV)
	}
	// (0,1) cell should be 100 (80/80 * 100).
	good := layer.Payload.Matrix.Cells[0][1]
	if v, _ := good.Value.(float64); math.Abs(v-100.0) > 1e-9 {
		t.Errorf("non-zero-ref cell = %g, want 100", v)
	}
}

// TestApplyIndexVsStage_MatrixMissingRefCellWarns exercises the
// missing-reference defence — a target cell whose (rowKey, colKey)
// pair has no matching reference cell fires PULSE_OVERLAY_REF_ZERO
// with a "ref_missing" Detail. The overlay cell stays absent (silent
// zero contribution to denominator per acceptance).
func TestApplyIndexVsStage_MatrixMissingRefCellWarns(t *testing.T) {
	rowKeys := []types.AxisKey{{"r0"}}
	colKeys := []types.AxisKey{{"c0"}, {"c1"}}
	target := matrixResponse(rowKeys, colKeys, [][]*float64{{fpr(100), fpr(200)}})
	// Reference only has c0; c1 is structurally absent.
	ref := matrixResponse(rowKeys, colKeys, [][]*float64{{fpr(50), nil}})
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyIndexVsStage(spec, target, ref, 0, 0)
	if err != nil {
		t.Fatalf("applyIndexVsStage: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one missing-ref warning", warnings)
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning Code = %q, want %q", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	if got := warnings[0].Details["ref_missing"]; got != true {
		t.Errorf("warning Details.ref_missing = %v, want true", got)
	}
	// (0,0) populated; (0,1) absent.
	c00 := layer.Payload.Matrix.Cells[0][0]
	if v, _ := c00.Value.(float64); math.Abs(v-200.0) > 1e-9 {
		t.Errorf("c0 cell = %g, want 200", v)
	}
	if layer.Payload.Matrix.Cells[0][1].Present {
		t.Errorf("missing-ref cell should be absent, got present=%v value=%v",
			layer.Payload.Matrix.Cells[0][1].Present, layer.Payload.Matrix.Cells[0][1].Value)
	}
}

// seriesResponse builds a Response carrying a grouped-Process style
// Data slice for the series-arm tests. Each entry pairs a single
// group-key column ("group") with one aggregator value ("value").
func seriesResponse(rows []map[string]any) *types.Response {
	return &types.Response{Data: rows}
}

// TestApplyIndexVsStage_SeriesHappyPath drives the series arm with
// 3-row target × 3-row reference. Per-row index = target_value /
// ref_value * 100; entries align by row-key string.
func TestApplyIndexVsStage_SeriesHappyPath(t *testing.T) {
	target := seriesResponse([]map[string]any{
		{"group": "us", "sum": 120.0},
		{"group": "uk", "sum": 60.0},
		{"group": "ca", "sum": 24.0},
	})
	ref := seriesResponse([]map[string]any{
		{"group": "us", "sum": 100.0},
		{"group": "uk", "sum": 50.0},
		{"group": "ca", "sum": 20.0},
	})
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyIndexVsStage(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyIndexVsStage: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("happy path warnings = %+v, want none", warnings)
	}
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("layer shape = %q, want %q", layer.Payload.Shape, types.OverlayShapeSeries)
	}
	if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != 3 {
		t.Fatalf("layer.Series.Entries len = %d, want 3", len(layer.Payload.Series.Entries))
	}
	// Each entry's Statistic ~ 120 (the inputs are all designed so the
	// ratio resolves to the same constant).
	for i, entry := range layer.Payload.Series.Entries {
		if entry.Summary.Statistic == nil {
			t.Errorf("entry[%d] Statistic = nil", i)
			continue
		}
		if math.Abs(*entry.Summary.Statistic-120.0) > 1e-9 {
			t.Errorf("entry[%d] Statistic = %g, want 120.0", i, *entry.Summary.Statistic)
		}
	}
}

// TestApplyIndexVsStage_SeriesMissingRefKeyWarns covers the
// missing-reference-row case on the series arm. A target row whose
// row key is not present in the reference table fires
// PULSE_OVERLAY_REF_ZERO with ref_missing=true.
func TestApplyIndexVsStage_SeriesMissingRefKeyWarns(t *testing.T) {
	target := seriesResponse([]map[string]any{
		{"group": "us", "sum": 120.0},
		{"group": "mx", "sum": 30.0}, // missing in ref
	})
	ref := seriesResponse([]map[string]any{
		{"group": "us", "sum": 100.0},
	})
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyIndexVsStage(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyIndexVsStage: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one missing-ref warning", warnings)
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning Code = %q, want %q", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	if got := warnings[0].Details["ref_missing"]; got != true {
		t.Errorf("warning Details.ref_missing = %v, want true", got)
	}
	// One entry produced (us only).
	if got := len(layer.Payload.Series.Entries); got != 1 {
		t.Errorf("entries = %d, want 1 (mx dropped)", got)
	}
}

// TestApplyIndexVsStage_ScalarHappyPath covers the scalar arm — both
// responses carry a single numeric value, the index is the simple
// target / ref * 100.
func TestApplyIndexVsStage_ScalarHappyPath(t *testing.T) {
	target := &types.Response{Data: []map[string]any{{"sum": 250.0}}}
	ref := &types.Response{Data: []map[string]any{{"sum": 100.0}}}
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeTotal}

	// The series shape detection treats len(Data)>0 as series; only
	// truly-empty Data + nil Crosstab is scalar. To exercise the scalar
	// arm directly we call applyIndexVsStageScalar (the internal dispatch
	// helper) with stages that have no group columns. A pure-scalar
	// Process response in practice carries Data: [{aggregator}] (no
	// group key) so the series arm and scalar arm both work — we test
	// the scalar dispatch path explicitly here to keep the kernel
	// covered.
	layer, warnings, err := applyIndexVsStageScalar(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyIndexVsStageScalar: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("scalar happy path warnings = %+v, want none", warnings)
	}
	if layer.Payload.Shape != types.OverlayShapeScalar {
		t.Fatalf("layer shape = %q, want scalar", layer.Payload.Shape)
	}
	if layer.Payload.Scalar == nil {
		t.Fatalf("layer.Payload.Scalar = nil, want 250.0")
	}
	if math.Abs(*layer.Payload.Scalar-250.0) > 1e-9 {
		t.Errorf("scalar = %g, want 250.0", *layer.Payload.Scalar)
	}
}

// TestApplyIndexVsStage_ScalarZeroRefNaN covers the zero-reference
// scalar arm. The kernel substitutes NaN and emits
// PULSE_OVERLAY_REF_ZERO.
func TestApplyIndexVsStage_ScalarZeroRefNaN(t *testing.T) {
	target := &types.Response{Data: []map[string]any{{"sum": 100.0}}}
	ref := &types.Response{Data: []map[string]any{{"sum": 0.0}}}
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyIndexVsStageScalar(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyIndexVsStageScalar: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one zero-ref warning", warnings)
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning Code = %q, want %q", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	if layer.Payload.Scalar == nil || !math.IsNaN(*layer.Payload.Scalar) {
		t.Errorf("Payload.Scalar = %v, want NaN", layer.Payload.Scalar)
	}
}

func TestApplyIndexVsStage_ShapeDivergenceWarning(t *testing.T) {
	target := matrixResponse(
		[]types.AxisKey{{"r0"}},
		[]types.AxisKey{{"c0"}},
		[][]*float64{{fpr(1)}},
	)
	ref := seriesResponse([]map[string]any{{"group": "us", "sum": 1.0}})
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyIndexVsStage(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyIndexVsStage: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one divergence warning", warnings)
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT) {
		t.Errorf("warning Code = %q, want %q",
			warnings[0].Code, pulseerrors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT)
	}
	if got := warnings[0].Details["target_shape"]; got != string(types.OverlayShapeMatrix) {
		t.Errorf("warning Details.target_shape = %v, want %q", got, types.OverlayShapeMatrix)
	}
	if got := warnings[0].Details["ref_shape"]; got != string(types.OverlayShapeSeries) {
		t.Errorf("warning Details.ref_shape = %v, want %q", got, types.OverlayShapeSeries)
	}
	// The fallback empty payload inherits the target's shape.
	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Errorf("layer shape = %q, want %q (inherits target)", layer.Payload.Shape, types.OverlayShapeMatrix)
	}
}

// TestApplyIndexVsStage_DispatchReplacesStub asserts that the dispatch
// table entry for OVERLAY_INDEX_VS_STAGE is no longer the chassis
// stub (applyChainStub) — it is now applyIndexVsStage. Walks the
// runtime function pointer; runtime.FuncForPC keeps the assertion
// stable across binary layouts.
func TestApplyIndexVsStage_DispatchReplacesStub(t *testing.T) {
	handler, ok := chainOverlayHandlers[types.OverlayKindIndexVsStage]
	if !ok {
		t.Fatal("OVERLAY_INDEX_VS_STAGE has no dispatch entry")
	}
	got := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
	want := runtime.FuncForPC(reflect.ValueOf(applyIndexVsStage).Pointer()).Name()
	if got != want {
		t.Errorf("OVERLAY_INDEX_VS_STAGE handler = %q, want %q (still stub?)", got, want)
	}
	stub := runtime.FuncForPC(reflect.ValueOf(applyChainStub).Pointer()).Name()
	if got == stub {
		t.Errorf("OVERLAY_INDEX_VS_STAGE handler still points to applyChainStub")
	}
}

func TestApplyIndexVsStage_SpecOrderPreserved(t *testing.T) {
	target := seriesResponse([]map[string]any{{"group": "us", "sum": 120.0}})
	ref := seriesResponse([]map[string]any{{"group": "us", "sum": 100.0}})
	stages := []*types.Response{ref, target}

	specs := []*types.ChainOverlaySpec{
		{
			Kind:   types.OverlayKindIndexVsStage,
			Scope:  types.OverlayScopeTotal,
			Ref:    types.StageRef{Index: ptrInt(0)},
			Target: types.StageRef{Index: ptrInt(1)},
		},
		{
			Kind:   types.OverlayKindDeltaVsStage,
			Scope:  types.OverlayScopeTotal,
			Ref:    types.StageRef{Index: ptrInt(0)},
			Target: types.StageRef{Index: ptrInt(1)},
		},
	}
	layers, _, err := ApplyChainOverlays(specs, stages, []string{"", ""})
	if err != nil {
		t.Fatalf("ApplyChainOverlays: %v", err)
	}
	if got := len(layers); got != 2 {
		t.Fatalf("layers = %d, want 2", got)
	}
	if layers[0].Kind != types.OverlayKindIndexVsStage {
		t.Errorf("layers[0].Kind = %q, want %q", layers[0].Kind, types.OverlayKindIndexVsStage)
	}
	if layers[1].Kind != types.OverlayKindDeltaVsStage {
		t.Errorf("layers[1].Kind = %q, want %q", layers[1].Kind, types.OverlayKindDeltaVsStage)
	}
	if layers[0].Payload.Series == nil || len(layers[0].Payload.Series.Entries) != 1 {
		t.Fatalf("INDEX layer should have one series entry, got %+v", layers[0].Payload)
	}
	entry := layers[0].Payload.Series.Entries[0]
	if entry.Summary.Statistic == nil {
		t.Fatal("INDEX entry Summary.Statistic = nil")
	}
	if math.Abs(*entry.Summary.Statistic-120.0) > 1e-9 {
		t.Errorf("INDEX entry Statistic = %g, want 120.0", *entry.Summary.Statistic)
	}
}

// TestIndexKernel_ZeroReferenceReturnsNaN exercises the shared kernel
// directly so any future deltaKernel-style refactor preserves the
// canonical zero-reference contract.
func TestIndexKernel_ZeroReferenceReturnsNaN(t *testing.T) {
	got, ok := indexKernel(50.0, 0.0)
	if ok {
		t.Fatalf("indexKernel(_,0) ok = true, want false")
	}
	if !math.IsNaN(got) {
		t.Errorf("indexKernel(_,0) value = %g, want NaN", got)
	}
}

// TestIndexKernel_HappyPath confirms the kernel produces the
// documented `target / ref * 100` shape for a known input pair.
func TestIndexKernel_HappyPath(t *testing.T) {
	got, ok := indexKernel(250.0, 100.0)
	if !ok {
		t.Fatalf("indexKernel ok = false, want true")
	}
	if math.Abs(got-250.0) > 1e-9 {
		t.Errorf("indexKernel(250,100) = %g, want 250", got)
	}
}
