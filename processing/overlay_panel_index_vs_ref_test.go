package processing

import (
	stderrors "errors"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// composeSpecMultiTargetPanelIndex wires a multi-target
// ComposeOverlaySpec for the PANEL_INDEX_VS_REF tests. `targets` is the
// renderer-facing target slot labels; `opts` is the per-spec
// OverlayOptions slot (nil ⇒ default-16 cap, no DictPrefixFast).
func composeSpecMultiTargetPanelIndex(name string, targets []string, opts *types.OverlayOptions) types.ComposeOverlaySpec {
	return types.ComposeOverlaySpec{
		Name:      name,
		Kind:      types.OverlayKindPanelIndexVsRef,
		Scope:     types.OverlayScopeCell,
		Reference: "baseline",
		Targets:   targets,
		Options:   opts,
	}
}

// TestApplyPanelIndexVsRef_MultiLayer_3Targets_Matrix is the canonical
// 3-target acceptance test. Asserts:
//
//   - len(layers) == 3 (one per target)
//   - every layer carries the reference matrix's row/col axes (shared
//     coord space)
//   - per-target math matches OVERLAY_INDEX_VS_REF for that pair
//   - layers[i] corresponds to spec.Targets[i] (spec.order preserved)
func TestApplyPanelIndexVsRef_MultiLayer_3Targets_Matrix(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	// Three targets: t0 = ref * 1.5 ⇒ index 150; t1 = ref * 2.0 ⇒ index
	// 200; t2 = ref * 0.5 ⇒ index 50.
	t0 := makeMatrixFromValues([3][3]float64{
		{15, 30, 45},
		{60, 75, 90},
		{105, 120, 135},
	})
	t1 := makeMatrixFromValues([3][3]float64{
		{20, 40, 60},
		{80, 100, 120},
		{140, 160, 180},
	})
	t2 := makeMatrixFromValues([3][3]float64{
		{5, 10, 15},
		{20, 25, 30},
		{35, 40, 45},
	})
	spec := composeSpecMultiTargetPanelIndex("panel", []string{"t0", "t1", "t2"}, nil)
	layers, warnings, err := applyPanelIndexVsRef(&spec, ref, []*types.Response{t0, t1, t2}, 0, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("applyPanelIndexVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	if len(layers) != 3 {
		t.Fatalf("len(layers) = %d, want 3 (one per target)", len(layers))
	}
	// Every emitted layer must be MATRIX shape and inherit the reference
	// matrix's row/col axes (shared coord space invariant).
	refMx := ref.Crosstab.Matrix
	for i, layer := range layers {
		if layer.Payload.Shape != types.OverlayShapeMatrix {
			t.Errorf("layer[%d].Payload.Shape = %q, want %q", i, layer.Payload.Shape, types.OverlayShapeMatrix)
		}
		if layer.Kind != types.OverlayKindPanelIndexVsRef {
			t.Errorf("layer[%d].Kind = %q, want %q", i, layer.Kind, types.OverlayKindPanelIndexVsRef)
		}
		mx := layer.Payload.Matrix
		if mx == nil {
			t.Fatalf("layer[%d].Payload.Matrix is nil", i)
		}
		if len(mx.RowKeys) != len(refMx.RowKeys) {
			t.Errorf("layer[%d].RowKeys len = %d, want %d (shared coord space)", i, len(mx.RowKeys), len(refMx.RowKeys))
		}
		if len(mx.ColumnKeys) != len(refMx.ColumnKeys) {
			t.Errorf("layer[%d].ColumnKeys len = %d, want %d (shared coord space)", i, len(mx.ColumnKeys), len(refMx.ColumnKeys))
		}
		for j := range mx.RowKeys {
			if len(mx.RowKeys[j]) != len(refMx.RowKeys[j]) {
				t.Errorf("layer[%d].RowKeys[%d] tuple len = %d, want %d", i, j, len(mx.RowKeys[j]), len(refMx.RowKeys[j]))
				continue
			}
			for k := range mx.RowKeys[j] {
				if mx.RowKeys[j][k] != refMx.RowKeys[j][k] {
					t.Errorf("layer[%d].RowKeys[%d][%d] = %q, want %q (shared coord space)", i, j, k, mx.RowKeys[j][k], refMx.RowKeys[j][k])
				}
			}
		}
	}
	// Per-target math: t0 = 150 across the grid; t1 = 200; t2 = 50.
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			approxEqual(t, "layer[0] cell", cellAt(t, layers[0], r, c), 150.0, 1e-9)
			approxEqual(t, "layer[1] cell", cellAt(t, layers[1], r, c), 200.0, 1e-9)
			approxEqual(t, "layer[2] cell", cellAt(t, layers[2], r, c), 50.0, 1e-9)
		}
	}
}

