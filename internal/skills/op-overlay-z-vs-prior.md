---
name: op-overlay-z-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_Z_VS_PRIOR
description: Large-sample z-test of each period's mean against the period before it on an ordered SERIES host or a crosstab with no column grouper; independent samples only.
type: reference
applies_to: process
examples_tags: [overlay, time-series, hypothesis-test, two-sample, z, welford-triple]
---

Inferential prior-period test. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | On a SERIES host, the OUTPUT LABEL of the aggregation to score (explicit label, else TYPE_field) or one expanded element column "<label>[<element label>]"; absent, the first aggregation. |
| `partition_by` | list | — | Over two or more groupers, the leading group key columns each partition shares (default every key but the last; [] = one walk over every row; the YoY overlay: exactly the non-date keys). |

`Scope` must be `group`, `Ref.Prior` (or empty; lag 1 only), `Level` / `Within` `0`; refusals and `params.field` / `params.partition_by` as on the prior delta: `overlay-reference-tests`.

## Host shape

Two Request hosts: an ordered grouped Process (SERIES; partitions as the delta twin) or a crosstab with NO `columns` — its rows are the periods of the implicit Total column. A crosstab with columns, or several measures, → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`reason: column_grouper`). The scored measure must carry the Welford triple {mean, variance, n} (<!-- feature: AGG_WELFORD -->`AGG_WELFORD`<!-- /feature --><!-- feature: AGG_VARIANCE -->, `AGG_VARIANCE`<!-- /feature --><!-- feature: AGG_STDDEV -->, `AGG_STDDEV`<!-- /feature -->), else the same code with `measure_type`. Legs `{n, mean, variance}` come from the measure's per-group (or per-cell) components: components disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

SERIES — one `SeriesEntry` per period in host order: `Summary.Statistic` = z of this period minus the prior, `Summary.PValue` its two-sided normal p — fair only when every period is large. Layer `Summary`: `count`, `min` / `max`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- **Independence assumed**: a panel (same respondents both waves) makes the p-value wrong, undetected.
- First period → `statistic` null, no `p_value`; a missing period keeps its key, the carrier does not advance (`overlay-reference-tests`).
- Degenerate pair (`n < 2`, `se == 0`) → NaN entry; one `PULSE_OVERLAY_REF_ZERO` per skip reason (`details.reason`).
- Weighted: N* = `sum_weights` (frequency) or `n_eff` (probability), variance from `m2`.

## See

- Skills: `overlay-reference-tests`, `overlay-system`, `op-overlay-delta-vs-prior`, `op-overlay-t-vs-prior`, `op-overlay-prop-z-vs-prior`.
