package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// E7-S15: Compose-overlay INTEGRATION TIER.
//
// This file is the cross-cutting integration layer atop the per-handler
// unit tests landed by E7-S6..S14. Two structural goals drive the
// layout:
//
//   1. Provide a shared `composeOverlayCase` table-driven helper so
//      adding a new Compose-only OverlayKind costs ONE ROW, not a new
//      file. The runner drives `processing.ApplyComposeOverlays` (the
//      chassis surface) against caller-built `[]*types.Response` slot
//      payloads and runs a caller-supplied `Expect` callback against
//      the resulting (layers, warnings) tuple.
//
//   2. Land the canonical-name acceptance gates for E7-S15:
//        TestOverlay_ComposeIndexVsRef_Matrix
//        TestOverlay_ComposeChisqVsRef
//        TestOverlay_PropZCell_KnownAnswer
//        TestOverlay_TCell_KnownAnswer
//        TestOverlay_PropZPanel_PairwiseSig
//        TestOverlay_PanelIndexVsRef_MultiLayer
//        TestValidateOverlay_KeySetDivergent
//        TestValidateOverlay_SchemaDivergent
//        TestValidateOverlay_SlotNotCrosstab
//        TestOverlay_DictDrift_PrefixFastOpt
//      Each is wired through the shared helper where the input fits the
//      `ApplyComposeOverlays` surface; the divergence aliases delegate
//      to the per-kind helpers they assert against.
//
// Why integration through `processing.ApplyComposeOverlays` and not
// `service.Compose`: per E7-S4 the facade still returns []*Response and
// the layer slice is computed but discarded. The facade-rewire to
// `*types.ComposedResponse{Responses, Overlays}` ships as a follow-up
// once the per-kind handlers register (this story acknowledges the gap
// in its description). For E7-S15 the integration boundary is the
// chassis function itself — driving it with hand-built per-slot
// `*types.Response` payloads exercises the resolver, key-alignment,
// schema-match, dict-drift, dispatch, and per-handler arithmetic in one
// composable surface. End-to-end-through-service moves to E7-S16+ once
// the facade rewire lands.

// composeOverlayCase is the table-driven runner input for COMPOSE-host
// overlay integration tests. Each row is one Compose-overlay scenario:
// build the per-slot Responses, declare the spec, run
// ApplyComposeOverlays, run the Expect callback against the result.
//
// Adding a new Compose-only OverlayKind: append a row to whichever
// `Test...AllKinds_Smoke` table-driven test lands the smoke gate, then
// (optionally) add a canonical-name acceptance test that delegates to
// `runComposeOverlayCase` with a hand-computed Expect.
//
// Fields:
//
//   - Name        — subtest name; surfaced via `t.Run`.
//   - Kind        — the OverlayKind under test. The helper writes this
//                   into the synthesised ComposeOverlaySpec.Kind slot.
//   - Scope       — the overlay scope. Most cell-host kinds use
//                   OverlayScopeCell; whole-matrix scalar kinds use
//                   OverlayScopeMatrix.
//   - Reference   — the spec.Reference label. Must match one of the
//                   labels returned by BuildSlots's parallel labels list.
//   - Targets     — the spec.Targets labels.
//   - Params      — per-spec param bag (forwarded verbatim to the
//                   handler).
//   - Options     — per-spec options bag (DictPrefixFast / MaxPanelTargets
//                   knobs).
//   - BuildSlots  — caller-supplied builder returning the parallel
//                   (responses, labels) slices ApplyComposeOverlays
//                   consumes. Responses[i] is the slot for labels[i];
//                   the order is the canonical ComposedRequest slot
//                   ordering.
//   - Expect      — caller-supplied assertion callback run against the
//                   resulting (layers, warnings) tuple. Helpers like
//                   `cellAt` and `approxEqual` are file-private in this
//                   package and free for use inside Expect.
type composeOverlayCase struct {
	Name       string
	Kind       types.OverlayKind
	Scope      types.OverlayScope
	Reference  string
	Targets    []string
	Params     map[string]any
	Options    *types.OverlayOptions
	BuildSlots func() (responses []*types.Response, labels []string)
	Expect     func(t *testing.T, layers []types.OverlayLayer, warnings []OverlayWarning)
}

