```yaml
name: op-overlay-prop-z-panel
kind: operator
category: OVERLAY
operator: OVERLAY_PROP_Z_PANEL
description: Compose-host multi-reference per-cell pairwise two-proportion z-test across N+1 slots.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, proportion-analysis]
```

Compose-only multi-reference: every pair of N+1 slots tested per cell with the pooled two-proportion z.

## Use when

Two-proportion z-tests between every pair of requests in a Compose panel, for each crosstab cell: which waves or markets differ in share?

Questions it answers:

- Across four waves, which pairs of waves differ in this answer's share?
- Which of five markets differ from each other in brand share?

Use something else:

- `OVERLAY_PROP_Z_CELL` when there is only one target.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `n_source` | enum | `row_margin_value` | Where each slot's n is read: `row_margin_value`, `cell_n_unweighted`, `row_margin_value_within`, `row_margin_distinct_within` (one respondent, several records). |
| `n_within_depth` | int | — | The `_within` modes only: the row-key prefix depth margins are summed within; omitted = no summing (≠ `0`); refused with any other mode. |

`Scope` = `cell`. `Reference` = panel 0, `Targets` = 1..N. `MaxPanelTargets` (`OverlayOptions`, default 16) caps `len(Targets)` and refuses FIRST (`PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`) — an `Options` knob, never a param. Every mode, the depth pointer, the row partition gate (`PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`), the distinct-mode admission and why `n_within` / `row_margin_n` are refused here: [`panel-n-sources`](panel-n-sources.md).

## Host shape

COMPOSE — MATRIX crosstab per slot, order `{Reference, Targets…}`. `cell_n_unweighted` and `row_margin_distinct_within` need components on EVERY slot (`PULSE_OVERLAY_COMPONENTS_REQUIRED`; unresolved ⇒ `PULSE_OVERLAY_SLOT_NOT_CROSSTAB`); the default never gates. The distinct mode admits only a distinct-key cell aggregator, the SAME one on every slot, else the whole spec is refused (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, runtime only).

## Output

MATRIX — `Cells[r][c].Value` is `[]float64`: upper-triangular p-values (row-major, no diagonal), length `M(M-1)/2`, `M = N+1`. Pair index `i*(2*M-i-1)/2 + (j-i-1)`. `Baseline` unset.

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds a list of two-sided p-values, one per pair of requests, in upper-triangle order with the reference at index 0: (0,1), (0,2), ..., (1,2), ...; M requests give M(M-1)/2 entries.
  - Caveat: The normal approximation needs roughly 10 successes and 10 failures per side.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `row`, `column`, `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Pairs byte-equal the cell proportion z (shared `twoProportionZ`).
- Degenerate `(n == 0, pooled ∈ {0,1}, se == 0)` → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per (cell, pair).
- Absent value → nil slice + `REF_ZERO` `ref_missing`; an unreadable counted n adds `n_missing` + `slot_index`. An unemitted distinct margin skips the coordinate, never tests against 0.
- Weighted slots: p̂ = cell / margin (Σw), n = the base's N*; the counted and distinct modes are `PROCESSING_CONFIG` there ([`panel-n-sources`](panel-n-sources.md)). `Summary.Parameters` adds `sum_weights` (+ `n_eff`).

## See

- Skills: [`panel-n-sources`](panel-n-sources.md), [`overlay-system`](overlay-system.md), [`pairwise-n-sources`](pairwise-n-sources.md), [`compose-requests`](compose-requests.md), [`op-overlay-prop-z-cell`](op-overlay-prop-z-cell.md).
