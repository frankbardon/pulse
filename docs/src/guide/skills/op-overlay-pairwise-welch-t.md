```yaml
name: op-overlay-pairwise-welch-t
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_WELCH_T
description: Intra-matrix axis-pairwise Welch–Satterthwaite t-test on AGG_WELFORD cells.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise, welford-triple]
```

Welch–Satterthwaite t on MEANS between host slots ALONG one axis of the SAME crosstab. Shared pairwise contract (spec, grid, letters, skips, moment-cell hosts, weighted legs): [`overlay-pairwise`](overlay-pairwise.md).

## Use when

Welch t-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean?

Questions it answers:

- Which age bands differ from each other in average spend, product by product?
- Which pairs of depots differ in average delivery time per month?

Use something else:

- `OVERLAY_PAIRWISE_TWO_MEANS_Z` when every cell is large and you want the normal-curve version.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pair_along_dim` | int | — | Restrict pairs to same-bucket comparisons on the pair axis. |
| `n_within_depth` | int | `0` | Accepted and inert here (n comes from the Welford triple). |
| `letters` | object | — | `{}` or `{"alpha": a}`, `0 < a < 1`: adds the `letters` part. |

`Scope` (required) — `row` or `column`. `Ref` empty. `n_source` / `p_source` are NOT accepted: predict refuses both (`PULSE_OVERLAY_PARAM_MISSING`), every mode; runtime leaves them inert.

## Host shape

MATRIX crosstab whose **cell aggregator is `AGG_WELFORD`** + `Response.Components.Crosstab`, read for the `{mean, variance, n}` triple per cell. A cell without `mean` + `variance` components → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime. A population-variance cell is converted to the sample variance ([`overlay-pairwise`](overlay-pairwise.md)).

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values (layout: [`overlay-pairwise`](overlay-pairwise.md)). Per pair: `a = v_i/n_i`, `b = v_j/n_j`, `se = sqrt(a + b)`, `t = (m_i - m_j) / se`, `df = (a+b)² / (a²/(n_i-1) + b²/(n_j-1))`, two-sided via the `statdist.StudentTTwoSidedP` helper the t-tests use. `letters`: `higher` from the sign of `t`.

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each mean should be roughly normal: safe for big cells, risky for small skewed ones.
- `parts.letters.cells.value`: Significance letters for this host cell: its column's (or row's) letter, and in above the letters of the same-group columns it is significantly higher than here.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `row`, `column`, `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Either leg with `n <= 1`, or a `null` Welford variance (n < 2 / Σw ≤ 1), skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`) — never scored off a 0 variance.
- Weighted host: legs read N* (`sum_weights` frequency, `n_eff` probability), variance from `m2` on w*, df on N*.
- The HOST is buffered too: the Welford cell is non-mergeable, so the crosstab never fuses (`TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays`).

## See

- Skills: [`overlay-pairwise`](overlay-pairwise.md), [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-pairwise-two-means-z`](op-overlay-pairwise-two-means-z.md), [`op-agg-welford`](op-agg-welford.md), [`op-test-welch`](op-test-welch.md).
