package types

import "encoding/json"

// Overlay system — universal foundational types.
//
// The overlay layer is an additive, request-driven family of derived
// computations that decorate a primary result (today: crosstab matrices;
// future: regressions, time series, group results) with one or more
// secondary projections — index-vs-margin scores, sibling comparisons,
// baseline lifts, population deltas, etc. Every overlay shares one
// declarative surface (OverlaySpec) and one structured response surface
// (OverlayLayer). Downstream renderers can lay an overlay on top of a
// base result without re-deriving the projection.
//
// File scope (E1-S1):
//   - Universal kind/shape/scope enums + the OverlayRef discriminated
//     union (E1-S1).
//   - Request-side OverlaySpec (E1-S1) and response-side OverlayLayer
//     wrapper (E1-S1).
//   - OverlayPayload scalar/series/matrix union + minimal SeriesPayload
//     placeholder (E1-S1; SeriesPayload may grow as future families
//     surface time-series overlays).
//
// Subsequent stories layer descriptor validation (E1-S2), processing
// dispatch + INDEX_VS_MARGIN math (E1-S3), MCP schema bindings (E1-S7),
// canonical-hash extension (E1-S8), and the remaining overlay families
// (subsequent epics). No execution logic ships in this file.

// OverlayKind identifies one entry in the overlay catalog. On the wire
// every kind is SCREAMING_SNAKE and prefixed `OVERLAY_`; the exported
// Go identifier uses mixed case.
type OverlayKind string

