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

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | On a SERIES host, the OUTPUT LABEL of the aggregation to score (explicit label, else TYPE_field) or one expanded element column "&lt;label&gt;[<element label>]"; absent, the first aggregation. |
| `partition_by` | list | — | Over two or more groupers, the leading group key columns each partition shares (default every key but the last; [] = one walk over every row; the YoY overlay: exactly the non-date keys). |

`Scope` must be `group`, `Ref.Prior` (or empty; lag 1 only), `Level` / `Within` `0`; refusals and `params.field` / `params.partition_by` as on the prior delta: [`overlay-reference-tests`](overlay-reference-tests.md).

## Host shape

Two Request hosts: an ordered grouped Process (SERIES; partitions as the delta twin) or a crosstab with NO `columns` — its rows are the periods of the implicit Total column. A crosstab with columns, or several measures, → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`reason: column_grouper`). The scored measure must carry the Welford triple {mean, variance, n} (`AGG_WELFORD`, `AGG_VARIANCE`, `AGG_STDDEV`), else the same code with `measure_type`. Legs `{n, mean, variance}` come from the measure's per-group (or per-cell) components: components disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

SERIES — one `SeriesEntry` per period in host order: `Summary.Statistic` = Welch t of this period minus the prior, `Summary.PValue` its two-sided p. Layer `Summary`: `count`, `min` / `max`.

## Reading the output

- `summary.statistic`: In each series entry (one per period): the Welch t statistic of this period minus the period before it, in standard errors. Null on the first period and on a test that could not be computed; read the entry's p_value for the test.
  - Sign: positive means this period is higher than the one before; negative means this period is lower than the one before.
- `summary.p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- **Independence assumed**: a panel (same respondents both waves) makes the p-value wrong, undetected.
- First period → `statistic` null, no `p_value`; a missing period keeps its key, the carrier does not advance ([`overlay-reference-tests`](overlay-reference-tests.md)).
- Degenerate pair (`n < 2`, `se == 0`) → NaN entry; one `PULSE_OVERLAY_REF_ZERO` per skip reason (`details.reason`).
- Weighted: N* = `sum_weights` (frequency) or `n_eff` (probability), variance from `m2`.

## See

- Skills: [`overlay-reference-tests`](overlay-reference-tests.md), [`overlay-system`](overlay-system.md), [`op-overlay-delta-vs-prior`](op-overlay-delta-vs-prior.md), [`op-overlay-z-vs-prior`](op-overlay-z-vs-prior.md), [`op-overlay-prop-z-vs-prior`](op-overlay-prop-z-vs-prior.md).
