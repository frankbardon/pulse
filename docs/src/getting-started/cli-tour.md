# CLI Tour

**Audience:** new CLI users who want a map of the `pulse` command tree.

`pulse --help` lists the command groups and `pulse <group> --help` a
group's leaves. The generated
[Command & flag reference](../cli/reference.md) has every leaf, flag and
default. This page groups the commands by task and links each group to its
chapter. [Your First Analysis](first-analysis.md) is a full import, predict,
process and export run.

> **LLM agents using MCP:** agents drive Pulse through MCP tools, not the
> CLI. Start at the `session-bootstrap` skill.

## Commands by task

| Task | Commands | Read |
|---|---|---|
| Bring data in | `import <format>` (`csv`, `tsv`, `ndjson`, `jsonarray`, `parquet`, `arrow`, `excel`, `spss`), `import schema-template`, `import predict`, `import transfer` | [Your First Cohort](first-cohort.md), [`import spss`](../cli/import-spss.md), [Transfer Compression](../format/transfer.md) |
| Managed import pool | `import auto SOURCE` (TTL sidecar, `--ttl 7d` by default, `pin` to keep), `import list`, `import drop HANDLE`, all under `PULSE_IMPORTS_DIR` | [Options](../library/options.md#environment-variables) |
| Take data out | `export <format>` (the same formats, plus `transfer`), `export predict`, `convert SOURCE TARGET` | [`export spss`](../cli/export-spss.md), [`--labels`](../cli/flags.md#--labels-export) |
| Find the data | `cohorts` (every `.pulse` file under the data directory, with its fields), `examples data --out DIR` (writes the example cohorts the examples and the Analysis Guide read) | [Get the data](../examples/library.md#get-the-data) |
| Look at a cohort | `cohort inspect` (header only, never reads records), `api sample`, `api facet` | [`cohort inspect`](../cli/cohort-inspect.md), [`api sample`](../cli/api-sample.md), [`api facet`](../cli/api-facet.md) |
| Run requests | `api process`, `api compose`, `api process-chain`, `api lookup` | [`api process`](../cli/api-process.md), [`api compose`](../cli/api-compose.md), [`api lookup`](../cli/api-lookup.md) |
| Validate without running | `api predict`, `api predict-compose`, `api predict-facet`, `api predict-chain` | [`api predict`](../cli/api-predict.md) |
| Write a derived cohort | `cohort filter`, `api materialize`, `rake`, `dedup` | [Filter to File](../library/filter-to-file.md), [Materialize](../library/materialize.md), [Raking](../library/rake.md), [Parent Groups](../format/parent-groups.md) |
| Change a cohort in place | `cohort zone`, `widen`, `shard create` / `add` / `remove` / `list` / `extract` / `verify` / `compact`, `index build` / `list` / `verify` / `drop` | [Time Zones](../library/time-zones.md), [Managing Shard Archives](../internals/managing-shard-archives.md), [Point Lookup](../library/point-lookup.md) |
| Generate synthetic data | `synth from-schema`, `synth from-profile`, `profile create` | [`synth from-schema`](../cli/synth-from-schema.md), [`synth from-profile`](../cli/synth-from-profile.md), [`profile create`](../cli/profile-create.md) |
| Pick and read an analysis | `recommend --intent ID --cohort PATH` (draft requests for a question kind), `explain` (a request or a saved response in plain words) | [Analysis Guide](../guide/index.md) |
| Discover what Pulse offers | `pulse --json` (the manifest), `schema`, `skills list` / `show`, `examples search` / `show`, `errors lookup` / `list`, `docs export` | [`manifest`](../cli/manifest.md), [`schema`](../cli/schema.md), [Examples Library](../examples/library.md) |
| Restrict an instance | `features init` / `check` / `diff` / `show` | [Feature Profiles](../library/feature-profiles.md) |
| Serve agents | `mcp` | [`pulse mcp`](../cli/mcp.md), [MCP Integration](../mcp/index.md) |
| Housekeeping | `version`, `completion` | [`completion`](../cli/completion.md) |

A `.pulse` path may be a single file or a **shard archive** (a Zip64 of
shards plus a `_schema.pulse` entry). Every command reads a shard archive
as the union of its shards; `archive.pulse#shard.pulse` opens one shard.

## Pipeline order

Inside one request, operators run in a fixed order:

```
Load -> Features -> Filter -> Attributes -> Group -> Aggregate -> Windows -> Sort -> Output
```

Each stage's operators are in the catalog:
[Features](../guide/catalog/feature.md) (derived columns),
[Filter](../guide/catalog/filterer.md),
[Attributes](../guide/catalog/attribute.md) (per-row values such as a
date part), [Group](../guide/catalog/grouper.md),
[Aggregate](../guide/catalog/aggregator.md) and
[Windows](../guide/catalog/window.md) (row-to-row figures such as a lag
or running total). Features run before filterers, so a derived column can be a filter, group,
attribute or window input. Windows run after aggregation, on the
aggregated rows. A name exists only downstream of the operator that makes
it.

## Shared flags

- Global: `--json` (the manifest), `--slim`, `--intent`, `--log-level`,
  `--limit`, `--memory-limit`, worker counts.
- `--json` on a leaf prints the envelope
  ([`--json` envelope](../cli/flags.md#--json-envelope)).
- `--echo-request` (the `api` leaves) puts the normalized request on the
  envelope's `request`.
- `--no-defaults` (`api process`, `api compose`, `api process-chain`)
  turns off [smart defaults](../cli/api-predict.md#smart-defaults).

Semantics: [CLI concepts & shared flags](../cli/flags.md). The one
required environment variable is `PULSE_DATA_DIR`
([Installation](installation.md#set-the-data-directory)).
