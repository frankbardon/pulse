```yaml
name: op-overlay-zscore-vs-total
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_TOTAL
description: Per-group streamable z-score against the SERIES host's grand-total distribution (population SD).
type: reference
applies_to: process, compose
examples_tags: [overlay, outlier-detection, streaming-friendly]
```

Overlays decorate the host; no `Response.Components`.

## Use when

How many standard deviations each group's value sits from the average of all groups on a grouped result: which groups stand out?

Questions it answers:

- Which stores' sales are unusually high or low compared with the other stores?
- Which regions stand out from the rest on average rating?

Use something else:

- `TEST_ANOVA_WELCH` when you want to test whether group averages differ, from raw rows.

## Params

`Scope` required, must be `group`. `Ref` empty (implicit grand-total; populated → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`). `Level` / `Within` must be `0`.

## Host shape

SERIES grouped Process host. Streamable alongside the total index and grand-total share.

## Output

SERIES — one `SeriesEntry` per host group key in host order carrying `z = (group_val - mean) / sd` on `Summary.Statistic`, `sd = sqrt(M2 / N)` (**POPULATION SD**). Layer `Baseline = 0`.

## Reading the output

- `summary.statistic`: In each series entry: how far the group's value sits from the average of all groups, in standard deviations of the group values (dividing by the number of groups). 0 means the group equals that average.
  - Sign: positive means the value sits above its centre; negative means the value sits below its centre.
  - Caveat: The spread is between groups, not between rows within a group.

## Gotchas

- **POPULATION SD**: the per-group set IS the standardisation target. Contrast the rolling z-score (sample SD, n-1); matches the margin z-score's and the row z-score attribute's denominator — but the variance is across GROUPS, not raw records.
- `sd == 0` (all groups equal, all zero, single group) → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer. Absent host group → unset entry, no Welford contribution.
- Streamable — the Welford `(count, mean, M2)` triple rides the streaming fold, byte-equal within ULP across every path.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-index-vs-total`](op-overlay-index-vs-total.md), [`op-overlay-zscore-vs-rolling`](op-overlay-zscore-vs-rolling.md).
