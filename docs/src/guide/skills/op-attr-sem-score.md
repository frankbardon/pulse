```yaml
name: op-attr-sem-score
description: Per-row score on one latent of a structural equation model fitted inline with the SEM matrix operator's knobs; lavaan's regression or Bartlett scores (empirical Bayes modal on an ordinal fit), each group scored with its own group's parameters; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_SEM_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, feature-engineering, comparison]
```

Row scalars; no `Response.Components`.

## Use when

Adds to every row its score on one hidden quality of a structural equation model you write, estimated from the measures it drives.

Questions it answers:

- What is each respondent's perceived-value score under our brand model, to crosstab it by segment?
- Can every row carry its service-quality score, with each survey wave scored by its own wave's fit, to track the shift?

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `latent` | string | — | Absent: the only. |
| `method` | enum | `regression` | Or `bartlett` (ML only). |
| `model` | string | — | By label; absent: `F1 =~` all. |
| `meanstructure` | bool | `false` | |
| `group` | string | — | Multi-group fit. |
| `group_reference` | string | — | |
| `group_equal` | list | — | |
| `group_partial` | list | — | |
| `group_levels` | array | — | [`sem-groups`](sem-groups.md). |
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
| `fit` | string | — | Compose / `.pulse-sem.json`. |
| `labels` | list | — | |
| `missing` | enum | `listwise` | |

The fit knobs are `MAT_SEM`'s; no bootstrap. `weight` — frequency only, fit only.

## Inputs

`vector` or `fields` (no `field`); `group` is never a member.

## Output

One `f64` per record: latent-mean scale with a mean structure (or `group`), else centered at fit rows. Null member / group → null.

## Reading the output

- `value`: The row's estimated score on the chosen latent quality, in the units of its first (marker) measure: higher is toward the end where that measure is high; with a mean structure 0 is the reference group's average.
  - Caveat: A score is an estimate, not the quality itself: regression scores are shrunk toward the group's mean, Bartlett scores are unbiased but noisier.
  - Caveat: Group averages of the scores track the model's latent means only as far as the equality constraints you set make the measures comparable across groups.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Streaming: streamable.

## Gotchas

- Fit is cohort-wide, never per request group (two-pass; `fit`: row-local).
- A fit-less level or unseen ordered value scores null + `PULSE_SEM_SCORE_LEVEL_UNSEEN`.
- Null + warning: `PULSE_MATRIX_INSUFFICIENT_N`, `_SINGULAR`, `_NOT_IDENTIFIED`; scored + warning: `_NOT_CONVERGED`, `_NEAREST_PD`.

## See

- `pulse_examples_search tags=[multivariate, comparison]`
- Skills: [`sem-fits`](sem-fits.md), [`sem-groups`](sem-groups.md), [`sem-modeling`](sem-modeling.md), [`sem-ordinal`](sem-ordinal.md), [`op-mat-sem`](op-mat-sem.md)