const (
	// OverlayKindChiSqMatrix emits a whole-matrix χ² independence test
	// across the row × column contingency table built from the host
	// crosstab cells. MATRIX scope over a MATRIX (crosstab) host with
	// SCALAR payload — the layer carries a single chi-square statistic
	// plus its degrees of freedom and the corresponding p-value, all
	// surfaced through OverlaySummary (Statistic / PValue / Parameters
	// {"df"}). First inferential overlay kind and first SCALAR-shape
	// Crosstab overlay; establishes the SCALAR payload plumbing pattern
	// the remaining E2 inferential kinds and the E5 post-test family
	// reuse.
	//
	// Math:
	//
	//	expected[r,c] = row_margin[r] * col_margin[c] / grand_total
	//	chisq         = Σ_{r,c} (observed[r,c] - expected[r,c])² / expected[r,c]
	//	df            = (rows - 1) * (cols - 1)
	//	p_value       = 1 - chi2_cdf(chisq, df)
	//
	// Implementation reuses the χ² survival helper that backs TEST_CHISQ
	// (processing/test_stat.go chiSquareSurvival) so the overlay and the
	// row-test surface produce identical p-values for the same
	// contingency.
	//
	// Absent-cell policy: a structurally absent host cell (Present=false)
	// is treated as an observed count of 0 — the matrix shape stays
	// rectangular, the row / column / grand margins continue to drive
	// the expected-count recurrence, and an absent observation does not
	// invent a count. The handler documents the policy alongside the
	// runtime dispatch.
	//
	// Low-expected-count warning: when any expected[r,c] < 5 the handler
	// emits a single PULSE_OVERLAY_EXPECTED_LOW warning (canonical χ²
	// low-count heuristic; mirrors PULSE_TEST_EXPECTED_COUNT_TOO_LOW on
	// the TEST_CHISQ surface). The canonical
	// errors.PULSE_OVERLAY_EXPECTED_LOW constant is the source of truth
	// (promoted from a stub string in E2-S10).
	//
	// Scope is MATRIX (not CELL) because the test is whole-table; the
	// validator rejects any other scope. The Ref union is left empty —
	// the test is implicit-margin (uses the host's row / column / grand
	// margins inline), so callers supplying a Ref.Margin (or any other
	// ref-family pointer) get PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
	//
	// Inherently buffered because the host crosstab path is always
	// buffered (margins recomputed from raw rows).
	OverlayKindChiSqMatrix OverlayKind = "OVERLAY_CHISQ_MATRIX"

	// OverlayKindChiSqRow emits a per-row χ² goodness-of-fit test
	// across the host crosstab's row × column contingency table. ROW
	// scope over a MATRIX (crosstab) host with SERIES payload — one
	// per-row entry carrying the row's χ² statistic, degrees of
	// freedom (cols - 1), and p-value via OverlaySummary
	// {Statistic, PValue, Parameters{"df"}}. First SERIES-shape
	// Crosstab overlay; establishes the SeriesPayload entries
	// plumbing pattern that the remaining E2 / E3 series families
	// reuse.
	//
	// Math (per row r):
	//
	//	observed[c] = host.Cell(r, c)
	//	expected[c] = row_margin[r] * col_margin[c] / grand_total
	//	chisq_r    = Σ_c (observed[c] - expected[c])² / expected[c]
	//	df          = cols - 1
	//	p_value     = chi2_survival(chisq_r, df)   // = 1 - chi2_cdf
	//
	// Tests whether each row's observed column distribution differs
	// from the expected distribution derived from column margins under
	// independence. The χ² survival helper (chiSquareSurvival) is the
	// same helper that backs TEST_CHISQ and the CHISQ_MATRIX overlay —
	// overlay surfaces produce identical p-values for the same
	// contingency.
	//
	// Absent-cell policy: a structurally absent host cell (Present=
	// false) is treated as an observed count of 0 (matches the
	// CHISQ_MATRIX policy). The per-row recurrence still consumes
	// every column slot; an absent observation does not collapse the
	// column count.
	//
	// Low-expected-count warning: when any expected[c] < 5 in row r
	// the handler emits ONE PULSE_OVERLAY_EXPECTED_LOW warning per
	// offending row (not per cell — the row is the diagnostic unit
	// for goodness-of-fit). Canonical errors.PULSE_OVERLAY_EXPECTED_LOW
	// constant (promoted from a stub string in E2-S10).
	//
	// Scope is ROW (not CELL or MATRIX) because each row's test is
	// independent — the validator rejects any other scope. The Ref
	// union is left empty — the test is implicit-margin (uses the
	// host's row / column / grand margins inline), so callers
	// supplying a Ref.Margin (or any other ref-family pointer) get
	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
	//
	// Inherently buffered because the host crosstab path is always
	// buffered (margins recomputed from raw rows).
	OverlayKindChiSqRow OverlayKind = "OVERLAY_CHISQ_ROW"

	// OverlayKindChiSqCol emits a per-column χ² goodness-of-fit test
	// across the host crosstab's row × column contingency table. COLUMN
	// scope over a MATRIX (crosstab) host with SERIES payload — one
	// per-column entry carrying the column's χ² statistic, degrees of
	// freedom (rows - 1), and p-value via OverlaySummary
	// {Statistic, PValue, Parameters{"df"}}. Mechanical column-axis twin
	// of OVERLAY_CHISQ_ROW; mirrors the SeriesPayload entries plumbing
	// pattern (Entries[i].Key == host ColumnKeys[i] element-for-element).
	//
	// Math (per column c):
	//
	//	observed[r] = host.Cell(r, c)
	//	expected[r] = row_margin[r] * col_margin[c] / grand_total
	//	chisq_c    = Σ_r (observed[r] - expected[r])² / expected[r]
	//	df          = rows - 1
	//	p_value     = chi2_survival(chisq_c, df)   // = 1 - chi2_cdf
	//
	// Tests whether each column's observed row distribution differs
	// from the expected distribution derived from row margins under
	// independence. The χ² survival helper (chiSquareSurvival) is the
	// same helper that backs TEST_CHISQ and the CHISQ_MATRIX / CHISQ_ROW
	// overlays — overlay surfaces produce identical p-values for the
	// same contingency.
	//
	// Absent-cell policy: a structurally absent host cell (Present=
	// false) is treated as an observed count of 0 (matches the
	// CHISQ_MATRIX / CHISQ_ROW policy). The per-column recurrence still
	// consumes every row slot; an absent observation does not collapse
	// the row count.
	//
	// Low-expected-count warning: when any expected[r] in column c is
	// below 5 the handler emits ONE PULSE_OVERLAY_EXPECTED_LOW warning
	// per offending column (not per cell — the column is the diagnostic
	// unit for goodness-of-fit). Canonical
	// errors.PULSE_OVERLAY_EXPECTED_LOW constant (promoted from a stub
	// string in E2-S10).
	//
	// Scope is COLUMN (not CELL, ROW, or MATRIX) because each column's
	// test is independent — the validator rejects any other scope. The
	// Ref union is left empty — the test is implicit-margin (uses the
	// host's row / column / grand margins inline), so callers supplying
	// a Ref.Margin (or any other ref-family pointer) get
	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
	//
	// Inherently buffered because the host crosstab path is always
	// buffered (margins recomputed from raw rows).
	OverlayKindChiSqCol OverlayKind = "OVERLAY_CHISQ_COL"

	// OverlayKindDeltaVsMargin emits a per-cell additive delta against
	// the matching axis margin: cell - margin. CELL-scoped over a MATRIX
	// (crosstab) host. Unlike INDEX_VS_MARGIN (a ratio) and the SHARE_OF_*
	// triad (each a ratio scaled to 1.0), DELTA_VS_MARGIN preserves the
	// host cell's units — a $-valued AGG_SUM cell minus a $-valued row
	// margin yields a $-valued deviation in the same currency. There is
	// no division and no Welford recurrence, so the handler never
	// surfaces PULSE_OVERLAY_REF_ZERO. Supports all three axes (row /
	// column / grand) — callers pick the axis explicitly via
	// Ref.Margin.Axis and the handler dispatches the matching margin
	// slot. Inherently buffered because the host crosstab path is always
	// buffered (margins recomputed from raw rows).
	OverlayKindDeltaVsMargin OverlayKind = "OVERLAY_DELTA_VS_MARGIN"

	// OverlayKindFisherExactCell emits a per-cell Fisher's exact two-
	// sided p-value computed against a 2×2 contingency table formed from
	// the host crosstab cell, its row margin, its column margin, and the
	// grand total. CELL-scoped over a MATRIX (crosstab) host. Closes the
	// E2 inferential overlay catalog as the canonical low-count χ²
	// backstop — when any expected count in the 2×2 falls below 5 the
	// χ² approximation becomes unreliable and Fisher's exact is the
	// correct surface to compute the p-value.
	//
	// Per-cell 2×2 contingency (for cell at (rowIdx, colIdx)):
	//
	//	            col=c              col≠c
	//	  row=r     cell               row_margin - cell
	//	  row≠r    col_margin - cell   grand - row_margin - col_margin + cell
	//
	// All four cells of the 2×2 are non-negative because every margin is
	// recomputed by the buffered crosstab orchestrator from the same
	// filter-passing row set the cells were built from — the row total
	// dominates each individual row's cell, the column total dominates
	// each individual column's cell, and the grand total equals the sum
	// of row totals (= sum of column totals).
	//
	// Math: Fisher's exact two-sided p-value sums hypergeometric
	// probabilities P(X = x | row_margin, col_margin, grand_total) for
	// every feasible x in the marginal-constrained range whose log-
	// probability is at most logPObs (the observed table's log-prob).
	// Reuses logHypergeometric (processing/test_fisher.go) so the overlay
	// surface produces identical p-values to TEST_FISHER_EXACT for the
	// same 2×2.
	//
	// Output shape: MATRIX payload mirroring the host's RowKeys /
	// ColumnKeys / headers so renderers can lay the overlay on top of
	// the base matrix with the same header machinery as INDEX_VS_MARGIN.
	// Each present host cell becomes a MatrixCell whose Value is the
	// two-sided p-value as a float64. Missing host cells stay absent on
	// the overlay; cells with a missing row or column margin become
	// absent overlay cells (defense in depth — the buffered crosstab
	// orchestrator already populates margins before the overlay fold,
	// so this branch should not fire in practice).
	//
	// Absent-cell policy: a structurally absent host cell (Present=
	// false) stays absent on the overlay (mirrors the SHARE_OF_* triad
	// and INDEX_VS_MARGIN policy — an absent observation does not
	// invent a 2×2).
	//
	// Ref handling: implicit-margin (empty Ref accepted). Row + column
	// margins are resolved from the buffered crosstab host view's
	// MarginFor(Row/Col, ...) resolver. Explicit Ref-family pointers
	// (Margin / Sibling / BaselineIndex / Population / Stage / Slot)
	// fire PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time.
	// Mirrors the CHISQ_MATRIX / CHISQ_ROW / CHISQ_COL ref policy.
	//
	// Low expected-cell warning (Cochran rule): when ANY of the four
	// 2×2 expected counts {row_margin × col_margin / grand,
	// row_margin × (grand - col_margin) / grand,
	// (grand - row_margin) × col_margin / grand,
	// (grand - row_margin) × (grand - col_margin) / grand} is below 1,
	// OR when at least 20% of the four expected counts are below 5,
	// the handler emits one PULSE_OVERLAY_EXPECTED_LOW warning per
	// offending cell. Canonical errors.PULSE_OVERLAY_EXPECTED_LOW
	// constant (promoted from a stub string in E2-S10). The warning is
	// advisory — Fisher's exact stays exact in the low-count regime and
	// the p-value is still emitted alongside.
	// The threshold runs on the OVERLAY itself (not the underlying χ²
	// surface) because Fisher's exact is the SOLUTION to the low-
	// expected-count problem — the warning's intent here is to flag the
	// CELLS where Fisher's exact (rather than the cheaper χ²
	// approximation) is structurally required.
	//
	// Degenerate inputs:
	//   - grand_total <= 0: layer emits absent cells everywhere
	//     (no 2×2 can be formed); single PULSE_OVERLAY_REF_ZERO warning
	//     surfaces the degenerate host.
	//   - row_margin <= 0 OR col_margin <= 0 OR row_margin >= grand
	//     OR col_margin >= grand for some cell: the 2×2 collapses to a
	//     degenerate shape (one of the four cells must be zero); the
	//     handler still emits a p-value of 1.0 (no rejection possible
	//     under the fully-degenerate hypergeometric) without a warning.
	//
	// Inherently buffered because the host crosstab path is always
	// buffered (margins recomputed from raw rows). PRD § 4.C FR-C2
	// calls out OVERLAY_FISHER_EXACT_CELL as the canonical low-count
	// contingency overlay closing the E2 inferential family.
	OverlayKindFisherExactCell OverlayKind = "OVERLAY_FISHER_EXACT_CELL"

	// OverlayKindDeltaVsBaseline emits a per-point additive delta against a
	// single fixed positional baseline of an ordered SERIES (grouped
	// Process) host: `delta_i = point_value_i - baseline_value` where
	// `baseline_value = host.ValueAt(Ref.BaselineIndex.Position)`. GROUP
	// scope over a SERIES host with SERIES payload — one `SeriesEntry` per
	// host group key in host order, each carrying the delta on
	// `Summary.Statistic`. Absolute-difference sibling of
	// `OVERLAY_INDEX_VS_BASELINE` (E4-S2) and third windowed-Process kind
	// in the catalog (E4-S3). Like its sibling it consumes the
	// `Ref.BaselineIndex.Position` arm of the OverlayBaselineIndexRef
	// discriminated union landed at E4-S1.
	//
	// Math (per host group ordinal `i`):
	//
	//	baseline_value = host.ValueAt(Ref.BaselineIndex.Position)
	//	if present[i]  ⇒ point - baseline
	//	if !present[i] ⇒ absent passthrough (no Statistic)
	//
	// Baseline resolution: the handler resolves the baseline value ONCE
	// up front via `processing.ResolveBaselineIndex(host,
	// Ref.BaselineIndex)` (the E4-S1 foundation helper). Negative or
	// out-of-range `Position` values fail at predict time
	// (`descriptor.validateOverlayBaselineIndexPredict`) and at runtime
	// (`processing.ResolveBaselineIndex`) with
	// `PULSE_OVERLAY_REF_UNKNOWN` carrying `{baseline_index,
	// series_length}` Details. The handler propagates the resolver's
	// CodedError verbatim — the runtime resolver's coded shape IS the
	// kind's runtime range-check surface (same contract as the
	// `OVERLAY_INDEX_VS_BASELINE` twin).
	//
	// Baseline-at-self semantics: when the host's first present point IS
	// at `Position` (typical "anchor against first point" authoring), that
	// ordinal's delta is exactly `0.0` (self-vs-self under additive
	// subtraction). Renderers centre diverging colour ramps on
	// `baseline = 0` (mirrors `OVERLAY_DELTA_VS_MARGIN` /
	// `OVERLAY_DELTA_VS_SIBLING` / `OVERLAY_ZSCORE_VS_*`).
	//
	// Zero-baseline semantics: unlike the `OVERLAY_INDEX_VS_BASELINE` twin
	// (which divides by the baseline and rejects zero with
	// `PULSE_OVERLAY_REF_ZERO`), DELTA_VS_BASELINE performs subtraction
	// and is mathematically defined for every finite baseline value
	// including zero. The handler does NOT emit
	// `PULSE_OVERLAY_REF_ZERO`; a zero baseline simply yields
	// `delta_i = point_value_i - 0 = point_value_i` (the raw host value
	// passes through). Mirrors the existing `OVERLAY_DELTA_VS_SIBLING`
	// rule against zero sibling values.
	//
	// Absent-point policy: a host that did not produce a value for group
	// `i` (the resolver returns `(0, false)`) surfaces a `SeriesEntry`
	// whose `Summary` leaves `Statistic` unset — the canonical "present
	// slot, empty summary" shape from the E3-S1 SERIES dispatch contract.
	// Absent groups do NOT participate in the delta computation. The
	// baseline ordinal itself MAY be absent — `ResolveBaselineIndex` calls
	// `host.ValueAt` and the host's own resolver decides whether the
	// requested ordinal surfaces a present value or reports absent. An
	// absent baseline (`present=false` at the baseline ordinal) yields a
	// baseline value of `0.0` from the host; subsequent points subtract
	// that zero and the layer carries the raw host values verbatim (no
	// warning — distinct from the `OVERLAY_INDEX_VS_BASELINE` zero-
	// baseline `PULSE_OVERLAY_REF_ZERO` arm).
	//
	// Ref handling: `Ref.BaselineIndex` (with `Position >= 0`) is
	// REQUIRED. Empty `Ref` is rejected at predict time
	// (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`). Any other ref-family
	// pointer (`Margin` / `Sibling` / `Prior` / `Population` / `Stage` /
	// `Slot`) is rejected with the same code.
	//
	// Scope must be GROUP. `Level` / `Within` MUST be zero (the baseline
	// is a single fixed positional anchor, not an axis prefix; non-zero
	// values fire `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` mirroring the
	// `INDEX_VS_BASELINE` / `INDEX_VS_TOTAL` / `INDEX_VS_PRIOR`
	// implicit-margin / windowed family rule).
	//
	// Buffered. Per `types/overlay_streamability.go`, the streamability
	// row is `false` — resolving a single positional baseline requires
	// the materialised host series (`host.ValueAt(Position)` reads after
	// finalize). The handler runs at the buffered post-host-finalize exit
	// via `ApplyOverlaysSeries`. Forward-compat: a future story may lift
	// the baseline resolver into a streaming-aware shape; when that lands
	// the streamability flag flips to true.
	OverlayKindDeltaVsBaseline OverlayKind = "OVERLAY_DELTA_VS_BASELINE"

	// OverlayKindDeltaVsSibling emits a per-group additive delta against a
	// sibling group named in `Ref.Sibling`. GROUP scope over a SERIES
	// (grouped Process) host with SERIES payload — one SeriesEntry per
	// host group key in host order, each carrying `group_val - sibling_val`
	// on `Summary.Statistic`. The sibling is identified by `(Field, Value)`
	// — `Field` names a grouper Field present on the SERIES host, `Value`
	// names the specific axis-key value to compare against. The sibling
	// reference resolves to a single host group via the sibling resolver
	// (`processing/overlay_sibling_resolver.go`); every present group
	// emits a delta against that fixed reference point. The sibling
	// group itself emits `0` (self-vs-self under additive subtraction).
	//
	// Buffered (per kind-catalog-v1 "Streaming-capable subset"): sibling
	// resolution requires the full materialised SeriesPayload — the
	// streaming Process pass cannot resolve a `(Field, Value)` lookup
	// against the per-group accumulators until they are finalised, so the
	// handler runs at the buffered post-host-finalize exit. The
	// `OverlayStreamability[OverlayKindDeltaVsSibling]` row is `false`
	// (mirrors `OverlayKindIndexVsSibling`).
	//
	// Math (per host group i):
	//
	//	sibling_val = host[Ref.Sibling.Field, Ref.Sibling.Value]   // resolver lookup
	//	delta_i     = group_val[i] - sibling_val
	//
	// Absent-group policy: a host that did not produce a value for group
	// i (resolver returns `(0, false)`) surfaces a SeriesEntry whose
	// Summary leaves Statistic unset — canonical "present slot, empty
	// summary" shape from the E3-S1 SERIES dispatch contract. Absent
	// groups do NOT participate in the delta computation (and DO NOT
	// surface zero — they carry no Statistic).
	//
	// Unknown sibling path: when `Ref.Sibling.Field` is not a grouper
	// field on the host OR `Ref.Sibling.Value` does not match any
	// observed axis-key value, the handler emits ONE
	// PULSE_OVERLAY_REF_UNKNOWN warning carrying the offending
	// `(field, value)` pair and surfaces NaN statistics across every
	// present entry. Mirrors the INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES
	// PULSE_OVERLAY_REF_ZERO emission shape (one warning per layer, not
	// per cell). DELTA is mathematically defined even when sibling is
	// resolved AND sibling_val is zero — the delta is simply
	// `group_val[i] - 0 = group_val[i]`, so the zero-sibling-value case
	// does NOT raise PULSE_OVERLAY_REF_ZERO on this kind (unlike the
	// INDEX_VS_SIBLING twin which divides by sibling and rejects zero).
	//
	// Ref handling: `Ref.Sibling` is REQUIRED — both `Field` and `Value`
	// must be non-empty strings. Any other ref-family pointer (Margin /
	// BaselineIndex / Population / Stage / Slot) fails
	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time.
	//
	// Scope must be GROUP. The validator rejects any other scope.
	OverlayKindDeltaVsSibling OverlayKind = "OVERLAY_DELTA_VS_SIBLING"

	// OverlayKindIndexVsBaseline emits a per-point ratio index against a
	// single fixed positional baseline of an ordered SERIES (grouped
	// Process) host: `index_i = point_value_i / baseline_value * 100`
	// where `baseline_value` is the host's metric at the ordinal pinned
	// by `Ref.BaselineIndex.Position`. GROUP scope over a SERIES host with
	// SERIES payload — one `SeriesEntry` per host group key in host order,
	// each carrying the index on `Summary.Statistic`. Second windowed-
	// Process overlay in the catalog (E4-S2; the first windowed kind was
	// `OVERLAY_INDEX_VS_PRIOR` / E4-S4) and the first kind to consume the
	// `Ref.BaselineIndex.Position` arm of the OverlayBaselineIndexRef
	// discriminated union landed at E4-S1.
	//
	// Math (per host group ordinal `i`):
	//
	//	baseline_value = host.ValueAt(Ref.BaselineIndex.Position)
	//	if baseline_value == 0 ⇒ NaN + warning  (PULSE_OVERLAY_REF_ZERO)
	//	if present[i]          ⇒ point / baseline * 100
	//	if !present[i]         ⇒ absent passthrough (no Statistic)
	//
	// Baseline resolution: the handler resolves the baseline value ONCE
	// up front via `processing.ResolveBaselineIndex(host,
	// Ref.BaselineIndex)` (the E4-S1 foundation helper). Negative or
	// out-of-range `Position` values fail at predict time
	// (`descriptor.validateOverlayBaselineIndexPredict`) and at runtime
	// (`processing.ResolveBaselineIndex`) with
	// `PULSE_OVERLAY_REF_UNKNOWN` carrying `{baseline_index,
	// series_length}` Details. The handler propagates the resolver's
	// CodedError verbatim — the runtime resolver's coded shape IS the
	// kind's runtime range-check surface.
	//
	// Baseline-at-self semantics: when the host's first present point is
	// at `Position` (typical "anchor against first point" authoring), that
	// ordinal's index is exactly `100.0` (self-vs-self under the ratio
	// scaling). Renderers centre diverging colour ramps on `baseline = 100`
	// (mirrors `OVERLAY_INDEX_VS_MARGIN` / `OVERLAY_INDEX_VS_TOTAL` /
	// `OVERLAY_INDEX_VS_SIBLING` / `OVERLAY_INDEX_VS_PRIOR`).
	//
	// Zero-baseline path: when the resolved baseline value is `0` the
	// handler emits ONE `PULSE_OVERLAY_REF_ZERO` warning carrying the
	// overlay kind, the host group count, and the baseline ordinal; every
	// emitted entry's `Summary.Statistic` is NaN. Division by zero is
	// mathematically undefined and the same `PULSE_OVERLAY_REF_ZERO`
	// contract used by the share / index / zscore family applies (one
	// warning per layer, not per cell). Distinct from the
	// `OVERLAY_DELTA_VS_BASELINE` twin (E4-S3 will land it) which performs
	// subtraction and does NOT raise on zero baseline.
	//
	// Absent-point policy: a host that did not produce a value for group
	// `i` (the resolver returns `(0, false)`) surfaces a `SeriesEntry`
	// whose `Summary` leaves `Statistic` unset — the canonical "present
	// slot, empty summary" shape from the E3-S1 SERIES dispatch contract.
	// Absent groups do NOT participate in the index computation. The
	// baseline ordinal itself MAY be absent — `ResolveBaselineIndex` calls
	// `host.ValueAt` and the host's own resolver decides whether the
	// requested ordinal surfaces a present value or reports absent. An
	// absent baseline (`present=false` at the baseline ordinal) yields a
	// baseline value of `0.0` from the host, which then routes through
	// the zero-baseline `PULSE_OVERLAY_REF_ZERO` arm above.
	//
	// Ref handling: `Ref.BaselineIndex` (with `Position >= 0`) is REQUIRED.
	// Empty `Ref` is rejected at predict time
	// (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`). Any other ref-family
	// pointer (`Margin` / `Sibling` / `Prior` / `Population` / `Stage` /
	// `Slot`) is rejected with the same code.
	//
	// Scope must be GROUP. `Level` / `Within` MUST be zero (the baseline
	// is a single fixed positional anchor, not an axis prefix; non-zero
	// values fire `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` mirroring the
	// `INDEX_VS_TOTAL` / `INDEX_VS_PRIOR` implicit-margin / windowed
	// family rule).
	//
	// Buffered. Per `types/overlay_streamability.go`, the streamability
	// row is `false` — resolving a single positional baseline requires
	// the materialised host series (`host.ValueAt(Position)` reads after
	// finalize). The handler runs at the buffered post-host-finalize exit
	// via `ApplyOverlaysSeries`. Forward-compat: a future story may lift
	// the baseline resolver into a streaming-aware shape (carrying the
	// resolved baseline inline as a kind-specific accumulator advanced
	// during the streaming pass once the orchestrator hits the baseline
	// ordinal); when that lands the streamability flag flips to true.
	OverlayKindIndexVsBaseline OverlayKind = "OVERLAY_INDEX_VS_BASELINE"

	// OverlayKindIndexVsMargin produces an index score per cell (or per
	// row/column, depending on Scope) by comparing the cell value against
	// the matching axis margin: 100 * cell / margin. Scope=CELL emits one
	// scalar per cell; Scope=ROW or COLUMN emits one scalar per axis key
	// when the comparison degenerates (e.g. row-share index vs grand
	// total). The default reference (Ref.Margin) names the axis whose
	// margin is the denominator. Inherently buffered because margins are
	// always recomputed from raw rows in the crosstab path.
	OverlayKindIndexVsMargin OverlayKind = "OVERLAY_INDEX_VS_MARGIN"

	// OverlayKindIndexVsPrior emits a per-point windowed index against the
	// immediately preceding point of an ordered SERIES (grouped Process)
	// host: `index_i = point_value_i / prior_value_{i-1} * 100`. GROUP
	// scope over a SERIES host with SERIES payload — one `SeriesEntry`
	// per host group key in host order, each carrying the index on
	// `Summary.Statistic`. First **streamable** windowed-Process overlay
	// in the catalog (E4-S4) and the first kind to use the windowed
	// `Ref.Prior` arm of the `OverlayRef` discriminated union.
	//
	// Math (per host group ordinal `i`):
	//
	//	if i == 0           ⇒ NaN              (no prior available)
	//	if prior_value == 0 ⇒ NaN + warning    (PULSE_OVERLAY_REF_ZERO)
	//	otherwise           ⇒ point / prior * 100
	//
	// Carrier shape (single-state lag): the handler walks the ordered
	// host series once. A single `float64` lag carrier remembers the most
	// recently seen PRESENT value; each subsequent present point divides
	// by the carrier and then advances the carrier to its own value.
	// Absent host points (resolver reports `(0, false)`) emit NaN for
	// that ordinal and DO NOT advance the carrier — the "prior" for the
	// next present point remains the last present value. This single-
	// state lag is what makes the kind streamable: the streaming-Process
	// orchestrator carries one f64 lag value alongside the per-group
	// accumulators inside the streaming fold, and the post-host finalize
	// is the divide step.
	//
	// First-point semantics: the first ordinal has no prior by
	// construction. The handler emits NaN on the first present entry and
	// does NOT raise `PULSE_OVERLAY_REF_ZERO` — this is "no comparison
	// available" rather than "denominator was zero". Renderers should
	// surface the first entry as "no comparison" (typically a blank
	// cell) rather than a degenerate signal.
	//
	// Zero-prior path: when the lag carrier is `0` at the divide step
	// (the previous present point had a value of zero), the handler emits
	// one `PULSE_OVERLAY_REF_ZERO` warning and surfaces NaN on the
	// affected entries. Subsequent points continue to use the same lag
	// carrier (since absent points do not advance the carrier, but a
	// PRESENT zero DOES advance the carrier — and the next point will
	// then again hit the zero-prior path). Mirrors the existing
	// `PULSE_OVERLAY_REF_ZERO` contract used by the share / index /
	// zscore family.
	//
	// Forward-compat lag knob: the `Ref.Prior.Lag` slot is reserved for
	// future window-N priors (e.g. lag-3 for "compare against three
	// points ago"). v1 ships lag-1 only — non-zero `Lag` is not
	// exercised by this kind today; later stories will widen the carrier
	// to a small ring buffer.
	//
	// Ref handling: the windowed `Ref.Prior` arm is the implicit default
	// for this kind. The validator accepts both a populated
	// `Ref.Prior` (with `Lag` zero or unset for v1) AND an entirely empty
	// `Ref` (the omitempty-friendly authoring shape) — both spell "lag-
	// 1 prior". Any other ref-family pointer (`Margin` / `Sibling` /
	// `BaselineIndex` / `Population` / `Stage` / `Slot`) fires
	// `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` at predict time.
	//
	// Scope must be GROUP. `Level` / `Within` MUST be zero (windowed
	// kind — the lag carrier folds across the ordered axis without a
	// prefix-bucket denominator; non-zero values fire
	// `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` mirroring the
	// `INDEX_VS_TOTAL` / χ² / Fisher implicit-margin family).
	//
	// Streamable. Per `types/overlay_streamability.go`, the streamability
	// row is `true` — the single-state lag carrier is one f64 carried
	// alongside the per-group accumulators inside the streaming Process
	// fold, and the divide step happens at host finalize. Renderers
	// centre diverging colour ramps on `baseline = 100` (mirrors
	// `OVERLAY_INDEX_VS_MARGIN` / `OVERLAY_INDEX_VS_TOTAL`).
	OverlayKindIndexVsPrior OverlayKind = "OVERLAY_INDEX_VS_PRIOR"

	// OverlayKindIndexVsRollingMean emits a per-point windowed index against
	// the arithmetic mean of the W immediately preceding points of an ordered
	// SERIES (grouped Process) host: `index_i = point_value_i / mean(point_{i-W} .. point_{i-1}) * 100`.
	// GROUP scope over a SERIES host with SERIES payload — one `SeriesEntry`
	// per host group key in host order, each carrying the index on
	// `Summary.Statistic`. Fourth windowed-Process overlay in the catalog
	// (E4-S5; siblings: `OVERLAY_INDEX_VS_PRIOR` / E4-S4, `OVERLAY_INDEX_VS_BASELINE`
	// / E4-S2, `OVERLAY_DELTA_VS_BASELINE` / E4-S3) and the first kind to
	// consume the `Ref.RollingMean` arm of the OverlayRef discriminated union.
	//
	// Window-via-Params convention: the rolling window width is supplied via
	// `OverlaySpec.Params["window"]` as a positive integer (mirrors the
	// `WIN_*` operator convention; see `skills/window-operations.md` for the
	// pattern). The `Ref.RollingMean` arm is an empty marker struct
	// (`OverlayRollingMeanRef{}`) tagging the ref family — the v1 window
	// value lives entirely on `Params`. Forward-compat: `OverlayRollingMeanRef`
	// may grow non-Window knobs (e.g. weighting modes) without re-opening
	// the parent `OverlayRef`.
	//
	// Math (per host group ordinal `i`):
	//
	//	W = Params["window"] (positive int)
	//	if fewer than W present prior points have been seen ⇒ NaN (no warning)
	//	if mean(prior W points) == 0                       ⇒ NaN + PULSE_OVERLAY_REF_ZERO
	//	otherwise                                          ⇒ point / mean * 100
	//
	// Carrier shape (rolling window, buffered): the handler maintains a ring
	// buffer of the W most recently observed PRESENT values. For E4-S5 the
	// stored shape is the (count, mean, M2) Welford triple — only the mean
	// is read by INDEX_VS_ROLLING_MEAN, but the M2 slot is reserved so the
	// E4-S6 `OVERLAY_ZSCORE_VS_ROLLING` handler can lift `sqrt(M2 / (count-1))`
	// from the same carrier (the per-group accumulator cost is +1 f64 per
	// layer per group — trivial relative to the overlay grand-budget; the
	// reuse keeps the windowed family's accumulator footprint flat as more
	// kinds land).
	//
	// Absent-point policy (ring buffer does not advance): an absent host
	// ordinal (resolver reports `(0, false)`) emits a present `SeriesEntry`
	// whose Summary leaves Statistic unset and DOES NOT advance the ring
	// buffer. Mirrors the `OVERLAY_INDEX_VS_PRIOR` absent-point lag carrier
	// rule — the next present ordinal will compare against the most recent
	// W PRESENT values, not absent slots.
	//
	// Window-fill semantics: the first W present ordinals all emit NaN
	// without warning — "window not yet filled" is structurally distinct
	// from "denominator was zero" (mirrors the `OVERLAY_INDEX_VS_PRIOR`
	// first-point NaN rule). When the host series is shorter than W (no
	// ordinal ever sees a full window) every entry's Statistic is NaN with
	// no warning.
	//
	// Zero-rolling-mean path: when the window mean is exactly `0` at the
	// divide step (e.g. every prior point in the window was zero) the
	// handler emits ONE `PULSE_OVERLAY_REF_ZERO` warning and surfaces NaN
	// on the affected entry. Subsequent points may again hit the zero-mean
	// path as the ring rolls; one warning per layer per zero-mean event
	// occurrence. Mirrors the share / index / zscore family's
	// zero-denominator contract.
	//
	// Window param validation: `Params["window"]` MUST be present and
	// MUST be a positive integer (JSON numbers decoded as float64 are
	// accepted when integral). Missing param fires
	// `PULSE_OVERLAY_PARAM_MISSING` carrying `{kind, param}` Details at
	// both predict (`descriptor.validateOverlayIndexVsRollingMean`) and
	// runtime (`processing.applyIndexVsRollingMean`). Window `<= 0` fires
	// `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` carrying `{window}` Details — the
	// LEVEL_OUT_OF_RANGE code is reused for "value out of valid range"
	// semantics so the new code surface stays narrow (mirrors the existing
	// usage on Level / Within slots).
	//
	// Ref handling: `Ref.RollingMean` MUST be populated (empty struct is
	// fine; the marker tags the ref family). Any other ref-family pointer
	// (`Margin` / `Sibling` / `BaselineIndex` / `Prior` / `Population` /
	// `Stage` / `Slot`) fires `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`
	// at predict time.
	//
	// Scope must be GROUP. `Level` / `Within` MUST be zero (windowed kind —
	// the rolling-mean carrier folds across the ordered axis without a
	// prefix-bucket denominator; non-zero values fire
	// `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` mirroring the
	// `INDEX_VS_PRIOR` / `INDEX_VS_BASELINE` / `INDEX_VS_TOTAL`
	// implicit-margin / windowed family).
	//
	// Buffered. Per `types/overlay_streamability.go`, the streamability row
	// is `false` — the ring buffer carries the full window of present values
	// per group, which the streaming Process pass cannot maintain inline
	// with the per-record fold today (the carrier widens beyond a single
	// f64 — `OVERLAY_INDEX_VS_PRIOR`'s streamable lag carrier is one f64;
	// rolling-mean's ring is `W` f64s and grows the streaming-fold state
	// past v1's single-state lag accumulator). Forward-compat: a future
	// story may lift the rolling-mean ring into a streaming-aware shape
	// (the streaming orchestrator's per-group accumulator carries the ring
	// inline alongside the per-group online aggregator); when that lands
	// the streamability flag flips to true.
	OverlayKindIndexVsRollingMean OverlayKind = "OVERLAY_INDEX_VS_ROLLING_MEAN"

	// OverlayKindIndexVsSibling emits a per-group index score against a
	// sibling group named in `Ref.Sibling`: `(group_val / sibling_val) *
	// 100.0` per host group key. GROUP scope over a SERIES (grouped
	// Process) host with SERIES payload — one SeriesEntry per host group
	// key in host order, each carrying the index on `Summary.Statistic`.
	// The sibling is identified by `(Field, Value)` — `Field` names a
	// grouper Field present on the SERIES host, `Value` names the
	// specific axis-key value to compare against. The sibling reference
	// resolves to a single host group via the sibling resolver
	// (`processing/overlay_sibling_resolver.go`); every present group
	// emits an index against that fixed reference point. The sibling
	// group itself emits `100.0` (self-vs-self under the ratio scaling).
	//
	// Buffered (per kind-catalog-v1 "Streaming-capable subset"): sibling
	// resolution requires the full materialised SeriesPayload — the
	// streaming Process pass cannot resolve a `(Field, Value)` lookup
	// against the per-group accumulators until they are finalised, so the
	// handler runs at the buffered post-host-finalize exit. The
	// `OverlayStreamability[OverlayKindIndexVsSibling]` row is `false`
	// (mirrors `OverlayKindDeltaVsSibling`).
	//
	// Math (per host group i):
	//
	//	sibling_val = host[Ref.Sibling.Field, Ref.Sibling.Value]   // resolver lookup
	//	index_i     = (group_val[i] / sibling_val) * 100.0
	//
	// Absent-group policy: a host that did not produce a value for group
	// i (resolver returns `(0, false)`) surfaces a SeriesEntry whose
	// Summary leaves Statistic unset — canonical "present slot, empty
	// summary" shape from the E3-S1 SERIES dispatch contract. Absent
	// groups do NOT participate in the index computation.
	//
	// Unknown sibling path: when `Ref.Sibling.Field` is not a grouper
	// field on the host OR `Ref.Sibling.Value` does not match any
	// observed axis-key value, the handler emits ONE
	// PULSE_OVERLAY_REF_UNKNOWN warning carrying the offending
	// `(field, value)` pair and surfaces NaN statistics across every
	// present entry. Mirrors DELTA_VS_SIBLING's unknown-sibling
	// emission shape.
	//
	// Zero-sibling path: when the sibling resolves but its value is zero
	// (legitimate group with a zero post-filter sum), the handler emits
	// ONE PULSE_OVERLAY_REF_ZERO warning and surfaces NaN across every
	// present entry — division by zero is mathematically undefined and
	// the same PULSE_OVERLAY_REF_ZERO contract the SERIES INDEX_VS_TOTAL
	// / SHARE_OF_TOTAL kinds use applies here. DELTA_VS_SIBLING does NOT
	// emit this warning (subtraction by zero is well-defined).
	//
	// Ref handling: `Ref.Sibling` is REQUIRED — both `Field` and `Value`
	// must be non-empty strings. Any other ref-family pointer (Margin /
	// BaselineIndex / Population / Stage / Slot) fails
	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time.
	//
	// Scope must be GROUP. The validator rejects any other scope.
	OverlayKindIndexVsSibling OverlayKind = "OVERLAY_INDEX_VS_SIBLING"

	// OverlayKindIndexVsTotal emits a per-group index score against the
	// grand total of a SERIES host (grouped Process result): for each
	// host group key the layer surfaces `(group_val / grand_total) *
	// 100.0`. GROUP scope over a SERIES (grouped Process) host with
	// SERIES payload — one SeriesEntry per host group key in host order,
	// each carrying the index score on `Summary.Statistic`. First
	// streamable overlay in the catalog (per kind-catalog-v1 §
	// "Streaming-capable subset"): the handler folds at the end of the
	// existing streaming Process pass via a running grand-total
	// accumulator that runs alongside the per-group accumulators — no
	// second pass over records, the post-host finalize divides each
	// group value by the running grand total.
	//
	// Math (per host group i):
	//
	//	grand_total = Σ_j group_val[j]   (post-filter rows, AGG_SUM
	//	                                   semantics — never reads pre-
	//	                                   filter row count)
	//	index_i     = (group_val[i] / grand_total) * 100.0
	//
	// Zero-grand-total path: when grand_total == 0 (every group's
	// post-filter value sums to zero, including the degenerate "no
	// groups survived the filter" case) the handler emits ONE
	// PULSE_OVERLAY_REF_ZERO warning and populates every entry's
	// Summary.Statistic with NaN. Mirrors the existing
	// PULSE_OVERLAY_REF_ZERO contract used by the share / index family
	// against a missing axis margin on the MATRIX host.
	//
	// Absent-group policy: a host that did not produce a record for
	// group i (the resolver returns (0, false)) surfaces a SeriesEntry
	// whose Summary leaves Statistic unset — the canonical "present
	// slot, empty summary" shape established by the E3-S1 SERIES
	// dispatch contract. Absent groups do NOT contribute to the grand
	// total (the resolver gates the accumulator the same way it gates
	// per-group emission).
	//
	// Ref handling: implicit-grand-total. The Ref union is left EMPTY
	// — the kind's denominator is the host series' own grand total, so
	// callers supplying any Ref family pointer (Margin / Sibling /
	// BaselineIndex / Population / Stage / Slot) fail
	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time
	// (mirrors the CHISQ_* implicit-margin contract).
	//
	// Scope is GROUP (not CELL or ROW): the kind decorates each grouper
	// level the host emits. The validator rejects any other scope.
	//
	// Streamable. Per types/overlay_streamability.go, the streamability
	// row is `true` — the grand-total accumulator is one f64 carried
	// alongside the per-group accumulators inside the streaming Process
	// fold, and the division step happens at host finalize. The E3-S6
	// streaming-Process orchestrator wiring lands the inner per-record
	// hook; this kind ships with a SERIES-host post-finalize entry
	// (ApplyOverlaysSeries route) so the streaming-vs-buffered byte-
	// identity test holds at the entry-point level today.
	OverlayKindIndexVsTotal OverlayKind = "OVERLAY_INDEX_VS_TOTAL"

	// OverlayKindShareOfCol emits a per-cell share-of-column ratio: cell
	// / col_margin. CELL-scoped over a MATRIX (crosstab) host. Cells
	// along a single column sum to 1.0 in the absence of missing cells;
	// renderers can present the layer as a 100%-stacked vertical
	// projection. Structural twin of OVERLAY_SHARE_OF_ROW with the
	// column-axis margin slot as the denominator. Inherently buffered for
	// the same reason as INDEX_VS_MARGIN — the host crosstab
	// orchestrator recomputes margins from raw rows before ApplyOverlays
	// runs.
	OverlayKindShareOfCol OverlayKind = "OVERLAY_SHARE_OF_COL"

	// OverlayKindShareOfRow emits a per-cell share-of-row ratio: cell /
	// row_margin. CELL-scoped over a MATRIX (crosstab) host. Cells along
	// a single row sum to 1.0 in the absence of missing cells; renderers
	// can present the layer as a 100%-stacked horizontal projection.
	// Inherently buffered for the same reason as INDEX_VS_MARGIN — the
	// host crosstab orchestrator recomputes margins from raw rows before
	// ApplyOverlays runs.
	OverlayKindShareOfRow OverlayKind = "OVERLAY_SHARE_OF_ROW"

	// OverlayKindShareOfTotal emits a share-of-grand-total ratio.
	// Dual-shape overload — the dispatch selects the host shape and the
	// runtime handler differs by host:
	//
	//   - MATRIX dispatch (E2-S3): per-cell ratio `cell / grand_total`.
	//     CELL-scoped over a MATRIX (crosstab) host. The entire matrix
	//     sums to 1.0 in the absence of missing cells; renderers can
	//     present the layer as a single-population share projection
	//     where each cell's contribution to the whole table is visible
	//     at a glance. Completes the matrix share triad (row / col /
	//     total). Structural twin of OVERLAY_SHARE_OF_ROW and
	//     OVERLAY_SHARE_OF_COL with the grand-axis margin slot as the
	//     denominator. Buffered (the host crosstab orchestrator
	//     recomputes margins from raw rows before ApplyOverlays runs).
	//     `Ref.Margin` is required (grand-axis-locked even though the
	//     handler ignores the axis value).
	//
	//   - SERIES dispatch (E3-S3): per-group ratio
	//     `group_val / grand_total`, scale 1.0 (no ×100 — emits the raw
	//     share so cells over a complete partition sum to 1.0 within
	//     ULP). GROUP-scoped over a SERIES (grouped Process) host with
	//     SERIES payload — one `SeriesEntry` per host group key in host
	//     order, each carrying the share on `Summary.Statistic`.
	//     Streamable — sibling kind to OVERLAY_INDEX_VS_TOTAL, same
	//     grand-total accumulator (`computeSeriesGrandTotal` in
	//     processing/overlay_series.go) carried alongside the per-group
	//     accumulators in the streaming Process fold (the streaming
	//     orchestrator wiring lands in E3-S6; this kind ships the
	//     post-finalize entry today). `Ref` MUST be empty
	//     (implicit-grand-total); any Ref-family pointer fires
	//     `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` at predict time.
	//     Zero `grand_total` emits one `PULSE_OVERLAY_REF_ZERO` warning
	//     and populates every entry's Statistic with NaN. Absent host
	//     groups (resolver reports `(0, false)`) surface a present
	//     SeriesEntry whose Summary leaves Statistic unset and do NOT
	//     contribute to the grand total.
	//
	// The kind-catalog-v1 interview's resolved-decision rule kept the
	// SHARE and INDEX kind names distinct ("readable kind names beat
	// scale-param overloading at JSON-authoring time"), so the SERIES
	// SHARE_OF_TOTAL dispatch is intentionally a separate kind from
	// INDEX_VS_TOTAL even though the math overlaps — callers cannot
	// confuse `share` with `index / 100` at the spec authoring surface.
	OverlayKindShareOfTotal OverlayKind = "OVERLAY_SHARE_OF_TOTAL"

	// OverlayKindZScoreVsRolling emits a per-point windowed z-score against
	// the rolling-window mean + sample SD of the W immediately preceding
	// points of an ordered SERIES (grouped Process) host:
	// `zscore_i = (point_value_i - rolling_mean(W)) / rolling_sd(W)`
	// where `rolling_sd = sqrt(M2 / (count - 1))` (SAMPLE SD, n-1
	// denominator). GROUP scope over a SERIES host with SERIES payload —
	// one `SeriesEntry` per host group key in host order, each carrying
	// the z-score on `Summary.Statistic`. Fifth windowed-Process overlay
	// in the catalog (E4-S6; siblings: `OVERLAY_INDEX_VS_PRIOR` / E4-S4,
	// `OVERLAY_INDEX_VS_BASELINE` / E4-S2, `OVERLAY_DELTA_VS_BASELINE` /
	// E4-S3, `OVERLAY_INDEX_VS_ROLLING_MEAN` / E4-S5) and the second
	// consumer of the `Ref.RollingMean` arm of the OverlayRef
	// discriminated union (sibling windowed-rolling family — both kinds
	// carry the window width on `Params["window"]`).
	//
	// Window-via-Params convention: the rolling window width is supplied
	// via `OverlaySpec.Params["window"]` as a positive integer (mirrors
	// the `WIN_*` operator convention; see `skills/window-operations.md`).
	// The `Ref.RollingMean` arm is an empty marker struct
	// (`OverlayRollingMeanRef{}`) tagging the ref family — identical to
	// the `OVERLAY_INDEX_VS_ROLLING_MEAN` shape so the validator's
	// "exactly one ref arm populated per kind" contract stays uniform.
	//
	// Variance choice (SAMPLE, not population) — KEY CONTRAST with
	// `OVERLAY_ZSCORE_VS_TOTAL`: the rolling z-score uses SAMPLE SD
	// (divide by `count - 1`, NOT N). The rationale is structural: a
	// rolling window of W observations IS a sample of the wider time
	// series, so unbiased sample variance is the correct convention. By
	// contrast `OVERLAY_ZSCORE_VS_TOTAL` uses POPULATION SD (`sqrt(M2 /
	// N)`) because the per-group aggregation set IS the whole population
	// being standardised against. The two surfaces are intentionally
	// orthogonal: rolling = local sample; total = global population. The
	// E4-S5 `OVERLAY_INDEX_VS_ROLLING_MEAN` carrier stores the Welford
	// `(count, mean, M2)` trio precisely so this story can lift the
	// rolling SD via `sqrt(M2 / (count - 1))` without re-folding.
	//
	// Math (per host group ordinal `i`):
	//
	//	W = Params["window"] (positive int)
	//	if fewer than 2 PRESENT prior points in the window
	//	                                                  ⇒ NaN (no warning)
	//	if rolling_sd(W) == 0                             ⇒ NaN + PULSE_OVERLAY_REF_ZERO
	//	otherwise                                         ⇒ (point - mean) / sd
	//
	// Carrier shape (shared with `OVERLAY_INDEX_VS_ROLLING_MEAN`): the
	// handler maintains the same per-group ring buffer of the W most
	// recently observed PRESENT values plus the Welford-Pébaÿ (count,
	// mean, M2) trio updated alongside each push/evict. INDEX_VS_ROLLING_MEAN
	// reads only `mean`; ZSCORE_VS_ROLLING reads BOTH `mean` and `M2`. The
	// shared carrier keeps the per-group accumulator footprint flat as
	// more rolling-family kinds land.
	//
	// Window-fill semantics: when the carrier `count < 2` (the Welford
	// recurrence requires at least two observations to define a sample
	// variance), the handler emits NaN without warning — "window not yet
	// filled with at least 2 priors" is structurally distinct from
	// "denominator was zero" (mirrors the `OVERLAY_INDEX_VS_ROLLING_MEAN`
	// window-not-yet-filled rule). When the host series is shorter than W
	// (no ordinal ever sees a full window) every entry's Statistic is NaN
	// with no warning.
	//
	// Absent-point policy (ring buffer does not advance): an absent host
	// ordinal (resolver reports `(0, false)`) emits a present
	// `SeriesEntry` whose Summary leaves Statistic unset and DOES NOT
	// advance the ring buffer. Mirrors the
	// `OVERLAY_INDEX_VS_ROLLING_MEAN` / `OVERLAY_INDEX_VS_PRIOR` absent-
	// point carrier rule — the next present ordinal will compare against
	// the most recent W PRESENT values, not absent slots.
	//
	// Zero rolling-SD path: when the rolling SD is exactly `0` at the
	// divide step (e.g. every prior point in the window has the same
	// value — constant series), the handler emits ONE
	// `PULSE_OVERLAY_REF_ZERO` warning per layer and surfaces NaN on the
	// affected entry. Subsequent points may again hit the zero-SD path
	// as the ring rolls; one warning per layer per zero-SD event
	// occurrence. Mirrors the share / index / zscore family's zero-
	// denominator contract.
	//
	// Window param validation: `Params["window"]` MUST be present and
	// MUST be a positive integer (JSON numbers decoded as float64 are
	// accepted when integral). Missing param fires
	// `PULSE_OVERLAY_PARAM_MISSING` carrying `{kind, param}` Details at
	// both predict (`descriptor.validateOverlayZScoreVsRolling`) and
	// runtime (`processing.applyZScoreVsRolling`). Window `<= 0` fires
	// `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` carrying `{window}` Details —
	// the LEVEL_OUT_OF_RANGE code is reused for "value out of valid
	// range" semantics so the new code surface stays narrow (mirrors the
	// existing usage on Level / Within slots).
	//
	// Ref handling: `Ref.RollingMean` MUST be populated (empty struct is
	// fine; the marker tags the ref family). Any other ref-family
	// pointer (`Margin` / `Sibling` / `BaselineIndex` / `Prior` /
	// `Population` / `Stage` / `Slot`) fires
	// `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` at predict time.
	//
	// Scope must be GROUP. `Level` / `Within` MUST be zero (windowed
	// kind — the rolling-window carrier folds across the ordered axis
	// without a prefix-bucket denominator; non-zero values fire
	// `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` mirroring the
	// `INDEX_VS_ROLLING_MEAN` / `INDEX_VS_PRIOR` / `INDEX_VS_BASELINE` /
	// `INDEX_VS_TOTAL` implicit-margin / windowed family).
	//
	// Buffered. Per `types/overlay_streamability.go`, the streamability
	// row is `false` — the ring buffer carries the full window of present
	// values plus the Welford trio per group, which the streaming Process
	// pass cannot maintain inline with the per-record fold today (matches
	// the `OVERLAY_INDEX_VS_ROLLING_MEAN` buffered rationale verbatim).
	// Forward-compat: a future story may lift the rolling carrier into a
	// streaming-aware shape; when that lands the streamability flag flips
	// to true.
	//
	// Chan-Welford merge documentation (per CLAUDE.md § Execution modes
	// → Parallel buffered Process): the Welford triple combines under
	// Chan-Welford with the standard `delta = mean_B - mean_A;
	// mean = mean_A + delta * n_B/n;
	// M2 = M2_A + M2_B + delta² * n_A*n_B/n` recurrence. v1 ships
	// serial-per-group execution so the merge path is not exercised
	// today, but the carrier shape stays parallel-safe so a future story
	// that lifts the rolling fold into the parallel buffered Process
	// pipeline can reuse the existing merge plumbing.
	//
	// Renderers centre diverging colour ramps on `baseline = 0` (mirrors
	// `OVERLAY_ZSCORE_VS_TOTAL` / `OVERLAY_ZSCORE_VS_MARGIN` — every
	// z-score family produces a centred distribution, not a ratio).
	OverlayKindZScoreVsRolling OverlayKind = "OVERLAY_ZSCORE_VS_ROLLING"

	// OverlayKindZScoreVsMargin emits a per-cell standardized-margin
	// z-score: (cell - margin) / sd where margin is the matching axis
	// margin slot and sd is the population standard deviation of the
	// cell values within the same margin slice. CELL-scoped over a
	// MATRIX (crosstab) host. Unlike the SHARE_OF_* triad (each of
	// which is structurally axis-locked), ZSCORE_VS_MARGIN supports
	// every axis a Margin reference can target (row / column / grand);
	// callers pick the axis explicitly via Ref.Margin.Axis and the
	// handler dispatches the matching slice. First non-ratio overlay
	// in the catalog — output is unitless deviation, not a ratio or
	// percentage. Inherently buffered for the same reason as
	// INDEX_VS_MARGIN — both the host crosstab path and the per-slice
	// Welford recurrence rely on a fully-materialised matrix.
	OverlayKindZScoreVsMargin OverlayKind = "OVERLAY_ZSCORE_VS_MARGIN"

	// OverlayKindYoY emits a per-point year-over-year ratio against the
	// same period one year prior in an ordered SERIES (grouped Process)
	// host whose grouper is `GROUP_DATE`. GROUP scope over a SERIES host
	// with SERIES payload — one `SeriesEntry` per host group key in host
	// order, each carrying the YoY ratio on `Summary.Statistic`. Sixth
	// windowed-Process overlay in the catalog (E4-S7; siblings:
	// `OVERLAY_INDEX_VS_PRIOR` / E4-S4, `OVERLAY_INDEX_VS_BASELINE` /
	// E4-S2, `OVERLAY_DELTA_VS_BASELINE` / E4-S3,
	// `OVERLAY_INDEX_VS_ROLLING_MEAN` / E4-S5, `OVERLAY_ZSCORE_VS_ROLLING` /
	// E4-S6) and the first consumer of the empty `Ref.YoY` marker arm of
	// the OverlayRef discriminated union.
	//
	// Math (per host group ordinal `i`):
	//
	//	frequency = spec.Params["frequency"] OR req.Groups[0].Params["frequency"]
	//	prior_i = lookup for the host series point at "i - <stride>" based on
	//	          frequency:
	//	            annual    ⇒ i - 1
	//	            quarterly ⇒ i - 4
	//	            monthly   ⇒ i - 12
	//	            weekly    ⇒ i - 52
	//	            daily     ⇒ exact-key lookup against host key index at
	//	                        host.Key(i).AddDate(-1, 0, 0); Feb 29 in a
	//	                        non-leap prior year emits NaN (no exact-key
	//	                        match — explicit non-goal: no day-of-week or
	//	                        leap-year realignment in v1).
	//	            hourly    ⇒ exact-key lookup against host key index at
	//	                        host.Key(i).Add(-365*24*time.Hour) with the
	//	                        same exact-key rule.
	//	if !present_at(prior_i)  ⇒ NaN (first year of data is legitimate —
	//	                            "no comparison available" rather than
	//	                            "denominator was zero"; no warning)
	//	if prior_value == 0      ⇒ NaN + PULSE_OVERLAY_REF_ZERO warning
	//	otherwise                ⇒ point_value / prior_value * 100
	//
	// Required GROUP_DATE host grouper: the kind only operates against a
	// SERIES host whose single grouper is `GROUP_DATE`. Other grouper
	// kinds (CATEGORY / RANGE / ROUNDED / QUANTILE / SET_VALUE) cannot
	// resolve the "same period one year prior" semantics — predict
	// (`descriptor.validateOverlayYoY`) and runtime (the handler's host
	// introspection arm via `SeriesHostView.GrouperKind`) both reject
	// non-DATE hosts with `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
	//
	// Required `frequency` Param: the kind cannot infer the correct prior-
	// period stride from the GROUP_DATE `component` slot alone because
	// the per-component stride for "one year prior" varies by component
	// (annual ⇒ 1, quarterly ⇒ 4, monthly ⇒ 12, weekly ⇒ 52, daily ⇒
	// 365-day calendar arithmetic, hourly ⇒ 365×24-hour arithmetic). The
	// handler reads the explicit `frequency` value from
	// `spec.Params["frequency"]` first (the YoY's own override) and falls
	// back to `req.Groups[0].Params["frequency"]` (the canonical GROUP_DATE
	// authoring slot). Missing both fires
	// `PULSE_OVERLAY_YOY_FREQUENCY_MISSING` with `{kind, host_grouper}`
	// Details at both predict and runtime. Outside the supported set
	// (`annual` | `quarterly` | `monthly` | `weekly` | `daily` | `hourly`)
	// fires `PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY` with
	// `{frequency, supported}` Details.
	//
	// Calendar-week / day-of-week realignment is an explicit non-goal in
	// v1: weekly frequency uses calendar-week-aligned `i - 52` arithmetic
	// (no day-of-week realignment); daily frequency uses exact-key lookup
	// against the host key index after subtracting one year via
	// `time.Time.AddDate(-1, 0, 0)` (Feb 29 in a non-leap prior year
	// emits NaN — no exact-key match). The runtime documents both rules
	// explicitly so callers do not silently get realigned results.
	//
	// Absent-point policy: a host that did not produce a value for group
	// `i` (the resolver returns `(0, false)`) surfaces a `SeriesEntry`
	// whose `Summary` leaves `Statistic` unset — the canonical "present
	// slot, empty summary" shape from the E3-S1 SERIES dispatch contract.
	// Absent groups do NOT participate in the YoY computation. The first
	// year of data (every ordinal whose prior-year index lands at < 0 OR
	// whose daily/hourly prior-year date does not match an exact host
	// key) emits NaN without warning — "no comparison available" is
	// structurally distinct from "denominator was zero" (mirrors the
	// `OVERLAY_INDEX_VS_PRIOR` first-point NaN rule).
	//
	// Zero-prior path: when the resolved prior value is `0` at the divide
	// step (legitimate prior point with a zero post-filter sum) the
	// handler emits ONE `PULSE_OVERLAY_REF_ZERO` warning per layer and
	// surfaces NaN on the affected entry. Mirrors the share / index /
	// zscore family's zero-denominator contract (one warning per layer,
	// not per cell).
	//
	// Ref handling: `Ref.YoY` MUST be populated (empty marker is fine —
	// the v1 frequency value lives on Params per the WIN_* operator
	// convention). Any other ref-family pointer (`Margin` / `Sibling` /
	// `BaselineIndex` / `Prior` / `RollingMean` / `Population` / `Stage` /
	// `Slot`) fires `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
	//
	// Scope must be GROUP. `Level` / `Within` MUST be zero (windowed
	// kind — the prior-period lookup folds across the ordered axis
	// without a prefix-bucket denominator); non-zero values fire
	// `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` mirroring the
	// `INDEX_VS_PRIOR` / `INDEX_VS_BASELINE` / `INDEX_VS_ROLLING_MEAN`
	// family.
	//
	// Buffered. Per `types/overlay_streamability.go`, the streamability
	// row is `false` — the prior-period lookup requires the materialised
	// host series (daily / hourly arms walk an exact-key index over the
	// full host key list; coarser frequencies index into an arbitrary
	// prior ordinal). The handler runs at the buffered post-host-
	// finalize exit via `ApplyOverlaysSeries`. Forward-compat: a future
	// story may lift the per-frequency lookup into a streaming-aware
	// shape when the streaming-Process orchestrator carries an exact-key
	// host index inline.
	//
	// Renderers centre diverging colour ramps on `baseline = 100`
	// (mirrors `OVERLAY_INDEX_VS_MARGIN` / `OVERLAY_INDEX_VS_TOTAL` /
	// `OVERLAY_INDEX_VS_SIBLING` / `OVERLAY_INDEX_VS_PRIOR` /
	// `OVERLAY_INDEX_VS_BASELINE` / `OVERLAY_INDEX_VS_ROLLING_MEAN`).
	OverlayKindYoY OverlayKind = "OVERLAY_YOY"

	// OverlayKindZScoreVsTotal emits a per-group standardized z-score
	// against the host series' grand-total distribution: for each host
	// group key the layer surfaces `(group_val - mean) / sd` where
	// `mean = Σ_j group_val[j] / N` and `sd = sqrt(M2 / N)` (population
	// variance) folded across the N present per-group aggregated values.
	// GROUP scope over a SERIES (grouped Process) host with SERIES
	// payload — one SeriesEntry per host group key in host order, each
	// carrying the z-score on `Summary.Statistic`. Third and final
	// streamable overlay in the E3 grouped-Process subset (sibling to
	// OVERLAY_INDEX_VS_TOTAL and the SERIES dispatch of
	// OVERLAY_SHARE_OF_TOTAL): the Welford accumulator is one (count,
	// mean, M2) triple carried alongside the per-group accumulators
	// inside the streaming Process fold — no second pass over records,
	// the post-host finalize emits `(group_val - mean) / sd` per group.
	//
	// Math (per host group i):
	//
	//	count       = number of present per-group values
	//	mean        = Σ_j group_val[j] / count       (Welford recurrence,
	//	                                              single-pass)
	//	M2          = Σ_j (group_val[j] - mean)²    (Welford accumulator)
	//	sd          = sqrt(M2 / count)               (population SD)
	//	zscore_i    = (group_val[i] - mean) / sd
	//
	// Variance choice (population, not sample): the kind name says
	// `_VS_TOTAL` which implies the host's per-group aggregation set IS
	// the whole population we are standardising against — we are not
	// inferring a wider population from a sample of groups, we are
	// standardising every present group against the entire observed
	// distribution. Population variance (divide by N, not N-1) is the
	// correct convention; the sibling buffered `ATTR_ZSCORE` attribute
	// reuses the same convention against raw records. Reuses the same
	// numerical convention (single-pass Welford-Pébaÿ) as the parallel
	// buffered Process path and the crosstab ZSCORE_VS_MARGIN handler so
	// cross-mode equivalence tests stay byte-equal within ULP.
	//
	// Zero-variance path: when `sd == 0` (every present group value is
	// equal, including the every-group-zero degenerate case and the
	// single-present-group case) the handler emits ONE
	// PULSE_OVERLAY_REF_ZERO warning and populates every entry's
	// `Summary.Statistic` with NaN. Mirrors the existing
	// PULSE_OVERLAY_REF_ZERO contract used by the share / index family
	// against a missing axis margin on the MATRIX host AND the sibling
	// SERIES kinds against a zero grand total.
	//
	// Absent-group policy: a host that did not produce a record for
	// group i (the resolver returns (0, false)) surfaces a SeriesEntry
	// whose Summary leaves Statistic unset — the canonical "present
	// slot, empty summary" shape established by the E3-S1 SERIES
	// dispatch contract. Absent groups do NOT contribute to the
	// Welford accumulator (the resolver gates the fold the same way it
	// gates per-group emission, identical to INDEX_VS_TOTAL).
	//
	// Ref handling: implicit-grand-total. The Ref union is left EMPTY —
	// the kind's centerpoint is the host series' own grand-total
	// distribution (mean), so callers supplying any Ref family pointer
	// (Margin / Sibling / BaselineIndex / Population / Stage / Slot)
	// fail PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time
	// (mirrors INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES contract).
	//
	// Scope is GROUP (not CELL or ROW): the kind decorates each grouper
	// level the host emits. The validator rejects any other scope.
	//
	// Streamable. Per types/overlay_streamability.go, the streamability
	// row is `true` — the Welford accumulator (count + mean + M2) is
	// three f64s carried alongside the per-group accumulators inside
	// the streaming Process fold, and the standardisation step happens
	// at host finalize. The E3-S6 streaming-Process orchestrator wiring
	// lands the inner per-record hook; this kind ships with a SERIES-
	// host post-finalize entry (ApplyOverlaysSeries route) so the
	// streaming-vs-buffered byte-identity test holds at the entry-point
	// level today. Gotcha (per the story note): the streaming pass
	// folds Welford over GROUPS, not raw records — variance is across
	// N groups, not N records, distinct from `ATTR_ZSCORE`'s record-
	// level semantics.
	OverlayKindZScoreVsTotal OverlayKind = "OVERLAY_ZSCORE_VS_TOTAL"
)

