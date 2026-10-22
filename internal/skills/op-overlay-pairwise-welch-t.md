---
name: op-overlay-pairwise-welch-t
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_WELCH_T
description: Intra-matrix axis-pairwise Welch–Satterthwaite t-test on AGG_WELFORD cells.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise, welford-triple]
---

Intra-matrix pairwise on MEANS along one axis of the SAME crosstab: `row` scope pairs row indices per column, `column` pairs column indices per row. Overlays decorate the host; no `Response.Components` (this family READS them).

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pair_along_dim` | int | — | Restrict pairs to same-bucket comparisons on the pair axis. |
| `n_within_depth` | int | `0` | With a within-prefix n_source, the pair-axis dim positions (depth + 1) the n denominator is summed within; inert otherwise. |
| `letters` | object | — | `{}` or `{"alpha": a}`, `0 < a < 1` (else `PULSE_OVERLAY_PARAM_MISSING`, predict and runtime): adds the `letters` part. |

`Scope` (enum, required) — `row` or `column`. `Ref` (object, empty) — intra-matrix — leave empty.

`n_source` / `p_source` are NOT accepted — n, mean and variance all come from the Welford triple, so either would be a silent no-op. Predict refuses both (`PULSE_OVERLAY_PARAM_MISSING`), for EVERY mode and not just the distinct-key ones; runtime does not, the param being inert (weighted host: see Gotchas). `n_within_depth` stays accepted and inert. Detail: `pairwise-n-sources`.

## Host shape

MATRIX crosstab whose **cell aggregator is `AGG_WELFORD`** + `Response.Components.Crosstab`, from which it reads the `{mean, variance, n}` triple per cell. Non-Welford host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, predict and runtime alike: predict reads the cell type's declared `ComponentSchema` (built-in or extension) and refuses one without `mean` + `variance` (a sum or mode cell, …); components-disabled (options or request) → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime. The test reads the SAMPLE variance m2 / (n − 1): an unweighted<!-- feature: AGG_VARIANCE --> `AGG_VARIANCE`<!-- /feature --><!-- feature: AGG_STDDEV --> `AGG_STDDEV`<!-- /feature --> cell (population m2 / n) is converted, so it scores as `AGG_WELFORD` (to the accumulators' last-bit rounding); an extension cell is read as is and must expose the sample variance.

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values<!-- feature: OVERLAY_PAIRWISE_PROP_Z --> (layout as `op-overlay-pairwise-prop-z`)<!-- /feature -->. Per pair: `a = v_i/n_i`, `b = v_j/n_j`, `se = sqrt(a + b)`, `t = (m_i - m_j) / se`, `df = (a+b)² / (a²/(n_i-1) + b²/(n_j-1))` (Welch–Satterthwaite), two-sided via the `statdist.StudentTTwoSidedP` helper the t-tests use.

`payload.parts.letters` (with `params.letters`): per host cell of a compared index `{letter, group, higher, above}` — its letter (restarting per `pair_along_dim` group), the letters its mean beats (sign of `t`) and the significant subset (raw p < `0.05` or `alpha`<!-- feature: capability:multiplicity -->; `significant_adjusted` under a correction<!-- /feature -->). The implicit Total column gets none.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Weighted host (both kinds, any source): legs read N* = `sum_weights` (frequency) / `n_eff` (probability), variance from `m2` on w*, df on N*; `Summary.Parameters` adds `sum_weights` (+ `n_eff`). There an unweighted-count `n_source` (and a weight-sum one under probability) is `PROCESSING_CONFIG` in predict AND runtime (`pairwise-n-sources`).
- Either leg with `n <= 1`, or an undefined (`null`) Welford variance (n < 2 / Σw ≤ 1), skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`) — never scored off a 0 variance.
- The primary stays RAW p-values; direction and significance ride `letters` only when asked.
- Buffered (inferential) — and so is the HOST: the `AGG_WELFORD` cell is non-mergeable, so `CanFuseCrosstab` rejects on the cell-aggregator arm. Expected (`TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays`).

## See

- Skills: `overlay-system`, `pairwise-n-sources`, `crosstab-guide`, `op-overlay-pairwise-two-means-z`, `op-agg-welford`, `op-test-welch`.
