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

`Scope` must be `group`. `Ref.Prior` (empty `Ref` accepted; lag 1 only); other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0` (`PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`). `params.field` / `params.partition_by` as on the delta twin (unknown → `PULSE_OVERLAY_FIELD_UNKNOWN` / `PULSE_OVERLAY_PARTITION_INVALID`).


## Host shape

Two Request hosts: an ordered grouped Process (SERIES; partitions as the delta twin) or a crosstab with NO `columns` — its rows are the periods of the implicit Total column. A crosstab with columns, or several measures, → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`reason: column_grouper`). The scored measure must carry the Welford triple {mean, variance, n} (<!-- feature: AGG_WELFORD -->`AGG_WELFORD`<!-- /feature --><!-- feature: AGG_VARIANCE -->, `AGG_VARIANCE`<!-- /feature --><!-- feature: AGG_STDDEV -->, `AGG_STDDEV`<!-- /feature -->), else the same code with `measure_type`. Legs `{n, mean, variance}` come from the measure's per-group (or per-cell) components: components disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`. Predict raises every refusal with the runtime's code.

## Output

SERIES — one `SeriesEntry` per period in host order: `Summary.Statistic` = z of this period minus the prior, `Summary.PValue` its two-sided normal p — fair only when every period is large. Layer `Summary`: `count` (periods tested), `min` / `max` of the statistic.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- **Independence assumed.** The two periods must be separate samples; a panel (same respondents both waves) makes the p-value wrong — Pulse cannot detect it.
- First period → `statistic` null, no `p_value` (null, never 0)<!-- feature: capability:multiplicity -->, not a multiplicity member<!-- /feature -->.
- A missing period keeps its key with no statistic and the carrier does not advance.
- Degenerate pair (`n < 2`, `se == 0`) → NaN entry; one `PULSE_OVERLAY_REF_ZERO` warning per skip reason (`details.reason` a `PULSE_STATS_SKIP_*` code, `skipped`).
- Weighted: N* = `sum_weights` (frequency) or `n_eff` (probability), variance from `m2`.

## See

- Skills: `overlay-system`, `op-overlay-delta-vs-prior`, `op-overlay-t-vs-prior`, `op-overlay-prop-z-vs-prior`.