// AllOverlayKinds returns every defined overlay kind in alphabetical
// order. Mirrors AllAggregationTypes / AllRegressionTypes — the
// streamability table and per-kind validator iterate this surface so a
// new kind only needs to be appended here, declared in the constant
// block above, and have its streamability row added in
// types/overlay_streamability.go (the TestStreamability_OverlaysKnown
// gate enforces table completeness).
func AllOverlayKinds() []OverlayKind {
	return []OverlayKind{
		OverlayKindChiSqCol,
		OverlayKindChiSqMatrix,
		OverlayKindChiSqRow,
		OverlayKindDeltaVsBaseline,
		OverlayKindDeltaVsMargin,
		OverlayKindDeltaVsSibling,
		OverlayKindFisherExactCell,
		OverlayKindIndexVsBaseline,
		OverlayKindIndexVsMargin,
		OverlayKindIndexVsPrior,
		OverlayKindIndexVsRollingMean,
		OverlayKindIndexVsSibling,
		OverlayKindIndexVsTotal,
		OverlayKindShareOfCol,
		OverlayKindShareOfRow,
		OverlayKindShareOfTotal,
		OverlayKindYoY,
		OverlayKindZScoreVsMargin,
		OverlayKindZScoreVsRolling,
		OverlayKindZScoreVsTotal,
	}
}

