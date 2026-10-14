---
name: op-overlay-zscore-vs-total
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_TOTAL
description: Per-group streamable z-score against the SERIES host's grand-total distribution (population SD).
type: reference
applies_to: process
examples_tags: [overlay, outlier-detection, streaming-friendly]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` required, must be `group`. `Ref` empty (implicit grand-total; populated → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`). `Level` / `Within` must be `0`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES grouped Process host. Over two or more `groups` the mean and SD are each partition's own — the rows sharing the leading keys `params.partition_by` names (default all but the last; `[]` = every row), matched by key equality. Streamable alongside the total index and grand-total share.

## Output

SERIES — one `SeriesEntry` per host group key in host order carrying `z = (group_val - mean) / sd` on `Summary.Statistic`, `sd = sqrt(M2 / N)` (**POPULATION SD**). Layer `Baseline = 0`.

<!-- generated: reading-the-output -->

## Gotchas

- **POPULATION SD**: the per-group set IS the standardisation target. Contrast the rolling z-score (sample SD, n-1); matches the margin z-score's and the row z-score attribute's denominator — but the variance is across GROUPS, not raw records.
- `sd == 0` (all groups equal, all zero, single group) → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer. Absent host group → unset entry, no Welford contribution.
- Streamable — the Welford `(count, mean, M2)` triple rides the streaming fold, byte-equal within ULP across every path.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-total`, `op-overlay-zscore-vs-rolling`.
