---
name: tool-lookup
kind: tool
description: Resolve a point lookup against a cohort's prebuilt sidecar index.
type: reference
applies_to: mcp
---

## When to use

O(1) row addressing by key instead of a full scan — when you know the exact key value(s) and want the matching row(s) fast. Requires a sidecar index already built for the target field(s) (via `pulse index build` or `Service.BuildIndex`); `pulse_lookup` never builds one itself. Prefer `pulse_process` / `pulse_sample` for anything that is not an exact-key match.

## Input

Structured `types.LookupRequest` at top level:

- `cohort` (required): same shape as every other request's `cohort` slot.
- `field` / `value` (strings): single-key convenience path — the schema field name and literal key value as text.
- `keys` (`[{field, value}]`): ordered composite-key path. Takes precedence over `field`/`value` when non-empty. Order MUST match the index's build-time key-field order.
- `return_columns` (`string[]`, optional): fields to project into each row. Empty = every schema field.
- `multiplicity` (string enum, optional, default `"assert_unique"`): `assert_unique` errors on >1 match; `first` deterministically returns the lowest row-id without erroring; `all` returns every matching row, ascending row-id.

## Output

`LookupOut` (`types.LookupResult`): `rows` — matched row(s) as field-name→value maps, projected per `return_columns`; `warnings` — reserved, empty in v1.

## Gotchas

- No index, no lookup: `PULSE_INDEX_MISSING` when no sidecar exists for the requested key field(s). Build one first.
- `PULSE_INDEX_STALE` when the cohort changed since the index was built — rebuild.
- `PULSE_INDEX_UNSUPPORTED_SHARDED` for shard-archive cohorts — point lookup is single-file only.
- `PULSE_LOOKUP_NOT_FOUND` — index is fresh but no record matches the key.
- `PULSE_LOOKUP_AMBIGUOUS` — default `multiplicity` (`assert_unique`) rejects a key that resolves to more than one row; opt into `first` or `all` if duplicate keys are expected.
- `keys` order is significant end to end — it must mirror the order `Service.BuildIndex` was called with, not just the same field set.

## See

- `request-envelope` — cohort slot shape shared across request types.
- `response-components` — `Run` counters do not apply to lookup (single/O(1) reads).
- `cohort-schema-design` — sidecar index file format and staleness detection.
