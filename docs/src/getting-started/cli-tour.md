# CLI Tour

**Audience:** anyone who wants a map of every `pulse` subcommand before
diving into per-command details.

This page is a one-liner index of the CLI tree. Each row links to its
detailed chapter where applicable; commands that are minor variants of
each other (per-format `import`/`export` leaves) are listed compactly.

> **LLM agents using MCP:** there is no equivalent skill — agents drive
> Pulse through MCP tools, not the CLI. Start at the `getting-started`
> skill instead.

## Top-level groups

```
pulse [--json] [--slim]
├── import      Tabular → .pulse (csv, tsv, ndjson, jsonarray, parquet, arrow, excel)
├── export      .pulse  → tabular (same format set)
├── convert     Tabular → tabular, with .pulse as the transparent middle
├── cohort      Inspect or filter an existing .pulse file
├── api         Processing operations (process, compose, predict, sample, facet)
├── synth       Generate synthetic cohorts (from-schema, from-profile)
├── profile     Capture a statistical profile of a cohort
├── skills      Read the embedded LLM skill pack
└── mcp         Run the Model Context Protocol server over stdio
```

Bare `pulse --json` prints the self-describing root manifest — commands,
components, field types, and skill metadata in one envelope. Pass
`--slim` to drop prose descriptions for size-sensitive clients.

## API operations

The "processing facade" — these are the operations exposed via the
[Go library API](../library/overview.md) and the
[MCP tool set](../mcp/index.md).

| Command | Purpose | Chapter |
|---|---|---|
| `pulse api process`  | Execute one request against a cohort | [api process](../cli/api-process.md) |
| `pulse api compose`  | Execute multiple requests in batch / parallel | [api compose](../cli/api-compose.md) |
| `pulse api predict`  | Validate a request without executing | [api predict](../cli/api-predict.md) |
| `pulse api sample`   | Return up to N rows | [api sample](../cli/api-sample.md) |
| `pulse api facet`    | Return distinct values of a field | [api facet](../cli/api-facet.md) |

## Cohort lifecycle

| Command | Purpose | Chapter |
|---|---|---|
| `pulse cohort inspect PATH` | Read header + schema (no record data) | [cohort inspect](../cli/cohort-inspect.md) |
| `pulse cohort filter`       | Write a filtered subset to a new `.pulse` | See [Internals → Architecture](../internals/architecture.md) |

## Import / export / convert

`pulse import <format>` and `pulse export <format>` share the same flag
shape per format (`--input`, `--output`, `--schema` for import).
Supported formats today:

`csv` · `tsv` · `ndjson` · `jsonarray` · `parquet` · `arrow` · `excel`

Each format has a per-leaf command (e.g. `pulse import csv`). Run
`pulse import --help` or `pulse export --help` for the full list.

`pulse convert SOURCE TARGET` chains import + export with no
intermediate file unless `--keep-pulse PATH` is passed. Format is
auto-detected from extensions.

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
| `pulse mcp`                 | Serve MCP over stdio | [mcp](../cli/mcp.md) |

## Cross-cutting flags

Most leaves accept `--json` (envelope output), `--no-defaults` (turn off
smart operator-type inference), and the operation-specific flags
documented per page. Full list: [Flag Reference](../cli/flags.md).

The single environment variable to know is `PULSE_DATA_DIR` — see
[Installation](installation.md).
