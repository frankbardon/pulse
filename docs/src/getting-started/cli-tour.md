# CLI Tour

**Audience:** anyone who wants a map of every `pulse` subcommand before
diving into per-command details.

This page is a one-liner index of the CLI tree. Each row links to its
detailed chapter where applicable; commands that are minor variants of
each other (per-format `import`/`export` leaves, per-leaf `shard`
maintenance commands) are listed compactly.

> **LLM agents using MCP:** there is no equivalent skill — agents drive
> Pulse through MCP tools, not the CLI. Start at the `getting-started`
> skill instead.

## Top-level groups

```
pulse [--json] [--slim]
├── import      Tabular → .pulse (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, auto)
├── export      .pulse  → tabular (same format set)
├── convert     Tabular → tabular, with .pulse as the transparent middle
├── cohort      Inspect or filter an existing .pulse file
├── api         Processing operations (process, process-chain, compose, predict, sample, facet)
├── shard       Build and maintain shard archives
├── synth       Generate synthetic cohorts (from-schema, from-profile)
├── profile     Capture a statistical profile of a cohort
├── skills      Read the embedded LLM skill pack
├── examples    Search and fetch the embedded runnable request library
├── errors      Look up an error code's message and recovery fixups
└── mcp         Run the Model Context Protocol server over stdio
```

Bare `pulse --json` prints the self-describing root manifest — commands,
components, field types, and skill metadata in one envelope. Pass
`--slim` to drop prose descriptions for size-sensitive clients.

## End-to-end worked tour

A complete read-aggregate cycle, one command per stage:

```bash
# 1. Import a CSV into a managed .pulse handle (TTL sidecar tracked).
pulse import auto sales.csv --handle sales --ttl 30d

# 2. Read the cohort's schema — types, descriptions, dictionaries.
pulse cohort inspect sales.pulse

# 3. Validate a request against the schema without running it.
cat > req.json <<'EOF'
{
  "cohort": {"filename": "sales.pulse"},
  "groups":       [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [{"type": "AGG_SUM",        "field": "revenue", "label": "total"}]
}
EOF
pulse api predict --request req.json --json | jq '.data | {valid, streamable, defaults_applied}'

# 4. Execute the same request.
pulse api process --request req.json --json
```

`api predict` reads only the header and schema, so it stays cheap even
on multi-GB cohorts; the `streamable` / `streamable_reasons` fields
tell you whether the request will run through the single-pass streaming
path or be buffered. See [Performance Notes](../ops/performance.md).

## Pipeline order

Inside `api process`, operators run in a fixed sequence:

```
Load -> Features -> Filter -> Attributes -> Group -> Aggregate -> Windows -> Sort -> Output
```

Features run **before** filterers (so derived columns are addressable
as filter, group, attribute, and window inputs); windows run **after**
aggregation, on the post-aggregate row set.

## API operations

The "processing facade" — these are the operations exposed via the
[Go library API](../library/overview.md) and the
[MCP tool set](../mcp/index.md).

| Command | Purpose | Chapter |
|---|---|---|
| `pulse api process`       | Execute one request against a cohort | [api process](../cli/api-process.md) |
| `pulse api process-chain` | Source-rooted linear chain of mergeable processing stages | (see `cmd/pulse/main.go`) |
| `pulse api compose`       | Execute multiple requests in batch / parallel | [api compose](../cli/api-compose.md) |
| `pulse api predict`       | Validate a request without executing | [api predict](../cli/api-predict.md) |
| `pulse api sample`        | Return up to N rows | [api sample](../cli/api-sample.md) |
| `pulse api facet`         | Return distinct values of a field (simple) or a multi-field rich summary | [api facet](../cli/api-facet.md) |

`api process-chain` accepts a `ChainRequest` whose stages each see the
previous stage's rows as their input cohort. Mergeable-only at v1 —
non-mergeable stages fail with `PULSE_CHAIN_NOT_MERGEABLE` so callers
can fall back to per-stage `api process` calls.

## Cohort lifecycle

