---
name: tool-sample
kind: tool
description: Return up to N rows from a cohort for eyeball / preview.
type: reference
applies_to: sample, mcp
---

## When to use

Diagnostic / preview tool. After running a request when you want to inspect the underlying data, or before authoring a filter when you need to see typical row shapes. NOT a query interface — use `pulse_process` for selection / aggregation.

## Input

- `path` (string, required): filesystem path to the `.pulse` file.
- `count` (number, optional): maximum rows to return. Default `10`.

## Output

`descriptor.Envelope` wrapping a row slice — each row is a map keyed by field name with values decoded according to the schema. Categorical fields surface display labels by default; null fields appear as `null`. `Response.Components.Run` reports `total_records` and `filtered_records` (always equal under sample). See `response-components`.

## Gotchas

- Reads from the head of the cohort — NOT random sampling. For randomized sampling use a `pulse_process` request with a sampling filter.
- Shard archives: `path` may be either the archive or `archive.pulse#shard.pulse` anchor. Sample reads from the resolved cohort's first records.
- `count <= 0` or non-numeric returns the default 10.

## See

- `request-envelope` — envelope shape.
- `response-components` — `Run` counters surfaced even on sample.
- `tool-facet` / `tool-facet-schema` — per-field discovery alternatives.
