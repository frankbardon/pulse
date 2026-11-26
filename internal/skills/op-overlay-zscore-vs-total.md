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

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |

`scope` required, must be `group`. `ref` empty (implicit grand-total; populated → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`). `level` / `within` must be `0`.

## Host shape

SERIES grouped Process host. Over two or more `groups` the mean and SD are each partition's own — the rows sharing the leading keys `params.partition_by` names (default all but the last; `[]` = every row), matched by key equality. Streamable alongside the total index and grand-total share.

## Output

SERIES — one entry per host group key in host order, `summary.statistic` = `(group − mean) / sd` across the present groups, `sd` the **POPULATION SD** (÷ N). Layer `summary.baseline` = `0`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- **POPULATION SD**: the per-group set IS the standardisation target. Contrast the rolling z-score (sample SD, n-1); matches the margin z-score's denominator — but the variance is across GROUPS, not raw records.
- `sd == 0` (all groups equal, or a single group) → `null` + ONE `PULSE_OVERLAY_REF_ZERO` per layer. Absent host group → no `statistic`, not in the mean or SD.
- Streamable — the mean and SD accumulate inside the Process fold.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-total`, `op-overlay-zscore-vs-rolling`.
