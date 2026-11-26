```yaml
name: tool-facet-schema
kind: tool
description: Multi-field rich facet — counts, nulls, percentiles, histograms, additive contributions.
type: reference
applies_to: facet, mcp
```

## When to use

CALL TO SUMMARISE SEVERAL FIELDS AT ONCE WITH COUNTS, NULLS, STATS OR HISTOGRAMS.
Also top-K truncation and additive-contribution counts: the faceted-UI / discovery sibling to `pulse_facet`. For "if I added value V to my filter on F, how many records survive", list F in `additive_fields`.

## Input

The facet request's keys at top level (no wrapper): `cohort` (`{filename}`, or `union` instead), `fields` (`string[]`, required), `filterers`, `additive_fields`, `discrete_top_k`, `numeric_percentiles` (`float[]`, `0.5` → `p50`), `include_histogram` + `histogram_bins` + `histogram_range` (`[min, max]`), `labels`, `time_zone` / `time_zone_field`, `params` (constants expression filters read as `params.<name>`; [`request-params`](request-params.md)), `overlays` (FACET-host kinds).

## Output

The facet result (no envelope): `fields` keyed by name, each `{kind, type_name, description, null_count}` plus `discrete {values: [{value, count}], distinct_count, truncated_at}` (categorical / boolean / set fields; a `u4`–`u32` field under an augment label binding counts per code) or `numeric {count, sum, min, max, mean, stddev, percentiles, histogram {min, max, bins}}`; `additive` keyed the same way; `total_records`, `filtered_records`; `overlays` (FACET-host layers); `warnings` as coded `{code, message, details}` (truncation `PULSE_FACET_VALUES_TRUNCATED`, a stat asked of a non-numeric field, labels, overlays).

## Gotchas

- `numeric_percentiles` buffers each numeric field for a sort — not streaming.
- `include_histogram` needs `histogram_range` with min < max, else `SERVICE_VALIDATION`.
- `additive_fields` drops that field's own filter clauses before counting, so each value reports the records it would add.
- A percentile or histogram asked of a non-numeric field is ignored with a warning, not refused.

## See

- [`facet-design`](facet-design.md) — full endpoint contract + overlay wiring.
- [`overlay-system`](overlay-system.md) — FACET-host overlay kinds.
- [`tool-facet`](tool-facet.md) — one field's distinct values.
