package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestPredict_OverlaysApplied_AllE2Kinds(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	specs := []types.OverlaySpec{
		{
			Name:  "chisq_col",
			Kind:  types.OverlayKindChiSqCol,
			Scope: types.OverlayScopeColumn,
		},
		{
			Name:  "chisq_matrix",
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
		},
		{
			Name:  "chisq_row",
			Kind:  types.OverlayKindChiSqRow,
			Scope: types.OverlayScopeRow,
		},
		{
			Name:  "delta_row",
			Kind:  types.OverlayKindDeltaVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		{
			Name:  "fisher",
			Kind:  types.OverlayKindFisherExactCell,
			Scope: types.OverlayScopeCell,
		},
		{
			Name:   "formula",
			Kind:   types.OverlayKindFormula,
			Scope:  types.OverlayScopeCell,
			Params: json.RawMessage(`{"formula":"cell"}`),
		},
		{
			Name:  "index_row",
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		{
			Name:  "share_col",
			Kind:  types.OverlayKindShareOfCol,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn},
			},
		},
		{
			Name:  "share_row",
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		{
			Name:  "share_total",
			Kind:  types.OverlayKindShareOfTotal,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand},
			},
		},
		{
			Name:  "zscore_row",
			Kind:  types.OverlayKindZScoreVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
	}

	matrixHostKinds := map[types.OverlayKind]bool{}
	for _, k := range types.AllOverlayKinds() {
		switch k {
		case types.OverlayKindIndexVsTotal,
			types.OverlayKindZScoreVsTotal,
			types.OverlayKindDeltaVsSibling,
			types.OverlayKindIndexVsSibling,
			types.OverlayKindIndexVsPrior,
			types.OverlayKindIndexVsRollingMean,
			types.OverlayKindZScoreVsRolling,
			types.OverlayKindIndexVsBaseline,
			types.OverlayKindDeltaVsBaseline,
			types.OverlayKindYoY,
			types.OverlayKindIndexVsPop,
			types.OverlayKindZScoreVsPop,
			types.OverlayKindChiSqVsPop,
			types.OverlayKindKSVsPop,
			types.OverlayKindIndexVsStage,
			types.OverlayKindDeltaVsStage,
			types.OverlayKindIndexVsRef,
			types.OverlayKindDeltaVsRef,
			types.OverlayKindPropZCell,
			types.OverlayKindPropZPanel,
			types.OverlayKindPanelIndexVsRef,
			types.OverlayKindTCell,
			types.OverlayKindTVsRef,
			types.OverlayKindZCell,
			types.OverlayKindZVsRef,
			types.OverlayKindChiSqVsRef,
			types.OverlayKindRank:
			continue
		}
		matrixHostKinds[k] = true
	}
	if len(specs) != len(matrixHostKinds) {
		t.Fatalf("test fixture out of date: %d specs vs %d known MATRIX-host kinds — add the new MATRIX-host kind to the spec slice",
			len(specs), len(matrixHostKinds))
	}
	for _, spec := range specs {
		if !matrixHostKinds[spec.Kind] {
			t.Errorf("test fixture out of date: spec kind %q is not a MATRIX-host kind", spec.Kind)
		}
	}

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: specs,
	}

	env := PredictFromBytes(data, req, nil)
	result, ok := env.Data.(*PredictResult)
	if !ok {
		t.Fatalf("envelope Data is not *PredictResult: %T", env.Data)
	}

	// One descriptor per spec, matching order.
	if got, want := len(result.OverlaysApplied), len(specs); got != want {
		t.Fatalf("OverlaysApplied length = %d, want %d", got, want)
	}

	for i, spec := range specs {
		desc := result.OverlaysApplied[i]
		if desc.Name != spec.Name {
			t.Errorf("OverlaysApplied[%d].Name = %q, want %q", i, desc.Name, spec.Name)
		}
		if desc.Kind != spec.Kind {
			t.Errorf("OverlaysApplied[%d].Kind = %q, want %q", i, desc.Kind, spec.Kind)
		}
		if desc.Scope != spec.Scope {
			t.Errorf("OverlaysApplied[%d].Scope = %q, want %q", i, desc.Scope, spec.Scope)
		}
		wantStreamable, known := types.OverlayStreamable(spec.Kind)
		if !known {
			t.Errorf("OverlaysApplied[%d]: kind %q absent from streamability table", i, spec.Kind)
			continue
		}
		if desc.Streamable != wantStreamable {
			t.Errorf("OverlaysApplied[%d].Streamable = %v, want %v (from OverlayStreamable)",
				i, desc.Streamable, wantStreamable)
		}
	}

	if got, want := len(result.OverlayCost), len(specs); got != want {
		t.Errorf("OverlayCost length = %d, want %d", got, want)
	}
	for i, spec := range specs {
		cost, ok := result.OverlayCost[spec.Name]
		if !ok {
			t.Errorf("OverlayCost missing key %q (spec index %d)", spec.Name, i)
			continue
		}
		wantCost := overlayCostForKind(spec.Kind)
		if cost != wantCost {
			t.Errorf("OverlayCost[%q] = %v, want %v (overlayCostForKind dispatch on kind %q)",
				spec.Name, cost, wantCost, spec.Kind)
		}
	}

	if result.OverlaysSchemaDivergence == nil {
		t.Errorf("OverlaysSchemaDivergence = nil; expected non-nil empty slice")
	}
	if got := len(result.OverlaysSchemaDivergence); got != 0 {
		t.Errorf("OverlaysSchemaDivergence length = %d, want 0 (Compose-driven divergence not yet wired)", got)
	}
}