| Command | Purpose | Chapter |
|---|---|---|
| `pulse cohort inspect PATH` | Read header + schema (no record data) | [cohort inspect](../cli/cohort-inspect.md) |
| `pulse cohort filter`       | Write a filtered subset to a new `.pulse` (single file, shard archive, or `archive.pulse#shard.pulse` anchor) | See [Internals → Architecture](../internals/architecture.md) |

## Import / export / convert

`pulse import <format>` and `pulse export <format>` share the same flag
shape per format (`--input`, `--output`, `--schema` for import).
Supported formats today:

`csv` · `tsv` · `ndjson` · `jsonarray` · `parquet` · `arrow` · `excel`

Each format has a per-leaf command (e.g. `pulse import csv`). Run
`pulse import --help` or `pulse export --help` for the full list.

`pulse import auto SOURCE` auto-detects the source format and converts
into the managed `.pulse` pool under `PULSE_IMPORTS_DIR`, with a TTL
sidecar (`--ttl 7d` by default, `pin` to opt out). Use `pulse import
list` to see managed handles and `pulse import drop HANDLE` to remove
one.

`pulse convert SOURCE TARGET` chains import + export with no
intermediate file unless `--keep-pulse PATH` is passed. Format is
auto-detected from extensions.

## Shard archives

A `.pulse` path may resolve to either a single-file cohort or a
**shard archive** (uncompressed Zip64 carrying one canonical
`_schema.pulse` entry plus N standalone shard payloads). Every facade
method (`Process`, `Compose`, `Sample`, `Facet`, `Inspect`, `Predict`,
`ProcessStream`) operates transparently on the union of shards.

| Command | Purpose |
|---|---|
| `pulse shard create ARCHIVE --include SHARD ...` | Create a new archive from one or more single-file `.pulse` shards (atomic temp+rename) |
| `pulse shard add ARCHIVE SHARD`                  | Append a shard to an existing archive (cohesion validated) |
| `pulse shard remove ARCHIVE BASENAME`            | Remove a shard from an archive by basename |
| `pulse shard list ARCHIVE`                       | List shards inside an archive with per-shard record counts |
| `pulse shard extract ARCHIVE BASENAME`           | Write one shard's standalone `.pulse` bytes to stdout |
| `pulse shard verify ARCHIVE`                     | Strict dict-prefix cohesion check across shards |
| `pulse shard compact ARCHIVE`                    | Defragment the archive in place |

The `archive.pulse#shard.pulse` anchor opens one shard as a one-shard
cohort. See [Internals → Managing Shard Archives](../internals/managing-shard-archives.md).

## Synthetic data

| Command | Purpose | Chapter |
|---|---|---|
| `pulse synth from-schema`   | Generate from a JSON spec | [synth from-schema](../cli/synth-from-schema.md) |
| `pulse synth from-profile`  | Generate from a captured profile | [synth from-profile](../cli/synth-from-profile.md) |
| `pulse profile create`      | Capture a profile from an existing cohort | [profile create](../cli/profile-create.md) |

## Self-description & LLM surface

| Command | Purpose | Chapter |
|---|---|---|
| `pulse --json`              | Root manifest (commands, components, field types, skills) | [manifest](../cli/manifest.md) |
| `pulse skills list`         | List embedded skills with metadata | [How LLMs Use Pulse](../mcp/index.md) |
| `pulse skills show NAME`    | Print a skill's full markdown body | same |
| `pulse examples search`     | Search embedded runnable request examples by tag, category, or operator | [Examples Library](../examples/library.md) |
| `pulse examples get NAME`   | Print one example's full JSON body | same |
| `pulse errors lookup CODE`  | Print an error code's canonical message and recovery fixups | (see `errors/`) |
| `pulse mcp`                 | Serve MCP over stdio | [mcp](../cli/mcp.md) |

## Cross-cutting flags

Most leaves accept `--json` (envelope output), `--no-defaults` (turn off
smart operator-type inference — see
[`api predict` → Smart defaults](../cli/api-predict.md#smart-defaults)),
and `--echo-request` (include the normalized request on
`envelope.request`). Full list: [Flag Reference](../cli/flags.md).

The single environment variable to know is `PULSE_DATA_DIR` — see
[Installation](installation.md).
