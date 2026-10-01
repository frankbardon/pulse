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

**Keyable types:** every field type except `set_*` (ambiguous mask equality — use `FILTER_SET` instead). Per-type equality caveats (`f32`/`f64` bit-pattern, `decimal128` mantissa, `categorical_*` dictionary ID, `datetime` literal form) are in `cohort-schema-design`'s field-type matrix.

## Output

`types.LookupResult`: `rows` — matched row(s), field→value maps projected per `return_columns`; `warnings` — reserved, empty in v1.

## Gotchas

- `PULSE_INDEX_MISSING` — no sidecar for the key field(s); build one. To discover which tuples DO exist, read `pulse index list` / `Pulse.ListIndexes` — a sidecar's filename is a hash of its key tuple, so globbing `.idx` tells you nothing and is unavailable on object storage; the listing is served from the keyless `<cohort>.indexes.json` manifest.
- `PULSE_INDEX_STALE` — the cohort really changed: a different size, or a content hash that disagrees. An mtime that moved while the size held is NOT stale — it falls through to the fingerprint (memoised per stat pair), so an index read through a bucket still serves. `details.fingerprint_checked` says which arm refused. Rebuild.
- `PULSE_INDEX_MANIFEST_INVALID` / `_STALE` — the discovery manifest is unreadable, or names a sidecar that is gone. Rebuild or drop that tuple.
- `PULSE_INDEX_UNSUPPORTED_SHARDED` — shard archives unsupported; `archive.pulse#shard.pulse` anchor works around it.
- `PULSE_LOOKUP_NOT_FOUND` — fresh index, no matching record.
- `PULSE_LOOKUP_AMBIGUOUS` — default `assert_unique` rejects >1-row matches; opt into `first`/`all` for duplicates.
- Perf shape: indexed lookup is flat O(1) (~5µs, 10k→1M rows); scan is linear — gap widens with size.

## See

- `request-envelope` — cohort slot shape shared across request types.
- `cohort-schema-design` — sidecar byte format (v3), field-type matrix (keyable types + equality caveats), staleness.