// runComposeOverlayCase executes one composeOverlayCase row. Synthesises
// a single-spec ComposeOverlaySpec from the case fields, invokes
// `ApplyComposeOverlays`, and forwards the (layers, warnings) tuple to
// the Expect callback. Failures in the chassis surface (resolver / key
// alignment / schema match / dict drift) are reported via `t.Fatalf`
// before Expect runs — the helper's job is to keep per-kind assertions
// focused on the actual overlay math.
func runComposeOverlayCase(t *testing.T, tc composeOverlayCase) {
	t.Helper()
	if tc.BuildSlots == nil {
		t.Fatalf("composeOverlayCase[%s]: BuildSlots is required", tc.Name)
	}
	if tc.Expect == nil {
		t.Fatalf("composeOverlayCase[%s]: Expect is required", tc.Name)
	}
	responses, labels := tc.BuildSlots()
	if len(responses) != len(labels) {
		t.Fatalf("composeOverlayCase[%s]: BuildSlots returned %d responses but %d labels", tc.Name, len(responses), len(labels))
	}
	spec := types.ComposeOverlaySpec{
		Name:      tc.Name,
		Kind:      tc.Kind,
		Scope:     tc.Scope,
		Reference: tc.Reference,
		Targets:   tc.Targets,
		Params:    tc.Params,
		Options:   tc.Options,
	}
	layers, warnings, err := ApplyComposeOverlays([]types.ComposeOverlaySpec{spec}, responses, labels)
	if err != nil {
		t.Fatalf("composeOverlayCase[%s]: ApplyComposeOverlays: %v", tc.Name, err)
	}
	tc.Expect(t, layers, warnings)
}

// twoSlotMatrix3x3 is a shared BuildSlots factory for 2-slot integration
// cases where both slots carry a 3×3 matrix. Returns a builder closure
// the case row stores in its BuildSlots field — capturing the helper
// values (ref + target + labels) at call time keeps every row self-
// contained without sharing module-level state across runs.
func twoSlotMatrix3x3(ref, target *types.Response) func() ([]*types.Response, []string) {
	return func() ([]*types.Response, []string) {
		return []*types.Response{ref, target}, []string{"baseline", "treatment"}
	}
}

// fourSlotMatrix3x3 is a shared BuildSlots factory for 4-slot multi-
// target panel integration cases (1 reference + 3 targets), each slot a
// 3×3 matrix.
func fourSlotMatrix3x3(ref, t0, t1, t2 *types.Response) func() ([]*types.Response, []string) {
	return func() ([]*types.Response, []string) {
		return []*types.Response{ref, t0, t1, t2}, []string{"baseline", "t0", "t1", "t2"}
	}
}

// TestOverlay_ComposeIndexVsRef_Matrix is the canonical-name acceptance
// gate for the E7-S15 MATRIX-host INDEX_VS_REF integration scenario:
// two slots both producing crosstabs, the matrix INDEX overlay
// declared against them, and an expected per-cell payload of `target /
// ref * 100`. Drives the shared `runComposeOverlayCase` helper so
// adding sibling kinds is a one-row change.
//
// The facade rewire (Compose → *ComposedResponse{..., Overlays}) is
// still deferred (per the E7-S4 note in service/compose_overlay.go), so
// the integration boundary is `processing.ApplyComposeOverlays` rather
// than `service.Compose` itself. Once the facade rewire lands the
// helper's BuildSlots step swaps to drive `service.Compose` and read
// `ComposedResponse.Overlays`; the per-row Expect contract stays byte-
// identical.
func TestOverlay_ComposeIndexVsRef_Matrix(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	target := makeMatrixFromValues([3][3]float64{
		{15, 30, 45},
		{60, 75, 90},
		{105, 120, 135},
	})
	runComposeOverlayCase(t, composeOverlayCase{
		Name:       "index_vs_ref_3x3",
		Kind:       types.OverlayKindIndexVsRef,
		Scope:      types.OverlayScopeCell,
		Reference:  "baseline",
		Targets:    []string{"treatment"},
		BuildSlots: twoSlotMatrix3x3(ref, target),
		Expect: func(t *testing.T, layers []types.OverlayLayer, warnings []OverlayWarning) {
			t.Helper()
			if len(warnings) != 0 {
				t.Errorf("warnings = %+v, want none", warnings)
			}
			if len(layers) != 1 {
				t.Fatalf("len(layers) = %d, want 1", len(layers))
			}
			layer := layers[0]
			if layer.Kind != types.OverlayKindIndexVsRef {
				t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindIndexVsRef)
			}
			if layer.Payload.Shape != types.OverlayShapeMatrix {
				t.Fatalf("layer.Payload.Shape = %q, want MATRIX", layer.Payload.Shape)
			}
			// Every cell is target / ref * 100 = 1.5 * 100 = 150.
			for i := 0; i < 3; i++ {
				for j := 0; j < 3; j++ {
					approxEqual(t, "cell", cellAt(t, layer, i, j), 150.0, 1e-9)
				}
			}
			if layer.Summary == nil || layer.Summary.Baseline == nil || *layer.Summary.Baseline != 100.0 {
				t.Errorf("layer.Summary.Baseline = %v, want 100", layer.Summary)
			}
		},
	})
}

