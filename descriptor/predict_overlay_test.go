package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestPredict_OverlaysApplied_AllE2Kinds is the E2-S15 close-out gate.
// It pins the contract that PredictResult.OverlaysApplied enumerates
// every spec the caller authored, in spec order, with the catalog
// identity (Name + Kind + Scope + Streamable) populated from the
// spec + types.OverlayStreamable(kind).
//
// E1 shipped one kind (OVERLAY_INDEX_VS_MARGIN) and the
// populateOverlayDescriptors loop has always handled the full
// req.Overlays slice; this test pins the contract across the full E2
// catalog (10 kinds) so any future regression that quietly truncates
// the descriptor list (or drops a kind's per-axis flag) fails closed.
//
// PRD §I-FR-I3 names the trio
// `OverlaysApplied + OverlaysSchemaDivergence + OverlayCost`:
//   - OverlaysApplied: 10 entries in matching order.
//   - OverlaysSchemaDivergence: empty (Compose-driven divergence
//     detection lands later in the effort).
//   - OverlayCost: per-kind multiplier — streamable kinds carry the
//     low score (~0.05); buffered kinds carry the high score (~1.0).
//     E3-S11 replaced the E2 flat-1.0 stub with the streamability-
//     derived dispatch.
func TestPredict_OverlaysApplied_AllE2Kinds(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	// Build one well-formed spec per E2 kind. Each spec must satisfy
	// its per-kind scope + ref-family gate so ValidateOverlays does not
	// add a structural error that would short-circuit downstream
	// validation. Order mirrors types.AllOverlayKinds() so the
	// matching-order assertion below reads against a known sort.
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

	// Acceptance criterion 1 of the story explicitly names every E2
	// MATRIX-host kind in types.AllOverlayKinds(); guard against catalog
	// drift by asserting one spec per known MATRIX-host kind before the
	// predict call. E3 SERIES-host kinds (OVERLAY_INDEX_VS_TOTAL,
	// OVERLAY_ZSCORE_VS_TOTAL) target a grouped Process result, not a
	// crosstab — they are covered by their own per-kind happy-path tests
	// (TestValidateOverlay_<Kind>_HappyPath) against
	// indexVsTotalSeriesHostReq()-style fixtures rather than the MATRIX-
	// host fixture this test pins. OVERLAY_SHARE_OF_TOTAL is dual-shape
	// (E2-S3 MATRIX dispatch + E3-S3 SERIES dispatch) and rides on the
	// MATRIX-host catalog gate via the same Ref.Margin shape the rest of
	// the matrix triad uses.
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
			// OVERLAY_INDEX_VS_POP (E5-S2) is a FACET-host kind —
			// neither MATRIX nor SERIES. The predict-side validator
			// lands in E5-S6 / E5-S10; until then this MATRIX-host
			// gate skips the kind so it does not collide with the
			// crosstab-host fixture.
			types.OverlayKindIndexVsPop,
			// OVERLAY_ZSCORE_VS_POP (E5-S3) is a FACET-host kind too —
			// sibling streamable kind to INDEX_VS_POP. Same MATRIX-host
			// gate skip rule (the predict-side validator lands in
			// E5-S6 / E5-S7).
			types.OverlayKindZScoreVsPop,
			// OVERLAY_CHISQ_VS_POP (E5-S4) is a FACET-host kind too —
			// first inferential FACET-host kind. Unlike INDEX_VS_POP /
			// ZSCORE_VS_POP it is BUFFERED (per PRD §2 Non-Goals
			// "Streaming overlay path for inferential kinds"), but the
			// host-shape skip still applies because the kind does not
			// belong on the MATRIX-host fixture. The per-kind validator
			// lands in E5-S6 / E5-S7.
			types.OverlayKindChiSqVsPop,
			// OVERLAY_KS_VS_POP (E5-S5) is the fourth and final FACET-host
			// kind — second inferential FACET-host kind (sibling to
			// CHISQ_VS_POP). NUMERIC-arm only (CHISQ_VS_POP is the
			// discrete-arm equivalent). BUFFERED per PRD §2 Non-Goals
			// "Streaming overlay path for inferential kinds". Host-shape
			// skip applies because the kind does not belong on the
			// MATRIX-host fixture. The per-kind validator lands in E5-S10.
			types.OverlayKindKSVsPop,
			// OVERLAY_INDEX_VS_STAGE (E6-S2 declares; E6-S4 lands the
			// handler) is a whole-chain (CHAIN-host) kind — neither
			// MATRIX nor SERIES nor FACET. The chain barrier runs at
			// the post-stage-loop hook inside `service.ProcessChain`
			// against already-materialised stage responses, not at the
			// per-stage Crosstab overlay path tested here. Skip from
			// the MATRIX-host fixture; per-kind validator lands in
			// E6-S7.
			types.OverlayKindIndexVsStage,
			// OVERLAY_DELTA_VS_STAGE (E6-S2 declares; E6-S5 lands the
			// handler) is the sibling subtractive twin of
			// INDEX_VS_STAGE — same CHAIN-host skip rule applies.
			types.OverlayKindDeltaVsStage,
			// COMPOSE-host kinds (E7-S9) — the six crosstab-shape
			// Compose-only kinds run at the post-slot-barrier fold
			// inside `service.Compose` / `service.ComposeParallel`,
			// not at the per-Request Crosstab overlay path tested
			// here. Skip from the MATRIX-host fixture; per-kind
			// validator lands in E7-S14.
			types.OverlayKindIndexVsRef,
			types.OverlayKindDeltaVsRef,
			types.OverlayKindPropZCell,
			// PROP_Z_PANEL (E7-S11) is the multi-ref pairwise-Sig sibling
			// of PROP_Z_CELL — same COMPOSE-host skip rule applies.
			types.OverlayKindPropZPanel,
			// PANEL_INDEX_VS_REF (E7-S12) is the multi-ref descriptive
			// sibling of OVERLAY_INDEX_VS_REF — same COMPOSE-host skip
			// rule applies. Emits one layer per target sharing the
			// reference slot's coord space.
			types.OverlayKindPanelIndexVsRef,
			types.OverlayKindTCell,
			// T_VS_REF (E7-S10) is the series-shape COMPOSE-only sibling
			// of T_CELL — same COMPOSE-host skip rule.
			types.OverlayKindTVsRef,
			types.OverlayKindChiSqVsRef,
			types.OverlayKindRank:
			// SERIES / FACET host: skipped from the MATRIX-host catalog gate.
			// E3-S5 adds DELTA_VS_SIBLING + INDEX_VS_SIBLING — both ride
			// on the same SERIES-host predicate as INDEX_VS_TOTAL /
			// ZSCORE_VS_TOTAL; they are covered by their own per-kind
			// happy-path tests against indexVsTotalSeriesHostReq()-style
			// fixtures rather than this MATRIX-host fixture. E4-S4 adds
			// INDEX_VS_PRIOR (windowed lag-1) and E4-S2 adds
			// INDEX_VS_BASELINE (windowed positional anchor) which are
			// also SERIES-host and ride on the same skip rule. E4-S3 adds
			// DELTA_VS_BASELINE (absolute-difference twin of
			// INDEX_VS_BASELINE) — same SERIES-host predicate. E4-S6
			// adds ZSCORE_VS_ROLLING (sibling windowed-rolling kind to
			// INDEX_VS_ROLLING_MEAN) — same SERIES-host predicate. E4-S7
			// adds OVERLAY_YOY (windowed year-over-year against a
			// GROUP_DATE host) — same SERIES-host predicate.
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
		// Streamable echoes the static table; every E2 kind is buffered
		// today so Streamable must be false. The assertion is structural,
		// not "must be false" — when a future kind flips streamable this
		// test still passes because the assertion reads the table.
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

	// OverlayCost: per-kind multiplier keyed by the renderer-facing
	// name. E3-S11 replaced the E2 flat-1.0 stub with the streamability-
	// derived dispatch (overlayCostForKind) — streamable kinds carry
	// overlayCostStreamable, buffered kinds carry overlayCostBuffered.
	// The MATRIX-host catalog gate this test exercises is intrinsically
	// streamable=false for every kind it hits today (SHARE_OF_TOTAL's
	// MATRIX dispatch piggybacks on the SERIES streamable flag, so the
	// cost map still reads the streamable score for that single spec —
	// the host-gate buffered fallback is separate and lives in
	// canFuseCrosstab).
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

	// OverlaysSchemaDivergence stays empty in E2 — Compose-driven
	// divergence lands in E7-S14. The slot must be present (never nil)
	// for JSON-shape stability per PRD §I-FR-I3.
	if result.OverlaysSchemaDivergence == nil {
		t.Errorf("OverlaysSchemaDivergence = nil; expected non-nil empty slice")
	}
	if got := len(result.OverlaysSchemaDivergence); got != 0 {
		t.Errorf("OverlaysSchemaDivergence length = %d, want 0 (Compose-driven divergence lands in E7-S14)", got)
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

// TestPredict_OverlayCost_StreamableKindsLow is the E3-S11 close-out
// gate for the streamable subset of the OverlayCost map: every
// streamable overlay kind (per types.OverlayStreamable) must surface
// the low cost multiplier (overlayCostStreamable) in
// PredictResult.OverlayCost. The streamable trio shipped by E3 —
// OVERLAY_INDEX_VS_TOTAL, OVERLAY_SHARE_OF_TOTAL (SERIES dispatch),
// OVERLAY_ZSCORE_VS_TOTAL — folds inside the streaming Process pass
// via a kind-specific accumulator, so the marginal record-count
// multiplier should be near-zero (5% per PRD §I-FR-I3). Anchored
// against types.AllOverlayKinds() so any future kind that flips
// streamable is automatically covered without a fixture edit.
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
		// INDEX_VS_PRIOR (E4-S4): first streamable windowed-Process kind.
		// SERIES host (same indexVsTotalSeriesHostReq fixture); the
		// implicit-default authoring shape leaves Ref empty.
		types.OverlayKindIndexVsPrior: {
			Name:  "idx_prior",
			Kind:  types.OverlayKindIndexVsPrior,
			Scope: types.OverlayScopeGroup,
		},
		// INDEX_VS_POP (E5-S2): first streamable FACET-host kind. The
		// predict-layer fixture exercises only the cost-dispatch surface
		// — overlayCostForKind reads the streamability table and emits
		// overlayCostStreamable for every streamable kind regardless of
		// the host shape the request actually carries. The per-kind
		// validator lands in E5-S6 / E5-S10 so this story leaves the
		// validator silent for the FACET-host kind — the cost test
		// still exercises the streamable bucket correctly.
		types.OverlayKindIndexVsPop: {
			Name:  "idx_pop",
			Kind:  types.OverlayKindIndexVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		// ZSCORE_VS_POP (E5-S3): second streamable FACET-host kind.
		// Pairs with INDEX_VS_POP as the two streamable Facet overlay
		// kinds — the cost-dispatch surface reads the streamability
		// table and emits overlayCostStreamable for every streamable
		// kind regardless of the host shape the request actually
		// carries. The per-kind validator lands in E5-S6 / E5-S7 so
		// this story leaves the validator silent for the FACET-host
		// kind — the cost test still exercises the streamable bucket
		// correctly.
		types.OverlayKindZScoreVsPop: {
			Name:  "z_pop",
			Kind:  types.OverlayKindZScoreVsPop,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				Population: &types.OverlayPopulationRef{Cohort: "population"},
			},
		},
		// INDEX_VS_REF (E7-S9 MATRIX arm + E7-S10 SERIES arm): dual-shape
		// COMPOSE-only kind. Streamable flag describes the kind's
		// INTRINSIC streaming capability via its SERIES handler; the
		// MATRIX arm is forced buffered through the Compose slot barrier.
		// The cost-dispatch surface (overlayCostForKind) is a pure
		// function of the streamability table so it is exercised
		// mechanically — no per-kind COMPOSE fixture is needed and the
		// per-kind validator for COMPOSE-only kinds lands in E7-S14
		// (descriptor.ValidateComposedRequest).
		types.OverlayKindIndexVsRef: {
			Name:  "idx_ref",
			Kind:  types.OverlayKindIndexVsRef,
			Scope: types.OverlayScopeGroup,
		},
		// DELTA_VS_REF (E7-S9 MATRIX arm + E7-S10 SERIES arm): dual-shape
		// COMPOSE-only kind, subtractive sibling of INDEX_VS_REF. Same
		// cost-dispatch rationale as INDEX_VS_REF.
		types.OverlayKindDeltaVsRef: {
			Name:  "delta_ref",
			Kind:  types.OverlayKindDeltaVsRef,
			Scope: types.OverlayScopeGroup,
		},
		// PANEL_INDEX_VS_REF (E7-S12): multi-reference dual-shape
		// COMPOSE-only kind, descriptive twin of OVERLAY_PROP_Z_PANEL.
		// Streamable flag describes the kind's INTRINSIC streaming
		// capability via its per-target SERIES emission — each emitted
		// layer reuses OVERLAY_INDEX_VS_REF for one (reference,
		// target[i]) pair. Same cost-dispatch rationale as INDEX_VS_REF.
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

// TestPredict_OverlayCost_BufferedKindsHigh is the E3-S11 close-out
// gate for the buffered subset of the OverlayCost map. Specifically
// targets the SIBLING family (OVERLAY_DELTA_VS_SIBLING /
// OVERLAY_INDEX_VS_SIBLING) — both shipped by E3-S5 and both buffered
// because sibling resolution requires the finalized per-group
// accumulators. Each kind must surface the buffered cost multiplier
// (overlayCostBuffered) in PredictResult.OverlayCost.
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
			// E4-S2 INDEX_VS_BASELINE: SERIES-host windowed kind anchored
			// against a positional baseline (Ref.BaselineIndex.Position).
			// Buffered because baseline resolution requires the
			// materialised host series — mirrors the sibling-family
			// buffered cost dispatch. The siblingHostReq fixture exposes a
			// single GROUP_CATEGORY grouper over `region` (2 dict entries)
			// so Position=0 stays in-range at predict time.
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
			// E4-S3 DELTA_VS_BASELINE: SERIES-host windowed kind, absolute-
			// difference twin of INDEX_VS_BASELINE. Same buffered cost
			// dispatch — baseline resolution requires the materialised host
			// series. Same Position=0 in-range trick against the
			// siblingHostReq region-grouper fixture (2 dict entries).
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
			// E4-S5 INDEX_VS_ROLLING_MEAN: SERIES-host windowed kind, ring-
			// buffer rolling-window carrier (buffered because the ring is
			// W f64s — larger than the streamable INDEX_VS_PRIOR single-
			// state lag accumulator). Window=2 (positive integer per
			// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE guard) keeps the spec
			// well-formed; the Params blob uses encoding/json so the
			// predict gate sees a parseable shape.
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
			// E4-S6 ZSCORE_VS_ROLLING: SERIES-host windowed kind, sibling
			// to INDEX_VS_ROLLING_MEAN (E4-S5) — shares the same ring-
			// buffer rolling-window carrier (buffered for the same reason
			// the sibling kind is buffered: the ring is W f64s plus a
			// Welford trio). Window=2 keeps the spec well-formed for the
			// predict gate; the Params blob uses encoding/json so the
			// predict gate sees a parseable shape.
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
			// E4-S7 OVERLAY_YOY: SERIES-host windowed kind, year-over-year
			// ratio against the same period one year prior. Requires a
			// GROUP_DATE host (the standard siblingHostReq uses
			// GROUP_CATEGORY, so this case uses its own yoyHostReq
			// fixture). Buffered — the per-frequency prior-period
			// lookup requires the materialised host series.
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
			// Sanity-check the streamability table: SIBLING kinds must
			// stay buffered. A future epic that streams the family
			// (E3-S7 / E3-S8 forward-compat note in
			// types/overlay_streamability.go) will trip this guard —
			// move the spec to the streamable test above when that
			// lands.
			streamable, known := types.OverlayStreamable(tc.spec.Kind)
			if !known {
				t.Fatalf("kind %q absent from streamability table", tc.spec.Kind)
			}
			if streamable {
				t.Fatalf("kind %q flipped streamable; move to TestPredict_OverlayCost_StreamableKindsLow",
					tc.spec.Kind)
			}

			// E4-S7 YoY requires a GROUP_DATE host; other windowed kinds
			// use the siblingHostReq GROUP_CATEGORY fixture.
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

// TestPredict_OverlayCost_E2KindsBufferedDefault sanity-checks that
// the E2 buffered MATRIX-host kinds (CHISQ family, FISHER_EXACT_CELL,
// DELTA_VS_MARGIN, INDEX_VS_MARGIN, SHARE_OF_ROW / SHARE_OF_COL,
// ZSCORE_VS_MARGIN) continue to map to overlayCostBuffered after the
// E3-S11 dispatch flip. Pre-E3 the populator emitted a flat 1.0 stub
// for every kind; the new streamability-derived dispatch must preserve
// that value for every kind whose streamability flag is false. Anchored
// against types.AllOverlayKinds() so any future MATRIX-host kind is
// automatically covered without a fixture edit.
func TestPredict_OverlayCost_E2KindsBufferedDefault(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	// Per-kind well-formed spec table — every MATRIX-host buffered kind
	// shipped by E2 (and INDEX_VS_MARGIN from E1) lands here.
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
		// OVERLAY_INDEX_VS_POP (E5-S2) is a FACET-host kind — neither
		// MATRIX nor SERIES. The buffered MATRIX-host gate skips the
		// kind here so it does not collide with the crosstab-host
		// fixture; the kind is intentionally streamable (covered by
		// TestPredict_OverlayCost_StreamableKindsLow), so it would not
		// belong in this buffered-cost test even if the host shape
		// matched.
		types.OverlayKindIndexVsPop: true,
		// OVERLAY_ZSCORE_VS_POP (E5-S3) is a FACET-host kind too —
		// sibling streamable kind to INDEX_VS_POP. Same skip rule
		// (covered by TestPredict_OverlayCost_StreamableKindsLow).
		types.OverlayKindZScoreVsPop: true,
		// OVERLAY_CHISQ_VS_POP (E5-S4) is a FACET-host kind too —
		// first inferential FACET-host kind (buffered per PRD §2 Non-
		// Goals "Streaming overlay path for inferential kinds"). The
		// buffered MATRIX-host gate skips the kind here because it
		// does not belong on the crosstab-host fixture (FACET host,
		// not MATRIX host); the cost-dispatch surface
		// (overlayCostForKind) is a pure function of the streamability
		// table so it is exercised mechanically — no per-kind fixture
		// is needed and the per-kind predict-side validator lands in
		// E5-S6 / E5-S7.
		types.OverlayKindChiSqVsPop: true,
		// OVERLAY_KS_VS_POP (E5-S5) is the fourth and final FACET-host
		// kind — second inferential FACET-host kind (sibling to
		// CHISQ_VS_POP). NUMERIC-arm only and BUFFERED per PRD §2 Non-
		// Goals. The buffered MATRIX-host gate skips the kind here
		// because it does not belong on the crosstab-host fixture
		// (FACET host, not MATRIX host); the cost-dispatch surface
		// (overlayCostForKind) is a pure function of the streamability
		// table so it is exercised mechanically — no per-kind fixture
		// is needed and the per-kind predict-side validator lands in
		// E5-S10.
		types.OverlayKindKSVsPop: true,
		// OVERLAY_INDEX_VS_STAGE (E6-S2 declares; E6-S4 lands the
		// runtime handler) is a whole-chain (CHAIN-host) kind — the
		// barrier runs at the post-stage-loop hook inside
		// `service.ProcessChain` against already-materialised stage
		// responses, not at the per-stage Crosstab overlay path tested
		// here. The MATRIX-host gate skips the kind so it does not
		// collide with the crosstab-host fixture; the cost-dispatch
		// surface is a pure function of the streamability table and
		// `OverlayStreamability[OverlayKindIndexVsStage] == false`
		// (whole-chain kinds are buffered by construction).
		types.OverlayKindIndexVsStage: true,
		// OVERLAY_DELTA_VS_STAGE (E6-S2 declares; E6-S5 lands the
		// runtime handler) is the sibling subtractive twin of
		// INDEX_VS_STAGE — same CHAIN-host skip rule applies.
		types.OverlayKindDeltaVsStage: true,
		// COMPOSE-host kinds (E7-S9) — the six crosstab-shape
		// Compose-only kinds run at the post-slot-barrier fold inside
		// `service.Compose` / `service.ComposeParallel` against
		// already-materialised per-slot *Response objects, not at the
		// per-Request Crosstab overlay path tested here. The MATRIX-
		// host gate skips them so they do not collide with the
		// crosstab-host fixture; the cost-dispatch surface is a pure
		// function of the streamability table and every COMPOSE kind
		// is buffered by construction. The per-kind validator lands
		// in E7-S14 (descriptor.ValidateComposedRequest).
		types.OverlayKindIndexVsRef: true,
		types.OverlayKindDeltaVsRef: true,
		types.OverlayKindPropZCell:  true,
		// PROP_Z_PANEL (E7-S11) is the multi-ref pairwise-Sig sibling of
		// PROP_Z_CELL — same COMPOSE-host skip rule applies. Buffered
		// (inferential family).
		types.OverlayKindPropZPanel: true,
		// PANEL_INDEX_VS_REF (E7-S12) is the multi-ref descriptive sibling
		// of OVERLAY_INDEX_VS_REF — same COMPOSE-host skip rule applies.
		// Streamable via per-target SERIES emission (the kind inherits
		// OVERLAY_INDEX_VS_REF's dual-shape streamability convention).
		types.OverlayKindPanelIndexVsRef: true,
		types.OverlayKindTCell:      true,
		// T_VS_REF (E7-S10) is the series-shape sibling of T_CELL — same
		// COMPOSE-host skip rule applies. Buffered (inferential family).
		types.OverlayKindTVsRef:     true,
		types.OverlayKindChiSqVsRef: true,
		types.OverlayKindRank:       true,
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
				t.Errorf("OverlayCost[%q] = %v, want %v (buffered E2 kind %q must map to overlayCostBuffered)",
					spec.Name, cost, overlayCostBuffered, kind)
			}
		})
	}
}

