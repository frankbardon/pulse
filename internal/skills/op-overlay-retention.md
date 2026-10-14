---
name: op-overlay-retention
kind: operator
category: OVERLAY
operator: OVERLAY_RETENTION
description: Retention rates of a from × to crosstab — per starting state, the share that stayed, switched or churned into a named absorbing state.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` `row`. `Ref` empty. `Level`/`Within` `0`.

- `absorbing` — optional non-empty array of distinct state keys (row or column) whose arrivals count as churn. Omitted ⇒ no churn split.

## Host shape

MATRIX crosstab: earlier-state rows × later-state columns, one grouper each, count (or sum) cells. Staying = the column whose key names the row's state (by key, never position).

## Output

SERIES, one entry per from-state in row order: `summary.statistic` = stay `n_ii / R_i`; `summary.parameters` `switch`, `from_total` (`R_i`, the row total) and, with `absorbing`, `churn` — stay + switch + churn = 1. Layer `summary.parameters` pool them: `stay`, `switch`, `churn`, `states`, `total`.

<!-- generated: reading-the-output -->

## Gotchas

- A from-state named in `absorbing` is not reported (churn leads into it).
- Zero row total ⇒ that entry's rates `null` + one `PULSE_OVERLAY_REF_ZERO` (`reason: zero_row`).
- An `absorbing` key the host lacks ⇒ `PULSE_OVERLAY_PARAM_MISSING` at runtime only (predict reads no record).
- Weighted: rates read the Σw table as is under either kind (scale-invariant); `from_total` is Σw.
- Grouper counts differ ⇒ `PULSE_OVERLAY_MATRIX_NOT_SQUARE`; non-count / negative cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`; an undefined (NaN) cell is absent (0).

## See

- Skills: `overlay-system`, `op-overlay-markov`, `op-overlay-symmetry`, `op-overlay-share-of-row`.