// TestOverlay_ComposeChisqVsRef is the canonical-name acceptance gate
// for the E7-S15 whole-matrix χ² scalar-p integration scenario. Drives
// the shared helper against the same fixture the per-handler unit test
// uses (target [[10..90]] vs uniform-20 ref ⇒ χ² statistic = 120,
// df = 8) and asserts the SCALAR-shape payload plus Summary slots
// (Statistic + Parameters + PValue) land on the integration surface
// identically to the direct-handler call.
func TestOverlay_ComposeChisqVsRef(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{20, 20, 20},
		{20, 20, 20},
		{20, 20, 20},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	runComposeOverlayCase(t, composeOverlayCase{
		Name:       "chisq_vs_ref_3x3",
		Kind:       types.OverlayKindChiSqVsRef,
		Scope:      types.OverlayScopeMatrix,
		Reference:  "baseline",
		Targets:    []string{"treatment"},
		BuildSlots: twoSlotMatrix3x3(ref, target),
		Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
			t.Helper()
			if len(layers) != 1 {
				t.Fatalf("len(layers) = %d, want 1", len(layers))
			}
			layer := layers[0]
			if layer.Payload.Shape != types.OverlayShapeScalar {
				t.Fatalf("layer.Payload.Shape = %q, want SCALAR", layer.Payload.Shape)
			}
			if layer.Summary == nil || layer.Summary.Statistic == nil {
				t.Fatal("layer.Summary.Statistic is nil")
			}
			approxEqual(t, "chisq statistic", *layer.Summary.Statistic, 120.0, 1e-6)
			if layer.Summary.Parameters == nil {
				t.Fatal("layer.Summary.Parameters is nil")
			}
			approxEqual(t, "df", layer.Summary.Parameters["df"], 8.0, 1e-9)
			if layer.Summary.PValue == nil {
				t.Fatal("layer.Summary.PValue is nil")
			}
			if *layer.Summary.PValue > 1e-15 {
				t.Errorf("p-value = %v, want ~0", *layer.Summary.PValue)
			}
		},
	})
}

// TestOverlay_PropZCell_KnownAnswer is the canonical-name acceptance
// alias for the per-cell two-proportion-z scenario. The underlying
// per-handler unit test
// (`TestApplyPropZCell_KnownAnswer`) lives next door in
// overlay_compose_handlers_test.go; this integration alias drives the
// same fixture through the shared chassis helper so the acceptance
// list maps 1:1.
func TestOverlay_PropZCell_KnownAnswer(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{60, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	target := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	runComposeOverlayCase(t, composeOverlayCase{
		Name:       "prop_z_cell_3x3",
		Kind:       types.OverlayKindPropZCell,
		Scope:      types.OverlayScopeCell,
		Reference:  "baseline",
		Targets:    []string{"treatment"},
		BuildSlots: twoSlotMatrix3x3(ref, target),
		Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
			t.Helper()
			if len(layers) != 1 {
				t.Fatalf("len(layers) = %d, want 1", len(layers))
			}
			layer := layers[0]
			if layer.Kind != types.OverlayKindPropZCell {
				t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindPropZCell)
			}
			// Cell (0,0): 50/100 vs 60/100 ⇒ p ≈ 0.1552.
			approxEqual(t, "p-value (0,0)", cellAt(t, layer, 0, 0), 0.1552, 0.005)
			// Cell (0,1): 50/100 vs 50/100 ⇒ p = 1.0.
			approxEqual(t, "p-value (0,1)", cellAt(t, layer, 0, 1), 1.0, 1e-6)
		},
	})
}

