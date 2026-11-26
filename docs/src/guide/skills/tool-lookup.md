```yaml
name: tool-lookup
kind: tool
description: Resolve a point lookup against a cohort's prebuilt sidecar index.
type: reference
applies_to: mcp
```

## When to use

CALL TO FETCH ROWS BY EXACT KEY FROM A COHORT WITH A PREBUILT INDEX.
O(1) row addressing, not a scan — needs a sidecar index already built (`pulse index build -i COHORT --key F[,G]`, library `Pulse.BuildIndex`); this tool never builds one.

## Input

The lookup request's keys at top level:

- `cohort` (`{filename}`), or `union` (`[{cohort, label}]`: members are SCANNED in order, no index read; rows carry `union_source_field`, default `_source`).
- `field` / `value` (a string, parsed per the field type): single-key path.
- `keys` (`[{field, value}]`): ordered composite key, wins over `field`/`value`. Order MUST match the index's build-time key order (`[region, date]` != `[date, region]`).
- `return_columns` (`string[]`): project these fields; empty = all.
- `multiplicity` (default `assert_unique`): errors on >1 match; `first` = lowest row-id; `all` = every match, ascending row-id.

**Keyable types:** every scalar type; not `set_*` (no single equality value — use a set filterer) or `vec_*`. Equality caveats (`f32`/`f64` bit pattern, `decimal128` mantissa, `categorical_*` label → dictionary ID, `datetime` literal form): [`cohort-sidecar-index`](cohort-sidecar-index.md).

## Output

`{rows: [...]}` (no envelope): the matched row(s) as field→value maps, cells rendered as [`tool-sample`](tool-sample.md)'s (`date` `YYYY-MM-DD`, `datetime` RFC 3339 UTC, `decimal128` a string, `u64` exact even above 2^53).

## Gotchas

- `PULSE_INDEX_MISSING` — no sidecar for the key field(s); build one. List built tuples with `pulse index list` / `Pulse.ListIndexes`, never by globbing `.idx` files (their names are hashes).
- `PULSE_INDEX_STALE` — the cohort changed (size or content hash; a moved mtime alone is not enough), or a shard the index does not cover (`details.shards_uncovered`). Rebuild.
- `PULSE_INDEX_MANIFEST_INVALID` / `_STALE` — the discovery manifest is unreadable or names a gone sidecar. Rebuild or drop that tuple.
- `PULSE_LOOKUP_NOT_FOUND` — fresh index, no matching record.
- `PULSE_LOOKUP_AMBIGUOUS` — `assert_unique` met >1 row (`details.match_count`); opt into `first` / `all`.

## See

- [`cohort-sidecar-index`](cohort-sidecar-index.md) — format, staleness, discovery, shards, keyable types.
- [`request-envelope`](request-envelope.md) — the cohort slot shape.
