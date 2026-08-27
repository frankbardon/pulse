---
name: tool-lookup
kind: tool
description: Resolve a point lookup against a cohort's prebuilt sidecar index.
type: reference
applies_to: mcp
---

## When to use

O(1) row addressing by exact key, not a scan — requires a sidecar index already built (`pulse index build` / `Service.BuildIndex`); this tool never builds one. Prefer `pulse_process` / `pulse_sample` for non-exact-key work.

## Input

`types.LookupRequest`:

- `cohort` (required).
- `field` / `value`: single-key convenience path.
- `keys` (`[{field, value}]`): ordered composite-key path, wins over `field`/`value`. Order MUST match the index's build-time key order — `[region, date]` != `[date, region]`.
- `return_columns` (`string[]`, optional): project these fields; empty = all.
- `multiplicity` (default `assert_unique`): errors on >1 match; `first` = lowest row-id; `all` = every match, ascending row-id.

**Keyable types:** ALLOW `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64` (bit-pattern equality — `-0.0`/NaN caveat), `date`, `datetime` (literal `2024-03-04T10:11:12Z`), `decimal128` (exact mantissa), `categorical_*` (dictionary ID), `packed_bool`. REJECT `set_*` (ambiguous mask equality) — use `FILTER_SET` instead.

## Output

`types.LookupResult`: `rows` — matched row(s), field→value maps projected per `return_columns`; `warnings` — reserved, empty in v1.

## Gotchas

- `PULSE_INDEX_MISSING` — no sidecar for the key field(s); build one.
- `PULSE_INDEX_STALE` — cohort changed since build (O(1) size+mtime check; `verify` is the authoritative full-hash check). Rebuild.
- `PULSE_INDEX_UNSUPPORTED_SHARDED` — shard archives unsupported; `archive.pulse#shard.pulse` anchor works around it.
- `PULSE_LOOKUP_NOT_FOUND` — fresh index, no matching record.
- `PULSE_LOOKUP_AMBIGUOUS` — default `assert_unique` rejects >1-row matches; opt into `first`/`all` for duplicates.
- Perf shape: indexed lookup is flat O(1) (~5µs, 10k→1M rows); scan is linear — gap widens with size.

## See

- `request-envelope` — cohort slot shape shared across request types.
- `cohort-schema-design` — sidecar byte format (v3), keyable-type policy, staleness.