// OverlayShape declares the structural footprint of an overlay's
// rendered payload. Downstream renderers branch on this to lay the
// overlay grid on top of the base result.
type OverlayShape string

const (
	// OverlayShapeScalar carries a single float64 — a Total-scoped index,
	// a single sibling-vs-baseline delta, etc.
	OverlayShapeScalar OverlayShape = "scalar"

	// OverlayShapeSeries carries one float64 per axis key — a row-wise
	// index strip, a per-column deviation strip. SeriesPayload below
	// carries the keys + values in matching order.
	OverlayShapeSeries OverlayShape = "series"

	// OverlayShapeMatrix carries a full row × column grid of float64
	// cells — most commonly produced by Scope=CELL overlays where every
	// cell of the base matrix receives a derived score. MatrixPayload
	// (from crosstab.go) is reused so renderers handle both layers with
	// one shape.
	OverlayShapeMatrix OverlayShape = "matrix"
)

// OverlayScope declares where an overlay's computation lands in the
// base result. It is independent of OverlayShape — a CELL-scoped
// overlay typically produces a matrix payload, a ROW-scoped overlay
// typically produces a series, but the choice is per-overlay.
type OverlayScope string

const (
	// OverlayScopeCell decorates every cell of the base result. For a
	// crosstab base this is one value per (row_key, column_key) pair.
	OverlayScopeCell OverlayScope = "cell"

	// OverlayScopeRow decorates every row tuple of the base result —
	// one value per row key, independent of columns.
	OverlayScopeRow OverlayScope = "row"

	// OverlayScopeColumn decorates every column tuple of the base
	// result — one value per column key, independent of rows.
	OverlayScopeColumn OverlayScope = "column"

	// OverlayScopeMatrix decorates the matrix as a whole; the payload
	// typically carries a derived matrix that mirrors the base shape
	// (e.g. a column-normalized re-projection of the cell values).
	OverlayScopeMatrix OverlayScope = "matrix"

	// OverlayScopeGroup decorates one grouper level. Reserved for future
	// nested-axis families; v1 emits OVERLAY_NOT_IMPLEMENTED if used
	// against OVERLAY_INDEX_VS_MARGIN.
	OverlayScopeGroup OverlayScope = "group"

	// OverlayScopeTotal decorates the grand-total margin slot — a single
	// scalar covering the whole result.
	OverlayScopeTotal OverlayScope = "total"
)