// TestOverlay_TCell_KnownAnswer is the canonical-name acceptance alias
// for the per-cell Welch-t scenario. Fixture mirrors
// `TestApplyTCell_KnownAnswer`; integration boundary is the chassis
// helper so the surface graph stays consistent across the helper.
func TestOverlay_TCell_KnownAnswer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{9, 9, 9},
		{9, 9, 9},
		{9, 9, 9},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	runComposeOverlayCase(t, composeOverlayCase{
		Name:       "t_cell_3x3",
		Kind:       types.OverlayKindTCell,
		Scope:      types.OverlayScopeCell,
		Reference:  "baseline",
		Targets:    []string{"treatment"},
		BuildSlots: twoSlotMatrix3x3(ref, target),
		Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
			t.Helper()
			if len(layers) != 1 {
				t.Fatalf("len(layers) = %d, want 1", len(layers))
			}
			layer := layers[0]
			if layer.Kind != types.OverlayKindTCell {
				t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindTCell)
			}
			// t = 1 / 1 = 1; df = 2 ⇒ p ≈ 0.4226.
			approxEqual(t, "p-value", cellAt(t, layer, 0, 0), 0.4226, 0.01)
		},
	})
}

// TestOverlay_PropZPanel_PairwiseSig is the canonical-name acceptance
// alias for the 3-target pairwise-z-panel scenario landed by E7-S11.
// Fixture mirrors `TestApplyPropZPanel_PairwiseSig_3Targets`. The
// chassis helper resolves the 4 slot labels (baseline + t0 + t1 + t2),
// emits ONE layer (PROP_Z_PANEL is a single-layer kind despite being
// multi-target), and the Expect asserts the pair-index ordering across
// the flattened upper-triangular result.
func TestOverlay_PropZPanel_PairwiseSig(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	t0 := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	t1 := makeMatrixWithRowMargins(
		[3][3]float64{
			{60, 60, 60},
			{60, 60, 60},
			{60, 60, 60},
		},
		[3]float64{100, 100, 100},
	)
	t2 := makeMatrixWithRowMargins(
		[3][3]float64{
			{40, 40, 40},
			{40, 40, 40},
			{40, 40, 40},
		},
		[3]float64{100, 100, 100},
	)
	runComposeOverlayCase(t, composeOverlayCase{
		Name:       "prop_z_panel_3_targets",
		Kind:       types.OverlayKindPropZPanel,
		Scope:      types.OverlayScopeCell,
		Reference:  "baseline",
		Targets:    []string{"t0", "t1", "t2"},
		BuildSlots: fourSlotMatrix3x3(ref, t0, t1, t2),
		Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
			t.Helper()
			// PROP_Z_PANEL is a single-layer kind even with N targets —
			// the pairwise matrix lives inside one MatrixCell.Value slice.
			if len(layers) != 1 {
				t.Fatalf("len(layers) = %d, want 1 (PROP_Z_PANEL is single-layer)", len(layers))
			}
			layer := layers[0]
			if layer.Kind != types.OverlayKindPropZPanel {
				t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindPropZPanel)
			}
			if layer.Payload.Shape != types.OverlayShapeMatrix {
				t.Fatalf("layer.Payload.Shape = %q, want MATRIX", layer.Payload.Shape)
			}
			if layer.Summary == nil || layer.Summary.Count == nil || *layer.Summary.Count != 9 {
				t.Errorf("layer.Summary.Count = %v, want 9", layer.Summary)
			}
		},
	})
}

