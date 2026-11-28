---
name: op-overlay-pairwise-two-means-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_TWO_MEANS_Z
description: Intra-matrix axis-pairwise two-means z-test on AGG_WELFORD cells (normal-CDF tail, no df).
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise, welford-triple]
---

Normal-CDF two-means z on MEANS between host slots ALONG one axis of the SAME crosstab — sibling of the pairwise Welch t: same standard error, no Satterthwaite df. Shared pairwise contract (spec, grid, letters, skips, moment-cell hosts): `overlay-pairwise`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pair_along_dim` | int | — | Restrict pairs to same-bucket comparisons on the pair axis. |
| `n_within_depth` | int | `0` | Not read here (n comes from the Welford triple): non-zero → `PULSE_OVERLAY_PARAM_MISSING`. |
| `letters` | object | — | `{}` or `{"alpha": a}`, `0 < a < 1`: adds the `letters` part. |

`scope` (required) — `row` or `column`. `ref` empty. `n_source` / `p_source` are NOT accepted: predict refuses both (`PULSE_OVERLAY_PARAM_MISSING`), every mode; runtime leaves them inert. `n_basis` is inert; predict refuses it.

## Host shape

MATRIX crosstab whose **cell aggregator is `AGG_WELFORD`** + `components.crosstab`, read for the `{mean, variance, n}` triple per cell. A cell without `mean` + `variance` components → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime. Multi-measure host (`cells`): a measure without the triple is refused ALONE — its rows null, a same-code warning (`details.measure`); every measure refused → the layer refuses. A population-variance cell is converted to the sample variance (`overlay-pairwise`).

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values (layout: `overlay-pairwise`). Per pair: `a = v_i/n_i`, `b = v_j/n_j`, `se = sqrt(a + b)`, `z = (m_i - m_j) / se`, `p = 2 * Φ(-|z|)`, as the two-sample z-test. `letters`: `higher` from the sign of `z`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Either leg with `n <= 1`, or a `null` Welford variance (n < 2), skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`) — never scored off a 0 variance.
- Assumes the normal approximation: no small-sample df correction.
- The HOST is buffered too: the Welford cell is non-mergeable, so the crosstab never fuses.

## See

- Skills: `overlay-pairwise`, `overlay-system`, `crosstab-guide`, `op-overlay-pairwise-welch-t`, `op-overlay-pairwise-weighted-two-means-z`, `op-agg-welford`, `op-test-z-two-sample`.
