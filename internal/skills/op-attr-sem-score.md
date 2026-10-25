---
name: op-attr-sem-score
description: Per-row score on one latent of a structural equation model fitted inline with the SEM matrix operator's knobs; lavaan's regression or Bartlett scores (empirical Bayes modal on an ordinal fit), each group scored with its own group's parameters; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_SEM_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, feature-engineering, comparison]
---

Row scalars; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `latent` | string | — | Absent: the only. |
| `method` | enum | `regression` | Or `bartlett` (ML fit only). |
| `model` | string | — | By label; absent: `F1 =~` all. |
| `meanstructure` | bool | `false` | Latent-mean scale. |
| `group` | string | — | Multi-group fit. |
| `group_reference` | string | — | |
| `group_equal` | list | — | |
| `group_partial` | list | — | |
| `solver` | enum | `slsqp` | Or `fisher`. |
| `information` | enum | `dense` | |
| `ftol` | float | 1e-06 | |
| `max_iter` | int | — | |
| `grad_tol` | float | 1e-10 | |
| `singular` | enum | `nearest` | Or `refuse`. |
| `estimator` | enum | `ml` | `dwls` / `wlsmv`: EBM. |
| `ordered` | list | — | |
| `parameterization` | enum | `delta` | |
| `fit_on` | any | `filtered` | Picks FIT rows. |
| `fit` | string | — | Compose / `.pulse-sem.json`; no knobs. |
| `labels` | list | — | |
| `missing` | enum | `listwise` | Only. |

The fit knobs are<!-- feature: MAT_SEM --> `MAT_SEM`'s<!-- /feature -->; no bootstrap. `weight` — frequency only, fit only.

## Inputs

`vector` or `fields` (no `field`); `group` is never a member.

## Output

One `f64` per record: latent-mean scale with a mean structure (or `group`), else centered at the fit rows. Null member or group → null.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Fit is cohort-wide, never per request group (two-pass; `fit`: row-local).
- A fit-less level, or an unseen ordered value, scores null + `PULSE_SEM_SCORE_LEVEL_UNSEEN`.
- All null + warning: `PULSE_MATRIX_INSUFFICIENT_N`, `_SINGULAR`, `_NOT_IDENTIFIED`; scored + warning: `_NOT_CONVERGED`, `_NEAREST_PD`.

## See

- `pulse_examples_search tags=[multivariate, comparison]`
- Skills: `sem-fits`, `sem-modeling`, `sem-ordinal`, `op-mat-sem`
