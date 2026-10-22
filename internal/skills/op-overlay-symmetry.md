---
name: op-overlay-symmetry
kind: operator
category: OVERLAY
operator: OVERLAY_SYMMETRY
description: Paired-change test on a before × after crosstab — McNemar (2 × 2) or Bowker (k × k): are moves one way as common as moves back?
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` `matrix`. `Ref` empty. `Level`/`Within` `0`.

| Name | Type | Default | Description |
|---|---|---|---|
| `correct` | bool | `true` | (R's `mcnemar.test`): 2 × 2 continuity correction `(\|n_12 − n_21\| − 1)²`, clamped at 0; inert on k × k. |

## Host shape

MATRIX crosstab of PAIRED units: earlier-state rows × later-state columns, one grouper each, count cells. States align by key; a one-axis state is a zero row / column. A long panel needs no pre-pivot: previous-state rows (`op-attr-previous-state`) × the state.

## Output

SCALAR χ² = Σ_{i<j} `(n_ij − n_ji)² / (n_ij + n_ji)`. `Summary.Parameters`: `df` (pairs kept), `states`, `discordant`, `pairs_dropped`, `continuity_corrected`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An empty pair (`n_ij + n_ji = 0`) is dropped, lowering `df` (R gives NaN). None kept ⇒ statistic / p `null`.
- Pair expected `(n_ij + n_ji) / 2 < 5` ⇒ one `PULSE_OVERLAY_EXPECTED_LOW`.
- Weighted: Σw table, Kish-scaled under probability (+ `sum_weights`, `n_eff`).
- Grouper counts differ ⇒ `PULSE_OVERLAY_MATRIX_NOT_SQUARE`; non-count / negative cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`; an undefined (NaN) cell is absent (0).

## See

- Skills: `overlay-system`, `op-overlay-chisq-matrix`, `op-overlay-markov`, `op-overlay-retention`, `op-attr-previous-state`.