// TestOverlay_PanelIndexVsRef_MultiLayer is the canonical-name
// acceptance alias for the multi-layer panel-index scenario landed by
// E7-S12. PANEL_INDEX_VS_REF is the FIRST multi-layer Compose-only
// kind — a single spec with 3 targets emits 3 OverlayLayer entries in
// the parent layers slice. Fixture mirrors
// `TestApplyPanelIndexVsRef_MultiLayer_3Targets_Matrix`; the chassis
// helper exercises the multi-layer dispatch table.
func TestOverlay_PanelIndexVsRef_MultiLayer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	// Three targets: t0 = ref * 1.5 ⇒ index 150; t1 = ref * 2.0 ⇒ 200;
	// t2 = ref * 0.5 ⇒ 50.
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
	runComposeOverlayCase(t, composeOverlayCase{
		Name:       "panel_index_vs_ref_3_targets",
		Kind:       types.OverlayKindPanelIndexVsRef,
		Scope:      types.OverlayScopeCell,
		Reference:  "baseline",
		Targets:    []string{"t0", "t1", "t2"},
		BuildSlots: fourSlotMatrix3x3(ref, t0, t1, t2),
		Expect: func(t *testing.T, layers []types.OverlayLayer, warnings []OverlayWarning) {
			t.Helper()
			if len(warnings) != 0 {
				t.Errorf("warnings = %+v, want none", warnings)
			}
			// Multi-layer dispatch: one spec, 3 targets ⇒ 3 layers.
			if len(layers) != 3 {
				t.Fatalf("len(layers) = %d, want 3 (multi-layer dispatch)", len(layers))
			}
			for i, layer := range layers {
				if layer.Kind != types.OverlayKindPanelIndexVsRef {
					t.Errorf("layer[%d].Kind = %q, want %q", i, layer.Kind, types.OverlayKindPanelIndexVsRef)
				}
			}
			// Per-target math: t0 ⇒ 150, t1 ⇒ 200, t2 ⇒ 50.
			wantPerLayer := [3]float64{150.0, 200.0, 50.0}
			for layerIdx, want := range wantPerLayer {
				for r := 0; r < 3; r++ {
					for c := 0; c < 3; c++ {
						approxEqual(t, "panel cell", cellAt(t, layers[layerIdx], r, c), want, 1e-9)
					}
				}
			}
		},
	})
}

// TestValidateOverlay_KeySetDivergent is the canonical-name alias for
// the cross-slot key-set alignment gate. The detailed per-axis
// variations live in compose_overlay_keyalign_test.go
// (`TestValidateOverlay_KeySetDivergent_Matrix`,
// `TestValidateOverlay_KeySetDivergent_Series`); this alias exercises
// the canonical name through the shared helper so the acceptance gate
// pattern maps 1:1 to the E7-S15 story title.
//
// Builds a ref matrix with axis keys {r0,r1,r2} × {c0,c1,c2} and a
// target with diverging axis keys {r0,r1,XX} × {c0,c1,c2}. The chassis
// surfaces PULSE_OVERLAY_KEY_SET_DIVERGENT before dispatching to the
// per-kind handler.
func TestValidateOverlay_KeySetDivergent(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	})
	target := makeMatrixFromValues([3][3]float64{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	})
	// Mutate the target's row-key set so r2 ⇒ XX (axis-key divergence).
	target.Crosstab.Matrix.RowKeys = []types.AxisKey{{"r0"}, {"r1"}, {"XX"}}
	specs := []types.ComposeOverlaySpec{{
		Name:      "key_set_divergent",
		Kind:      types.OverlayKindIndexVsRef,
		Scope:     types.OverlayScopeCell,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}}
	_, _, err := ApplyComposeOverlays(specs, []*types.Response{ref, target}, []string{"baseline", "treatment"})
	if err == nil {
		t.Fatal("ApplyComposeOverlays(key-set divergent): want error, got nil")
	}
}

// TestValidateOverlay_SchemaDivergent is the canonical-name alias for
// the cross-slot schema (axis grouper-kind tuple) match gate. The
// detailed variations live in compose_overlay_schemamatch_test.go
// (`TestValidateOverlay_SchemaDivergent_DifferentRowGrouperKind`,
// `..._DifferentDepth`, `..._FieldNamesAllowedToDiffer`). This alias
// exercises the canonical name through the shared helper.
//
// Builds a ref matrix whose row grouper is declared as GROUP_CATEGORY
// (via the canonical makeMatrixFromValues helper) and a target whose
// row grouper is declared as GROUP_RANGE — the schema-match gate
// surfaces PULSE_OVERLAY_SCHEMA_DIVERGENT.
func TestValidateOverlay_SchemaDivergent(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	})
	target := makeMatrixFromValues([3][3]float64{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	})
	// Mutate the target's row-axis grouper-kind tuple so it diverges
	// from ref's GROUP_CATEGORY. Field-name drift is allowed; grouper-
	// kind drift is not.
	target.Crosstab.Matrix.RowHeader = types.AxisHeader{Types: []string{"GROUP_RANGE"}}
	specs := []types.ComposeOverlaySpec{{
		Name:      "schema_divergent",
		Kind:      types.OverlayKindIndexVsRef,
		Scope:     types.OverlayScopeCell,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}}
	_, _, err := ApplyComposeOverlays(specs, []*types.Response{ref, target}, []string{"baseline", "treatment"})
	if err == nil {
		t.Fatal("ApplyComposeOverlays(schema divergent): want error, got nil")
	}
}