// MarginAxis names which margin family an OverlayMarginRef targets.
// Mirrors the AxisKey conventions used by CrosstabSpec.
type MarginAxis string

const (
	// MarginAxisRow targets the row-margin vector (Σ over columns per
	// row key).
	MarginAxisRow MarginAxis = "row"

	// MarginAxisColumn targets the column-margin vector (Σ over rows
	// per column key).
	MarginAxisColumn MarginAxis = "column"

	// MarginAxisGrand targets the grand-total margin (Σ over every
	// filter-passing row).
	MarginAxisGrand MarginAxis = "grand"
)

// OverlayMarginRef references one of the base result's margin slots.
// E1 ships only this family; later epics drop additional pointer fields
// into OverlayRef for sibling cells, baseline indices, population
// comparisons, multi-stage chain references, and slot lookups.
type OverlayMarginRef struct {
	// Axis selects which margin slot is the denominator. Required.
	Axis MarginAxis `json:"axis"`
}

// OverlaySiblingRef is reserved for sibling-cell comparison overlays
// (e.g. compare each cell against the cell at the same row but a
// different column key). Not populated in E1; included so later
// stories drop in without re-opening this file.
type OverlaySiblingRef struct {
	// Field names the axis dimension whose sibling is referenced
	// (typically a grouper Field name on the row or column axis).
	Field string `json:"field,omitempty"`

	// Value names the specific axis-key value to compare against.
	Value string `json:"value,omitempty"`
}

