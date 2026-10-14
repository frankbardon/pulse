---
name: op-overlay-markov
kind: operator
category: OVERLAY
operator: OVERLAY_MARKOV
description: Markov chain of a from × to transition crosstab — transition matrix, n-step powers, projections, steady state and time to absorption.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` `matrix`. `Ref` empty. `Level`/`Within` `0`.

- `steps` — default `[1,3,6,12]`; distinct integers 1–1000 (≤ 64).
- `absorbing` — state keys, e.g. `["churned"]`.
- `initial` — `"margin"` or `{state: share}`.

## Host shape

MATRIX crosstab, from-state rows × to-state columns, same keys (aligned by key); count or sum cells.

## Output

Primary MATRIX: transition P (rows sum to 1). Parts: `step_<n>` (MATRIX Pⁿ), `distribution_<n>` (SERIES, with `initial`), `steady_state` (SERIES), `absorption_time` (SERIES over non-absorbing states, with `absorbing`). Summary params: `states`, `classes`, `irreducible`, `period`.

<!-- generated: reading-the-output -->

## Gotchas

- Key sets differ ⇒ `PULSE_OVERLAY_MATRIX_NOT_SQUARE`, unless every column-only state is in `absorbing` (unit row synthesized, `REF_ZERO` warning).
- Reducible / periodic ⇒ `PULSE_OVERLAY_MARKOV_NOT_ERGODIC`, no `steady_state`.
- Zero row ⇒ absorbing + `PULSE_OVERLAY_REF_ZERO`.
- Never-absorbed state ⇒ `null` time.
- Bad (negative / undefined NaN) cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`.

## See

- Skills: `overlay-system`, `op-overlay-share-of-row`, `op-overlay-chisq-matrix`, `op-overlay-symmetry`, `op-overlay-retention`.
