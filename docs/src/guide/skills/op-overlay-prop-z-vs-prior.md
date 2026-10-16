```yaml
name: op-overlay-prop-z-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_PROP_Z_VS_PRIOR
description: Two-proportion z-test of each period's share against the period before it on an ordered SERIES host or a crosstab with no column grouper; independent samples only.
type: reference
applies_to: process
examples_tags: [overlay, time-series, hypothesis-test, two-sample, proportion-analysis]
```

Inferential prior-period test of a share. Overlays decorate the host; no `Response.Components`.

## Use when

Two-proportion z-test of each period's share against the period before it: did awareness (or any yes/no share) change since last wave?

Questions it answers:

- Did brand awareness change significantly from last wave?
- Which months' conversion rate moved from the month before beyond chance?

Use something else:

- `OVERLAY_T_VS_PRIOR` when the measure is an average of a scale, not a 0/1 share.

## Params

`Scope` must be `group`. `Ref.Prior` (empty `Ref` accepted; lag 1 only); other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0` (`PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`). `params.field` / `params.partition_by` as on the delta twin (unknown → `PULSE_OVERLAY_FIELD_UNKNOWN` / `PULSE_OVERLAY_PARTITION_INVALID`).

- `multiplicity` — optional `{method, family, alpha}`; adds per-entry `p_adjusted` + `significant_adjusted`, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

Two Request hosts: an ordered grouped Process (SERIES) or a crosstab with NO `columns` — its rows are the periods of the implicit Total column. A crosstab with columns, or several measures, → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`reason: column_grouper`). The scored measure must be `AGG_AVERAGE` of a 0/1 indicator (another built-in → same code, `measure_type`); on a crosstab `normalize` must be none (`normalize` detail). p̂ is the measure's value, n its components' n: components disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`. Predict raises every refusal with the runtime's code.

## Output

SERIES — one `SeriesEntry` per period in host order: `Summary.Statistic` = pooled two-proportion z of this period minus the prior, `Summary.PValue` its two-sided p. Layer `Summary`: `count` (periods tested), `min` / `max` of the statistic.

## Reading the output

- `summary.statistic`: In each series entry (one per period): the two-proportion z statistic of this period minus the period before it, in standard errors. Null on the first period and on a test that could not be computed; read the entry's p_value for the test.
- `summary.p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- **Independence assumed.** The two periods must be separate samples; a panel (same respondents both waves) makes the p-value wrong — Pulse cannot detect it.
- First period → `statistic` null, no `p_value` (null, never 0), not a multiplicity member.
- A missing period keeps its key with no statistic and the carrier does not advance.
- n of 0, a share outside [0, 1] or a pooled share of 0 / 1 → NaN entry; one `PULSE_OVERLAY_REF_ZERO` warning per skip reason (`details.reason` a `PULSE_STATS_SKIP_*` code, `skipped`).
- Weighted: p̂ is the weighted mean, n = `sum_weights` (frequency) or Kish `n_eff` (probability).
- Buffered (not streamable): inferential policy, reads components.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-delta-vs-prior`](op-overlay-delta-vs-prior.md), [`op-overlay-t-vs-prior`](op-overlay-t-vs-prior.md), [`op-overlay-pairwise-prop-z`](op-overlay-pairwise-prop-z.md).
