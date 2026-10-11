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

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row its score on one factor behind several related numeric fields: an estimate of the hidden trait they measure together.

Questions it answers:

- What is each respondent's score on the satisfaction factor behind these rating questions?
- Can I score every row on the two constructs a factor analysis found, to compare segments on them?

## Params

- `factor` — required, 1-based. `method` — `regression` (Thurstone, default) or `bartlett`. `missing` — `listwise` only.
- `factors` (int or `"kaiser"`, default), `extraction`, `rotation`, `kappa`, `gamma` — the factor fit's knobs and rules (`MAT_FACTOR`'s); no `repair`.
- `fit_on` — `"filtered"` (default) or a filterer array picking the FIT rows; every filtered row is scored.
- `weight` — both kinds; fit only. A zero-weight row is scored, not fitted.

Slot: exactly one of `vector` (a `vectors` entry) / `fields`; no `field`. Default label `ATTR_FACTOR_SCORE_<vector or first field>_F<factor>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

One `f64` per record: zᵀw on members standardized by the fit; regression w = R⁻¹LΦ, Bartlett w = U⁻²L(LᵀU⁻²L)⁻¹ (psych `factor.scores`). Mean 0 over the fit rows. Null member → null.

## Reading the output

- `value`: The row's estimated score on the chosen factor, on the scale of the fields standardized over the fitted rows: 0 is an average row, positive is toward the end where the fields loading positively on the factor are high.
  - Caveat: The direction is MAT_FACTOR's convention (each factor's loadings sum positive), not a meaning: check its loadings on the same fields before calling a high score good or bad.
  - Caveat: After an oblique rotation (promax, oblimin) the factors correlate, so scores on two factors move together.

## Gotchas

- Fit is cohort-wide, never per group; groupers or features force buffered.
- All null + warning: singular fit (`PULSE_MATRIX_SINGULAR`), N* ≤ p (`PULSE_MATRIX_INSUFFICIENT_N`), k not identified or `"kaiser"` keeping fewer than `factor` (`PULSE_MATRIX_NOT_IDENTIFIED`), Bartlett over a uniqueness ≤ 0 (`PULSE_MATRIX_HEYWOOD`).
- Scored + warning: a Heywood case (regression; ML floor), an iteration cap (`PULSE_MATRIX_NOT_CONVERGED`; kept beside a refusal too).
- Two-pass, never merged.

## See

- `pulse_examples_search tags=[multivariate, feature-engineering]`
- Skills: [`multivariate-design`](multivariate-design.md), [`op-mat-factor`](op-mat-factor.md), [`op-attr-pc-score`](op-attr-pc-score.md)
