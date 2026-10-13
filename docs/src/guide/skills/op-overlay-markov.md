```yaml
name: op-overlay-markov
kind: operator
category: OVERLAY
operator: OVERLAY_MARKOV
description: Markov chain of a from × to transition crosstab — transition matrix, n-step powers, projections, steady state and time to absorption.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Markov chain of a from-state by to-state crosstab: transition probabilities, n-step projections, steady state and time to absorption.

Questions it answers:

- Where will customers sit across the tiers in 6 or 12 periods if they keep switching as they did?
- On average, how many periods does a customer in each tier take to churn?

## Params

`Scope` `matrix`. `Ref` empty. `Level`/`Within` `0`.

- `steps` — default `[1,3,6,12]`; distinct integers 1–1000 (≤ 64).
- `absorbing` — state keys (e.g. `["churned"]`).
- `initial` — `"margin"` (observed row mix) or `{state: share}` (normalized; missing = 0).

## Host shape

MATRIX crosstab: rows = from-state, columns = to-state, same key set (aligned by key). Count or sum cells.

## Output

Primary MATRIX: transition P (rows sum to 1). Parts: `step_<n>` (MATRIX Pⁿ), `distribution_<n>` (SERIES, with `initial`), `steady_state` (SERIES), `absorption_time` (SERIES over non-absorbing states, with `absorbing`). Summary params: `states`, `classes`, `irreducible`, `period`.

## Reading the output

- `cells.value`: The probability (0 to 1) of moving from the row's state to the column's state in one period; each row adds up to 1. The step_&lt;n&gt; parts read the same way over n periods.
  - Caveat: A row built from few moves gives noisy probabilities, and every projection compounds that noise.
- `parts.steady_state.summary.statistic`: In each series entry: the share of the population that would sit in the state in the long run if the switching probabilities never change; the shares add up to 1.

## Gotchas

- Key sets differ ⇒ `PULSE_OVERLAY_MATRIX_NOT_SQUARE`, unless every column-only state is in `absorbing` — its unit row is synthesized (`REF_ZERO` warning, `synthesized_rows`).
- Reducible / periodic ⇒ `PULSE_OVERLAY_MARKOV_NOT_ERGODIC`, no `steady_state`.
- Zero row ⇒ absorbing + `PULSE_OVERLAY_REF_ZERO`.
- Never-absorbed state ⇒ `null` time + warning.
- Bad cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-share-of-row`](op-overlay-share-of-row.md), [`op-overlay-chisq-matrix`](op-overlay-chisq-matrix.md).