// OverlayBaselineIndexRef discriminates two overlapping baseline-
// reference shapes:
//
//   - MATRIX-host crosstab-cell coordinate (Row + Column, reserved for
//     E1 PRD §4 follow-ups). Each slot names a baseline coordinate as
//     a sorted, axis-ordered list of dictionary keys. Empty lists mean
//     "use the grand total". Not populated by any shipping kind today
//     and consumed only by the future "vs designated cell" Crosstab
//     overlay family.
//
//   - SERIES-host ordered-axis positional baseline (Position, the
//     E4-S1 windowed-Process arm). Position pins a 0-based ordinal
//     against the host's ordered series — typically a GROUP_DATE-keyed
//     grouped Process result whose key order is the chronological
//     ordering the orchestrator baked in at finalize time. The
//     `OVERLAY_INDEX_VS_BASELINE` (E4-S2), `OVERLAY_DELTA_VS_BASELINE`
//     (E4-S3), `OVERLAY_INDEX_VS_ROLLING_MEAN` (E4-S5), and
//     `OVERLAY_YOY` (E4-S7) handlers resolve a single host value at
//     this ordinal via `processing.ResolveBaselineIndex` and compare
//     every other ordered-axis point against it. Negative or
//     out-of-range values are rejected at predict time
//     (`descriptor.ValidateOverlays`) and at runtime
//     (`processing.ResolveBaselineIndex`) with
//     `PULSE_OVERLAY_REF_UNKNOWN` plus a `{baseline_index,
//     series_length}` Details map.
//
// Exactly one of (Row + Column) or Position is meaningfully populated
// per spec — the union is host-shape-disambiguated rather than
// pointer-discriminated. The two arms never collide in practice (Row
// + Column resolve on a MATRIX host; Position resolves on a SERIES
// host) so a single struct cleanly carries both reservations until
// the future MATRIX baseline-cell family ships.
type OverlayBaselineIndexRef struct {
	// Row names the baseline row-axis tuple as a sorted, axis-ordered
	// list of dictionary keys for the MATRIX-host arm. Empty list
	// means "use the grand total". Reserved for the future "vs
	// designated cell" Crosstab overlay family — not consumed by any
	// shipping handler today.
	Row []string `json:"row,omitempty"`

	// Column names the baseline column-axis tuple for the MATRIX-host
	// arm. Empty list means "use the grand total". Reserved.
	Column []string `json:"column,omitempty"`

	// Position pins a 0-based ordinal on the host's ordered axis for
	// the SERIES-host arm — typically a `GROUP_DATE`-keyed grouped
	// Process result whose key order is the chronological ordering the
	// orchestrator baked in at finalize. Resolved at runtime via
	// `processing.ResolveBaselineIndex(host, ref)`; negative values
	// and values `>= len(series keys)` fail at predict time
	// (`descriptor.ValidateOverlays`) and runtime with
	// `PULSE_OVERLAY_REF_UNKNOWN` carrying `{baseline_index,
	// series_length}` Details. Consumed by E4-S2 (INDEX_VS_BASELINE),
	// E4-S3 (DELTA_VS_BASELINE), E4-S5 (INDEX_VS_ROLLING_MEAN), and
	// E4-S7 (YOY).
	Position int `json:"position,omitempty"`
}