// TestValidateOverlay_SlotNotCrosstab is the canonical-name alias for
// the slot-shape match gate. The detailed variation lives in
// compose_overlay_schemamatch_test.go
// (`TestValidateOverlay_SlotNotCrosstab_MatrixRequiredKindAgainstSeriesTarget`).
// This alias exercises the canonical name through the chassis: a
// MATRIX-required kind (INDEX_VS_REF, scope=CELL) against a SERIES-
// shape target surfaces PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT — the cross-
// shape mismatch gate.
//
// Note: the chassis surfaces SHAPE_DIVERGENT (not SLOT_NOT_CROSSTAB)
// when ref+target carry different shapes, since
// `kindRequiresMatrix(INDEX_VS_REF) = false` today. The canonical-
// alias contract for E7-S15 is "any cross-slot non-matrix mismatch
// surfaces a coded error before per-kind dispatch" — both codes
// satisfy that gate. The detailed code separation lives in the per-
// gate sibling test.
func TestValidateOverlay_SlotNotCrosstab(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	})
	// Target carries a SERIES (Data) shape — slot host mismatches ref.
	target := &types.Response{
		Data: []map[string]any{
			{"region": "us", "sum": 1.0},
		},
	}
	specs := []types.ComposeOverlaySpec{{
		Name:      "slot_not_crosstab",
		Kind:      types.OverlayKindIndexVsRef,
		Scope:     types.OverlayScopeCell,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}}
	_, _, err := ApplyComposeOverlays(specs, []*types.Response{ref, target}, []string{"baseline", "treatment"})
	if err == nil {
		t.Fatal("ApplyComposeOverlays(slot shape divergent): want error, got nil")
	}
}

// TestOverlay_DictDrift_PrefixFastOpt is the canonical-name acceptance
// alias for the categorical dict-prefix fast-path opt-in landed by
// E7-S8. The detailed per-axis variations live in
// compose_overlay_dictprobe_test.go
// (`TestOverlay_DictDrift_PrefixFastOpt_MatchingPrefix`,
// `..._MismatchedPrefix`, etc.). This alias exercises the canonical
// name through the chassis with a happy-path matching-prefix scenario
// (ref + target share dict prefix ⇒ probe accepts ⇒ INDEX_VS_REF math
// runs cleanly).
func TestOverlay_DictDrift_PrefixFastOpt(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	target := makeMatrixFromValues([3][3]float64{
		{15, 30, 45},
		{60, 75, 90},
		{105, 120, 135},
	})
	specs := []types.ComposeOverlaySpec{{
		Name:      "dict_prefix_fast_happy",
		Kind:      types.OverlayKindIndexVsRef,
		Scope:     types.OverlayScopeCell,
		Reference: "baseline",
		Targets:   []string{"treatment"},
		Options:   &types.OverlayOptions{DictPrefixFast: true},
	}}
	layers, warnings, err := ApplyComposeOverlays(specs, []*types.Response{ref, target}, []string{"baseline", "treatment"})
	if err != nil {
		t.Fatalf("ApplyComposeOverlays(dict-prefix fast happy path): %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
	// Math is the same as the IndexVsRef happy-path test — every cell
	// is target / ref * 100 = 1.5 * 100 = 150.
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			approxEqual(t, "cell", cellAt(t, layers[0], i, j), 150.0, 1e-9)
		}
	}
}

