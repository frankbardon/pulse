package types

// Overlay streamability table — source of truth for whether each
// OverlayKind can run inside the streaming Process path or forces the
// orchestrator down a buffered route. Mirrors the per-aggregator,
// per-attribute, per-test tables in streamability.go.
//
// Today every registered overlay rides on the buffered crosstab
// orchestrator (margins are recomputed from raw rows, see CLAUDE.md
// "Execution modes" → Crosstab and skills/crosstab-guide.md), so every
// row is false. Later epics that add streamable overlay families (e.g.
// a row-share overlay that can be folded into the streaming fused
// crosstab pass) flip the matching row to true and add the gate-side
// plumbing alongside.
//
// Adding a new OverlayKind:
//
//  1. Declare the constant + AllOverlayKinds() entry in overlay.go.
//  2. Add a row to overlayStreamability below.
//  3. TestStreamability_OverlaysKnown enforces table completeness — a
//     missing row fails the gate.
//
// The default branch of OverlayStreamable returns (false, false) so an
// unknown kind cannot accidentally stream — the validator
// (descriptor.ValidateOverlays) and the predict layer both treat
// "unknown" as "buffered".

// OverlayStreamability declares whether each overlay kind can be
// computed inside the streaming Process path. Keyed by the on-wire
// SCREAMING_SNAKE OverlayKind value.
//
// Per kind-catalog-v1.md "Streaming-capable subset", INDEX_VS_MARGIN
// is NOT in the streamable set today — its host crosstab is always
// buffered.
var OverlayStreamability = map[OverlayKind]bool{
	// OVERLAY_CHISQ_COL is inherently buffered — mechanical column-axis
	// twin of CHISQ_ROW. Each per-column goodness-of-fit test consumes
	// the column's observed row distribution AND every row margin (for
	// the expected-count recurrence), all of which the buffered host
	// crosstab orchestrator already materialised. Inferential overlays
	// as a family stay buffered until a streamable-test path is plumbed.
	OverlayKindChiSqCol: false,
	// OVERLAY_CHISQ_MATRIX is inherently buffered — the χ² independence
	// test consumes the whole-matrix observed × expected contingency
	// table, every margin recurrence is buffered on the host crosstab
	// path, and the runtime handler walks the materialised matrix
	// directly. Inferential overlays as a family stay buffered until a
	// future streamable-test path is plumbed.
	OverlayKindChiSqMatrix: false,
	// OVERLAY_CHISQ_ROW is inherently buffered — each per-row goodness-
	// of-fit test consumes the row's observed column distribution AND
	// every column margin (for the expected-count recurrence), all of
	// which the buffered host crosstab orchestrator already materialised.
	// Inferential overlays as a family stay buffered until a streamable-
	// test path is plumbed.
	OverlayKindChiSqRow: false,
	// OVERLAY_CHISQ_VS_POP is inherently buffered — first inferential
	// FACET-host kind. Per PRD §2 Non-Goals ("Streaming overlay
	// path for inferential kinds"), every inferential overlay stays
	// buffered regardless of host streamability. The handler computes a
	// single χ² goodness-of-fit statistic against the resolved population
	// distribution, walks the host's already-finalised discrete payload
	// (FacetField.Discrete.Values) in one pass to build the observed ×
	// expected contingency, and reuses the chiSquareSurvival helper that
	// backs TEST_CHISQ + the MATRIX-host CHISQ family. The handler reads
	// only POST-FINALIZE state — running it against a streaming Facet
	// host vs a buffered one produces byte-identical SCALAR output
	// because the input state is identical — but the streamability flag
	// stays false because the inferential family is buffered as a
	// matter of policy (the streamable subset is reserved for descriptive
	// kinds; sibling streamable FACET-host kinds are OVERLAY_INDEX_VS_POP
	// and OVERLAY_ZSCORE_VS_POP, both descriptive).
	OverlayKindChiSqVsPop: false,
	// OVERLAY_CHISQ_VS_REF is inherently buffered — COMPOSE-only kind
	// that runs at the post-slot-barrier fold once every slot
	// has produced a finalised *Response. Inferential kind in the COMPOSE
	// catalog (sibling to OVERLAY_PROP_Z_CELL / OVERLAY_T_CELL); per PRD
	// §2 Non-Goals ("Streaming overlay path for inferential kinds") every
	// inferential overlay stays buffered regardless of host streamability.
	// Reuses the chiSquareSurvival helper backing TEST_CHISQ and the
	// MATRIX-host CHISQ family. The COMPOSE host is always buffered by
	// construction — the slot barrier requires every slot's Response to
	// be finalised before the fold can run, so the streamable subset is
	// empty across the entire COMPOSE catalog.
	OverlayKindChiSqVsRef: false,
	// OVERLAY_DELTA_VS_BASELINE is buffered — resolving a single positional
	// baseline (Ref.BaselineIndex.Position) requires the materialised host
	// series (`host.ValueAt(Position)` is consulted after finalize). The
	// handler runs at the buffered post-host-finalize exit via
	// ApplyOverlaysSeries (mirrors OverlayKindIndexVsBaseline — the
	// absolute-difference twin of the same windowed positional-baseline
	// family). Forward-compat: future work may lift the baseline
	// resolver into a streaming-aware shape (carrying the resolved baseline
	// inline as a kind-specific accumulator advanced during the streaming
	// pass once the orchestrator hits the baseline ordinal); when that
	// lands the flag flips to true.
	OverlayKindDeltaVsBaseline: false,
	OverlayKindDeltaVsMargin:   false,
	// OVERLAY_DELTA_VS_REF is streamable via its SERIES-host dispatch
	// — sibling COMPOSE-only kind to OVERLAY_INDEX_VS_REF, subtractive
	// twin. The SERIES handler is fold-only (single accumulator per
	// group; no peer-cell lookup) and the kind-catalog-v1 "Streaming-
	// capable subset" lists DELTA_VS_REF among the fold-only streamable
	// COMPOSE kinds. The MATRIX-host dispatch remains
	// forced buffered by the slot barrier in `service.Compose` /
	// `service.ComposeParallel`; this flag describes the kind's
	// INTRINSIC streaming capability via its SERIES handler — not the
	// composed host-overlay routing decision (mirrors OverlayKindShareOfTotal's
	// dual-shape convention; MATRIX-arm forces buffered through the
	// slot barrier, SERIES-arm fold-streamable). Cost dispatcher
	// (overlayCostForKind) reads this flag to surface the streamable
	// cost multiplier for SERIES-host calls.
	OverlayKindDeltaVsRef: true,
	// OVERLAY_DELTA_VS_SIBLING is buffered — the SERIES sibling-reference
	// family resolves its comparison anchor via a (Field, Value) lookup
	// against the materialised per-group accumulators, which the streaming
	// Process pass cannot satisfy until finalize time. The handler runs at
	// the post-host-finalize exit (ApplyOverlaysSeries route) once the
	// host series is fully materialised. Mirrors OverlayKindIndexVsSibling.
	// Forward-compat: lifting the sibling resolver into a
	// streaming-aware shape (carrying the resolved sibling key inline as a
	// kind-specific accumulator) will flip the streamability flag to true
	// for both kinds.
	OverlayKindDeltaVsSibling: false,
	// OVERLAY_DELTA_VS_STAGE is buffered — second whole-chain kind,
	// sibling subtractive twin of OVERLAY_INDEX_VS_STAGE. Whole-chain
	// overlays run AFTER every stage has finalised by construction:
	// `service.ProcessChain` invokes `applyChainOverlays` at the
	// post-stage-loop barrier with already-materialised `*Response`
	// objects for each `Stages[i]`. There is no streamable arm for the
	// whole-chain kind family — the streamability flag stays `false`
	// for every whole-chain kind regardless of host shape.
	OverlayKindDeltaVsStage: false,
	// OVERLAY_FISHER_EXACT_CELL is inherently buffered — the per-cell 2×2
	// Fisher's exact test reads each cell value AND its row + column
	// margins to build the 2×2 contingency, all of which the buffered
	// host crosstab orchestrator already materialised. Inferential
	// overlays as a family stay buffered until a streamable-test path
	// is plumbed (PRD § 4.C FR-C2 — the canonical low-count contingency
	// overlay closing the inferential family).
	OverlayKindFisherExactCell: false,
	// OVERLAY_FORMULA is buffered-only at v1 for every host shape.
	// The per-host-shape variable namespace (research note
	// `.planning/result-overlay-system/research/formula-namespace.md` § 2)
	// depends on post-fold state — `margin_row` / `margin_col` /
	// `margin_grand` (MATRIX), `total` (SERIES), `sd_*` (MATRIX) all
	// require margins / totals / Welford SDs that are not available
	// mid-stream. A streamable FORMULA arm would either need to defer
	// evaluation to post-finalize (which is what buffered already does) or
	// restrict the variable table to a subset that excludes margin / total
	// / SD variables, splitting FORMULA into two kinds. Authoring clarity
	// argues against the split — better to keep one kind with a clear
	// "buffered" contract. Forward-compat: future work may carve out a
	// streamable variant under a distinct kind constant (e.g.
	// `OVERLAY_FORMULA_STREAMING`) with a restricted variable table
	// without re-opening this kind's contract; see research note § 6 for
	// the rationale.
	OverlayKindFormula: false,
	// OVERLAY_INDEX_VS_BASELINE is buffered — resolving a single positional
	// baseline (Ref.BaselineIndex.Position) requires the materialised host
	// series (`host.ValueAt(Position)` is consulted after finalize). The
	// handler runs at the buffered post-host-finalize exit via
	// ApplyOverlaysSeries (mirrors OVERLAY_INDEX_VS_SIBLING). Forward-compat:
	// future work may lift the baseline resolver into a streaming-aware
	// shape (carrying the resolved baseline inline as a kind-specific
	// accumulator advanced during the streaming pass once the orchestrator
	// hits the baseline ordinal); when that lands the flag flips to true.
	OverlayKindIndexVsBaseline: false,
	OverlayKindIndexVsMargin:   false,
	// OVERLAY_INDEX_VS_REF is streamable via its SERIES-host dispatch
	// — first COMPOSE-only kind in the catalog, ratio sibling of
	// OVERLAY_DELTA_VS_REF. The SERIES handler is fold-only
	// (single accumulator per group; no peer-cell lookup) and the
	// kind-catalog-v1 "Streaming-capable subset" lists INDEX_VS_REF
	// among the fold-only streamable COMPOSE kinds. The MATRIX-host
	// dispatch remains forced buffered by the slot barrier in
	// `service.Compose` / `service.ComposeParallel`; this flag describes
	// the kind's INTRINSIC streaming capability via its SERIES handler
	// — not the composed host-overlay routing decision (mirrors
	// OverlayKindShareOfTotal's dual-shape convention; MATRIX-arm forces
	// buffered through the slot barrier, SERIES-arm fold-streamable).
	// Cost dispatcher (overlayCostForKind) reads this flag to surface
	// the streamable cost multiplier for SERIES-host calls.
	OverlayKindIndexVsRef: true,
	// OVERLAY_INDEX_VS_POP is streamable — first FACET-host overlay in
	// the catalog. The handler runs as a post-finalize fold over
	// the host's already-materialized per-value distribution
	// (FacetField.Discrete.Values for categorical fields,
	// FacetNumeric.Histogram for numeric fields) and the resolver's
	// already-materialized FacetPopulationView. The host-side streaming
	// Facet pass keeps its existing online accumulators (Welford / per-
	// value count map) and the overlay does NOT widen the streaming
	// carrier — it consumes finalized host state only. The streaming
	// finalize hook IS the post-host-finalize entry: the streamable
	// guarantee is that running this handler against a streaming Facet
	// host vs a buffered one produces byte-identical SeriesPayload output
	// because the input state is identical. Per kind-catalog-v1
	// "Streaming-capable subset", OVERLAY_INDEX_VS_POP is explicitly
	// listed as YES.
	OverlayKindIndexVsPop: true,
	// OVERLAY_INDEX_VS_PRIOR is streamable — first windowed-Process
	// overlay in the streamable subset. The single-state lag
	// carrier is one f64 carried alongside the per-group accumulators
	// inside the streaming Process fold: each present point divides by
	// the carrier and then advances it to its own value (absent host
	// points do NOT advance the carrier — the "prior" stays the last
	// PRESENT value). The post-host finalize step is the divide. v1
	// ships lag-1 only via the implicit-default `Ref.Prior` arm.
	OverlayKindIndexVsPrior: true,
	// OVERLAY_INDEX_VS_ROLLING_MEAN is buffered — the per-group ring buffer
	// carries the full window of present values, which the streaming
	// Process pass cannot maintain inline with the per-record fold today
	// (the carrier widens beyond a single f64 — OVERLAY_INDEX_VS_PRIOR's
	// streamable lag carrier is one f64; rolling-mean's ring is W f64s and
	// grows the streaming-fold state past v1's single-state lag
	// accumulator). The handler runs at the buffered post-host-finalize
	// exit via ApplyOverlaysSeries. Forward-compat: future work may lift
	// the rolling-mean ring into a streaming-aware shape (the streaming
	// orchestrator's per-group accumulator carries the ring inline
	// alongside the per-group online aggregator); when that lands the flag
	// flips to true.
	OverlayKindIndexVsRollingMean: false,
	// OVERLAY_INDEX_VS_SIBLING is buffered — the SERIES sibling-reference
	// family resolves its comparison anchor via a (Field, Value) lookup
	// against the materialised per-group accumulators, which the streaming
	// Process pass cannot satisfy until finalize time. The handler runs at
	// the post-host-finalize exit (ApplyOverlaysSeries route) once the
	// host series is fully materialised. Mirrors OverlayKindDeltaVsSibling.
	// Forward-compat: lifting the sibling resolver into a
	// streaming-aware shape (carrying the resolved sibling key inline as a
	// kind-specific accumulator) will flip the streamability flag to true
	// for both kinds.
	OverlayKindIndexVsSibling: false,
	// OVERLAY_INDEX_VS_STAGE is buffered — first whole-chain kind,
	// first consumer of the StageRef discriminated reference family.
	// Whole-chain overlays run AFTER every stage has finalised by
	// construction: `service.ProcessChain` invokes `applyChainOverlays`
	// at the post-stage-loop barrier with already-materialised
	// `*Response` objects for each `Stages[i]`. There is no streamable
	// arm for the whole-chain kind family — the streamability flag
	// stays `false` for every whole-chain kind regardless of host
	// shape.
	OverlayKindIndexVsStage: false,
	// OVERLAY_INDEX_VS_TOTAL is streamable — first SERIES-host overlay
	// in the catalog with a streaming finalize hook. The grand-total
	// accumulator is one f64 carried alongside the per-group
	// accumulators inside the streaming Process fold (no second pass
	// over records); the post-host finalize divides each group value by
	// the running grand total. Per kind-catalog-v1
	// "Streaming-capable subset".
	OverlayKindIndexVsTotal: true,
	// OVERLAY_KS_VS_POP is inherently buffered — fourth FACET-host
	// kind and second inferential FACET-host kind (sibling to
	// OVERLAY_CHISQ_VS_POP). Per PRD §2 Non-Goals ("Streaming overlay path
	// for inferential kinds"), every inferential overlay stays buffered
	// regardless of host streamability. KS requires two empirical CDFs —
	// the handler reconstructs them post-finalize from the host and
	// population's already-folded numeric summary state (histogram
	// preferred, percentile-map fallback). The streamable subset is
	// reserved for descriptive kinds; sibling descriptive FACET-host kinds
	// are OVERLAY_INDEX_VS_POP and OVERLAY_ZSCORE_VS_POP.
	// KS is NUMERIC-arm only — categorical hosts fire
	// PULSE_OVERLAY_SCOPE_UNSUPPORTED at runtime (per-kind validator lives
	// in descriptor/overlay_facet.go). Future work may lift KS into a streaming carrier by
	// retaining a heap or finer histogram per group; v1 stays buffered.
	OverlayKindKSVsPop: false,
	// OVERLAY_PANEL_INDEX_VS_REF is streamable via its per-target SERIES
	// emission — second multi-reference COMPOSE-only kind in the catalog
	// (sibling to OVERLAY_PROP_Z_PANEL which is inferential and
	// stays buffered). Each emitted OverlayLayer reuses the
	// OVERLAY_INDEX_VS_REF math for one (reference, target[i]) pair, so
	// the per-target SERIES arm is fold-only (single accumulator per
	// group; no peer-cell lookup) and the MATRIX arm rides through the
	// slot barrier identically to its single-target sibling. Mirrors the
	// OVERLAY_INDEX_VS_REF dual-shape streamability convention — this
	// flag describes the kind's INTRINSIC streaming capability via its
	// SERIES handler. The MATRIX route is forced buffered by the slot
	// barrier in service.Compose / service.ComposeParallel (the same
	// chassis precedent every COMPOSE kind uses).
	OverlayKindPanelIndexVsRef: true,
	// The OVERLAY_PAIRWISE_* family is inherently buffered — per-Request
	// intra-matrix axis-pairwise significance tests that fold the host's
	// materialised cells, per-cell components (n / Welford triples), and
	// margin counts. Inferential kinds stay buffered per PRD §2 Non-Goals
	// regardless of host streamability.
	OverlayKindPairwiseProbitT:   false,
	OverlayKindPairwisePropZ:     false,
	OverlayKindPairwiseTwoMeansZ: false,
	OverlayKindPairwiseWelchT:    false,
	// OVERLAY_PROP_Z_CELL is inherently buffered — COMPOSE-only kind,
	// per-cell two-proportion z-test against the reference slot's
	// matching cell. Reuses the standardNormalCDF helper backing
	// TEST_PROP_Z. Inferential kind — per PRD §2 Non-Goals every
	// inferential overlay stays buffered regardless of host streamability.
	OverlayKindPropZCell: false,
	// OVERLAY_PROP_Z_PANEL is inherently buffered — COMPOSE-only kind,
	// multi-reference per-cell pairwise two-proportion z-test
	// across the panel's reference + N target slots. Emits a per-cell
	// flattened upper-triangular slice of pairwise p-values via
	// MatrixCell.Value carrying a []float64. Reuses the same
	// twoProportionZ + standardNormalCDF helpers backing OVERLAY_PROP_Z_CELL
	// and TEST_PROP_Z so every pairwise p-value matches the
	// corresponding single-pair output byte-for-byte. Per PRD §2
	// Non-Goals every inferential overlay stays buffered regardless of
	// host streamability.
	OverlayKindPropZPanel: false,
	// OVERLAY_RANK is inherently buffered — COMPOSE-only kind,
	// per-cell rank of each target cell within a configurable population
	// (`Params["population"]` = "row" / "column" / "matrix"). The
	// COMPOSE host is buffered by the slot barrier; ranking within the
	// target matrix requires the full materialised matrix anyway.
	OverlayKindRank:       false,
	OverlayKindShareOfCol: false,
	OverlayKindShareOfRow: false,
	// OVERLAY_SHARE_OF_TOTAL is streamable via its SERIES-host dispatch
	// — sibling kind to OVERLAY_INDEX_VS_TOTAL, same grand-total
	// accumulator (computeSeriesGrandTotal in
	// processing/overlay_series.go), different scaling (raw share, no
	// ×100). The MATRIX-host dispatch is not an in-pass computation:
	// the crosstab fold runs on the already-finalised matrix, on the
	// buffered and the fused arm alike (CanFuseCrosstab does NOT
	// exclude Request.Overlays). This flag therefore describes the
	// kind's INTRINSIC streaming capability — the streamable SERIES
	// handler exists — and says nothing about which crosstab arm the
	// host request lands on; that is the host gate's decision.
	OverlayKindShareOfTotal: true,
	// OVERLAY_T_CELL is inherently buffered — COMPOSE-only kind,
	// per-cell Welch t-test against the reference slot's matching cell.
	// Reuses the studentTTwoSidedP helper backing TEST_T. Inferential
	// kind — per PRD §2 Non-Goals every inferential overlay stays
	// buffered regardless of host streamability.
	OverlayKindTCell: false,
	// OVERLAY_T_VS_REF is inherently buffered — COMPOSE-only kind,
	// per-group Welch t-test against the reference slot's
	// matching group, against a SERIES host. Series-shape sibling of
	// OVERLAY_T_CELL; reuses the same studentTTwoSidedP helper backing
	// TEST_T. Inferential kind — per PRD §2 Non-Goals every inferential
	// overlay stays buffered regardless of host streamability. Distinct
	// from the streamable SERIES-arm of OVERLAY_INDEX_VS_REF /
	// OVERLAY_DELTA_VS_REF (which are descriptive fold-only kinds);
	// inferential kinds carry the family-buffered policy.
	OverlayKindTVsRef: false,
	// OVERLAY_YOY is buffered — the per-frequency prior-period lookup
	// requires the materialised host series. Coarse-frequency arms
	// (annual / quarterly / monthly / weekly) index into an arbitrary
	// prior ordinal `i - <stride>`; fine-frequency arms (daily / hourly)
	// walk an exact-key index built from the full host key list. Neither
	// shape rides inside the streaming Process fold today (the streaming
	// pass cannot resolve a backward lookup against per-group accumulators
	// before finalize). The handler runs at the buffered post-host-
	// finalize exit via ApplyOverlaysSeries (mirrors the
	// INDEX_VS_BASELINE / INDEX_VS_ROLLING_MEAN buffered rationale).
	// Forward-compat: future work may lift the per-frequency lookup
	// into a streaming-aware shape when the streaming-Process
	// orchestrator carries an exact-key host index inline.
	OverlayKindYoY:            false,
	OverlayKindZScoreVsMargin: false,
	// OVERLAY_ZSCORE_VS_POP is streamable — sibling FACET-host kind to
	// OVERLAY_INDEX_VS_POP. Pairs with INDEX_VS_POP as the two
	// streamable Facet overlay kinds — the viz developer requesting both
	// together gets two parallel series layers from a single Facet pass.
	// The handler runs as a post-finalize fold over the host's already-
	// materialised per-value distribution + the resolver's already-
	// materialised population view (sd_pop comes from the Welford
	// accumulator FacetSchema already folded for the population cohort).
	// The host-side streaming Facet pass keeps its existing online
	// accumulators (Welford trio / per-value count map); the overlay does
	// NOT widen the streaming carrier — it consumes finalized host state
	// only. The streamable guarantee is that running this handler against
	// a streaming Facet host vs a buffered one produces byte-identical
	// SeriesPayload output because the input state is identical. Per
	// kind-catalog-v1 "Streaming-capable subset", OVERLAY_ZSCORE_VS_POP
	// is listed as YES.
	OverlayKindZScoreVsPop: true,
	// OVERLAY_ZSCORE_VS_ROLLING is buffered — sibling windowed-rolling kind
	// to OVERLAY_INDEX_VS_ROLLING_MEAN. Both kinds share the per-
	// group ring buffer + Welford (count, mean, M2) trio carrier; the
	// ring is W f64s and grows the streaming-fold state past v1's single-
	// state lag accumulator the same way the sibling kind does. The
	// handler runs at the buffered post-host-finalize exit via
	// ApplyOverlaysSeries. Forward-compat: when the sibling
	// OVERLAY_INDEX_VS_ROLLING_MEAN flips streamable (future work
	// lifts the rolling carrier into the streaming-Process orchestrator's
	// per-group accumulator surface), this kind flips with it — the two
	// kinds share the same carrier and the streaming finalize is one
	// divide step per group for each variant.
	OverlayKindZScoreVsRolling: false,
	// OVERLAY_ZSCORE_VS_TOTAL is streamable — streamable SERIES-host
	// overlay in the grouped-Process subset (sibling to
	// OVERLAY_INDEX_VS_TOTAL and the SERIES dispatch of
	// OVERLAY_SHARE_OF_TOTAL). The Welford accumulator (count + mean +
	// M2) is three f64s carried alongside the per-group accumulators
	// inside the streaming Process fold; no second pass over records.
	// The post-host finalize emits `(group_val - mean) / sd` per group
	// using the population SD (`sqrt(M2 / N)`) — matches the same
	// numerical convention (single-pass Welford-Pébaÿ) used by the
	// parallel buffered Process path and the crosstab ZSCORE_VS_MARGIN
	// handler so cross-mode equivalence tests stay byte-equal within
	// ULP.
	OverlayKindZScoreVsTotal: true,
	// OVERLAY_Z_CELL is inherently buffered — COMPOSE-only kind,
	// per-cell two-sample z-test on the means against the reference slot's
	// matching cell. Reuses the standardNormalCDF helper backing
	// TEST_Z_TWO_SAMPLE. Inferential kind — per PRD §2 Non-Goals every
	// inferential overlay stays buffered regardless of host streamability.
	OverlayKindZCell: false,
	// OVERLAY_Z_VS_REF is inherently buffered — COMPOSE-only kind,
	// per-group two-sample z-test on the means against the
	// reference slot's matching group, against a SERIES host. Series-
	// shape sibling of OVERLAY_Z_CELL; reuses the same standardNormalCDF
	// helper backing TEST_Z_TWO_SAMPLE. Inferential kind — per PRD §2
	// Non-Goals every inferential overlay stays buffered regardless of
	// host streamability. Distinct from the streamable SERIES-arm of
	// OVERLAY_INDEX_VS_REF / OVERLAY_DELTA_VS_REF (which are descriptive
	// fold-only kinds); inferential kinds carry the family-buffered
	// policy.
	OverlayKindZVsRef: false,
}

// OverlayStreamable reports whether the given overlay kind streams and
// whether it is a known entry in OverlayStreamability. An unknown kind
// returns (false, false) — callers that want to treat "unknown" as
// "buffered" can ignore the known bit; callers wiring the validator or
// the manifest capability block can branch on `known` to surface a
// PULSE_OVERLAY_UNKNOWN-style diagnostic.
//
// Source of truth for predict.Streamable on overlay-bearing requests;
// cross-checked at test time by TestStreamability_OverlaysKnown which
// enumerates AllOverlayKinds() and demands every kind has a row here.
func OverlayStreamable(kind OverlayKind) (streamable, known bool) {
	streamable, known = OverlayStreamability[kind]
	return streamable, known
}