// OverlayPriorRef is the ref-arm that carries the "previous point in the
// ordered axis" semantics for windowed-SERIES kinds (the E4 windowed
// catalog — see kind-catalog-v1 PRD §4 windowed family). It tags a spec
// as "compare against the lag-N point" against a SERIES host whose
// group-key order is the chronological ordering the orchestrator baked
// in at finalize time (typically a `GROUP_DATE`-keyed grouped Process
// result).
//
// E4-S4 (`OVERLAY_INDEX_VS_PRIOR`) is the first kind to consume this
// arm and ships with lag-1 only. The `Lag` slot is reserved for future
// window-N priors (lag-3 for "compare against three points ago", etc.);
// non-zero `Lag` is NOT exercised by any shipping kind today. The
// authoring shape stays forward-compatible — a v1 caller can leave
// `Ref` entirely empty (the implicit default for the kind) OR populate
// `Ref.Prior` with `Lag = 0` (or unset), and both shapes spell "lag-1
// prior". When later windowed-N stories land they will accept positive
// `Lag` values and the runtime carrier widens from a single f64 to a
// small ring buffer.
type OverlayPriorRef struct {
	// Lag pins the window width back along the ordered axis. Zero (the
	// omitempty default) means lag-1 — the immediately preceding point.
	// Reserved: v1 windowed kinds ship lag-1 only and reject non-zero
	// values at predict time; later stories widen the carrier.
	Lag int `json:"lag,omitempty"`
}

// OverlayYoYRef is the ref-arm that tags a spec as a year-over-year
// windowed kind (E4 windowed catalog — see kind-catalog-v1 PRD §4
// windowed family) against a SERIES host whose grouper is `GROUP_DATE`.
//
// E4-S7 (`OVERLAY_YOY`) is the first kind to consume this arm and the
// empty marker is intentional — the v1 frequency value lives on
// `OverlaySpec.Params["frequency"]` (the YoY's own override) or falls
// back to the host's `GROUP_DATE` config at
// `req.Groups[0].Params["frequency"]`. The supported frequencies are
// `annual` | `quarterly` | `monthly` | `weekly` | `daily` | `hourly`;
// the handler picks the matching stride (annual ⇒ -1 ordinal,
// quarterly ⇒ -4 ordinals, monthly ⇒ -12 ordinals, weekly ⇒ -52
// ordinals, daily ⇒ -365-day exact-key lookup, hourly ⇒ -365×24-hour
// exact-key lookup).
//
// Calendar-week / day-of-week realignment is an explicit non-goal in
// v1: weekly frequency uses calendar-week-aligned `i - 52` arithmetic
// (no day-of-week realignment); daily frequency uses exact-key lookup
// against the host key index — Feb 29 in a non-leap prior year emits
// NaN because no exact-key match exists, not an off-by-one realignment
// to Feb 28 or Mar 1.
//
// The empty struct tags the ref family so the validator's "exactly one
// ref arm populated per kind" contract stays uniform with the rest of
// the catalog (every kind picks exactly one ref family, even when its
// parameters live on `Params`). Forward-compat: future stories may
// extend the struct with non-frequency knobs (e.g. fiscal-year
// alignment, leap-year fill policies) without re-opening the parent
// `OverlayRef`.
type OverlayYoYRef struct{}

// OverlayRollingMeanRef is the ref-arm that tags a spec as a windowed
// rolling-mean kind (E4 windowed catalog — see kind-catalog-v1 PRD §4
// windowed family) against a SERIES host whose group-key order is the
// chronological ordering the orchestrator baked in at finalize time
// (typically a `GROUP_DATE`-keyed grouped Process result).
//
// E4-S5 (`OVERLAY_INDEX_VS_ROLLING_MEAN`) is the first kind to consume
// this arm and the empty marker is intentional — the v1 window width
// lives on `OverlaySpec.Params["window"]` per the `WIN_*` operator
// convention (see `skills/window-operations.md`). The empty struct tags
// the ref family so the validator's "exactly one ref arm populated per
// kind" contract stays uniform with the rest of the catalog (every kind
// picks exactly one ref family, even when its parameters live on
// `Params`).
//
// Forward-compat: future stories may extend the struct with non-Window
// knobs (e.g. weighting modes for exponentially-weighted means, edge-
// fill policies for the warmup window) without re-opening the parent
// `OverlayRef`. The E4-S6 `OVERLAY_ZSCORE_VS_ROLLING` handler REUSES
// this same ref-arm (sibling windowed-rolling family — both kinds
// carry the window width on `Params["window"]`) so a single ref family
// tag suffices for the rolling-window catalog.
type OverlayRollingMeanRef struct{}

// OverlayPopulationRef is reserved for "vs population" comparisons that
// compare a filtered cohort against an unfiltered (or differently-
// filtered) population. Not populated in E1.
type OverlayPopulationRef struct {
	// Cohort names the .pulse cohort whose unfiltered (or alternately
	// filtered) statistics constitute the comparison population.
	Cohort string `json:"cohort,omitempty"`
}

// OverlayStageRef is reserved for ProcessChain-aware overlays that
// reference an earlier stage's result. Not populated in E1.
type OverlayStageRef struct {
	// Stage indexes the earlier ChainRequest stage whose output is the
	// comparison surface. Zero-based.
	Stage int `json:"stage,omitempty"`
}

// OverlaySlotRef is reserved for slot-aware overlays that reference a
// named slot of the base result (e.g. a labelled regression
// coefficient, a named percentile bucket). Not populated in E1.
type OverlaySlotRef struct {
	// Name identifies the slot to reference.
	Name string `json:"name,omitempty"`
}

// OverlayRef is the discriminated union identifying what an overlay
// compares against. Each pointer field corresponds to one comparison
// family; exactly one is meaningfully populated per OverlaySpec. The
// validator (E1-S2) rejects an OverlaySpec that populates the wrong
// pointer for its Kind.
//
// E1 only consumes Margin. The other pointers are placeholder slots so
// later stories drop in without re-opening this file (no migration of
// embedder-side JSON when subsequent overlay families land).
type OverlayRef struct {
	// Margin selects an axis-margin slot of the base result.
	Margin *OverlayMarginRef `json:"margin,omitempty"`

	// Sibling selects another cell on the same axis. Reserved.
	Sibling *OverlaySiblingRef `json:"sibling,omitempty"`

	// BaselineIndex selects a fixed baseline coordinate. Reserved.
	BaselineIndex *OverlayBaselineIndexRef `json:"baseline_index,omitempty"`

	// Prior selects the lag-N point along the host's ordered axis
	// (windowed-SERIES family — E4). E4-S4 (`OVERLAY_INDEX_VS_PRIOR`) is
	// the first consumer and ships with lag-1 only.
	Prior *OverlayPriorRef `json:"prior,omitempty"`

	// RollingMean tags the spec as a windowed rolling-mean kind. The
	// window width lives on `OverlaySpec.Params["window"]` per the
	// `WIN_*` operator convention; the marker struct is intentionally
	// empty. E4-S5 (`OVERLAY_INDEX_VS_ROLLING_MEAN`) is the first
	// consumer; E4-S6 (`OVERLAY_ZSCORE_VS_ROLLING`) reuses it.
	RollingMean *OverlayRollingMeanRef `json:"rolling_mean,omitempty"`

	// YoY tags the spec as a windowed year-over-year kind. The frequency
	// (`annual` | `quarterly` | `monthly` | `weekly` | `daily` | `hourly`)
	// lives on `OverlaySpec.Params["frequency"]` (the YoY's own override)
	// or falls back to the host's `GROUP_DATE` config at
	// `req.Groups[0].Params["frequency"]`; the marker struct is
	// intentionally empty. E4-S7 (`OVERLAY_YOY`) is the first consumer.
	YoY *OverlayYoYRef `json:"yoy,omitempty"`

	// Population selects an alternate cohort / population. Reserved.
	Population *OverlayPopulationRef `json:"population,omitempty"`

	// Stage selects an earlier ProcessChain stage. Reserved.
	Stage *OverlayStageRef `json:"stage,omitempty"`

	// Slot selects a named slot on the base result. Reserved.
	Slot *OverlaySlotRef `json:"slot,omitempty"`
}

