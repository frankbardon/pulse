```yaml
name: op-overlay-t-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_T_VS_PRIOR
description: Welch t-test of each period's mean against the period before it on an ordered SERIES host or a crosstab with no column grouper; independent samples only.
type: reference
applies_to: process
examples_tags: [overlay, time-series, hypothesis-test, two-sample, welch, welford-triple]
```

Inferential prior-period test. Overlays decorate the host; no `Response.Components`.

## Use when

Welch t-test of each period against the period before it: did the average really change from the last wave, or is it noise?

Questions it answers:

- Did the average satisfaction score change significantly from last wave?
- Which months' mean order value moved more than chance from the month before?

Use something else:

- `OVERLAY_PROP_Z_VS_PRIOR` when the measure is a share of respondents (a 0/1 indicator).

## Params

`Scope` must be `group`. `Ref.Prior` (empty `Ref` accepted; lag 1 only); other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0` (`PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`). `params.field` / `params.partition_by` as on the delta twin (unknown → `PULSE_OVERLAY_FIELD_UNKNOWN` / `PULSE_OVERLAY_PARTITION_INVALID`).

- `multiplicity` — optional `{method, family, alpha}`; adds per-entry `p_adjusted` + `significant_adjusted`, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

Two Request hosts: an ordered grouped Process (SERIES; partitions as the delta twin) or a crosstab with NO `columns` — its rows are the periods of the implicit Total column. A crosstab with columns, or several measures, → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`reason: column_grouper`). The scored measure must carry the Welford triple {mean, variance, n} (`AGG_WELFORD`, `AGG_VARIANCE`, `AGG_STDDEV`), else the same code with `measure_type`. Legs `{n, mean, variance}` come from the measure's per-group (or per-cell) components: components disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`. Predict raises every refusal with the runtime's code.

## Output

SERIES — one `SeriesEntry` per period in host order: `Summary.Statistic` = Welch t of this period minus the prior, `Summary.PValue` its two-sided p. Layer `Summary`: `count` (periods tested), `min` / `max` of the statistic.

## Reading the output

- `summary.statistic`: In each series entry (one per period): the Welch t statistic of this period minus the period before it, in standard errors. Null on the first period and on a test that could not be computed; read the entry's p_value for the test.
  - Sign: positive means this period is higher than the one before; negative means this period is lower than the one before.
- `summary.p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- **Independence assumed.** The two periods must be separate samples; a panel (same respondents both waves) makes the p-value wrong — Pulse cannot detect it.
- First period → `statistic` null, no `p_value` (null, never 0), not a multiplicity member.
- A missing period keeps its key with no statistic and the carrier does not advance.
- Degenerate pair (`n < 2`, `se == 0`) → NaN entry; one `PULSE_OVERLAY_REF_ZERO` warning per skip reason (`details.reason` a `PULSE_STATS_SKIP_*` code, `skipped`).
- Weighted: N* = `sum_weights` (frequency) or `n_eff` (probability), variance from `m2`.
- Buffered (not streamable): inferential policy, reads components.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-delta-vs-prior`](op-overlay-delta-vs-prior.md), [`op-overlay-z-vs-prior`](op-overlay-z-vs-prior.md), [`op-overlay-prop-z-vs-prior`](op-overlay-prop-z-vs-prior.md).