// TestPredict_OverlaysApplied_SynthesizesDefaultName pins the
// renderer-facing-name fallback contract: when an OverlaySpec omits
// Name, populateOverlayDescriptors synthesises a deterministic default
// from Kind + Scope (+ Ref.Margin.Axis when populated) so the
// OverlayCost map key and the OverlayLayer.Name stay aligned across
// the predict / runtime boundary. The test guards the synthesis
// branch against a future regression that quietly switches the key to
// an opaque hash or drops the axis suffix.
func TestPredict_OverlaysApplied_SynthesizesDefaultName(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				// Name intentionally omitted — exercises the synthesised
				// default branch.
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	result, ok := env.Data.(*PredictResult)
	if !ok {
		t.Fatalf("envelope Data is not *PredictResult: %T", env.Data)
	}

	if len(result.OverlaysApplied) != 1 {
		t.Fatalf("OverlaysApplied length = %d, want 1", len(result.OverlaysApplied))
	}
	desc := result.OverlaysApplied[0]
	want := "OVERLAY_INDEX_VS_MARGIN|cell|margin:row"
	if desc.Name != want {
		t.Errorf("synthesised Name = %q, want %q", desc.Name, want)
	}

	// OverlayCost key must match the descriptor Name — the map key and
	// the descriptor surface are the same identity. A drift here would
	// silently break renderer cost lookups.
	if _, ok := result.OverlayCost[want]; !ok {
		t.Errorf("OverlayCost missing synthesised key %q; keys: %v", want, costKeys(result.OverlayCost))
	}
}

