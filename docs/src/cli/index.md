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

`build` additionally maintains a **keyless catalog** beside the cohort,
`cohort.pulse.indexes.json`, recording each index's ordered key tuple.
It exists because `<keyhash>` is a hash **of** the key tuple, so a
sidecar cannot be opened without already knowing its key columns — and
object storage cannot list a directory, so globbing `.idx` files is not
an option where cohorts are usually served from. `list` reads the
catalog first.

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
    "manifest_path": "sales.pulse.indexes.json",
    "keys": ["region", "date"],
    "distinct_keys": 480,
    "indexed_records": 50000
  },
  "errors": [],
  "warnings": []
}
```

`manifest_path` is the keyless catalog this build upserted its entry
into — the one path that can be read to recover this index's key tuple
without knowing the tuple already.

## `pulse index list`

Enumerates every sidecar index built for a cohort, with each one's
ordered key columns. An empty result is not an error.

```bash
pulse index list --input sales.pulse
```

Two sources, catalog first:

1. **`cohort.pulse.indexes.json`** when it exists. It carries every
   entry's key tuple *and* its counts, so this path reads no sidecar at
   all — one read of the catalog plus one existence check per entry.
   That is what makes the listing work on object storage.
2. **The directory listing** (`cohort.pulse.<hash>.idx` beside the
   source file), reading each match's own key spec.

The two are **unioned** when both are available, so a sidecar built by
a Pulse that predates the catalog does not disappear the day one
appears beside it — and `build` seeds a catalog it is creating from the
sidecars already on disk, closing the same gap from the write side.
When the catalog exists, a directory that cannot be listed is not an
error.

A catalog that exists but cannot be parsed is
`PULSE_INDEX_MANIFEST_INVALID` rather than a silent fall back to the
directory listing: that fallback succeeds on a local disk and answers
"no indexes" on a bucket, which is the backend-dependent divergence the
catalog exists to remove. A catalog naming a sidecar that is not
present is `PULSE_INDEX_MANIFEST_STALE` rather than a skipped entry —
reporting two indexes for a catalog claiming three is indistinguishable
from a correct answer. Repair either with `pulse index build` for that
key tuple, or `pulse index drop`, which prunes an orphaned entry even
though the file is already gone.

## `pulse index verify`

Reports whether an index is still fresh relative to its source
cohort. The read path used by `lookup` accepts a matching size+mtime
pair without hashing; `verify` instead recomputes the **authoritative
full SHA-256** fingerprint whenever the size matches, so it catches the
residual case an in-place edit that happens to preserve file size and
mtime would otherwise miss. It also never reuses the digest cached by
the read path — its whole job is the verdict that does not rest on a
stat pair.

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

`reason` is one of `stat_mismatch` (the **size** alone was conclusive —
`fast_path: true`), `fingerprint_match`, or `fingerprint_mismatch`.

An additive `modtime_drift: true` (omitted when false) means the
cohort's modification time no longer matches the snapshot taken at
build while its size does, and the content hash settled the verdict.
That is **not** staleness on its own: `SourceModTime` is recorded as
Unix nanoseconds, an S3 `LastModified` is whole seconds, and every
copy, restore and cache hop truncates somewhere — so drift is not
evidence of mutation, and `lookup` escalates to the fingerprint there
rather than refusing. Seeing `fingerprint_match` with
`modtime_drift: true` means the index is good and the filesystem
reporting the cohort simply does not preserve the timestamp
resolution.

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
| `PULSE_INDEX_MISSING` | `verify` / `drop` targeted a key combination with no built sidecar (and, for `drop`, no catalog entry either) |
| `PULSE_INDEX_UNSUPPORTED_SHARDED` | The cohort is a shard archive — point-lookup indexing is single-file only |
| `PULSE_INDEX_MANIFEST_INVALID` | `cohort.pulse.indexes.json` exists but is not a readable catalog (malformed JSON, foreign `kind`, unknown `format_version`) |
| `PULSE_INDEX_MANIFEST_STALE` | The catalog names an index file that is not present — rebuild or drop that key tuple |
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
