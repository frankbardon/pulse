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

`Scope` must be `group`. `Ref.Prior` (empty `Ref` accepted; lag 1 only); other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0` (`PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`). `params.field` / `params.partition_by` as on the delta twin (unknown → `PULSE_OVERLAY_FIELD_UNKNOWN` / `PULSE_OVERLAY_PARTITION_INVALID`).

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family, alpha}`; adds per-entry `p_adjusted` + `significant_adjusted`, raw p untouched (`multiplicity-correction`).
<!-- /feature -->

## Host shape

Two Request hosts: an ordered grouped Process (SERIES) or a crosstab with NO `columns` — its rows are the periods of the implicit Total column. A crosstab with columns, or several measures, → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`reason: column_grouper`). The scored measure must be `AGG_AVERAGE` of a 0/1 indicator (another built-in → same code, `measure_type`); on a crosstab `normalize` must be none (`normalize` detail). p̂ is the measure's value, n its components' n: components disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`. Predict raises every refusal with the runtime's code.

## Output

SERIES — one `SeriesEntry` per period in host order: `Summary.Statistic` = pooled two-proportion z of this period minus the prior, `Summary.PValue` its two-sided p. Layer `Summary`: `count` (periods tested), `min` / `max` of the statistic.

<!-- generated: reading-the-output -->

## Gotchas

- **Independence assumed.** The two periods must be separate samples; a panel (same respondents both waves) makes the p-value wrong — Pulse cannot detect it.
- First period → `statistic` null, no `p_value` (null, never 0)<!-- feature: capability:multiplicity -->, not a multiplicity member<!-- /feature -->.
- A missing period keeps its key with no statistic and the carrier does not advance.
- n of 0, a share outside [0, 1] or a pooled share of 0 / 1 → NaN entry; one `PULSE_OVERLAY_REF_ZERO` warning per skip reason (`details.reason` a `PULSE_STATS_SKIP_*` code, `skipped`).
- Weighted: p̂ is the weighted mean, n = `sum_weights` (frequency) or Kish `n_eff` (probability).
- Buffered (not streamable): inferential policy, reads components.

## See

- Skills: `overlay-system`, `op-overlay-delta-vs-prior`, `op-overlay-t-vs-prior`, `op-overlay-pairwise-prop-z`.
