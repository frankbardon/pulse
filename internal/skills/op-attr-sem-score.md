---
name: op-attr-sem-score
description: Per-row score on one latent of a structural equation model fitted inline with the SEM matrix operator's knobs; lavaan's regression or Bartlett scores, each group scored with its own group's parameters; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_SEM_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, feature-engineering, comparison]
---

Row-level scalars; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `latent` | string | — | Latent (higher-order too); absent: the only one. |
| `method` | enum | `regression` | Or `bartlett` (lavaan `lavPredict`). |
| `model` | string | — | Members by label; absent: `F1 =~` all. |
| `meanstructure` | bool | `false` | Scores on the latent-mean scale. |
| `group` | string | — | Multi-group fit; rows score by group. |
| `group_reference` | string | — | Latent means 0. |
| `group_equal` | list | — | lavaan `group.equal`. |
| `group_partial` | list | — | Freed rows. |
| `solver` | enum | `slsqp` | Or `fisher`. |
| `information` | enum | `dense` | |
| `ftol` | float | 1e-06 | |
| `max_iter` | int | — | |
| `grad_tol` | float | 1e-10 | |
| `singular` | enum | `nearest` | Or `refuse`. |
| `fit_on` | any | `filtered` | Filterers picking the FIT rows. |
| `labels` | list | — | Inline `fields`: one label per member. |
| `missing` | enum | `listwise` | `listwise` only. |

The fit knobs are<!-- feature: MAT_SEM --> `MAT_SEM`'s<!-- /feature -->; no bootstrap. `weight` — frequency only, fit only.

## Inputs

`vector` or `fields` (no `field`); `group` is never a member.

## Output

One `f64` per record, E(η) + wᵀ(y − μ) per group: latent-mean scale with a mean structure (or `group`), else centered at the fit rows. Null member or group → null.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Fit is cohort-wide, never per request group (two-pass).
- A level with no fit rows scores null + `PULSE_SEM_SCORE_LEVEL_UNSEEN`.
- All null + warning: `PULSE_MATRIX_INSUFFICIENT_N`, `_SINGULAR`, `_NOT_IDENTIFIED`. Scored + warning: `_NOT_CONVERGED`, `_NEAREST_PD`.

## See

- `pulse_examples_search tags=[multivariate, comparison]`
- Skills: `sem-modeling`, `vector-row-scores`, `op-mat-sem`
