---
name: pairwise-n-sources
description: The sample-size vocabulary shared by every OVERLAY_PAIRWISE_* kind — the nine n_source modes, the three distinct-key modes and their cell-aggregator admission, the slab partition gate, the null rules, and p_source.
type: guide
kind: design
applies_to: process, compose, predict
covers: [OVERLAY, PairwiseOverlayParams, n_source, p_source]
requires: [capability:crosstab]
---

# Pairwise sample-size sources

Every `OVERLAY_PAIRWISE_*` kind decodes one shared `params` blob (`types.PairwiseOverlayParams`): `pair_along_dim`, `n_source`, `n_within_depth`, `p_source`, `n_basis`, `letters`. This is the n vocabulary; per-kind math, acceptance and letters: each atomic skill.

Every mode reads `Response.Components.Crosstab`, so a components-disabled host is refused first (`PULSE_OVERLAY_COMPONENTS_REQUIRED`). An unreadable leg SKIPS the pair (aggregated `PULSE_OVERLAY_REF_ZERO`), never a zero n.

## Choosing n

The question is: **what does one observation mean here?**

- Omit `n_source` → the cell's BASE, the figure its share is of: the cell itself under `normalize: none`, else the normalise denominator (column / row / grand margin, or the `normalize_level` / `normalize_within` one), always the admitted one: its record count, or weighted its N* (`sum_weights` frequency, Kish `n_eff` probability).
- Weighted host (any weight source) → omit `n_source`, or `row_margin_n` / `column_margin_n` for a DISPLAYED margin's N*. Unweighted counts (`cell_n_unweighted`, `n_within`, every distinct mode) are `PROCESSING_CONFIG`; `cell_weight_sum` / `cell_value_weighted` stand under frequency only.
- The pair compares slices of a wider population (the share of a region WITHIN an issuer) → `n_within`, holding the first `n_within_depth`+1 dims fixed.
- Several records per respondent and n must be respondents → a DISTINCT mode (below).

## The nine n_source modes

| `n_source` | Leg |
|---|---|
| `cell_n_unweighted` (= the omitted default only under `normalize: none`) | `CellCounts[r][c]` |
| `cell_value_weighted` | the cell VALUE, ROUNDED half up |
| `cell_weight_sum` | `CellComponents[r][c]["sum_weights"]`, ROUNDED likewise |
| `row_margin_n` / `column_margin_n` | `RowMarginCounts[r]` / `ColumnMarginCounts[c]` (weighted: that margin's floor N*) |
| `n_within` | the SLAB: `CellCounts` summed across the pair axis, first `n_within_depth`+1 dims held fixed |
| `n_within_distinct` | that slab, in distinct KEYS |
| `row_margin_distinct` / `column_margin_distinct` | `RowMarginComponents[r]` / `ColumnMarginComponents[c]`, in distinct KEYS |

`n_within_depth` applies to the two within modes only; a depth `>=` the pair-axis dim count is refused. The moment-based kinds (two-means z, Welch t, weighted two-means z) read n and both moments off their cell aggregator, so they REFUSE `n_source` and `p_source` outright — a selector there would be a silent no-op; the weighted kind instead REQUIRES `n_basis` (`weights` | `kish`). Refusals are `PULSE_OVERLAY_PARAM_MISSING`.

## Distinct keys versus records

Use a distinct mode when one respondent contributes several records. `n_within_distinct` SUMS per-cell cardinalities; the margin modes read ONE accumulated figure.

**Admission** is decided on the cell aggregator's IDENTITY, up front, not per pair — an exact component-key-set match against its `ComponentSchema`. Two are admitted: the sum-of-distinct aggregator (figure on `distinct_count`) and the distinct count (on `cardinality`). Everything else is `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` — including the modal aggregators, which ALSO emit a key spelled `distinct_count` but count distinct VALUES of the measure (answer codes), not respondents. A key-presence probe would read that as a sample size; identity, not presence, is the test.

## Null rules

The n leg counts exactly what the CELL counted, so the two admitted aggregators differ: sum-of-distinct registers a key only when the key AND the value are non-null; distinct count counts distinct non-null values.

A slab cell no record reached contributes a true zero, not an unreadable leg. A nil or absent MARGIN entry is the opposite: components or that margin's display flag off means the leg was never emitted, so the pair skips.

## The slab partition rule

Summing per-cell distinct cardinalities equals the slab's true count only when its cells PARTITION the key set. A fan-out grouper (one row in several buckets, e.g. per-element grouping of a `set_*` field) at a depth the slab sums across puts one key in two cells: n too big, every p-value too small, silently. So it is refused: `PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`, at predict and again at runtime (`pulse.Process` does not run predict).

Two shapes are ACCEPTED: a fan-out grouper at depth `<= n_within_depth` (inside the FIXED prefix it multiplies slabs, not cells) and one on the OPPOSITE axis, which the slab never sums across. Fix a refusal by raising `n_within_depth` past the offending dim, or use `n_within` — record counts ARE additive. The margin distinct modes are exact by construction (each record counted once) and not gated. Extension groupers declare fan-out, so are gated alike.

## p_source

Proportion-input kinds only. `cell_value_pct` reads the cell ÷100, `cell_value` as 0..1. Omitted: `cell_value` on a normalised host, else `cell_value_pct`. `PULSE_OVERLAY_PARAM_MISSING`: an explicit `cell_value_pct` on a normalised host, or the omitted default over an unnormalised built-in cell that is no count or sum (a mean; `cell_type`). A leg outside `[0,1]` skips its pair as `PULSE_STATS_SKIP_PROPORTION_INVALID` (one `PULSE_OVERLAY_REF_ZERO` warning per reason). Over a count cell, normalize first.

## Compose-host panel

The multi-slot panel overlay reads the same distinct-KEY quantity under the same admission rule and null rules, but under its OWN mode spellings — none of `row_margin_n`, `n_within`, `n_within_distinct` is accepted there — and it refuses the whole spec when two slots name different admitted aggregators<!-- feature: OVERLAY_PROP_Z_PANEL --> (`op-overlay-prop-z-panel`)<!-- /feature -->.

## See

`crosstab-guide` (cell aggregators, margins, display flags) · `response-components` · `overlay-system`<!-- feature: OVERLAY_PAIRWISE_PROP_Z --> · `op-overlay-pairwise-prop-z`<!-- /feature --><!-- feature: AGG_DISTINCT_SUM --> · `op-agg-distinct-sum`<!-- /feature --><!-- feature: AGG_DISTINCT_COUNT --> · `op-agg-distinct-count`<!-- /feature --><!-- feature: GROUP_SET_PER_ELEMENT --> · `op-group-set-per-element`<!-- /feature -->.

<!-- feature: OVERLAY_PAIRWISE_PROP_Z, AGG_DISTINCT_SUM, GROUP_CATEGORY, GROUP_RANGE, GROUP_SET_PER_ELEMENT -->
Runnable distinct-n slab over a fan-out axis: `pulse_examples_get crosstab-pairwise-distinct-n`.
<!-- /feature -->
