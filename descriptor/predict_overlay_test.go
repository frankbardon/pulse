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
			types.OverlayKindIndexVsBaseline,
			types.OverlayKindDeltaVsBaseline:
			// SERIES-host: skipped from the MATRIX-host catalog gate.
			// E3-S5 adds DELTA_VS_SIBLING + INDEX_VS_SIBLING — both ride
			// on the same SERIES-host predicate as INDEX_VS_TOTAL /
			// ZSCORE_VS_TOTAL; they are covered by their own per-kind
			// happy-path tests against indexVsTotalSeriesHostReq()-style
			// fixtures rather than this MATRIX-host fixture. E4-S4 adds
			// INDEX_VS_PRIOR (windowed lag-1) and E4-S2 adds
			// INDEX_VS_BASELINE (windowed positional anchor) which are
			// also SERIES-host and ride on the same skip rule. E4-S3 adds
			// DELTA_VS_BASELINE (absolute-difference twin of
			// INDEX_VS_BASELINE) — same SERIES-host predicate.
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

			req := siblingHostReq()
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