// TestPredict_OverlaysApplied_AllKinds_DescriptorCoverage is the E10-S1
// close-out gate. Audits PredictResult.OverlaysApplied descriptor
// completeness across the FULL overlay catalog (types.AllOverlayKinds())
// — for every registered kind, a well-formed spec must produce exactly
// one OverlayAppliedDescriptor populated with Name + Kind + Scope +
// Shape (when resolvable) + Ref (when applicable) + Streamable.
//
// Earlier per-host tests (TestPredict_OverlaysApplied_AllE2Kinds for
// MATRIX hosts; TestPredict_FacetOverlaysApplied for FACET hosts;
// per-kind happy-path tests in overlay_*_test.go for SERIES hosts)
// pinned the Name + Kind + Scope + Streamable contract per-host. This
// test extends the contract with the E10-S1 Shape + Ref additions and
// asserts coverage across EVERY kind in one place — a new overlay
// kind that doesn't add a fixture entry to per-kind-spec-table below
// fails closed at the audit point rather than slipping through with
// an empty descriptor surface.
//
// Implementation note: the audit drives the descriptor builder
// (appendOverlayDescriptors) directly with a per-kind well-formed
// spec rather than routing through PredictFromBytes against a host
// fixture. The per-host integration is covered by the existing
// dedicated tests; this test pins the per-spec descriptor surface
// (Shape + Ref population) independent of host wiring.
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

// TestPredict_OverlaysApplied_ShapeAndRefPopulated_RequestHost pins the
// E10-S1 Shape + Ref additions across the request-host predict path
// against a MATRIX (crosstab) fixture so the new fields land on the
// already-shipping predict surface. Sibling to
// TestPredict_OverlaysApplied_AllE2Kinds (per-host integration); this
// test guards the descriptor surface plumbing at the
// PredictFromBytes(... )-against-MATRIX-host call site rather than the
// pure descriptor builder.
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
