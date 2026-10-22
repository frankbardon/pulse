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

| Name | Type | Default | Description |
|---|---|---|---|
| `factor` | int | (required) | 1-based factor to score. |
| `method` | enum | `regression` | `regression` or `bartlett`. |
| `factors` | any | `kaiser` | Positive integer k (≥ `factor`) or `"kaiser"`. |
| `extraction` | enum | `minres` | `minres`, `principal_axis` or `ml`. |
| `rotation` | enum | `none` | Orthogonal or oblique (its factor correlation enters the weights). |
| `kappa` | float | `4` | Promax power. |
| `gamma` | float | `0` | Oblimin gamma. |
| `epsilon` | float | `0.01` | Geomin epsilon. |
| `fit_on` | any | `filtered` | `"filtered"` or a filterer array picking the FIT rows. |
| `fit` | string | — | A bound vector fit, matched by label; XOR `fit_on` (`vector-row-scores`). |
| `labels` | list | — | Inline `fields`: one label per member. |
| `missing` | enum | `listwise` | `listwise` only. |

The fit knobs are<!-- feature: MAT_FACTOR --> `MAT_FACTOR`'s<!-- /feature --> factor fit's; no `repair`. `weight` — fit only.

## Inputs

`vector` or `fields` (no `field`); members: see `## Contract`, source columns only.

## Output

One `f64` per record: zᵀw on fit-standardized members (psych `factor.scores`); mean 0 over the fit rows; null member → null.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Fit is cohort-wide, never per group: `fit_on` is two-pass, `fit` row-local; a failed fit nulls every score + `PULSE_FIT_UNAVAILABLE` (`vector-row-scores`).
- All null + warning: `PULSE_MATRIX_SINGULAR`, `_INSUFFICIENT_N`, `_NOT_IDENTIFIED`, `_HEYWOOD` (Bartlett). Scored + warning: regression Heywood, `_NOT_CONVERGED`.

## See

- `pulse_examples_search tags=[multivariate, feature-engineering]`
- Skills: `vector-row-scores`, `multivariate-outputs` (causes), `op-mat-factor`, `op-attr-pc-score`
