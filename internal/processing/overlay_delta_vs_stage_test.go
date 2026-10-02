package processing

import (
	"math"
	"reflect"
	"runtime"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestApplyDeltaVsStage_MatrixHappyPath drives the matrix arm with a
// 2×2 target × 2×2 reference. Deltas verify per-cell against the
// hand-computed `target - reference`. The layer carries one MATRIX
// payload with the matching shape inherited from the target stage.
// No warnings expected.
func TestApplyDeltaVsStage_MatrixHappyPath(t *testing.T) {
	rowKeys := []types.AxisKey{{"r0"}, {"r1"}}
	colKeys := []types.AxisKey{{"c0"}, {"c1"}}
	target := matrixResponse(rowKeys, colKeys, [][]*float64{
		{fpr(120), fpr(60)},
		{fpr(150), fpr(75)},
	})
	ref := matrixResponse(rowKeys, colKeys, [][]*float64{
		{fpr(100), fpr(50)},
		{fpr(140), fpr(70)},
	})
	spec := &types.ChainOverlaySpec{
		Kind:  types.OverlayKindDeltaVsStage,
		Scope: types.OverlayScopeTotal,
	}

	layer, warnings, err := applyDeltaVsStage(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyDeltaVsStage: %v", err)
	}
	if got := layer.Payload.Shape; got != types.OverlayShapeMatrix {
		t.Fatalf("layer.Payload.Shape = %q, want %q", got, types.OverlayShapeMatrix)
	}
	if len(warnings) != 0 {
		t.Errorf("happy path warnings = %+v, want none", warnings)
	}
	wantCells := [][]float64{
		{20.0, 10.0},
		{10.0, 5.0},
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
	// Summary baseline is 0 for DELTA family (vs 100 for INDEX).
	if layer.Summary == nil {
		t.Fatal("layer.Summary = nil, want populated")
	}
	if layer.Summary.Baseline == nil || *layer.Summary.Baseline != 0.0 {
		t.Errorf("Summary.Baseline = %v, want 0.0", layer.Summary.Baseline)
	}
}

// TestApplyDeltaVsStage_MatrixZeroRefNoNaN exercises the zero-reference
// path on the matrix arm. The (0,0) cell has a non-zero target and a
// ZERO reference — the cell should surface `target - 0 == target` with
// no NaN and no warning. Zero is a number, not a divisor.
func TestApplyDeltaVsStage_MatrixZeroRefNoNaN(t *testing.T) {
	rowKeys := []types.AxisKey{{"r0"}}
	colKeys := []types.AxisKey{{"c0"}, {"c1"}}
	target := matrixResponse(rowKeys, colKeys, [][]*float64{{fpr(50), fpr(80)}})
	ref := matrixResponse(rowKeys, colKeys, [][]*float64{{fpr(0), fpr(80)}})
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyDeltaVsStage(spec, target, ref, 0, 0)
	if err != nil {
		t.Fatalf("applyDeltaVsStage: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("zero-ref warnings = %+v, want none (zero is a number, not a divisor)", warnings)
	}
	cell00 := layer.Payload.Matrix.Cells[0][0]
	if !cell00.Present {
		t.Fatal("(0,0) cell expected present, got absent")
	}
	v, _ := cell00.Value.(float64)
	if math.IsNaN(v) {
		t.Errorf("(0,0) cell = NaN, want 50.0 (target - 0)")
	}
	if math.Abs(v-50.0) > 1e-9 {
		t.Errorf("(0,0) cell = %g, want 50.0", v)
	}
	// (0,1) cell: 80 - 80 == 0
	cell01 := layer.Payload.Matrix.Cells[0][1]
	v01, _ := cell01.Value.(float64)
	if math.Abs(v01-0.0) > 1e-9 {
		t.Errorf("(0,1) cell = %g, want 0.0", v01)
	}
}

func TestApplyDeltaVsStage_MatrixMissingRefCellWarns(t *testing.T) {
	rowKeys := []types.AxisKey{{"r0"}}
	colKeys := []types.AxisKey{{"c0"}, {"c1"}}
	target := matrixResponse(rowKeys, colKeys, [][]*float64{{fpr(100), fpr(200)}})
	// Reference only has c0; c1 is structurally absent.
	ref := matrixResponse(rowKeys, colKeys, [][]*float64{{fpr(50), nil}})
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyDeltaVsStage(spec, target, ref, 0, 0)
	if err != nil {
		t.Fatalf("applyDeltaVsStage: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one missing-ref warning", warnings)
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN) {
		t.Errorf("warning Code = %q, want %q", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN)
	}
	if got := warnings[0].Details["ref_missing"]; got != true {
		t.Errorf("warning Details.ref_missing = %v, want true", got)
	}
	// (0,0) populated as 100 - 50 == 50.
	c00 := layer.Payload.Matrix.Cells[0][0]
	if v, _ := c00.Value.(float64); math.Abs(v-50.0) > 1e-9 {
		t.Errorf("c0 cell = %g, want 50", v)
	}
	// (0,1) folds against implicit 0 reference: 200 - 0 == 200.
	c01 := layer.Payload.Matrix.Cells[0][1]
	if !c01.Present {
		t.Fatalf("missing-ref cell should be present (delta == target), got absent")
	}
	if v, _ := c01.Value.(float64); math.Abs(v-200.0) > 1e-9 {
		t.Errorf("missing-ref cell = %g, want 200 (target - 0)", v)
	}
}

// TestApplyDeltaVsStage_SeriesHappyPath drives the series arm with
// 3-row target × 3-row reference. Per-row delta = target_value -
// ref_value; entries align by row-key string. No warnings expected.
func TestApplyDeltaVsStage_SeriesHappyPath(t *testing.T) {
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
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyDeltaVsStage(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyDeltaVsStage: %v", err)
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
	// Expected deltas: us=20, uk=10, ca=4 (entries ordered by target's
	// Data slice; the canonical row-key is "group=us|" etc — the
	// happy-path test only confirms each entry surfaces a finite
	// statistic).
	wantByEntry := map[string]float64{
		"us": 20.0,
		"uk": 10.0,
		"ca": 4.0,
	}
	gotSet := make(map[float64]bool, 3)
	for i, entry := range layer.Payload.Series.Entries {
		if entry.Summary.Statistic == nil {
			t.Errorf("entry[%d] Statistic = nil", i)
			continue
		}
		gotSet[*entry.Summary.Statistic] = true
	}
	for label, want := range wantByEntry {
		if !gotSet[want] {
			t.Errorf("expected delta for %q = %g not found among entries", label, want)
		}
	}
	if layer.Summary == nil || layer.Summary.Baseline == nil || *layer.Summary.Baseline != 0.0 {
		t.Errorf("Summary.Baseline = %v, want 0.0", layer.Summary)
	}
}

// TestApplyDeltaVsStage_SeriesMissingRefKeyWarns covers the missing-
// reference-row case on the series arm. A target row whose row key
// is not present in the reference table fires
// PULSE_OVERLAY_REFERENCE_UNKNOWN with ref_missing=true AND surfaces a
// SeriesEntry whose Statistic equals the target value (delta against
// implicit zero reference per acceptance).
func TestApplyDeltaVsStage_SeriesMissingRefKeyWarns(t *testing.T) {
	target := seriesResponse([]map[string]any{
		{"group": "us", "sum": 120.0},
		{"group": "mx", "sum": 30.0}, // missing in ref
	})
	ref := seriesResponse([]map[string]any{
		{"group": "us", "sum": 100.0},
	})
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyDeltaVsStage(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyDeltaVsStage: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one missing-ref warning", warnings)
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN) {
		t.Errorf("warning Code = %q, want %q", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN)
	}
	if got := warnings[0].Details["ref_missing"]; got != true {
		t.Errorf("warning Details.ref_missing = %v, want true", got)
	}
	// Both target rows surface entries — us against ref, mx against
	// implicit 0 (delta == 30).
	if got := len(layer.Payload.Series.Entries); got != 2 {
		t.Fatalf("entries = %d, want 2 (us=20, mx=30 against implicit-zero ref)", got)
	}
	// Locate the entry with Statistic == 30 (the mx target - 0 delta).
	var foundMxDelta bool
	for _, e := range layer.Payload.Series.Entries {
		if e.Summary.Statistic == nil {
			continue
		}
		if math.Abs(*e.Summary.Statistic-30.0) < 1e-9 {
			foundMxDelta = true
		}
	}
	if !foundMxDelta {
		t.Errorf("missing-ref entry should carry Statistic == 30 (target - 0), not found")
	}
}

// TestApplyDeltaVsStage_ScalarHappyPath covers the scalar arm — both
// responses carry a single numeric value, the delta is the simple
// target - reference.
func TestApplyDeltaVsStage_ScalarHappyPath(t *testing.T) {
	target := &types.Response{Data: []map[string]any{{"sum": 250.0}}}
	ref := &types.Response{Data: []map[string]any{{"sum": 100.0}}}
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage, Scope: types.OverlayScopeTotal}

	// Call the scalar dispatch helper directly (the series arm fires
	// for any non-empty Data; we want explicit scalar coverage of the
	// kernel here).
	layer, warnings, err := applyDeltaVsStageScalar(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyDeltaVsStageScalar: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("scalar happy path warnings = %+v, want none", warnings)
	}
	if layer.Payload.Shape != types.OverlayShapeScalar {
		t.Fatalf("layer shape = %q, want scalar", layer.Payload.Shape)
	}
	if layer.Payload.Scalar == nil {
		t.Fatalf("layer.Payload.Scalar = nil, want 150.0")
	}
	if math.Abs(*layer.Payload.Scalar-150.0) > 1e-9 {
		t.Errorf("scalar = %g, want 150.0 (250 - 100)", *layer.Payload.Scalar)
	}
	if layer.Summary == nil || layer.Summary.Baseline == nil || *layer.Summary.Baseline != 0.0 {
		t.Errorf("Summary.Baseline = %v, want 0.0", layer.Summary)
	}
}

// TestApplyDeltaVsStage_ScalarZeroRefNoNaN covers the zero-reference
// scalar arm. Zero is a number; delta == target verbatim; no warning.
func TestApplyDeltaVsStage_ScalarZeroRefNoNaN(t *testing.T) {
	target := &types.Response{Data: []map[string]any{{"sum": 100.0}}}
	ref := &types.Response{Data: []map[string]any{{"sum": 0.0}}}
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyDeltaVsStageScalar(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyDeltaVsStageScalar: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("zero-ref warnings = %+v, want none (zero is a number, not a divisor)", warnings)
	}
	if layer.Payload.Scalar == nil {
		t.Fatalf("Payload.Scalar = nil, want 100.0")
	}
	if math.IsNaN(*layer.Payload.Scalar) {
		t.Errorf("Payload.Scalar = NaN, want 100.0 (target - 0)")
	}
	if math.Abs(*layer.Payload.Scalar-100.0) > 1e-9 {
		t.Errorf("Payload.Scalar = %g, want 100.0", *layer.Payload.Scalar)
	}
}

func TestApplyDeltaVsStage_ShapeDivergenceWarning(t *testing.T) {
	target := matrixResponse(
		[]types.AxisKey{{"r0"}},
		[]types.AxisKey{{"c0"}},
		[][]*float64{{fpr(1)}},
	)
	ref := seriesResponse([]map[string]any{{"group": "us", "sum": 1.0}})
	spec := &types.ChainOverlaySpec{Kind: types.OverlayKindDeltaVsStage, Scope: types.OverlayScopeTotal}

	layer, warnings, err := applyDeltaVsStage(spec, target, ref, 1, 0)
	if err != nil {
		t.Fatalf("applyDeltaVsStage: %v", err)
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
	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Errorf("layer shape = %q, want %q (inherits target)", layer.Payload.Shape, types.OverlayShapeMatrix)
	}
}

// TestApplyDeltaVsStage_DispatchReplacesStub asserts that the dispatch
// table entry for OVERLAY_DELTA_VS_STAGE is no longer the chassis
// stub (applyChainStub) — it is now applyDeltaVsStage. Walks the
// runtime function pointer; runtime.FuncForPC keeps the assertion
// stable across binary layouts.
func TestApplyDeltaVsStage_DispatchReplacesStub(t *testing.T) {
	handler, ok := chainOverlayHandlers[types.OverlayKindDeltaVsStage]
	if !ok {
		t.Fatal("OVERLAY_DELTA_VS_STAGE has no dispatch entry")
	}
	got := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
	want := runtime.FuncForPC(reflect.ValueOf(applyDeltaVsStage).Pointer()).Name()
	if got != want {
		t.Errorf("OVERLAY_DELTA_VS_STAGE handler = %q, want %q (still stub?)", got, want)
	}
	stub := runtime.FuncForPC(reflect.ValueOf(applyChainStub).Pointer()).Name()
	if got == stub {
		t.Errorf("OVERLAY_DELTA_VS_STAGE handler still points to applyChainStub")
	}
}

func TestApplyDeltaVsStage_SpecOrderPreserved(t *testing.T) {
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
	// INDEX layer should carry a series entry with Statistic ~ 120.
	if layers[0].Payload.Series == nil || len(layers[0].Payload.Series.Entries) != 1 {
		t.Fatalf("INDEX layer should have one series entry, got %+v", layers[0].Payload)
	}
	idxEntry := layers[0].Payload.Series.Entries[0]
	if idxEntry.Summary.Statistic == nil {
		t.Fatal("INDEX entry Summary.Statistic = nil")
	}
	if math.Abs(*idxEntry.Summary.Statistic-120.0) > 1e-9 {
		t.Errorf("INDEX entry Statistic = %g, want 120.0", *idxEntry.Summary.Statistic)
	}
	if layers[1].Payload.Series == nil || len(layers[1].Payload.Series.Entries) != 1 {
		t.Fatalf("DELTA layer should have one series entry (stub replaced), got %+v", layers[1].Payload)
	}
	deltaEntry := layers[1].Payload.Series.Entries[0]
	if deltaEntry.Summary.Statistic == nil {
		t.Fatal("DELTA entry Summary.Statistic = nil")
	}
	if math.Abs(*deltaEntry.Summary.Statistic-20.0) > 1e-9 {
		t.Errorf("DELTA entry Statistic = %g, want 20.0 (120 - 100)", *deltaEntry.Summary.Statistic)
	}
}

// TestDeltaKernel_HappyPath confirms the kernel produces the
// documented `target - reference` shape for a known input pair.
func TestDeltaKernel_HappyPath(t *testing.T) {
	got := deltaKernel(250.0, 100.0)
	if math.Abs(got-150.0) > 1e-9 {
		t.Errorf("deltaKernel(250,100) = %g, want 150", got)
	}
}

// TestDeltaKernel_ZeroReferenceFinite confirms the kernel returns the
// target verbatim when the reference is zero — no special-casing, no
// NaN. The DELTA kernel is unconditional float subtraction.
func TestDeltaKernel_ZeroReferenceFinite(t *testing.T) {
	got := deltaKernel(50.0, 0.0)
	if math.IsNaN(got) {
		t.Errorf("deltaKernel(50,0) = NaN, want 50 (zero is a number, not a divisor)")
	}
	if math.Abs(got-50.0) > 1e-9 {
		t.Errorf("deltaKernel(50,0) = %g, want 50", got)
	}
	// Negative deltas are well-defined too.
	negative := deltaKernel(10.0, 25.0)
	if math.Abs(negative-(-15.0)) > 1e-9 {
		t.Errorf("deltaKernel(10,25) = %g, want -15", negative)
	}
}
