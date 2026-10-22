```yaml
name: op-overlay-retention
kind: operator
category: OVERLAY
operator: OVERLAY_RETENTION
description: Retention rates of a from × to crosstab — per starting state, the share that stayed, switched or churned into a named absorbing state.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Retention rates of a from-state by to-state crosstab: for each starting state, the share that stayed, switched or churned.

Questions it answers:

- What share of each tier's customers kept their tier, moved to another tier or churned between the two snapshots?
- Which starting plan holds on to its accounts best?

Use something else:

- `OVERLAY_SYMMETRY` when you want to test whether moves one way outnumber moves back.

## Params

`Scope` `row`. `Ref` empty. `Level`/`Within` `0`.

- `absorbing` — optional non-empty array of distinct state keys (row or column) whose arrivals count as churn. Omitted ⇒ no churn split.

## Host shape

MATRIX crosstab: earlier-state rows × later-state columns, one grouper each, count (or sum) cells. Staying = the column whose key names the row's state (by key, never position). A long panel needs no pre-pivot: previous-state rows ([`op-attr-previous-state`](op-attr-previous-state.md)) × the state.

## Output

SERIES, one entry per from-state in row order: `summary.statistic` = stay `n_ii / R_i`; `summary.parameters` `switch`, `from_total` (`R_i`, the row total) and, with `absorbing`, `churn` — stay + switch + churn = 1. Layer `summary.parameters` pool them: `stay`, `switch`, `churn`, `states`, `total`.

## Reading the output

- `summary.statistic`: In each series entry: the stay rate, the share (0 to 1) of the starting state's units that were still in the same state at the end.
- `summary.parameters.switch`: The switch rate: the share (0 to 1) of the starting state's units that ended in another state other than a named absorbing one. In each entry it is that state's rate; on the layer it pools every reported state.
- `summary.parameters.from_total`: In each series entry: the starting state's row total, the number of units (or, on a weighted host, the sum of weights) the rates divide by.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- A from-state named in `absorbing` is not reported (churn leads into it).
- Zero row total ⇒ that entry's rates `null` + one `PULSE_OVERLAY_REF_ZERO` (`reason: zero_row`).
- An `absorbing` key the host lacks ⇒ `PULSE_OVERLAY_PARAM_MISSING` at runtime only (predict reads no record).
- Weighted: rates read the Σw table as is under either kind (scale-invariant); `from_total` is Σw.
- Grouper counts differ ⇒ `PULSE_OVERLAY_MATRIX_NOT_SQUARE`; non-count / negative cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`; an undefined (NaN) cell is absent (0).

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-markov`](op-overlay-markov.md), [`op-overlay-symmetry`](op-overlay-symmetry.md), [`op-overlay-share-of-row`](op-overlay-share-of-row.md), [`op-attr-previous-state`](op-attr-previous-state.md).
