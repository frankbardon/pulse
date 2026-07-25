# pulse index

**Audience:** CLI users managing the sidecar point-lookup index for a
cohort — the corpus-management counterpart to
[`pulse api lookup`](api-lookup.md). Defined in
[`internal/cli/index.go`](https://github.com/frankbardon/pulse/blob/main/internal/cli/index.go).

`pulse index` mirrors the `pulse shard` subcommand tree's shape: one
command group, four verbs (`build`, `list`, `verify`, `drop`) acting
on a cohort's sidecar index file(s). An index is a **separate file**
next to the cohort — `cohort.pulse.<keyhash>.idx` — the `.pulse` file
itself is never modified by any `index` subcommand.

> **LLM agents using MCP:** there is no dedicated MCP tool for index
> management in v1 — `pulse_lookup` assumes an index already exists.
> Build indexes via this CLI or `pulse.BuildIndex` / `pulse.VerifyIndex`
> / `pulse.ListIndexes` / `pulse.DropIndex` in the Go library.

## Synopsis

```
pulse index build  --input PATH --key FIELD[,FIELD...] [--json]
pulse index list   --input PATH [--json]
pulse index verify --input PATH --key FIELD[,FIELD...] [--json]
pulse index drop   --input PATH --key FIELD[,FIELD...] [--json]
```

`PATH` may also be given as the first positional argument in place of
`--input` on every subcommand.

## `pulse index build`

Scans the cohort once and writes a sidecar index keyed on `--key`
(comma-separated for a composite key; order is significant and is
what `pulse api lookup --key` must match). Idempotent — rebuilding
with the same key columns over an unchanged cohort produces a
byte-identical sidecar.

```bash
pulse index build --input sales.pulse --key region,date
```

```json
{
  "format_version": "1.1",
  "data": {
    "cohort": "sales.pulse",
    "index_path": "sales.pulse.a1b2c3d4.idx",
    "keys": ["region", "date"],
    "distinct_keys": 480,
    "indexed_records": 50000
  },
  "errors": [],
  "warnings": []
}
```

## `pulse index list`

Enumerates every sidecar index built for a cohort (globs
`cohort.pulse.<hash>.idx` next to the source file). An empty result is
not an error.

```bash
pulse index list --input sales.pulse
```

## `pulse index verify`

Reports whether an index is still fresh relative to its source
cohort. The read path used by `lookup` is a cheap O(1) size+mtime
stat; `verify` instead recomputes the **authoritative full SHA-256**
fingerprint, so it catches the residual case an in-place edit that
happens to preserve file size and mtime would otherwise miss.

```bash
pulse index verify --input sales.pulse --key region,date --json
```

```json
{
  "format_version": "1.1",
  "data": {
    "cohort": "sales.pulse",
    "index_path": "sales.pulse.a1b2c3d4.idx",
    "keys": ["region", "date"],
    "fresh": true,
    "reason": "fingerprint_match",
    "fast_path": false
  },
  "errors": [],
  "warnings": []
}
```

`reason` is one of `stat_mismatch` (size/mtime alone was conclusive —
`fast_path: true`), `fingerprint_match`, or `fingerprint_mismatch`.

## `pulse index drop`

Removes a sidecar index immediately — **destructive, non-interactive,
no confirmation prompt**. The sidecar is a cheap rebuild artifact
(`pulse index build` regenerates it byte-identically), so no undo is
offered.

```bash
pulse index drop --input sales.pulse --key region,date
```

## Flags (common to build / verify / drop)

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--input` | `-i` | string | (required) | Cohort `.pulse` file path (or the positional argument) |
| `--key`   | `-k` | string | (required for build/verify/drop) | Comma-separated key column(s), in build-time order |
| `--json`  |      | bool   | false | Emit the standard envelope |

## Coded errors

| Code | Meaning |
|---|---|
| `PULSE_INDEX_MISSING` | `verify` / `drop` targeted a key combination with no built sidecar |
| `PULSE_INDEX_UNSUPPORTED_SHARDED` | The cohort is a shard archive — point-lookup indexing is single-file only |
| `PROCESSING_CONFIG` | `--key` names a field whose type is not index-keyable (`set_*`), or another field-resolution failure |

## Constraints

- **Single-file cohorts only.** Shard archives reject with
  `PULSE_INDEX_UNSUPPORTED_SHARDED`; the `archive.pulse#shard.pulse`
  anchor works as a tested single-shard workaround.
- **Equality-only, full-key required.** No range or partial-key
  lookups in v1.
- `set_*` fields cannot be key columns — see the keyable-type policy
  on [`pulse api lookup`](api-lookup.md).

## Related

- [`pulse api lookup`](api-lookup.md) — read path that consumes the index this group manages
- `skills/cohort-schema-design.md` — sidecar index byte format (v3) + keyable-type policy
- [`pulse shard`](../internals/managing-shard-archives.md) — the subcommand-group shape this mirrors
