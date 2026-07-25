# pulse api lookup

**Audience:** CLI users resolving a single row (or a small duplicate
set) by an exact key value instead of scanning or filtering a whole
cohort. Defined in
[`internal/cli/api.go`](https://github.com/frankbardon/pulse/blob/main/internal/cli/api.go).

`pulse api lookup` is the CLI leaf for `Pulse.Lookup`. It resolves a
point lookup against a cohort's **prebuilt sidecar index** — see
[`pulse index`](index.md) for building one first. `lookup` never
builds or repairs an index itself; a missing or stale index is a
coded error, not an implicit rebuild.

`lookup` joins `process` / `compose` / `facet` / `sample` /
`process-chain` under `pulse api` because it is a request/response
operation against a cohort (execute-and-return), not a corpus-
management verb the way `pulse index {build,list,verify,drop}` is.

> **LLM agents using MCP:** see the `pulse_lookup` MCP tool and the
> `tool-lookup` / `cohort-schema-design` skills.

## Synopsis

```
pulse api lookup --input PATH --key FIELD=VALUE [--key FIELD=VALUE ...]
                 [--return FIELDS] [--mode MODE]
                 [--request FILE] [--json] [--echo-request]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--input`         | `-i` | string | (required unless `--request`) | Cohort `.pulse` file path |
| `--key`           | `-k` | string | (required unless `--request`) | `field=value` pair; repeat for a composite key, in the same column order the sidecar index was built with |
| `--return`        |      | string | (none) | Comma-separated field names to project into each result row; omit for every schema field |
| `--mode`          |      | string | `assert-unique` | Multiplicity when the key matches more than one row: `assert-unique` (errors `PULSE_LOOKUP_AMBIGUOUS`), `first`, or `all` |
| `--request`       | `-r` | string | (none) | Full `LookupRequest` JSON file; flags layer on top of (override) the file |
| `--json`          |      | bool   | false  | Emit the standard envelope |
| `--echo-request`  |      | bool   | false  | Include the resolved `LookupRequest` on `envelope.request` |

Composite keys are **order-significant end to end**: `--key
region=EU --key date=2026-01-01` and the reverse order name the same
two logical values but resolve against different sidecar indexes,
because a composite key is built as an ordered byte-concatenation, not
a set.

## Output (`--json`)

```json
{
  "format_version": "1.1",
  "data": {
    "rows": [
      { "order_id": 42, "region": "EU", "revenue": "129.50" }
    ],
    "warnings": []
  },
  "errors": [],
  "warnings": []
}
```

`rows` carries one entry under the default `assert-unique` mode or
`first`; 1..N entries, ascending row-id order, under `all`.

## Keyable-type policy

Not every field type may serve as a lookup key. ALLOW `u4` / `u8` /
`u16` / `u32` / `u64`, `f32` / `f64` (raw IEEE-754 bit-pattern equality
— `-0.0` and `0.0` key differently, NaN never self-matches),
`date`, `decimal128` (exact mantissa, no float round-trip),
`categorical_*` (dictionary ID), `packed_bool`. REJECT `set_*` — a
multi-select bitmask has no single unambiguous equality value; use a
`FILTER_SET` predicate via `pulse api process` instead.

## Exit codes / coded errors

| Code | Meaning |
|---|---|
| `PULSE_INDEX_MISSING` | No sidecar built for the requested key field(s) — run `pulse index build` first |
| `PULSE_INDEX_STALE` | The cohort changed since the index was built (O(1) size+mtime check) — rebuild |
| `PULSE_INDEX_UNSUPPORTED_SHARDED` | The cohort is a shard archive — point lookup is single-file only. The `archive.pulse#shard.pulse` anchor works as a tested single-shard workaround |
| `PULSE_LOOKUP_NOT_FOUND` | The index is fresh but no record matches the key |
| `PULSE_LOOKUP_AMBIGUOUS` | Default `assert-unique` mode rejects a key resolving to more than one row |

## Performance notes

Indexed lookup is **O(1)** — flat latency regardless of cohort size
(measured ~5µs across 10k→1M rows). An unindexed scan (`pulse api
process` with an equivalent `FILTER_INCLUDE`) is linear in record
count, so the gap between indexed and unindexed access widens, not
narrows, as a cohort grows — indexed lookup was measured roughly four
orders of magnitude faster at 1M rows. Build the index once with
`pulse index build`; every subsequent `lookup` against that key
combination pays only the O(1) cost.

## Examples

```bash
# Build the index once
pulse index build --input sales.pulse --key order_id

# Single-key lookup
pulse api lookup --input sales.pulse --key order_id=42 --json

# Composite key, projected columns, tolerate duplicates
pulse api lookup --input sales.pulse \
  --key region=EU --key date=2026-01-01 \
  --return order_id,revenue --mode all --json
```

## Related

- [Point Lookup & Index Management](../library/point-lookup.md) — the Go library equivalent (`Pulse.Lookup` / `Pulse.BuildIndex` / `Pulse.VerifyIndex` / `Pulse.ListIndexes` / `Pulse.DropIndex`)
- [`pulse index`](index.md) — build / list / verify / drop the sidecar index this command reads
- [`pulse api process`](api-process.md) — full scan with `FILTER_*` for non-exact-key or `set_*` membership queries
- [.pulse File Format → Header Layout](../format/header.md) — the cohort layout the sidecar sits alongside, unchanged
- `skills/cohort-schema-design.md` — sidecar index byte format (v3) + keyable-type policy
- `skills/tool-lookup.md` — MCP surface (`pulse_lookup`)
