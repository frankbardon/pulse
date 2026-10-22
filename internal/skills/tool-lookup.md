---
name: tool-lookup
kind: tool
description: Resolve a point lookup against a cohort's prebuilt sidecar index.
type: reference
applies_to: mcp
---

## When to use

CALL TO FETCH ROWS BY EXACT KEY FROM A COHORT WITH A PREBUILT INDEX.
O(1) row addressing, not a scan — needs a sidecar index already built (`pulse index build` / `Service.BuildIndex`); this tool never builds one.

## Input

`types.LookupRequest`:

- `cohort` (required).
- `field` / `value`: single-key convenience path.
- `keys` (`[{field, value}]`): ordered composite-key path, wins over `field`/`value`. Order MUST match the index's build-time key order — `[region, date]` != `[date, region]`.
- `return_columns` (`string[]`, optional): project these fields; empty = all.
- `multiplicity` (default `assert_unique`): errors on >1 match; `first` = lowest row-id; `all` = every match, ascending row-id.

**Keyable types:** every field type except `set_*` (ambiguous mask equality — use `FILTER_SET` instead). Per-type equality caveats (`f32`/`f64` bit-pattern, `decimal128` mantissa, `categorical_*` dictionary ID, `datetime` literal form) are in `cohort-schema-design`'s field-type matrix.

## Output

`types.LookupResult`: `rows` — matched row(s), field→value maps projected per `return_columns`, cells rendered as `tool-sample`'s (`date` `YYYY-MM-DD`, `datetime` RFC 3339 UTC `…Z`, `decimal128` a string, `u64` the exact integer even above 2^53); `warnings` — reserved, empty in v1.

## Gotchas

- `PULSE_INDEX_MISSING` — no sidecar for the key field(s); build one. Discover which tuples exist with `pulse index list` / `Pulse.ListIndexes` (the `indexes.json` manifest) — never by globbing `.idx` files, whose names are hashes.
- `PULSE_INDEX_STALE` — the cohort really changed (size or content hash; a moved mtime alone falls through to the fingerprint), or a shard the index does not cover (`details.shards_uncovered`). Rebuild.
- `PULSE_INDEX_MANIFEST_INVALID` / `_STALE` — the discovery manifest is unreadable or names a gone sidecar. Rebuild or drop that tuple.
- `PULSE_LOOKUP_NOT_FOUND` — fresh index, no matching record.
- `PULSE_LOOKUP_AMBIGUOUS` — default `assert_unique` rejects >1-row matches; opt into `first` / `all`.
- Indexed lookup is flat O(1) (~5µs, 10k→1M rows); a scan is linear. Format, staleness arms, shards: `cohort-sidecar-index`.

## See

- `request-envelope` — cohort slot shape shared across request types.
- `cohort-sidecar-index` — format, staleness, discovery, shards; `cohort-schema-design` — keyable types and equality caveats.