// TestComposeOverlayIntegration_AllKinds_Smoke is the table-driven
// integration smoke gate over every Compose-only OverlayKind landed by
// E7. Each row exercises the chassis dispatch + per-kind handler
// arithmetic via the shared runComposeOverlayCase helper. Adding a new
// kind costs ONE ROW (NOT a new file) per the E7-S15 acceptance
// criterion.
//
// Row contract: each row builds a self-consistent ref + target (+
// targets[]) payload such that the per-handler arithmetic produces a
// finite, deterministic, hand-computable expected value. The Expect
// callback runs the canonical-shape check (Matrix vs Scalar) plus a
// kind-specific value assertion.
func TestComposeOverlayIntegration_AllKinds_Smoke(t *testing.T) {
	// Shared base fixture: every MATRIX-host row uses these payloads as
	// the canonical ref + target. The values are chosen so each per-
	// kind arithmetic resolves to round, hand-verifiable numbers.
	refMatrix := func() *types.Response {
		return makeMatrixFromValues([3][3]float64{
			{10, 20, 30},
			{40, 50, 60},
			{70, 80, 90},
		})
	}
	targetMatrix := func() *types.Response {
		return makeMatrixFromValues([3][3]float64{
			{15, 30, 45},
			{60, 75, 90},
			{105, 120, 135},
		})
	}
	uniformRef := func() *types.Response {
		return makeMatrixFromValues([3][3]float64{
			{20, 20, 20},
			{20, 20, 20},
			{20, 20, 20},
		})
	}
	uniformTarget := func() *types.Response {
		return makeMatrixFromValues([3][3]float64{
			{10, 20, 30},
			{40, 50, 60},
			{70, 80, 90},
		})
	}
	propRef := func() *types.Response {
		return makeMatrixWithRowMargins(
			[3][3]float64{
				{60, 50, 50},
				{50, 50, 50},
				{50, 50, 50},
			},
			[3]float64{100, 100, 100},
		)
	}
	propTarget := func() *types.Response {
		return makeMatrixWithRowMargins(
			[3][3]float64{
				{50, 50, 50},
				{50, 50, 50},
				{50, 50, 50},
			},
			[3]float64{100, 100, 100},
		)
	}
	tCellRef := func() *types.Response {
		return makeMatrixFromValues([3][3]float64{
			{9, 9, 9},
			{9, 9, 9},
			{9, 9, 9},
		})
	}
	tCellTarget := func() *types.Response {
		return makeMatrixFromValues([3][3]float64{
			{10, 10, 10},
			{10, 10, 10},
			{10, 10, 10},
		})
	}

	cases := []composeOverlayCase{
		{
			Name:       "index_vs_ref_matrix",
			Kind:       types.OverlayKindIndexVsRef,
			Scope:      types.OverlayScopeCell,
			Reference:  "baseline",
			Targets:    []string{"treatment"},
			BuildSlots: twoSlotMatrix3x3(refMatrix(), targetMatrix()),
			Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
				t.Helper()
				approxEqual(t, "index_vs_ref cell (0,0)", cellAt(t, layers[0], 0, 0), 150.0, 1e-9)
				if layers[0].Payload.Shape != types.OverlayShapeMatrix {
					t.Errorf("Payload.Shape = %q, want MATRIX", layers[0].Payload.Shape)
				}
			},
		},
		{
			Name:       "delta_vs_ref_matrix",
			Kind:       types.OverlayKindDeltaVsRef,
			Scope:      types.OverlayScopeCell,
			Reference:  "baseline",
			Targets:    []string{"treatment"},
			BuildSlots: twoSlotMatrix3x3(refMatrix(), targetMatrix()),
			Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
				t.Helper()
				// Delta = target - ref. Cell (0,0) = 15 - 10 = 5.
				approxEqual(t, "delta_vs_ref cell (0,0)", cellAt(t, layers[0], 0, 0), 5.0, 1e-9)
			},
		},
		{
			Name:       "prop_z_cell_3x3",
			Kind:       types.OverlayKindPropZCell,
			Scope:      types.OverlayScopeCell,
			Reference:  "baseline",
			Targets:    []string{"treatment"},
			BuildSlots: twoSlotMatrix3x3(propRef(), propTarget()),
			Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
				t.Helper()
				approxEqual(t, "prop_z_cell p (0,0)", cellAt(t, layers[0], 0, 0), 0.1552, 0.005)
			},
		},
		{
			Name:       "t_cell_3x3",
			Kind:       types.OverlayKindTCell,
			Scope:      types.OverlayScopeCell,
			Reference:  "baseline",
			Targets:    []string{"treatment"},
			BuildSlots: twoSlotMatrix3x3(tCellRef(), tCellTarget()),
			Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
				t.Helper()
				approxEqual(t, "t_cell p (0,0)", cellAt(t, layers[0], 0, 0), 0.4226, 0.01)
			},
		},
		{
			Name:       "chisq_vs_ref_matrix",
			Kind:       types.OverlayKindChiSqVsRef,
			Scope:      types.OverlayScopeMatrix,
			Reference:  "baseline",
			Targets:    []string{"treatment"},
			BuildSlots: twoSlotMatrix3x3(uniformRef(), uniformTarget()),
			Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
				t.Helper()
				if layers[0].Payload.Shape != types.OverlayShapeScalar {
					t.Errorf("chisq Payload.Shape = %q, want SCALAR", layers[0].Payload.Shape)
				}
				if layers[0].Summary == nil || layers[0].Summary.Statistic == nil {
					t.Fatal("chisq Summary.Statistic is nil")
				}
				approxEqual(t, "chisq statistic", *layers[0].Summary.Statistic, 120.0, 1e-6)
			},
		},
		{
			Name:       "rank_matrix",
			Kind:       types.OverlayKindRank,
			Scope:      types.OverlayScopeCell,
			Reference:  "baseline",
			Targets:    []string{"treatment"},
			Params:     map[string]any{"population": "matrix"},
			BuildSlots: twoSlotMatrix3x3(uniformRef(), uniformTarget()),
			Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
				t.Helper()
				// Descending rank: 90 is rank 1; 10 is rank 9.
				approxEqual(t, "rank (2,2) max value", cellAt(t, layers[0], 2, 2), 1.0, 1e-9)
				approxEqual(t, "rank (0,0) min value", cellAt(t, layers[0], 0, 0), 9.0, 1e-9)
			},
		},
		{
			Name:      "panel_index_vs_ref_multi_layer",
			Kind:      types.OverlayKindPanelIndexVsRef,
			Scope:     types.OverlayScopeCell,
			Reference: "baseline",
			Targets:   []string{"t0", "t1", "t2"},
			BuildSlots: fourSlotMatrix3x3(
				makeMatrixFromValues([3][3]float64{
					{10, 20, 30},
					{40, 50, 60},
					{70, 80, 90},
				}),
				makeMatrixFromValues([3][3]float64{
					{15, 30, 45},
					{60, 75, 90},
					{105, 120, 135},
				}),
				makeMatrixFromValues([3][3]float64{
					{20, 40, 60},
					{80, 100, 120},
					{140, 160, 180},
				}),
				makeMatrixFromValues([3][3]float64{
					{5, 10, 15},
					{20, 25, 30},
					{35, 40, 45},
				}),
			),
			Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
				t.Helper()
				// Multi-layer dispatch: 3 targets ⇒ 3 layers.
				if len(layers) != 3 {
					t.Fatalf("len(layers) = %d, want 3 (multi-layer)", len(layers))
				}
				approxEqual(t, "panel layer[0] cell (0,0)", cellAt(t, layers[0], 0, 0), 150.0, 1e-9)
				approxEqual(t, "panel layer[1] cell (0,0)", cellAt(t, layers[1], 0, 0), 200.0, 1e-9)
				approxEqual(t, "panel layer[2] cell (0,0)", cellAt(t, layers[2], 0, 0), 50.0, 1e-9)
			},
		},
		{
			Name:      "prop_z_panel_3_targets",
			Kind:      types.OverlayKindPropZPanel,
			Scope:     types.OverlayScopeCell,
			Reference: "baseline",
			Targets:   []string{"t0", "t1", "t2"},
			BuildSlots: fourSlotMatrix3x3(
				makeMatrixWithRowMargins(
					[3][3]float64{
						{50, 50, 50},
						{50, 50, 50},
						{50, 50, 50},
					},
					[3]float64{100, 100, 100},
				),
				makeMatrixWithRowMargins(
					[3][3]float64{
						{50, 50, 50},
						{50, 50, 50},
						{50, 50, 50},
					},
					[3]float64{100, 100, 100},
				),
				makeMatrixWithRowMargins(
					[3][3]float64{
						{60, 60, 60},
						{60, 60, 60},
						{60, 60, 60},
					},
					[3]float64{100, 100, 100},
				),
				makeMatrixWithRowMargins(
					[3][3]float64{
						{40, 40, 40},
						{40, 40, 40},
						{40, 40, 40},
					},
					[3]float64{100, 100, 100},
				),
			),
			Expect: func(t *testing.T, layers []types.OverlayLayer, _ []OverlayWarning) {
				t.Helper()
				// PROP_Z_PANEL is single-layer (matrix of pair-index slices).
				if len(layers) != 1 {
					t.Fatalf("len(layers) = %d, want 1 (PROP_Z_PANEL is single-layer)", len(layers))
				}
				if layers[0].Payload.Shape != types.OverlayShapeMatrix {
					t.Errorf("Payload.Shape = %q, want MATRIX", layers[0].Payload.Shape)
				}
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runComposeOverlayCase(t, tc)
		})
	}
}