// TestApplyPanelIndexVsRef_LayerNamingConvention verifies the
// `<spec.Name>__<target_label>` naming template. With spec.Name = "panel"
// and targets = ["t0", "t1"], the emitted layers must be named
// "panel__t0" and "panel__t1". The MATRIX layer's CellLabel must also
// align with the layer Name for renderer-side matrix-cell-header
// alignment.
func TestApplyPanelIndexVsRef_LayerNamingConvention(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	})
	t0 := makeMatrixFromValues([3][3]float64{
		{2, 4, 6},
		{8, 10, 12},
		{14, 16, 18},
	})
	t1 := makeMatrixFromValues([3][3]float64{
		{3, 6, 9},
		{12, 15, 18},
		{21, 24, 27},
	})
	spec := composeSpecMultiTargetPanelIndex("panel", []string{"t0", "t1"}, nil)
	layers, _, err := applyPanelIndexVsRef(&spec, ref, []*types.Response{t0, t1}, 0, []int{1, 2})
	if err != nil {
		t.Fatalf("applyPanelIndexVsRef: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("len(layers) = %d, want 2", len(layers))
	}
	if layers[0].Name != "panel__t0" {
		t.Errorf("layers[0].Name = %q, want %q", layers[0].Name, "panel__t0")
	}
	if layers[1].Name != "panel__t1" {
		t.Errorf("layers[1].Name = %q, want %q", layers[1].Name, "panel__t1")
	}
	if layers[0].Payload.Matrix == nil || layers[0].Payload.Matrix.CellLabel != "panel__t0" {
		t.Errorf("layers[0].Payload.Matrix.CellLabel = %q, want %q", layers[0].Payload.Matrix.CellLabel, "panel__t0")
	}
	if layers[1].Payload.Matrix == nil || layers[1].Payload.Matrix.CellLabel != "panel__t1" {
		t.Errorf("layers[1].Payload.Matrix.CellLabel = %q, want %q", layers[1].Payload.Matrix.CellLabel, "panel__t1")
	}

	// Name-empty fallback: spec.Name = "" ⇒ "OVERLAY_PANEL_INDEX_VS_REF__<target_label>".
	specEmpty := composeSpecMultiTargetPanelIndex("", []string{"t0", "t1"}, nil)
	emptyLayers, _, err := applyPanelIndexVsRef(&specEmpty, ref, []*types.Response{t0, t1}, 0, []int{1, 2})
	if err != nil {
		t.Fatalf("applyPanelIndexVsRef (empty Name): %v", err)
	}
	wantEmpty0 := string(types.OverlayKindPanelIndexVsRef) + "__t0"
	wantEmpty1 := string(types.OverlayKindPanelIndexVsRef) + "__t1"
	if emptyLayers[0].Name != wantEmpty0 {
		t.Errorf("empty-Name layers[0].Name = %q, want %q", emptyLayers[0].Name, wantEmpty0)
	}
	if emptyLayers[1].Name != wantEmpty1 {
		t.Errorf("empty-Name layers[1].Name = %q, want %q", emptyLayers[1].Name, wantEmpty1)
	}
}

