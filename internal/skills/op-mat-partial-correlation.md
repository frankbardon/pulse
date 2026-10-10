---
name: op-mat-partial-correlation
description: Partial correlation matrix of a vector's numeric members — each pair with every other member, or the params.control fields, held fixed — listwise or pairwise, weighted; non-PSD input refused unless repair "nearest", singular input refused.
kind: operator
category: MAT
operator: MAT_PARTIAL_CORRELATION
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

Slot and members as for `op-mat-covariance`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `control` | `"all"` \| [fields] | `all` | Held-fixed set; a listed member leaves the axis, an outside field joins the fold. |
| `repair` | `nearest` | none | Repair non-PSD input. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As `op-mat-correlation`. |

## Inputs

Integer / float members and controls.

## Output

`primary`: partial r over the non-control members, diagonal 1; all null when an input r is undefined. Pairwise `auxiliary.n`; `warnings`. No p-values.

<!-- generated: reading-the-output -->

## Components

Over members + outside controls: `n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`<!-- feature: capability:weighting -->; weighted `sum_weights`, `n_eff`, `n_weight_invalid`<!-- /feature -->.

## Gotchas

- Non-PSD pairwise input: FATAL `PULSE_MATRIX_NOT_PSD`; `repair: "nearest"` (Higham) warns with `frobenius_adjustment`.
- Collinear fields: `PULSE_MATRIX_SINGULAR` (`dependent_fields`).

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `matrix-results`, `op-mat-correlation`, `weighting`
