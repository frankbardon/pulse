---
name: op-overlay-prop-z-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_PROP_Z_VS_PRIOR
description: Two-proportion z-test of each period's share against the period before it on an ordered SERIES host or a crosstab with no column grouper; independent samples only.
type: reference
applies_to: process
examples_tags: [overlay, time-series, hypothesis-test, two-sample, proportion-analysis]
---

Inferential prior-period test of a share. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | On a SERIES host, the OUTPUT LABEL of the aggregation to score (explicit label, else TYPE_field) or one expanded element column "<label>[<element label>]"; absent, the first aggregation. |
| `partition_by` | list | — | Over two or more groupers, the leading group key columns each partition shares (default every key but the last; [] = one walk over every row; the YoY overlay: exactly the non-date keys). |

`Scope` must be `group`, `Ref.Prior` (or empty; lag 1 only), `Level` / `Within` `0`; refusals and `params.field` / `params.partition_by` as on the prior delta: `overlay-reference-tests`.

## Host shape

Two Request hosts: an ordered grouped Process (SERIES) or a crosstab with NO `columns` — its rows are the periods of the implicit Total column. A crosstab with columns, or several measures, → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`reason: column_grouper`). The scored measure must be `AGG_AVERAGE` of a 0/1 indicator (any other built-in, a moment measure included → same code, `measure_type`); on a crosstab `normalize` must be none (`normalize` detail). p̂ is the measure's value, n its components' n: components disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

SERIES — one `SeriesEntry` per period in host order: `Summary.Statistic` = pooled two-proportion z of this period minus the prior, `Summary.PValue` its two-sided p. Layer `Summary`: `count`, `min` / `max`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- **Independence assumed**: a panel (same respondents both waves) makes the p-value wrong, undetected.
- First period → `statistic` null, no `p_value`; a missing period keeps its key, the carrier does not advance (`overlay-reference-tests`).
- n of 0, a share outside [0, 1] or a pooled share of 0 / 1 → NaN entry; one `PULSE_OVERLAY_REF_ZERO` per skip reason (`details.reason`).
- Weighted: p̂ is the weighted mean, n = `sum_weights` (frequency) or Kish `n_eff` (probability).

## See

- Skills: `overlay-reference-tests`, `overlay-system`, `op-overlay-delta-vs-prior`, `op-overlay-t-vs-prior`, `op-overlay-pairwise-prop-z`.
