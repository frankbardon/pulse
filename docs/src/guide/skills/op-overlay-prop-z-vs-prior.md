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

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | On a SERIES host, the OUTPUT LABEL of the aggregation to score (explicit label, else TYPE_field) or one expanded element column `"<label>[<element label>]"`; absent, the first aggregation. |
| `partition_by` | list | — | Over two or more groupers, the leading group key columns each partition shares (default every key but the last; [] = one walk over every row; the YoY overlay: exactly the non-date keys). |

`Scope` must be `group`, `Ref.Prior` (or empty; lag 1 only), `Level` / `Within` `0`; refusals and `params.field` / `params.partition_by` as on the prior delta: [`overlay-reference-tests`](overlay-reference-tests.md).

## Host shape

Two Request hosts: an ordered grouped Process (SERIES) or a crosstab with NO `columns` — its rows are the periods of the implicit Total column. A crosstab with columns, or several measures, → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`reason: column_grouper`). The scored measure must be `AGG_AVERAGE` of a 0/1 indicator (any other built-in, a moment measure included → same code, `measure_type`); on a crosstab `normalize` must be none (`normalize` detail). p̂ is the measure's value, n its components' n: components disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

SERIES — one `SeriesEntry` per period in host order: `Summary.Statistic` = pooled two-proportion z of this period minus the prior, `Summary.PValue` its two-sided p. Layer `Summary`: `count`, `min` / `max`.

## Reading the output

- `summary.statistic`: In each series entry (one per period): the two-proportion z statistic of this period minus the period before it, in standard errors. Null on the first period and on a test that could not be computed; read the entry's p_value for the test.
- `summary.p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- **Independence assumed**: a panel (same respondents both waves) makes the p-value wrong, undetected.
- First period → `statistic` null, no `p_value`; a missing period keeps its key, the carrier does not advance ([`overlay-reference-tests`](overlay-reference-tests.md)).
- n of 0, a share outside [0, 1] or a pooled share of 0 / 1 → NaN entry; one `PULSE_OVERLAY_REF_ZERO` per skip reason (`details.reason`).
- Weighted: p̂ is the weighted mean, n = `sum_weights` (frequency) or Kish `n_eff` (probability).

## See

- Skills: [`overlay-reference-tests`](overlay-reference-tests.md), [`overlay-system`](overlay-system.md), [`op-overlay-delta-vs-prior`](op-overlay-delta-vs-prior.md), [`op-overlay-t-vs-prior`](op-overlay-t-vs-prior.md), [`op-overlay-pairwise-prop-z`](op-overlay-pairwise-prop-z.md).
