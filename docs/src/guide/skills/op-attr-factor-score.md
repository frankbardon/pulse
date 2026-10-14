```yaml
name: op-attr-factor-score
description: Per-row score on one common factor of a vector of numeric members, from an inline fit with the factor-analysis matrix operator's knobs, loadings, order and sign; regression or Bartlett scores; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_FACTOR_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, feature-engineering, streaming-friendly]
```

Row-level scalars; no `Response.Components`.

## Use when

Adds to every row its score on one factor behind several related numeric fields: an estimate of the hidden trait they measure together.

Questions it answers:

- What is each respondent's score on the satisfaction factor behind these rating questions?
- Can I score every row on the two constructs a factor analysis found, to compare segments on them?

## Params

- `factor` — required, 1-based. `method` — `regression` (default) or `bartlett`. `missing` — `listwise` only.
- `factors`, `extraction`, `rotation` (oblique too), `kappa`, `gamma`, `epsilon` — the factor fit's knobs (`MAT_FACTOR`'s); no `repair`.
- `fit_on` — `"filtered"` (default) or a filterer array picking the FIT rows; every filtered row is scored.
- `fit` — a bound vector fit's name (a compose `fits` entry over one matrix or fitted-attribute battery, or a loaded `populations` entry: [`compose-fits`](compose-fits.md)), scored against instead of fitting (XOR `fit_on`: `fit_and_fit_on`); its own weight basis governs. Matched BY LABEL to the members (`labels` — inline `fields` only); mismatch `PULSE_VECTOR_REFERENCE_MISMATCH`, unbound `PULSE_MODEL_UNKNOWN`, a regression's name `PULSE_MODEL_KIND_MISMATCH`.
- `weight` — fit only; weight-0 rows are scored, not fitted.

Slot: exactly one of `vector` (a `vectors` entry) / `fields`; no `field`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64` source columns.

## Output

One `f64` per record: zᵀw on fit-standardized members (psych `factor.scores`); mean 0 over the fit rows; null member → null.

## Reading the output

- `value`: The row's estimated score on the chosen factor, on the scale of the fields standardized over the fitted rows: 0 is an average row, positive is toward the end where the fields loading positively on the factor are high.
  - Caveat: The direction is MAT_FACTOR's convention (each factor's loadings sum positive), not a meaning: check its loadings on the same fields before calling a high score good or bad.
  - Caveat: After an oblique rotation (promax, oblimin, geomin) the factors correlate, so scores on two factors move together.

## Gotchas

- Fit is cohort-wide, never per group; with `fit_on`, groupers or features force buffered — two-pass, never merged.
- With `fit` it is row-local: one pass, streams beside groupers and features, merges (shards, chain stages); same scores.
- All null + warning: `PULSE_MATRIX_SINGULAR`, `_INSUFFICIENT_N`, `_NOT_IDENTIFIED`, `_HEYWOOD` (Bartlett). Scored + warning: a regression Heywood case, `PULSE_MATRIX_NOT_CONVERGED`.

## See

- `pulse_examples_search tags=[multivariate, feature-engineering]`
- Skills: [`multivariate-outputs`](multivariate-outputs.md) (causes), [`op-mat-factor`](op-mat-factor.md), [`op-attr-pc-score`](op-attr-pc-score.md)
