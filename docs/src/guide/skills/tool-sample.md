```yaml
name: tool-sample
kind: tool
description: Return up to N rows from a cohort for eyeball / preview.
type: reference
applies_to: sample, mcp
```

## When to use

CALL TO EYEBALL A FEW RAW ROWS OF A COHORT.
After running a request, to inspect the underlying data, or before authoring a filter, to see typical row shapes. NOT a query interface: selection and aggregation belong to request execution.

## Input

- `path` (string): filesystem path to the `.pulse` file. Required unless `union` is set; never both.
- `count` (integer, optional): maximum rows to return. Default `10`; `0` or less also means 10.
- `union` (`[{cohort, label}]`, optional): several cohorts sampled as one, rows in member order, each carrying its member label in `union_source_field` (default `_source`). See [`cohort-union`](cohort-union.md).

## Output

`{rows: [...]}` (no envelope); each row maps field name to its decoded value. Categorical fields show their labels, `packed_bool` `0` / `1`, `decimal128` a decimal string at the field's scale, `date` `YYYY-MM-DD` and `datetime` RFC 3339 UTC (`2026-01-02T03:04:05Z`) — never epoch numbers, no zone applied; `u64` the exact integer, even above 2^53; `vec_*` an array. A null field is omitted from its row.

## Gotchas

- Reads from the head of the cohort — NOT random sampling. For a random subset run a request with a sampling filter.
- Shard archives: `path` may be the archive or an `archive.pulse#shard.pulse` anchor; rows come from the first records.
- An unknown argument is refused (`PULSE_REQUEST_UNKNOWN_FIELD`), like every tool.

## See

- [`tool-facet`](tool-facet.md) / [`tool-facet-schema`](tool-facet-schema.md) — per-field discovery alternatives.
- [`cohort-null-states`](cohort-null-states.md) — what an omitted key means.
