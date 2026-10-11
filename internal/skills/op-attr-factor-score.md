---
name: op-attr-factor-score
description: Per-row score on one common factor of a vector of numeric members, from an inline fit with the factor-analysis matrix operator's knobs, loadings, order and sign; regression or Bartlett scores; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_FACTOR_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, feature-engineering, streaming-friendly]
---

Row-level scalars; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `factor` — required, 1-based. `method` — `regression` (default) or `bartlett`. `missing` — `listwise` only.
- `factors`, `extraction`, `rotation`, `kappa`, `gamma` — the factor fit's knobs<!-- feature: MAT_FACTOR --> (`MAT_FACTOR`'s)<!-- /feature -->; no `repair`.
- `fit_on` — `"filtered"` (default) or a filterer array picking the FIT rows; every filtered row is scored.
- `weight` — fit only; weight-0 rows are scored, not fitted.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields`; no `field`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64` source columns.

## Output

One `f64` per record: zᵀw on fit-standardized members (psych `factor.scores`); mean 0 over the fit rows; null member → null.

<!-- generated: reading-the-output -->

## Gotchas

- Fit is cohort-wide, never per group; groupers or features force buffered. Two-pass, never merged.
- All null + warning: `PULSE_MATRIX_SINGULAR`, `_INSUFFICIENT_N`, `_NOT_IDENTIFIED`, `_HEYWOOD` (Bartlett). Scored + warning: a regression Heywood case, `PULSE_MATRIX_NOT_CONVERGED`.

## See

- `pulse_examples_search tags=[multivariate, feature-engineering]`
- Skills: `multivariate-outputs` (causes), `op-mat-factor`, `op-attr-pc-score`