// OverlaySpec is the request-side definition of one overlay layer.
// Multiple specs may ride the same Request.Overlays slice; each
// produces one OverlayLayer in Response.Overlays in matching order.
//
// Validation rules (enforced in descriptor + processing layers, not in
// this file — see E1-S2 / E1-S3):
//   - Kind is required and must be a known OverlayKind.
//   - Scope is required and must be a known OverlayScope.
//   - Ref must populate exactly one family pointer matching Kind's
//     contract (OVERLAY_INDEX_VS_MARGIN ⇒ Ref.Margin must be set).
//   - Name, when set, becomes the renderer-facing label; when empty the
//     processing layer synthesises a deterministic default keyed by
//     Kind + Scope + Ref.
//   - Params carries operator-specific configuration; the per-kind
//     schema lives alongside the kind's processor.
type OverlaySpec struct {
	// Name is the renderer-facing label for this overlay. When empty,
	// the processing layer synthesises a deterministic default.
	Name string `json:"name,omitempty"`

	// Kind selects the overlay catalog entry to execute.
	Kind OverlayKind `json:"kind"`

	// Scope declares where the overlay lands relative to the base
	// result.
	Scope OverlayScope `json:"scope"`

	// Ref names what the overlay compares against. Family pointer
	// selection depends on Kind.
	Ref OverlayRef `json:"ref"`

	// Params holds operator-specific configuration as raw JSON. Per-
	// kind schema documented alongside the kind's processor.
	Params json.RawMessage `json:"params,omitempty"`

	// Level truncates the same axis the overlay scopes (when paired
	// with a CELL-scope handler that honours Level/Within) to a
	// parent-grouper prefix at the configured depth. Default zero
	// (omitted on the wire) preserves the leaf-axis denominator, which
	// is byte-identical to the pre-Level handler output. Non-zero
	// values mirror the buffered crosstab's NormalizeLevel semantics:
	// the row/column-margin denominator is truncated to the configured
	// depth of the SAME axis the overlay axis-locks to. Honoured by the
	// share/index/delta/zscore family; the χ²/Fisher inferential family
	// rejects non-zero values with PULSE_OVERLAY_LEVEL_OUT_OF_RANGE
	// because those kinds compute their own contingency from the host
	// row + column margins (Level/Within would alter the implicit-
	// margin contract). See processing/crosstab_normalize.go for the
	// shared key-prefix helpers and skills/overlay-system.md for the
	// per-kind matrix.
	Level int `json:"level,omitempty"`

	// Within fixes a prefix of the OPPOSITE axis at the configured
	// depth (when paired with a CELL-scope handler that honours
	// Level/Within), producing a cross-axis partitioned denominator
	// rather than a same-axis truncated one. Default zero (omitted on
	// the wire) preserves the leaf-axis denominator, which is byte-
	// identical to the pre-Within handler output. Non-zero values
	// mirror the buffered crosstab's NormalizeWithin semantics: a
	// SHARE_OF_ROW overlay with Within=0 produces row shares that sum
	// to 1.0 within each fixed level-0 column prefix (instead of across
	// the full row). Composes with Level the same way the crosstab
	// path composes NormalizeLevel + NormalizeWithin. Honoured by the
	// share/index/delta/zscore family; the χ²/Fisher inferential family
	// rejects non-zero values with PULSE_OVERLAY_LEVEL_OUT_OF_RANGE
	// (same rationale as Level above). See
	// processing/crosstab_normalize.go and
	// skills/overlay-system.md for the per-kind matrix.
	Within int `json:"within,omitempty"`
}

// SeriesPayload is the canonical strip used by series-shaped overlay
// payloads. Each Entry pairs a composite axis-tuple key (matching the
// host layer's AxisKey shape element-for-element) with an
// OverlaySummary carrying the per-entry statistic / p-value / parameter
// map. The shape generalises across host families: for a Crosstab host
// the entry order matches MatrixPayload.RowKeys (CHISQ_ROW) or
// ColumnKeys (CHISQ_COL); for a future process-grouped host the entry
// order matches the host's grouper-key vector.
//
// Per-entry key contract (E2-S7 lands this for OVERLAY_CHISQ_ROW): the
// Series entries are a parallel slice to the host axis-key list — entry
// i carries the same composite key as RowKeys[i] (CHISQ_ROW) or
// ColumnKeys[i] (CHISQ_COL). This contract is the renderer-facing
// guarantee that overlay layers can be laid on top of the host axis
// without re-keying.
//
// Why the per-entry shape (Entries + Summary) over the earlier
// flat-strip placeholder (Keys + Values): the earlier shape carried a
// single float64 per key, which suits descriptive overlays but cannot
// surface inferential metadata (statistic, p-value, parameter map)
// without inventing a parallel summary slice. The canonical shape lets
// SCALAR + SERIES inferential overlays reuse the same OverlaySummary
// surface — CHISQ_ROW's per-row {Statistic, PValue, Parameters{"df"}}
// reuses E2-S6's CHISQ_MATRIX summary shape verbatim, and the per-row
// renderer reads each entry exactly the way it reads a SCALAR layer.
// Future descriptive series families (per-row deviation strips) populate
// only Summary.Min / Max / Count and leave Statistic / PValue unset.
//
// AxisKey is []any so numeric / categorical / date axis values keep
// their native types (matching MatrixPayload.RowKeys); renderers join
// or display tuple elements the same way they handle host row keys.
type SeriesPayload struct {
	// Entries is the ordered list of per-axis-key entries. Order
	// matches the host axis-key list element-for-element.
	Entries []SeriesEntry `json:"entries"`
}

// SeriesEntry is one per-axis-key entry in a series-shaped overlay
// payload. Key is the composite axis tuple (matching the host's
// AxisKey shape, e.g. MatrixPayload.RowKeys[i] for a CHISQ_ROW
// overlay); Summary carries the entry's statistic / p-value /
// parameters in the same shape the SCALAR-payload OverlaySummary
// uses on inferential overlays.
type SeriesEntry struct {
	// Key is the composite axis-tuple key identifying this entry.
	// Matches the host axis-key list element-for-element (RowKeys[i]
	// for CHISQ_ROW; ColumnKeys[i] for CHISQ_COL).
	Key AxisKey `json:"key"`

	// Summary carries the per-entry statistic / p-value / parameter
	// map. Mirrors the SCALAR-payload OverlaySummary shape so
	// inferential SERIES overlays surface the same renderer-facing
	// fields per entry (e.g. CHISQ_ROW: Statistic = χ²_r,
	// PValue = p_r, Parameters = {"df": cols - 1}).
	Summary OverlaySummary `json:"summary"`
}

// OverlayPayload is the discriminated union carrying the actual
// derived numbers an overlay produced. Exactly one of Scalar / Series
// / Matrix is meaningfully populated; Shape echoes which one.
//
// Renderers branch on Shape and read the matching field. Matrix
// reuses crosstab.MatrixPayload so a CELL-scoped overlay layered on
// top of a matrix base shares the same row/column header conventions
// as the base.
type OverlayPayload struct {
	// Shape declares which of Scalar / Series / Matrix is populated.
	Shape OverlayShape `json:"shape"`

	// Scalar is the single-value payload. Populated when Shape =
	// OverlayShapeScalar.
	Scalar *float64 `json:"scalar,omitempty"`

	// Series is the keys-and-values strip payload. Populated when
	// Shape = OverlayShapeSeries.
	Series *SeriesPayload `json:"series,omitempty"`

	// Matrix is the dense row × column payload. Populated when Shape =
	// OverlayShapeMatrix. Reuses crosstab.MatrixPayload so renderers
	// handle the overlay grid with the same header machinery as the
	// base layer.
	Matrix *MatrixPayload `json:"matrix,omitempty"`
}

// OverlaySummary carries optional renderer-friendly metadata for one
// overlay layer — min/max for colour-ramp scaling, count of present
// cells for sparsity hints, an optional baseline reference value.
// Every field is omitempty so a producer can populate just the
// summary slots that make sense for the kind in question (e.g.
// INDEX_VS_MARGIN reports min/max but not baseline; a future Z-score
// overlay reports baseline=0 and the populated standard deviation).
type OverlaySummary struct {
	// Min is the minimum derived value across the layer's payload.
	Min *float64 `json:"min,omitempty"`

	// Max is the maximum derived value across the layer's payload.
	Max *float64 `json:"max,omitempty"`

	// Count is the number of present (non-null, non-missing) entries
	// the layer produced. Zero is a valid value (e.g. an empty
	// matrix); the pointer distinguishes "0 known" from "not
	// reported".
	Count *int `json:"count,omitempty"`

	// Baseline is the comparison anchor — 100 for index-vs-margin
	// (anything < 100 underperforms, > 100 overperforms), 0 for delta
	// overlays, 1 for ratio overlays. Renderers use it to centre
	// diverging colour ramps.
	Baseline *float64 `json:"baseline,omitempty"`

	// Statistic is the headline test statistic for inferential overlays
	// (E2-S6: chi-square statistic for OVERLAY_CHISQ_MATRIX; later: KS
	// D statistic, Fisher odds ratio, etc.). Pointer + omitempty so
	// descriptive overlays leave the field absent — Statistic is only
	// meaningful when the kind produces a single distinguished scalar
	// the renderer should highlight (typically alongside PValue).
	Statistic *float64 `json:"statistic,omitempty"`

	// PValue is the inferential overlay's p-value (probability under
	// the null hypothesis). Pointer + omitempty so descriptive overlays
	// leave the field absent — non-nil only when the kind produces a
	// hypothesis-test result the renderer should surface alongside
	// Statistic.
	PValue *float64 `json:"p_value,omitempty"`

	// Parameters carries kind-specific test parameters in a flexible
	// shape so the inferential overlay catalog (E2-S6..S9, E5-S4) can
	// expose distribution / model parameters without per-kind struct
	// churn. Examples:
	//   OVERLAY_CHISQ_MATRIX  → {"df": 4.0}
	//   OVERLAY_FISHER_MATRIX → {"odds_ratio": 1.42}
	//   OVERLAY_KS_*          → {"d": 0.18}
	// Keys are SCREAMING_SNAKE-free lowercase strings; values are
	// float64. Empty / nil when the kind has no extra parameters to
	// surface. The map shape is forward-compatible — new keys land
	// additively without breaking existing renderer code.
	Parameters map[string]float64 `json:"parameters,omitempty"`
}

// OverlayLayer is the response-side wrapper for one executed overlay
// spec. Response.Overlays carries one OverlayLayer per
// Request.Overlays entry in matching order.
type OverlayLayer struct {
	// Name echoes the renderer-facing label — either the request
	// Name or the synthesised default.
	Name string `json:"name"`

	// Kind echoes the overlay catalog entry that produced this layer.
	Kind OverlayKind `json:"kind"`

	// Scope echoes the spec's scope.
	Scope OverlayScope `json:"scope"`

	// Ref echoes the spec's discriminated reference.
	Ref OverlayRef `json:"ref"`

	// Payload carries the derived numbers.
	Payload OverlayPayload `json:"payload"`

	// Summary carries optional renderer-friendly metadata. Omitted
	// when the layer reported nothing useful.
	Summary *OverlaySummary `json:"summary,omitempty"`
}
