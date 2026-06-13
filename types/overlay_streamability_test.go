package types

import "testing"

// TestStreamability_OverlaysKnown asserts every overlay kind returns
// the documented streamability value AND is enumerated in
// AllOverlayKinds(). Adding a new overlay kind requires extending the
// OverlayStreamability map in overlay_streamability.go, appending the
// constant to AllOverlayKinds() in overlay.go, AND adding an expected
// row here.
//
// Joins the TestStreamability_*Known family listed under CLAUDE.md
// "Non-Skippable CI Gates". A missing row, a stale row, or a divergent
// value all fail the gate so the table cannot silently drift.
func TestStreamability_OverlaysKnown(t *testing.T) {
	expected := map[OverlayKind]bool{
		// CHISQ_COL is inferential — mechanical column-axis twin of
		// CHISQ_ROW; each per-column goodness-of-fit test reads the
		// column's observed row distribution AND every row margin to
		// drive the expected-count recurrence, all of which require the
		// buffered host crosstab matrix.
		OverlayKindChiSqCol: false,
		// CHISQ_MATRIX is inferential and consumes the whole-matrix
		// contingency table — the host crosstab path is buffered and
		// the chi-square test itself walks the materialised matrix.
		OverlayKindChiSqMatrix: false,
		// CHISQ_ROW is inferential — each per-row goodness-of-fit test
		// reads the row's observed column distribution AND every column
		// margin to drive the expected-count recurrence, all of which
		// require the buffered host crosstab matrix.
		OverlayKindChiSqRow: false,
		// CHISQ_VS_POP is inferential (E5-S4) — first inferential
		// FACET-host kind. Per PRD §2 Non-Goals ("Streaming overlay
		// path for inferential kinds"), inferential overlays as a
		// family stay buffered regardless of host streamability. The
		// handler computes a single χ² goodness-of-fit statistic
		// against the resolved population distribution, walks the host's
		// already-finalised discrete payload to build the observed ×
		// expected contingency, and reuses chiSquareSurvival (the same
		// helper backing TEST_CHISQ + the MATRIX-host CHISQ family).
		// The buffered flag stays false as a matter of policy — the
		// streamable subset is reserved for descriptive kinds (sibling
		// streamable FACET-host kinds are INDEX_VS_POP / ZSCORE_VS_POP,
		// both descriptive).
		OverlayKindChiSqVsPop: false,
		// CHISQ_VS_REF is inferential COMPOSE-only kind (E7-S9) — runs at
		// the post-slot-barrier fold once every slot has produced a
		// finalised *Response. Per PRD §2 Non-Goals every inferential
		// overlay stays buffered regardless of host streamability.
		OverlayKindChiSqVsRef: false,
		// DELTA_VS_BASELINE is buffered — absolute-difference twin of
		// INDEX_VS_BASELINE. Resolving a single positional baseline
		// (Ref.BaselineIndex.Position) requires the materialised host
		// series; the handler runs at the buffered post-host-finalize exit
		// via ApplyOverlaysSeries.
		OverlayKindDeltaVsBaseline: false,
		// DELTA_VS_MARGIN shares INDEX_VS_MARGIN's buffered footprint —
		// its margin centerpoint is recomputed by the buffered crosstab
		// orchestrator before ApplyOverlays runs.
		OverlayKindDeltaVsMargin: false,
		// DELTA_VS_REF is streamable via its SERIES-host dispatch
		// (E7-S10) — sibling COMPOSE-only kind to OVERLAY_INDEX_VS_REF,
		// subtractive twin. Fold-only series handler; MATRIX-arm forced
		// buffered through the slot barrier. The flag describes the
		// kind's INTRINSIC streaming capability via its SERIES handler.
		OverlayKindDeltaVsRef: true,
		// DELTA_VS_SIBLING is buffered — sibling resolution against a
		// (Field, Value) pair requires the full materialised
		// SeriesPayload; the streaming Process pass cannot resolve the
		// reference until the per-group accumulators finalise. Mirrors
		// INDEX_VS_SIBLING.
		OverlayKindDeltaVsSibling: false,
		// DELTA_VS_STAGE is buffered — second whole-chain (E6) kind,
		// sibling subtractive twin of INDEX_VS_STAGE. Whole-chain
		// overlays run at the post-stage-loop barrier inside
		// `service.ProcessChain` against already-materialised
		// `*Response` objects; the whole-chain kind family has no
		// streamable arm by construction.
		OverlayKindDeltaVsStage: false,
		// FISHER_EXACT_CELL is inferential — per-cell 2×2 contingency
		// reads each cell value AND its row + column margins, all of
		// which require the buffered host crosstab matrix. PRD § 4.C
		// FR-C2 canonical low-count contingency overlay.
		OverlayKindFisherExactCell: false,
		// OVERLAY_FORMULA is buffered-only at v1 across every host shape
		// (E8-S2). The per-host-shape variable namespace depends on
		// post-fold state — `margin_*` (MATRIX), `total` (SERIES),
		// `sd_*` (MATRIX) all require margins / totals / Welford SDs
		// not available mid-stream. A streamable FORMULA arm would
		// either need to defer evaluation to post-finalize (which
		// buffered already does) or restrict the variable table to a
		// subset excluding margin / total / SD variables, splitting
		// FORMULA into two kinds; authoring clarity argues against the
		// split. Forward-compat: a future story may carve out a
		// streamable variant under a distinct kind constant (e.g.
		// OVERLAY_FORMULA_STREAMING) with a restricted variable table.
		OverlayKindFormula: false,
		// INDEX_VS_BASELINE is buffered — resolving a single positional
		// baseline (Ref.BaselineIndex.Position) requires the materialised
		// host series; the handler runs at the buffered post-host-finalize
		// exit via ApplyOverlaysSeries (mirrors INDEX_VS_SIBLING).
		OverlayKindIndexVsBaseline: false,
		// INDEX_VS_MARGIN rides on the buffered crosstab path — margins
		// are always recomputed from raw rows, so the host operator is
		// inherently buffered (see CLAUDE.md "Execution modes" →
		// Crosstab).
		OverlayKindIndexVsMargin: false,
		// INDEX_VS_POP is the first streamable FACET-host overlay (E5-S2).
		// The handler runs as a post-finalize fold over the host's already-
		// materialised per-value distribution and the resolver's
		// FacetPopulationView; no second pass over records and no
		// widening of the host streaming carrier. Per kind-catalog-v1
		// "Streaming-capable subset".
		OverlayKindIndexVsPop: true,
		// INDEX_VS_PRIOR is the first streamable windowed-Process overlay
		// (E4-S4). The single-state lag carrier is one f64 carried
		// alongside the per-group accumulators inside the streaming
		// Process fold; the post-host finalize is the divide step.
		OverlayKindIndexVsPrior: true,
		// INDEX_VS_REF is streamable via its SERIES-host dispatch
		// (E7-S10) — first COMPOSE-only kind in the catalog (E7-S9),
		// ratio sibling of OVERLAY_DELTA_VS_REF. Fold-only series
		// handler; MATRIX-arm forced buffered through the slot barrier.
		// The flag describes the kind's INTRINSIC streaming capability
		// via its SERIES handler.
		OverlayKindIndexVsRef: true,
		// INDEX_VS_ROLLING_MEAN is buffered (E4-S5) — the per-group ring
		// buffer carries the full window of present values (W f64s plus a
		// Welford trio reserved for the E4-S6 ZSCORE_VS_ROLLING sibling
		// kind), larger than the single-state lag accumulator
		// INDEX_VS_PRIOR uses, so the streaming Process pass cannot
		// maintain it inline with the per-record fold today. The handler
		// runs at the buffered post-host-finalize exit via
		// ApplyOverlaysSeries.
		OverlayKindIndexVsRollingMean: false,
		// INDEX_VS_SIBLING is buffered — sibling resolution against a
		// (Field, Value) pair requires the full materialised
		// SeriesPayload; the streaming Process pass cannot resolve the
		// reference until the per-group accumulators finalise. Mirrors
		// DELTA_VS_SIBLING.
		OverlayKindIndexVsSibling: false,
		// INDEX_VS_STAGE is buffered — first whole-chain (E6) kind,
		// first consumer of the StageRef discriminated reference family.
		// Whole-chain overlays run at the post-stage-loop barrier inside
		// `service.ProcessChain` against already-materialised
		// `*Response` objects; the whole-chain kind family has no
		// streamable arm by construction.
		OverlayKindIndexVsStage: false,
		// INDEX_VS_TOTAL is the first streamable overlay — the
		// grand-total accumulator is one f64 alongside the per-group
		// accumulators inside the streaming Process fold; no second pass
		// over records.
		OverlayKindIndexVsTotal: true,
		// KS_VS_POP is inferential (E5-S5) — fourth FACET-host kind and
		// second inferential FACET-host kind (sibling to CHISQ_VS_POP).
		// NUMERIC-arm only — categorical hosts fire
		// PULSE_OVERLAY_SCOPE_UNSUPPORTED at runtime. Per PRD §2 Non-Goals
		// ("Streaming overlay path for inferential kinds"), inferential
		// overlays as a family stay buffered regardless of host
		// streamability. The handler reconstructs empirical CDFs from
		// post-finalize summary state (histogram preferred, percentile-map
		// fallback) and reuses kolmogorovSurvival (the same helper backing
		// TEST_KS). The buffered flag stays false as a matter of policy.
		OverlayKindKSVsPop: false,
		// PANEL_INDEX_VS_REF is the multi-ref descriptive COMPOSE-only
		// kind (E7-S12). Sibling to OVERLAY_PROP_Z_PANEL (inferential,
		// buffered) but DESCRIPTIVE — each emitted layer reuses the
		// OVERLAY_INDEX_VS_REF math for one (reference, target[i]) pair,
		// so the per-target SERIES arm is fold-only and the kind inherits
		// the OVERLAY_INDEX_VS_REF dual-shape streamability convention.
		OverlayKindPanelIndexVsRef: true,
		// PROP_Z_CELL is inferential COMPOSE-only kind (E7-S9). Reuses
		// the standardNormalCDF helper backing TEST_PROP_Z. Per PRD §2
		// Non-Goals every inferential overlay stays buffered regardless
		// of host streamability.
		OverlayKindPropZCell: false,
		// PROP_Z_PANEL is the multi-ref inferential COMPOSE-only kind
		// (E7-S11). Emits a per-cell flattened upper-triangular slice of
		// pairwise two-proportion p-values across N + 1 slots. Reuses
		// the twoProportionZ helper that backs OVERLAY_PROP_Z_CELL.
		// Inferential family stays buffered per PRD §2 Non-Goals.
		OverlayKindPropZPanel: false,
		// RANK is buffered — COMPOSE-only kind (E7-S9), per-cell
		// rank-within-population (row / column / matrix). The COMPOSE
		// host is buffered by the slot barrier; ranking within the
		// target matrix requires the full materialised matrix anyway.
		OverlayKindRank: false,
		// SHARE_OF_COL shares INDEX_VS_MARGIN's buffered footprint — its
		// column-margin denominator is recomputed by the buffered
		// crosstab orchestrator before ApplyOverlays runs.
		OverlayKindShareOfCol: false,
		// SHARE_OF_ROW shares INDEX_VS_MARGIN's buffered footprint — its
		// row-margin denominator is recomputed by the buffered crosstab
		// orchestrator before ApplyOverlays runs.
		OverlayKindShareOfRow: false,
		// SHARE_OF_TOTAL is streamable via its SERIES-host dispatch
		// (E3-S3) — sibling kind to OVERLAY_INDEX_VS_TOTAL, same
		// grand-total accumulator (computeSeriesGrandTotal in
		// processing/overlay_series.go), different scaling (raw share,
		// no ×100). The MATRIX-host dispatch (E2-S3) remains inherently
		// buffered through the canFuseCrosstab "overlays force buffered"
		// arm, so this flag describes the kind's INTRINSIC streaming
		// capability via its SERIES handler — not the composed
		// host-overlay routing decision.
		OverlayKindShareOfTotal: true,
		// T_CELL is inferential COMPOSE-only kind (E7-S9). Reuses the
		// studentTTwoSidedP helper backing TEST_T. Per PRD §2 Non-Goals
		// every inferential overlay stays buffered regardless of host
		// streamability.
		OverlayKindTCell: false,
		// T_VS_REF is inferential COMPOSE-only kind (E7-S10) — series-shape
		// sibling of T_CELL, per-group Welch t-test against the reference
		// slot's matching group. Reuses studentTTwoSidedP. Per PRD §2
		// Non-Goals every inferential overlay stays buffered regardless
		// of host streamability.
		OverlayKindTVsRef: false,
		// OVERLAY_YOY (E4-S7) is buffered — the per-frequency prior-period
		// lookup requires the materialised host series. Coarse-frequency
		// arms (annual / quarterly / monthly / weekly) index into an
		// arbitrary prior ordinal `i - <stride>`; fine-frequency arms
		// (daily / hourly) walk an exact-key index built from the full
		// host key list. Neither shape rides inside the streaming Process
		// fold today.
		OverlayKindYoY: false,
		// Z_CELL is inferential COMPOSE-only kind (E1-S14). Reuses the
		// standardNormalCDF helper backing TEST_Z_TWO_SAMPLE. Per PRD §2
		// Non-Goals every inferential overlay stays buffered regardless of
		// host streamability.
		OverlayKindZCell: false,
		// ZSCORE_VS_MARGIN shares INDEX_VS_MARGIN's buffered footprint —
		// both the per-slice Welford recurrence and the margin
		// centerpoint require a fully-materialised matrix, so the host
		// crosstab path stays buffered.
		OverlayKindZScoreVsMargin: false,
		// ZSCORE_VS_POP is the second streamable FACET-host overlay (E5-S3).
		// Pairs with INDEX_VS_POP as the two streamable Facet overlay kinds —
		// the handler runs as a post-finalize fold over the host's already-
		// materialised per-value distribution and the resolver's
		// FacetPopulationView (the sd_pop value is sourced from the
		// population's already-folded Welford state on the categorical fast
		// path via DiscreteFrequencyStdev() and from FacetNumeric.StdDev on
		// the numeric path). No second pass over records and no widening of
		// the host streaming carrier.
		OverlayKindZScoreVsPop: true,
		// ZSCORE_VS_ROLLING is buffered (E4-S6) — sibling windowed-rolling
		// kind to INDEX_VS_ROLLING_MEAN (E4-S5). Both kinds share the
		// per-group ring buffer + Welford (count, mean, M2) trio carrier;
		// the ring is W f64s and grows the streaming-fold state past v1's
		// single-state lag accumulator the same way the sibling kind does.
		// The handler reads BOTH mean and M2 (sample SD via
		// sqrt(M2/(count-1))); the sibling reads only mean. Buffered
		// rationale matches the INDEX_VS_ROLLING_MEAN row verbatim.
		OverlayKindZScoreVsRolling: false,
		// ZSCORE_VS_TOTAL is streamable — third streamable SERIES-host
		// overlay in the E3 grouped-Process subset. The Welford
		// accumulator (count + mean + M2) is three f64s alongside the
		// per-group accumulators inside the streaming Process fold; no
		// second pass over records. Population SD (sqrt(M2/N)) matches
		// the same numerical convention as the parallel buffered
		// Process path so cross-mode equivalence stays byte-equal
		// within ULP.
		OverlayKindZScoreVsTotal: true,
	}

	for _, k := range AllOverlayKinds() {
		want, ok := expected[k]
		if !ok {
			t.Fatalf("overlay kind %s missing from streamability table — declare it in types/overlay_streamability.go and add an entry here", k)
		}
		got, known := OverlayStreamable(k)
		if !known {
			t.Errorf("OverlayStreamable(%s) returned known=false; AllOverlayKinds() entry has no row in OverlayStreamability", k)
		}
		if got != want {
			t.Errorf("OverlayStreamable(%s) = %v, want %v", k, got, want)
		}
	}

	if len(expected) != len(AllOverlayKinds()) {
		t.Fatalf("overlay streamability table size mismatch: %d expected entries, %d kinds in AllOverlayKinds()", len(expected), len(AllOverlayKinds()))
	}
	if len(OverlayStreamability) != len(AllOverlayKinds()) {
		t.Fatalf("OverlayStreamability map size mismatch: %d rows, %d kinds in AllOverlayKinds()", len(OverlayStreamability), len(AllOverlayKinds()))
	}
}