// TestApplyPanelIndexVsRef_SpecOrderTargetOrderPreserved verifies that
// layers[i] corresponds to spec.Targets[i]. Builds a panel where every
// target carries a distinct value so the math at any cell identifies the
// target by its index.
func TestApplyPanelIndexVsRef_SpecOrderTargetOrderPreserved(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	// t0 = 10 ⇒ index 100; t1 = 30 ⇒ index 300; t2 = 50 ⇒ index 500;
	// t3 = 70 ⇒ index 700. Each target's identity is its index value.
	t0 := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	t1 := makeMatrixFromValues([3][3]float64{
		{30, 30, 30},
		{30, 30, 30},
		{30, 30, 30},
	})
	t2 := makeMatrixFromValues([3][3]float64{
		{50, 50, 50},
		{50, 50, 50},
		{50, 50, 50},
	})
	t3 := makeMatrixFromValues([3][3]float64{
		{70, 70, 70},
		{70, 70, 70},
		{70, 70, 70},
	})
	// Targets in this exact order: ["a", "b", "c", "d"].
	spec := composeSpecMultiTargetPanelIndex("panel", []string{"a", "b", "c", "d"}, nil)
	layers, _, err := applyPanelIndexVsRef(&spec, ref, []*types.Response{t0, t1, t2, t3}, 0, []int{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("applyPanelIndexVsRef: %v", err)
	}
	if len(layers) != 4 {
		t.Fatalf("len(layers) = %d, want 4", len(layers))
	}
	// layers[0] ⇒ index 100 (t0); layers[1] ⇒ 300 (t1); layers[2] ⇒ 500;
	// layers[3] ⇒ 700.
	approxEqual(t, "layers[0] (a/t0)", cellAt(t, layers[0], 0, 0), 100.0, 1e-9)
	approxEqual(t, "layers[1] (b/t1)", cellAt(t, layers[1], 0, 0), 300.0, 1e-9)
	approxEqual(t, "layers[2] (c/t2)", cellAt(t, layers[2], 0, 0), 500.0, 1e-9)
	approxEqual(t, "layers[3] (d/t3)", cellAt(t, layers[3], 0, 0), 700.0, 1e-9)
	// Name template aligns with order — layers[i].Name carries
	// spec.Targets[i].
	wants := []string{"panel__a", "panel__b", "panel__c", "panel__d"}
	for i, want := range wants {
		if layers[i].Name != want {
			t.Errorf("layers[%d].Name = %q, want %q", i, layers[i].Name, want)
		}
	}
}

// TestApplyPanelIndexVsRef_OverCap_17Targets_Rejected pins the
// over-cap-rejection path: 17 targets against the default 16-target cap
// fires PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP at handler entry without
// producing any layer.
func TestApplyPanelIndexVsRef_OverCap_17Targets_Rejected(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	// 17 identical target slots — over the default 16-target cap.
	targets := make([]*types.Response, 17)
	targetIdxs := make([]int, 17)
	targetLabels := make([]string, 17)
	for i := range targets {
		targets[i] = makeMatrixFromValues([3][3]float64{
			{10, 10, 10},
			{10, 10, 10},
			{10, 10, 10},
		})
		targetIdxs[i] = i + 1
		targetLabels[i] = "t" + string(rune('a'+i))
	}
	spec := composeSpecMultiTargetPanelIndex("panel", targetLabels, nil)
	layers, _, err := applyPanelIndexVsRef(&spec, ref, targets, 0, targetIdxs)
	if err == nil {
		t.Fatal("applyPanelIndexVsRef returned nil error against 17 targets vs default cap 16, want PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP")
	}
	if layers != nil {
		t.Errorf("layers = %+v, want nil on cap rejection", layers)
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	gotCode, _ := coded.Details["code"].(string)
	if gotCode != string(pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP) {
		t.Errorf("error code = %q, want %q", gotCode, pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
	}
	observed, _ := coded.Details["observed"].(int)
	if observed != 17 {
		t.Errorf("observed = %v, want 17", observed)
	}
	capDetail, _ := coded.Details["cap"].(int)
	if capDetail != 16 {
		t.Errorf("cap = %v, want 16", capDetail)
	}
	kindDetail, _ := coded.Details["kind"].(string)
	if kindDetail != string(types.OverlayKindPanelIndexVsRef) {
		t.Errorf("kind detail = %q, want %q", kindDetail, string(types.OverlayKindPanelIndexVsRef))
	}
}

// TestApplyPanelIndexVsRef_DefaultMaxPanelTargets_16 locks the default
// cap value at 16. Mirrors TestApplyPropZPanel_DefaultMaxPanelTargets_16
// — the catalog standardises on one cap surface so renderers can budget
// panel-layout state against a single knob.
func TestApplyPanelIndexVsRef_DefaultMaxPanelTargets_16(t *testing.T) {
	// 16 targets succeed (at cap).
	ref := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	targets := make([]*types.Response, 16)
	targetIdxs := make([]int, 16)
	targetLabels := make([]string, 16)
	for i := range targets {
		targets[i] = makeMatrixFromValues([3][3]float64{
			{20, 20, 20},
			{20, 20, 20},
			{20, 20, 20},
		})
		targetIdxs[i] = i + 1
		targetLabels[i] = "t" + string(rune('a'+i))
	}
	spec := composeSpecMultiTargetPanelIndex("panel", targetLabels, nil)
	layers, _, err := applyPanelIndexVsRef(&spec, ref, targets, 0, targetIdxs)
	if err != nil {
		t.Fatalf("applyPanelIndexVsRef against 16 targets (at cap): %v", err)
	}
	if len(layers) != 16 {
		t.Fatalf("len(layers) = %d, want 16 (one per target)", len(layers))
	}
	for i, layer := range layers {
		if layer.Kind != types.OverlayKindPanelIndexVsRef {
			t.Errorf("layers[%d].Kind = %q, want %q", i, layer.Kind, types.OverlayKindPanelIndexVsRef)
		}
		approxEqual(t, "layers[i] cell", cellAt(t, layer, 0, 0), 200.0, 1e-9)
	}
}

// TestApplyPanelIndexVsRef_CustomMaxPanelTargets verifies the per-spec
// override surface. Custom cap = 2 accepts 2 targets; cap = 2 rejects
// 3 targets. Mirrors TestApplyPropZPanel_CustomMaxPanelTargets for the
// PANEL_INDEX_VS_REF kind.
func TestApplyPanelIndexVsRef_CustomMaxPanelTargets(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	makeTargets := func(n int) ([]*types.Response, []int, []string) {
		ts := make([]*types.Response, n)
		idxs := make([]int, n)
		labels := make([]string, n)
		for i := range ts {
			ts[i] = makeMatrixFromValues([3][3]float64{
				{20, 20, 20},
				{20, 20, 20},
				{20, 20, 20},
			})
			idxs[i] = i + 1
			labels[i] = "t" + string(rune('a'+i))
		}
		return ts, idxs, labels
	}

	// 2 targets, cap=2 ⇒ accept.
	t2, idxs2, labels2 := makeTargets(2)
	spec := composeSpecMultiTargetPanelIndex("panel", labels2, &types.OverlayOptions{MaxPanelTargets: 2})
	if _, _, err := applyPanelIndexVsRef(&spec, ref, t2, 0, idxs2); err != nil {
		t.Errorf("2 targets vs cap=2: %v, want nil", err)
	}

	// 3 targets, cap=2 ⇒ reject.
	t3, idxs3, labels3 := makeTargets(3)
	spec = composeSpecMultiTargetPanelIndex("panel", labels3, &types.OverlayOptions{MaxPanelTargets: 2})
	_, _, err := applyPanelIndexVsRef(&spec, ref, t3, 0, idxs3)
	if err == nil {
		t.Fatal("3 targets vs cap=2: nil error, want PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if code, _ := coded.Details["code"].(string); code != string(pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP) {
		t.Errorf("error code = %q, want %q", code, pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
	}
	if capDetail, _ := coded.Details["cap"].(int); capDetail != 2 {
		t.Errorf("cap = %v, want 2", capDetail)
	}
}

// TestApplyPanelIndexVsRef_SeriesShape_3Targets exercises the series-arm
// dispatch — when the reference and every target carry SERIES-shape
// host responses, the per-target emission lands SERIES-shape layers.
// Each emitted layer's series payload matches the corresponding
// single-target OVERLAY_INDEX_VS_REF SERIES output for that
// (reference, target[i]) pair.
func TestApplyPanelIndexVsRef_SeriesShape_3Targets(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south", "east", "west"}, []float64{10, 20, 30, 40})
	// t0 = 1.5x ref ⇒ index 150; t1 = 2.0x ref ⇒ index 200; t2 = 0.5x ref
	// ⇒ index 50.
	t0 := makeSeriesResponse([]string{"north", "south", "east", "west"}, []float64{15, 30, 45, 60})
	t1 := makeSeriesResponse([]string{"north", "south", "east", "west"}, []float64{20, 40, 60, 80})
	t2 := makeSeriesResponse([]string{"north", "south", "east", "west"}, []float64{5, 10, 15, 20})

	spec := types.ComposeOverlaySpec{
		Name:      "panel",
		Kind:      types.OverlayKindPanelIndexVsRef,
		Scope:     types.OverlayScopeGroup,
		Reference: "baseline",
		Targets:   []string{"t0", "t1", "t2"},
	}
	layers, warnings, err := applyPanelIndexVsRef(&spec, ref, []*types.Response{t0, t1, t2}, 0, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("applyPanelIndexVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	if len(layers) != 3 {
		t.Fatalf("len(layers) = %d, want 3", len(layers))
	}
	// Every emitted layer must be SERIES shape (per-target shape matches
	// host).
	for i, layer := range layers {
		if layer.Payload.Shape != types.OverlayShapeSeries {
			t.Errorf("layer[%d].Payload.Shape = %q, want %q", i, layer.Payload.Shape, types.OverlayShapeSeries)
		}
		if layer.Kind != types.OverlayKindPanelIndexVsRef {
			t.Errorf("layer[%d].Kind = %q, want %q", i, layer.Kind, types.OverlayKindPanelIndexVsRef)
		}
	}
	// Per-target math: t0 ⇒ 150, t1 ⇒ 200, t2 ⇒ 50.
	for i := 0; i < 4; i++ {
		approxEqual(t, "layers[0] entry (t0=1.5x)", entryStat(t, layers[0], i), 150.0, 1e-9)
		approxEqual(t, "layers[1] entry (t1=2.0x)", entryStat(t, layers[1], i), 200.0, 1e-9)
		approxEqual(t, "layers[2] entry (t2=0.5x)", entryStat(t, layers[2], i), 50.0, 1e-9)
	}
	// Naming convention extends to SERIES layers too.
	wantNames := []string{"panel__t0", "panel__t1", "panel__t2"}
	for i, want := range wantNames {
		if layers[i].Name != want {
			t.Errorf("layers[%d].Name = %q, want %q", i, layers[i].Name, want)
		}
	}
}

// TestApplyPanelIndexVsRef_LayerMathMatchesSingleTargetIndexVsRef
// exercises strict per-target equivalence with the single-target
// OVERLAY_INDEX_VS_REF output. For each target[i], the PANEL layer's
// per-cell value must equal the matching cell of the single-target
// applyIndexVsRef call for the same (reference, target[i]) pair. This
// pins the "math reuse" contract from the kind's catalog row.
func TestApplyPanelIndexVsRef_LayerMathMatchesSingleTargetIndexVsRef(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	t0 := makeMatrixFromValues([3][3]float64{
		{15, 30, 45},
		{60, 75, 90},
		{105, 120, 135},
	})
	t1 := makeMatrixFromValues([3][3]float64{
		{20, 40, 60},
		{80, 100, 120},
		{140, 160, 180},
	})

	panelSpec := composeSpecMultiTargetPanelIndex("panel", []string{"t0", "t1"}, nil)
	panelLayers, _, err := applyPanelIndexVsRef(&panelSpec, ref, []*types.Response{t0, t1}, 0, []int{1, 2})
	if err != nil {
		t.Fatalf("applyPanelIndexVsRef: %v", err)
	}

	// Build a single-target INDEX_VS_REF spec for each target and compare.
	singleSpecT0 := composeSpecMatrixRef(types.OverlayKindIndexVsRef, nil)
	singleSpecT0.Targets = []string{"t0"}
	singleLayerT0, _, err := applyIndexVsRef(&singleSpecT0, ref, []*types.Response{t0}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef t0: %v", err)
	}
	singleSpecT1 := composeSpecMatrixRef(types.OverlayKindIndexVsRef, nil)
	singleSpecT1.Targets = []string{"t1"}
	singleLayerT1, _, err := applyIndexVsRef(&singleSpecT1, ref, []*types.Response{t1}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef t1: %v", err)
	}

	// Compare every cell between panel[0] and single-target t0; every
	// cell between panel[1] and single-target t1.
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			panelV0 := cellAt(t, panelLayers[0], r, c)
			singleV0 := cellAt(t, singleLayerT0, r, c)
			approxEqual(t, "panel[0] vs single t0", panelV0, singleV0, 1e-12)
			panelV1 := cellAt(t, panelLayers[1], r, c)
			singleV1 := cellAt(t, singleLayerT1, r, c)
			approxEqual(t, "panel[1] vs single t1", panelV1, singleV1, 1e-12)
		}
	}
}
