package descriptor

import (
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
//   - OverlayCost: flat 1.0 per kind (per-kind heuristics land in
//     E10-S3).
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
		case types.OverlayKindIndexVsTotal, types.OverlayKindZScoreVsTotal:
			// SERIES-host: skipped from the MATRIX-host catalog gate.
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

	// OverlayCost: flat 1.0 per spec keyed by the renderer-facing name.
	if got, want := len(result.OverlayCost), len(specs); got != want {
		t.Errorf("OverlayCost length = %d, want %d", got, want)
	}
	for i, spec := range specs {
		cost, ok := result.OverlayCost[spec.Name]
		if !ok {
			t.Errorf("OverlayCost missing key %q (spec index %d)", spec.Name, i)
			continue
		}
		if cost != 1.0 {
			t.Errorf("OverlayCost[%q] = %v, want 1.0 (E1 stub; refined in E10-S3)", spec.Name, cost)
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
