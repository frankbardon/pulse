---
name: op-mat-partial-correlation
description: Partial correlation matrix of a vector's numeric members — each pair with every other member, or the params.control fields, held fixed — listwise or pairwise, weighted; non-PSD input refused unless repair "nearest", singular input refused; p-values and confidence intervals on request (params.inference).
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
| `control` | `"all"` \| [fields] | `all` | Held-fixed set; a listed member leaves the axis. |
| `repair` | `nearest` | none | Repair non-PSD input. |
| `inference` | bool | `false` | `auxiliary.p` / `ci_lower` / `ci_upper`, df = N* − 2 − g. |
| `alpha` | (0, 1) | `0.05` | Interval level; needs `inference`. |
| `missing`, `max_drop_share`, `fit`, `weight`, `encoding` | | | As `op-mat-correlation`. |

<!-- feature: capability:multiplicity -->
Spec `multiplicity` implies `inference` and adds `auxiliary.p_adjusted` / `significant_adjusted` over the distinct pairs, plus a `multiplicity` echo; family `matrix` (default) or `request`. Raw `p` untouched (`multiplicity-correction`).
<!-- /feature -->

## Inputs

Integer / float members and controls.

## Output

`primary`: partial r over the non-control members, diagonal 1. Pairwise `auxiliary.n`. `inference`: per pair p (t, = ppcor) and Fisher-z interval; diagonal null.

<!-- generated: reading-the-output -->

## Components

Over members + outside controls: `n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`<!-- feature: capability:weighting -->; weighted `sum_weights`, `n_eff`, `n_weight_invalid`<!-- /feature -->.

## Gotchas

- Non-PSD pairwise input: FATAL `PULSE_MATRIX_NOT_PSD`; `repair: "nearest"` (Higham) warns with `frobenius_adjustment`.
- Collinear fields: `PULSE_MATRIX_SINGULAR` (`dependent_fields`).
- `inference` N*: listwise the fold's, pairwise the smallest pair's (`PULSE_MATRIX_PAIRWISE_N_STAR`); too thin ⇒ null + `PULSE_MATRIX_INSUFFICIENT_N`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `matrix-results`, `op-mat-correlation`, `weighting`