// TestOverlayStreamable_UnknownKind asserts that an unknown overlay
// kind returns (false, false) — the contract callers (validator,
// predict gate) rely on to surface PULSE_OVERLAY_UNKNOWN-style
// diagnostics rather than silently streaming.
func TestOverlayStreamable_UnknownKind(t *testing.T) {
	streamable, known := OverlayStreamable("OVERLAY_DOES_NOT_EXIST")
	if known {
		t.Errorf("OverlayStreamable(unknown) known = true, want false")
	}
	if streamable {
		t.Errorf("OverlayStreamable(unknown) streamable = true, want false")
	}
}

// TestOverlayStreamable_KnownIndexVsMargin asserts the explicit
// acceptance-criterion case from E1-S2: OVERLAY_INDEX_VS_MARGIN must
// return (false, true).
func TestOverlayStreamable_KnownIndexVsMargin(t *testing.T) {
	streamable, known := OverlayStreamable(OverlayKindIndexVsMargin)
	if !known {
		t.Errorf("OverlayStreamable(OVERLAY_INDEX_VS_MARGIN) known = false, want true")
	}
	if streamable {
		t.Errorf("OverlayStreamable(OVERLAY_INDEX_VS_MARGIN) streamable = true, want false (host crosstab is buffered)")
	}
}