// costKeys returns a stable-ordered list of OverlayCost map keys for
// failure messages.
func costKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPredict_OverlayCost_StreamableKindsLow(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	// Per-kind well-formed spec table — every SERIES-host streamable
	// kind currently lands here. Add an entry when a future kind flips
	// streamable in types/overlay_streamability.go.
	specsByKind := map[types.OverlayKind]types.OverlaySpec{
		types.OverlayKindIndexVsTotal: {
			Name:  "idx_total",
			Kind:  types.OverlayKindIndexVsTotal,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindZScoreVsTotal: {
			Name:  "z_total",
			Kind:  types.OverlayKindZScoreVsTotal,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindShareOfTotal: {
			Name:  "share_total_series",
			Kind:  types.OverlayKindShareOfTotal,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindIndexVsPrior: {
			Name:  "idx_prior",
			Kind:  types.OverlayKindIndexVsPrior,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindIndexVsPop: {
			Name:  "idx_pop",
			Kind:  types.OverlayKindIndexVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		types.OverlayKindZScoreVsPop: {
			Name:  "z_pop",
			Kind:  types.OverlayKindZScoreVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		types.OverlayKindIndexVsRef: {
			Name:  "idx_ref",
			Kind:  types.OverlayKindIndexVsRef,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindDeltaVsRef: {
			Name:  "delta_ref",
			Kind:  types.OverlayKindDeltaVsRef,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindPanelIndexVsRef: {
			Name:  "panel_idx_ref",
			Kind:  types.OverlayKindPanelIndexVsRef,
			Scope: types.OverlayScopeGroup,
		},
	}

	for _, kind := range types.AllOverlayKinds() {
		streamable, known := types.OverlayStreamable(kind)
		if !known || !streamable {
			continue
		}
		spec, ok := specsByKind[kind]
		if !ok {
			t.Errorf("test fixture out of date: streamable kind %q missing a per-kind spec — add one to specsByKind", kind)
			continue
		}
		t.Run(string(kind), func(t *testing.T) {
			req := indexVsTotalSeriesHostReq()
			req.Overlays = []types.OverlaySpec{spec}

			env := PredictFromBytes(data, req, nil)
			result, ok := env.Data.(*PredictResult)
			if !ok {
				t.Fatalf("envelope Data is not *PredictResult: %T", env.Data)
			}
			cost, ok := result.OverlayCost[spec.Name]
			if !ok {
				t.Fatalf("OverlayCost missing key %q; keys: %v", spec.Name, costKeys(result.OverlayCost))
			}
			if cost != overlayCostStreamable {
				t.Errorf("OverlayCost[%q] = %v, want %v (streamable kind %q must map to overlayCostStreamable)",
					spec.Name, cost, overlayCostStreamable, kind)
			}
		})
	}
}

func TestPredict_OverlayCost_BufferedKindsHigh(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		spec types.OverlaySpec
	}{
		{
			name: "DeltaVsSibling",
			spec: types.OverlaySpec{
				Name:  "delta_sib",
				Kind:  types.OverlayKindDeltaVsSibling,
				Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{
					Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
				},
			},
		},
		{
			name: "IndexVsSibling",
			spec: types.OverlaySpec{
				Name:  "idx_sib",
				Kind:  types.OverlayKindIndexVsSibling,
				Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{
					Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
				},
			},
		},
		{
			name: "IndexVsBaseline",
			spec: types.OverlaySpec{
				Name:  "idx_baseline",
				Kind:  types.OverlayKindIndexVsBaseline,
				Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{
					BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
				},
			},
		},
		{
			name: "DeltaVsBaseline",
			spec: types.OverlaySpec{
				Name:  "delta_baseline",
				Kind:  types.OverlayKindDeltaVsBaseline,
				Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{
					BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
				},
			},
		},
		{
			name: "IndexVsRollingMean",
			spec: types.OverlaySpec{
				Name:  "idx_rolling_mean",
				Kind:  types.OverlayKindIndexVsRollingMean,
				Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{
					RollingMean: &types.OverlayRollingMeanRef{},
				},
				Params: json.RawMessage(`{"window": 2}`),
			},
		},
		{
			name: "ZScoreVsRolling",
			spec: types.OverlaySpec{
				Name:  "zscore_rolling",
				Kind:  types.OverlayKindZScoreVsRolling,
				Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{
					RollingMean: &types.OverlayRollingMeanRef{},
				},
				Params: json.RawMessage(`{"window": 2}`),
			},
		},
		{
			name: "YoY",
			spec: types.OverlaySpec{
				Name:  "yoy",
				Kind:  types.OverlayKindYoY,
				Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{
					YoY: &types.OverlayYoYRef{},
				},
				Params: json.RawMessage(`{"frequency": "monthly"}`),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			streamable, known := types.OverlayStreamable(tc.spec.Kind)
			if !known {
				t.Fatalf("kind %q absent from streamability table", tc.spec.Kind)
			}
			if streamable {
				t.Fatalf("kind %q flipped streamable; move to TestPredict_OverlayCost_StreamableKindsLow",
					tc.spec.Kind)
			}

			var req *types.Request
			if tc.spec.Kind == types.OverlayKindYoY {
				req = yoyHostReq()
			} else {
				req = siblingHostReq()
			}
			req.Overlays = []types.OverlaySpec{tc.spec}

			env := PredictFromBytes(data, req, nil)
			result, ok := env.Data.(*PredictResult)
			if !ok {
				t.Fatalf("envelope Data is not *PredictResult: %T", env.Data)
			}
			cost, ok := result.OverlayCost[tc.spec.Name]
			if !ok {
				t.Fatalf("OverlayCost missing key %q; keys: %v", tc.spec.Name, costKeys(result.OverlayCost))
			}
			if cost != overlayCostBuffered {
				t.Errorf("OverlayCost[%q] = %v, want %v (buffered SIBLING kind %q must map to overlayCostBuffered)",
					tc.spec.Name, cost, overlayCostBuffered, tc.spec.Kind)
			}
		})
	}
}

func TestPredict_OverlayCost_E2KindsBufferedDefault(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	specsByKind := map[types.OverlayKind]types.OverlaySpec{
		types.OverlayKindChiSqCol: {
			Name:  "chisq_col",
			Kind:  types.OverlayKindChiSqCol,
			Scope: types.OverlayScopeColumn,
		},
		types.OverlayKindChiSqMatrix: {
			Name:  "chisq_matrix",
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
		},
		types.OverlayKindChiSqRow: {
			Name:  "chisq_row",
			Kind:  types.OverlayKindChiSqRow,
			Scope: types.OverlayScopeRow,
		},
		types.OverlayKindDeltaVsMargin: {
			Name:  "delta_row",
			Kind:  types.OverlayKindDeltaVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		types.OverlayKindFisherExactCell: {
			Name:  "fisher",
			Kind:  types.OverlayKindFisherExactCell,
			Scope: types.OverlayScopeCell,
		},
		types.OverlayKindFormula: {
			Name:   "formula",
			Kind:   types.OverlayKindFormula,
			Scope:  types.OverlayScopeCell,
			Params: json.RawMessage(`{"formula":"cell"}`),
		},
		types.OverlayKindIndexVsMargin: {
			Name:  "index_row",
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		types.OverlayKindShareOfCol: {
			Name:  "share_col",
			Kind:  types.OverlayKindShareOfCol,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn},
			},
		},
		types.OverlayKindShareOfRow: {
			Name:  "share_row",
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		types.OverlayKindZScoreVsMargin: {
			Name:  "zscore_row",
			Kind:  types.OverlayKindZScoreVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
	}

	// SERIES-host kinds (INDEX_VS_TOTAL, ZSCORE_VS_TOTAL, SHARE_OF_TOTAL
	// SERIES dispatch, the SIBLING family, the windowed kinds) live in
	// the streamable / sibling / windowed tests above and intentionally
	// do NOT appear in this MATRIX-host gate.
	seriesHostKinds := map[types.OverlayKind]bool{
		types.OverlayKindIndexVsTotal:       true,
		types.OverlayKindZScoreVsTotal:      true,
		types.OverlayKindShareOfTotal:       true,
		types.OverlayKindDeltaVsSibling:     true,
		types.OverlayKindIndexVsSibling:     true,
		types.OverlayKindIndexVsPrior:       true,
		types.OverlayKindIndexVsRollingMean: true,
		types.OverlayKindIndexVsBaseline:    true,
		types.OverlayKindDeltaVsBaseline:    true,
		types.OverlayKindZScoreVsRolling:    true,
		types.OverlayKindYoY:                true,
		types.OverlayKindIndexVsPop:         true,
		types.OverlayKindZScoreVsPop:        true,
		types.OverlayKindChiSqVsPop:         true,
		types.OverlayKindKSVsPop:            true,
		types.OverlayKindIndexVsStage:       true,
		types.OverlayKindDeltaVsStage:       true,
		types.OverlayKindIndexVsRef:         true,
		types.OverlayKindDeltaVsRef:         true,
		types.OverlayKindPropZCell:          true,
		types.OverlayKindPropZPanel:         true,
		types.OverlayKindPanelIndexVsRef:    true,
		types.OverlayKindTCell:              true,
		types.OverlayKindTVsRef:             true,
		types.OverlayKindZCell:              true,
		types.OverlayKindZVsRef:             true,
		types.OverlayKindChiSqVsRef:         true,
		types.OverlayKindRank:               true,
	}

	for _, kind := range types.AllOverlayKinds() {
		if seriesHostKinds[kind] {
			continue
		}
		spec, ok := specsByKind[kind]
		if !ok {
			t.Errorf("test fixture out of date: MATRIX-host kind %q missing a per-kind spec — add one to specsByKind", kind)
			continue
		}
		t.Run(string(kind), func(t *testing.T) {
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{spec},
			}

			env := PredictFromBytes(data, req, nil)
			result, ok := env.Data.(*PredictResult)
			if !ok {
				t.Fatalf("envelope Data is not *PredictResult: %T", env.Data)
			}
			cost, ok := result.OverlayCost[spec.Name]
			if !ok {
				t.Fatalf("OverlayCost missing key %q; keys: %v", spec.Name, costKeys(result.OverlayCost))
			}
			if cost != overlayCostBuffered {
				t.Errorf("OverlayCost[%q] = %v, want %v (buffered kind %q must map to overlayCostBuffered)",
					spec.Name, cost, overlayCostBuffered, kind)
			}
		})
	}
}

func TestPredict_OverlaysApplied_AllKinds_DescriptorCoverage(t *testing.T) {
	// Per-kind spec table — every registered OverlayKind must carry a
	// well-formed entry. The spec's Scope + Ref pair is chosen to
	// match the kind's capability row (overlayCapabilityFor) so the
	// resolver produces a non-empty Shape (when a single shape can be
	// resolved at predict time — whole-chain kinds inherit the target
	// stage's shape at runtime and intentionally leave Shape empty).
	zero := 0
	specs := map[types.OverlayKind]types.OverlaySpec{
		// MATRIX-host inferential CHISQ family — implicit-margin, no Ref.
		types.OverlayKindChiSqCol: {
			Name:  "chisq_col",
			Kind:  types.OverlayKindChiSqCol,
			Scope: types.OverlayScopeColumn,
		},
		types.OverlayKindChiSqMatrix: {
			Name:  "chisq_matrix",
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
		},
		types.OverlayKindChiSqRow: {
			Name:  "chisq_row",
			Kind:  types.OverlayKindChiSqRow,
			Scope: types.OverlayScopeRow,
		},
		// FACET-host inferential — Ref.Population required.
		types.OverlayKindChiSqVsPop: {
			Name:  "chisq_vs_pop",
			Kind:  types.OverlayKindChiSqVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		// COMPOSE-only — slot-label-driven, no Ref family.
		types.OverlayKindChiSqVsRef: {
			Name:  "chisq_vs_ref",
			Kind:  types.OverlayKindChiSqVsRef,
			Scope: types.OverlayScopeMatrix,
		},
		// SERIES windowed family — Ref.BaselineIndex required.
		types.OverlayKindDeltaVsBaseline: {
			Name:  "delta_vs_baseline",
			Kind:  types.OverlayKindDeltaVsBaseline,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
			},
		},
		// MATRIX-host descriptive — Ref.Margin required.
		types.OverlayKindDeltaVsMargin: {
			Name:  "delta_vs_margin",
			Kind:  types.OverlayKindDeltaVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		// COMPOSE-only dual-shape — slot-label-driven, no Ref family.
		types.OverlayKindDeltaVsRef: {
			Name:  "delta_vs_ref",
			Kind:  types.OverlayKindDeltaVsRef,
			Scope: types.OverlayScopeGroup,
		},
		// SERIES sibling family — Ref.Sibling required.
		types.OverlayKindDeltaVsSibling: {
			Name:  "delta_vs_sibling",
			Kind:  types.OverlayKindDeltaVsSibling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
			},
		},
		// CHAIN whole-chain — Ref.Stage required.
		types.OverlayKindDeltaVsStage: {
			Name:  "delta_vs_stage",
			Kind:  types.OverlayKindDeltaVsStage,
			Scope: types.OverlayScopeTotal,
			Ref: types.OverlayRef{
				Stage: &types.StageRef{Index: &zero},
			},
		},
		// MATRIX-host inferential — implicit-margin, no Ref.
		types.OverlayKindFisherExactCell: {
			Name:  "fisher_exact_cell",
			Kind:  types.OverlayKindFisherExactCell,
			Scope: types.OverlayScopeCell,
		},
		// MATRIX-host descriptive — no Ref family (formula sources its
		// per-shape namespace from the host payload).
		types.OverlayKindFormula: {
			Name:   "formula",
			Kind:   types.OverlayKindFormula,
			Scope:  types.OverlayScopeCell,
			Params: json.RawMessage(`{"formula":"cell"}`),
		},
		// SERIES windowed — Ref.BaselineIndex required.
		types.OverlayKindIndexVsBaseline: {
			Name:  "index_vs_baseline",
			Kind:  types.OverlayKindIndexVsBaseline,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
			},
		},
		// MATRIX-host descriptive — Ref.Margin required.
		types.OverlayKindIndexVsMargin: {
			Name:  "index_vs_margin",
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		// FACET-host descriptive — Ref.Population required.
		types.OverlayKindIndexVsPop: {
			Name:  "index_vs_pop",
			Kind:  types.OverlayKindIndexVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		// SERIES windowed — Ref.Prior accepted (lag-1 implicit).
		types.OverlayKindIndexVsPrior: {
			Name:  "index_vs_prior",
			Kind:  types.OverlayKindIndexVsPrior,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Prior: &types.OverlayPriorRef{},
			},
		},
		// COMPOSE-only dual-shape — slot-label-driven, no Ref family.
		types.OverlayKindIndexVsRef: {
			Name:  "index_vs_ref",
			Kind:  types.OverlayKindIndexVsRef,
			Scope: types.OverlayScopeGroup,
		},
		// SERIES windowed — Ref.RollingMean marker required.
		types.OverlayKindIndexVsRollingMean: {
			Name:  "index_vs_rolling_mean",
			Kind:  types.OverlayKindIndexVsRollingMean,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				RollingMean: &types.OverlayRollingMeanRef{},
			},
			Params: json.RawMessage(`{"window": 2}`),
		},
		// SERIES sibling family — Ref.Sibling required.
		types.OverlayKindIndexVsSibling: {
			Name:  "index_vs_sibling",
			Kind:  types.OverlayKindIndexVsSibling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
			},
		},
		// CHAIN whole-chain — Ref.Stage required.
		types.OverlayKindIndexVsStage: {
			Name:  "index_vs_stage",
			Kind:  types.OverlayKindIndexVsStage,
			Scope: types.OverlayScopeTotal,
			Ref: types.OverlayRef{
				Stage: &types.StageRef{Index: &zero},
			},
		},
		// SERIES implicit-grand-total, no Ref.
		types.OverlayKindIndexVsTotal: {
			Name:  "index_vs_total",
			Kind:  types.OverlayKindIndexVsTotal,
			Scope: types.OverlayScopeGroup,
		},
		// FACET-host inferential — Ref.Population required.
		types.OverlayKindKSVsPop: {
			Name:  "ks_vs_pop",
			Kind:  types.OverlayKindKSVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		// COMPOSE-only multi-ref — slot-label-driven, no Ref family.
		types.OverlayKindPanelIndexVsRef: {
			Name:  "panel_index_vs_ref",
			Kind:  types.OverlayKindPanelIndexVsRef,
			Scope: types.OverlayScopeGroup,
		},
		// COMPOSE-only inferential — slot-label-driven, no Ref family.
		types.OverlayKindPropZCell: {
			Name:  "prop_z_cell",
			Kind:  types.OverlayKindPropZCell,
			Scope: types.OverlayScopeCell,
		},
		// COMPOSE-only inferential multi-ref — slot-label-driven, no Ref family.
		types.OverlayKindPropZPanel: {
			Name:  "prop_z_panel",
			Kind:  types.OverlayKindPropZPanel,
			Scope: types.OverlayScopeCell,
		},
		// COMPOSE-only descriptive — slot-label-driven, no Ref family.
		types.OverlayKindRank: {
			Name:  "rank",
			Kind:  types.OverlayKindRank,
			Scope: types.OverlayScopeCell,
		},
		// MATRIX-host descriptive — Ref.Margin required.
		types.OverlayKindShareOfCol: {
			Name:  "share_of_col",
			Kind:  types.OverlayKindShareOfCol,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn},
			},
		},
		types.OverlayKindShareOfRow: {
			Name:  "share_of_row",
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		// Dual-shape SHARE_OF_TOTAL — MATRIX arm via Ref.Margin grand;
		// SERIES arm exercised by the SERIES handler. The descriptor
		// builder is a pure function of (Kind, Scope, Ref) so either
		// arm exercises Shape + Ref resolution correctly. Use MATRIX
		// arm here to match the rest of the share family fixture.
		types.OverlayKindShareOfTotal: {
			Name:  "share_of_total",
			Kind:  types.OverlayKindShareOfTotal,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand},
			},
		},
		// COMPOSE-only inferential — slot-label-driven, no Ref family.
		types.OverlayKindTCell: {
			Name:  "t_cell",
			Kind:  types.OverlayKindTCell,
			Scope: types.OverlayScopeCell,
		},
		types.OverlayKindTVsRef: {
			Name:  "t_vs_ref",
			Kind:  types.OverlayKindTVsRef,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindZCell: {
			Name:  "z_cell",
			Kind:  types.OverlayKindZCell,
			Scope: types.OverlayScopeCell,
		},
		types.OverlayKindZVsRef: {
			Name:  "z_vs_ref",
			Kind:  types.OverlayKindZVsRef,
			Scope: types.OverlayScopeGroup,
		},
		// SERIES windowed YoY — Ref.YoY marker required.
		types.OverlayKindYoY: {
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
		// MATRIX-host descriptive — Ref.Margin required.
		types.OverlayKindZScoreVsMargin: {
			Name:  "zscore_vs_margin",
			Kind:  types.OverlayKindZScoreVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		// FACET-host descriptive — Ref.Population required.
		types.OverlayKindZScoreVsPop: {
			Name:  "zscore_vs_pop",
			Kind:  types.OverlayKindZScoreVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		// SERIES windowed — Ref.RollingMean marker required.
		types.OverlayKindZScoreVsRolling: {
			Name:  "zscore_vs_rolling",
			Kind:  types.OverlayKindZScoreVsRolling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				RollingMean: &types.OverlayRollingMeanRef{},
			},
			Params: json.RawMessage(`{"window": 2}`),
		},
		// SERIES implicit-grand-total, no Ref.
		types.OverlayKindZScoreVsTotal: {
			Name:  "zscore_vs_total",
			Kind:  types.OverlayKindZScoreVsTotal,
			Scope: types.OverlayScopeGroup,
		},
	}

	// Catalog completeness — every kind in types.AllOverlayKinds()
	// MUST have a fixture entry. A new kind without one fails closed
	// at this assertion.
	for _, kind := range types.AllOverlayKinds() {
		if _, ok := specs[kind]; !ok {
			t.Errorf("test fixture out of date: overlay kind %q is missing a per-kind spec entry — add one to the specs map", kind)
		}
	}

	// Spec-order preservation: iterate types.AllOverlayKinds() (which
	// is alphabetised — see types.AllOverlayKinds doc) and build the
	// spec slice in that order. The descriptor slice must come back
	// in matching order — each Descriptors[i].Kind == specs[i].Kind.
	kinds := types.AllOverlayKinds()
	orderedSpecs := make([]types.OverlaySpec, 0, len(kinds))
	for _, k := range kinds {
		spec, ok := specs[k]
		if !ok {
			// Already reported above; skip so the rest of the test
			// can run against the kinds that DID have fixtures.
			continue
		}
		orderedSpecs = append(orderedSpecs, spec)
	}

	costs := map[string]float64{}
	descriptors, _ := appendOverlayDescriptors(nil, costs, orderedSpecs)

	if got, want := len(descriptors), len(orderedSpecs); got != want {
		t.Fatalf("descriptors length = %d, want %d (one per spec)", got, want)
	}

	for i, spec := range orderedSpecs {
		desc := descriptors[i]
		// Name echoes spec.Name (every fixture sets a non-empty Name).
		if desc.Name != spec.Name {
			t.Errorf("descriptors[%d] (kind %q) Name = %q, want %q",
				i, spec.Kind, desc.Name, spec.Name)
		}
		// Kind matches the spec's Kind.
		if desc.Kind != spec.Kind {
			t.Errorf("descriptors[%d] Kind = %q, want %q",
				i, desc.Kind, spec.Kind)
		}
		// Scope reflects the spec's scope.
		if desc.Scope != spec.Scope {
			t.Errorf("descriptors[%d] (kind %q) Scope = %q, want %q",
				i, spec.Kind, desc.Scope, spec.Scope)
		}
		// Shape resolves from the capability table. Whole-chain kinds
		// (OVERLAY_INDEX_VS_STAGE / OVERLAY_DELTA_VS_STAGE) and the
		// few multi-shape FORMULA / DELTA_VS_REF / INDEX_VS_REF /
		// PANEL_INDEX_VS_REF / CHISQ_VS_REF kinds disambiguate via
		// scope. For whole-chain kinds the runtime shape depends on
		// the target stage's host shape (which Predict cannot resolve
		// without inspecting the chain), so an empty Shape is the
		// documented fallback.
		wantShape := resolveOverlayShape(spec.Kind, spec.Scope)
		if desc.Shape != wantShape {
			t.Errorf("descriptors[%d] (kind %q, scope %q) Shape = %q, want %q",
				i, spec.Kind, spec.Scope, desc.Shape, wantShape)
		}
		// Ref echoes the resolved discriminated union variant for
		// every kind whose catalog row consumes a Ref family pointer;
		// implicit-margin kinds and COMPOSE-only kinds leave Ref
		// empty by design.
		wantRef := resolveOverlayRef(&spec)
		if desc.Ref != wantRef {
			t.Errorf("descriptors[%d] (kind %q) Ref = %q, want %q",
				i, spec.Kind, desc.Ref, wantRef)
		}
		// Streamable mirrors the static streamability table.
		wantStream, known := types.OverlayStreamable(spec.Kind)
		if !known {
			t.Errorf("descriptors[%d] (kind %q) absent from streamability table",
				i, spec.Kind)
			continue
		}
		if desc.Streamable != wantStream {
			t.Errorf("descriptors[%d] (kind %q) Streamable = %v, want %v",
				i, spec.Kind, desc.Streamable, wantStream)
		}
	}
}

func TestPredict_OverlaysApplied_ShapeAndRefPopulated_RequestHost(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Name:  "index_row",
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{
					Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
				},
			},
			{
				Name:  "chisq_matrix",
				Kind:  types.OverlayKindChiSqMatrix,
				Scope: types.OverlayScopeMatrix,
			},
		},
	}

	env := PredictFromBytes(data, req, nil)
	result, ok := env.Data.(*PredictResult)
	if !ok {
		t.Fatalf("envelope Data is not *PredictResult: %T", env.Data)
	}
	if got, want := len(result.OverlaysApplied), 2; got != want {
		t.Fatalf("OverlaysApplied length = %d, want %d", got, want)
	}

	// INDEX_VS_MARGIN: matrix shape, margin:row ref.
	d0 := result.OverlaysApplied[0]
	if d0.Shape != types.OverlayShapeMatrix {
		t.Errorf("descriptors[0].Shape = %q, want %q", d0.Shape, types.OverlayShapeMatrix)
	}
	if d0.Ref != "margin:row" {
		t.Errorf("descriptors[0].Ref = %q, want %q", d0.Ref, "margin:row")
	}

	// CHISQ_MATRIX: scalar shape, no Ref (implicit-margin).
	d1 := result.OverlaysApplied[1]
	if d1.Shape != types.OverlayShapeScalar {
		t.Errorf("descriptors[1].Shape = %q, want %q", d1.Shape, types.OverlayShapeScalar)
	}
	if d1.Ref != "" {
		t.Errorf("descriptors[1].Ref = %q, want empty (CHISQ_MATRIX is implicit-margin)", d1.Ref)
	}
}

func TestPredict_OverlayCost_AllKindsHaveMultiplier(t *testing.T) {
	zero := 0
	// Per-kind spec table mirrors
	// TestPredict_OverlaysApplied_AllKinds_DescriptorCoverage's table —
	// every registered OverlayKind must carry a well-formed entry. The
	// fixture is duplicated here (vs. lifted into a shared helper) so
	// the two coverage gates stay independently maintainable; a kind
	// added to one without the other surfaces the gap explicitly.
	specs := map[types.OverlayKind]types.OverlaySpec{
		types.OverlayKindChiSqCol: {
			Name:  "chisq_col",
			Kind:  types.OverlayKindChiSqCol,
			Scope: types.OverlayScopeColumn,
		},
		types.OverlayKindChiSqMatrix: {
			Name:  "chisq_matrix",
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
		},
		types.OverlayKindChiSqRow: {
			Name:  "chisq_row",
			Kind:  types.OverlayKindChiSqRow,
			Scope: types.OverlayScopeRow,
		},
		types.OverlayKindChiSqVsPop: {
			Name:  "chisq_vs_pop",
			Kind:  types.OverlayKindChiSqVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		types.OverlayKindChiSqVsRef: {
			Name:  "chisq_vs_ref",
			Kind:  types.OverlayKindChiSqVsRef,
			Scope: types.OverlayScopeMatrix,
		},
		types.OverlayKindDeltaVsBaseline: {
			Name:  "delta_vs_baseline",
			Kind:  types.OverlayKindDeltaVsBaseline,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
			},
		},
		types.OverlayKindDeltaVsMargin: {
			Name:  "delta_vs_margin",
			Kind:  types.OverlayKindDeltaVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		types.OverlayKindDeltaVsRef: {
			Name:  "delta_vs_ref",
			Kind:  types.OverlayKindDeltaVsRef,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindDeltaVsSibling: {
			Name:  "delta_vs_sibling",
			Kind:  types.OverlayKindDeltaVsSibling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
			},
		},
		types.OverlayKindDeltaVsStage: {
			Name:  "delta_vs_stage",
			Kind:  types.OverlayKindDeltaVsStage,
			Scope: types.OverlayScopeTotal,
			Ref: types.OverlayRef{
				Stage: &types.StageRef{Index: &zero},
			},
		},
		types.OverlayKindFisherExactCell: {
			Name:  "fisher_exact_cell",
			Kind:  types.OverlayKindFisherExactCell,
			Scope: types.OverlayScopeCell,
		},
		types.OverlayKindFormula: {
			Name:   "formula",
			Kind:   types.OverlayKindFormula,
			Scope:  types.OverlayScopeCell,
			Params: json.RawMessage(`{"formula":"cell"}`),
		},
		types.OverlayKindIndexVsBaseline: {
			Name:  "index_vs_baseline",
			Kind:  types.OverlayKindIndexVsBaseline,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
			},
		},
		types.OverlayKindIndexVsMargin: {
			Name:  "index_vs_margin",
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		types.OverlayKindIndexVsPop: {
			Name:  "index_vs_pop",
			Kind:  types.OverlayKindIndexVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		types.OverlayKindIndexVsPrior: {
			Name:  "index_vs_prior",
			Kind:  types.OverlayKindIndexVsPrior,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Prior: &types.OverlayPriorRef{},
			},
		},
		types.OverlayKindIndexVsRef: {
			Name:  "index_vs_ref",
			Kind:  types.OverlayKindIndexVsRef,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindIndexVsRollingMean: {
			Name:  "index_vs_rolling_mean",
			Kind:  types.OverlayKindIndexVsRollingMean,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				RollingMean: &types.OverlayRollingMeanRef{},
			},
			Params: json.RawMessage(`{"window": 2}`),
		},
		types.OverlayKindIndexVsSibling: {
			Name:  "index_vs_sibling",
			Kind:  types.OverlayKindIndexVsSibling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"},
			},
		},
		types.OverlayKindIndexVsStage: {
			Name:  "index_vs_stage",
			Kind:  types.OverlayKindIndexVsStage,
			Scope: types.OverlayScopeTotal,
			Ref: types.OverlayRef{
				Stage: &types.StageRef{Index: &zero},
			},
		},
		types.OverlayKindIndexVsTotal: {
			Name:  "index_vs_total",
			Kind:  types.OverlayKindIndexVsTotal,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindKSVsPop: {
			Name:  "ks_vs_pop",
			Kind:  types.OverlayKindKSVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		types.OverlayKindPanelIndexVsRef: {
			Name:  "panel_index_vs_ref",
			Kind:  types.OverlayKindPanelIndexVsRef,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindPropZCell: {
			Name:  "prop_z_cell",
			Kind:  types.OverlayKindPropZCell,
			Scope: types.OverlayScopeCell,
		},
		types.OverlayKindPropZPanel: {
			Name:  "prop_z_panel",
			Kind:  types.OverlayKindPropZPanel,
			Scope: types.OverlayScopeCell,
		},
		types.OverlayKindRank: {
			Name:  "rank",
			Kind:  types.OverlayKindRank,
			Scope: types.OverlayScopeCell,
		},
		types.OverlayKindShareOfCol: {
			Name:  "share_of_col",
			Kind:  types.OverlayKindShareOfCol,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn},
			},
		},
		types.OverlayKindShareOfRow: {
			Name:  "share_of_row",
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		types.OverlayKindShareOfTotal: {
			Name:  "share_of_total",
			Kind:  types.OverlayKindShareOfTotal,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand},
			},
		},
		types.OverlayKindTCell: {
			Name:  "t_cell",
			Kind:  types.OverlayKindTCell,
			Scope: types.OverlayScopeCell,
		},
		types.OverlayKindTVsRef: {
			Name:  "t_vs_ref",
			Kind:  types.OverlayKindTVsRef,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindZCell: {
			Name:  "z_cell",
			Kind:  types.OverlayKindZCell,
			Scope: types.OverlayScopeCell,
		},
		types.OverlayKindZVsRef: {
			Name:  "z_vs_ref",
			Kind:  types.OverlayKindZVsRef,
			Scope: types.OverlayScopeGroup,
		},
		types.OverlayKindYoY: {
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
		types.OverlayKindZScoreVsMargin: {
			Name:  "zscore_vs_margin",
			Kind:  types.OverlayKindZScoreVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
		types.OverlayKindZScoreVsPop: {
			Name:  "zscore_vs_pop",
			Kind:  types.OverlayKindZScoreVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		types.OverlayKindZScoreVsRolling: {
			Name:  "zscore_vs_rolling",
			Kind:  types.OverlayKindZScoreVsRolling,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				RollingMean: &types.OverlayRollingMeanRef{},
			},
			Params: json.RawMessage(`{"window": 2}`),
		},
		types.OverlayKindZScoreVsTotal: {
			Name:  "zscore_vs_total",
			Kind:  types.OverlayKindZScoreVsTotal,
			Scope: types.OverlayScopeGroup,
		},
	}

	// Catalog completeness — every kind in types.AllOverlayKinds() MUST
	// have a fixture entry. A new kind without one fails closed at this
	// assertion BEFORE the cost-map per-key checks below so the failure
	// message points at the fixture, not at an absent map entry whose
	// owner is harder to track down.
	kinds := types.AllOverlayKinds()
	for _, kind := range kinds {
		if _, ok := specs[kind]; !ok {
			t.Errorf("test fixture out of date: overlay kind %q is missing a per-kind spec entry — add one to the specs map", kind)
		}
	}

	// Drive the descriptor builder directly. orderedSpecs walks
	// AllOverlayKinds() (alphabetised) so the descriptor / cost emission
	// order is deterministic.
	orderedSpecs := make([]types.OverlaySpec, 0, len(kinds))
	for _, k := range kinds {
		spec, ok := specs[k]
		if !ok {
			continue
		}
		orderedSpecs = append(orderedSpecs, spec)
	}

	costs := map[string]float64{}
	descriptors, costs := appendOverlayDescriptors(nil, costs, orderedSpecs)

	if got, want := len(descriptors), len(orderedSpecs); got != want {
		t.Fatalf("descriptors length = %d, want %d (one per spec)", got, want)
	}
	if got, want := len(costs), len(orderedSpecs); got != want {
		t.Fatalf("OverlayCost length = %d, want %d (one entry per spec)", got, want)
	}

	// Per-kind multiplier presence: every spec.Name must appear as a
	// key in the OverlayCost map AND carry the streamability-derived
	// score for its kind. The per-key value assertion uses
	// overlayCostForKind directly so a future streamability flip
	// re-derives the expected score automatically; the test does NOT
	// hard-code the streamable / buffered split per kind (the dedicated
	// streamable / buffered tests above own that contract).
	for i, spec := range orderedSpecs {
		cost, ok := costs[spec.Name]
		if !ok {
			t.Errorf("OverlayCost missing key %q (spec index %d, kind %q)",
				spec.Name, i, spec.Kind)
			continue
		}
		want := overlayCostForKind(spec.Kind)
		if cost != want {
			t.Errorf("OverlayCost[%q] = %v, want %v (overlayCostForKind dispatch on kind %q)",
				spec.Name, cost, want, spec.Kind)
		}
		// Zero-value guard: an unset multiplier is the failure shape
		// callers see when a kind silently slips past the dispatcher.
		// The == 0 check is belt-and-braces alongside the wantCost
		// check above — every legitimate kind currently maps to either
		// overlayCostStreamable (0.05) or overlayCostBuffered (1.0),
		// neither of which is zero.
		if cost == 0 {
			t.Errorf("OverlayCost[%q] = 0; expected a non-zero multiplier (kind %q)",
				spec.Name, spec.Kind)
		}
	}

	// Empty map sanity-check: with zero specs the dispatcher should not
	// emit any keys (the map stays empty-but-not-nil).
	emptyCosts := map[string]float64{}
	_, emptyCosts = appendOverlayDescriptors(nil, emptyCosts, nil)
	if emptyCosts == nil {
		t.Errorf("OverlayCost map = nil; expected empty (non-nil) map")
	}
	if len(emptyCosts) != 0 {
		t.Errorf("OverlayCost map = %+v; expected empty", emptyCosts)
	}
}
