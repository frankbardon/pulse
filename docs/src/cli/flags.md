# Flag Reference

**Audience:** CLI users who want one page that lists every flag and
every environment variable in scope across the binary.

The per-command pages list each command's full flag set; this page is
the cross-cutting reference for flags that appear on multiple
commands and for the environment variables Pulse reads.

> **LLM agents using MCP:** there is no LLM-facing skill for the CLI
> surface. Agents go via MCP tools (`pulse_process`, `pulse_inspect`,
> ...) — see [`pulse mcp`](mcp.md) and
> [Adding an MCP tool](../internals/adding-mcp-tool.md).

## Global flags

Available on the bare `pulse` invocation:

| Flag | Effect |
|---|---|
| `--json` | Print the root manifest as JSON (envelope-wrapped) |
| `--slim` | With `--json`, drop prose descriptions for size-sensitive clients |

Both default to off. `pulse --json` is the discovery entry point — it
emits the manifest documented at [`pulse manifest`](manifest.md).

## Environment variables

| Variable | Used by | Required | Purpose |
|---|---|---|---|
| `PULSE_DATA_DIR`        | All commands when no path override is given; required by `pulse mcp` | conditionally | Base directory for cohort files. Relative cohort paths resolve against it |
| `PULSE_IMPORTS_DIR`     | `pulse import auto / list / drop`                                    | no | Managed-imports subdir under the data root. Defaults to `imports` |
| `PULSE_IMPORT_TTL`      | `pulse import auto`                                                  | no | Default TTL for managed imports. Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin`. Defaults to `7d` |
| `PULSE_LABEL_TABLES_DIR`| `pulse api sample --labels`, `pulse api facet --labels`              | no | Directory of JSON files auto-loaded as label tables at `pulse.New` time; each `*.json` becomes one table keyed by its filename |

`PULSE_DATA_DIR` is the only required `PULSE_*` environment variable.
The Makefile auto-loads a repo-root `.env` file so you can keep these
(and any future env vars) there for development.

When embedding the library, you can bypass the env vars entirely by
passing `pulse.Options{DataDir: "/path"}`, `pulse.Options{ImportsDir,
ImportTTL, LabelTablesDir, FS: myFs}` etc. — see
[`pulse.New` & Options](../library/options.md).

## `--json` envelope

Almost every leaf command accepts `--json`, which switches output
from human prose to a structured envelope. The envelope shape is
fixed and documented in CLAUDE.md → Output Format Contract:

```json
{
  "format_version": "1.1",
  "data":     { /* operation-specific result */ },
  "request":  { /* normalized request, omitted unless --echo-request was passed */ },
  "errors":   [ /* {"code": "...", "message": "...", "details": {...}} */ ],
  "warnings": [ /* same shape */ ]
}
```

`format_version` is currently `"1.0"`. `errors` and `warnings` are
always arrays (never null) so JSON consumers can index without
nullable-check overhead. `request` is opt-in (see
[`--echo-request`](#--echo-request) below); `data.components` is the
additive `Response.Components` slot documented per leaf — see
[`api process` → `--json`](api-process.md#-json).

## Shared per-command flags

Several flags appear on multiple commands with identical semantics.

### `--no-defaults`

Available on: `api process`, `api compose`.

Disable the runtime smart-defaults pass that infers operator `Type`
from the named field's schema type when the caller omits it. Forces
the request to be source-of-truth. See [pulse.New &
Options](../library/options.md) for the underlying library option.

### `--stream`

Available on: `api process`, `api compose`.

Stream result rows as NDJSON (one row per line) instead of buffering
the full result. For `compose`, each line carries an `{"index": N,
"row": {...}}` shape so consumers know which sub-request produced
each row. See [Streaming & ProcessStream](../library/streaming.md).

### `--strict`

Available on: `api process`, `api predict`.

Treat request-validation warnings (e.g. numeric aggregation on a
categorical field, low-quality field description) as errors. On
`api predict` this fails validation; on `api process` it refuses to
execute. Useful in CI gates that want the strictest possible
validation.

### `--echo-request`

Available on: `api process`, `api process-chain`, `api compose`,
`api predict`, `api sample`, `api facet`.

Include the *normalized* request — smart defaults resolved, label
bindings expanded — on `envelope.request`. Absent (and omitted from
JSON) by default so the envelope shape is unchanged for callers that
do not need it. Streaming output (`--stream`) skips the echo because
NDJSON has no envelope.

### `--full-dict`

Available on: `cohort inspect`.

Print full categorical dictionaries instead of truncating after 100
entries. Pair with `--json` for programmatic consumption.

### `--strict` / `--seed` / `--rows`

`synth from-schema` and `synth from-profile` use `--seed` (for
deterministic RNG) and `--rows` (override the spec's row count). See
the per-command pages.

## Command index

Every runnable leaf the binary exposes, with the page that documents it
in depth. A leaf whose "Documented in" column names no dedicated page is
covered by the surrounding topic page or by `--help`; the list itself is
the contract, and `TestSkillsCoverAllCliLeaves` fails if a new leaf is
added without naming it somewhere under `skills/` or `docs/src/`.

| Leaf | Purpose | Documented in |
|---|---|---|
| `pulse api compose` | Execute multiple processing requests in batch | [api compose](api-compose.md) |
| `pulse api facet` | Distinct values for a field, or a rich multi-field summary | [api facet](api-facet.md) |
| `pulse api lookup` | Key-exact row read against a prebuilt sidecar index | [api lookup](api-lookup.md) |
| `pulse api predict` | Validate a request against a cohort without executing | [api predict](api-predict.md) |
| `pulse api process` | Execute a processing request against a cohort | [api process](api-process.md) |
| `pulse api process-chain` | Source-rooted linear chain of mergeable stages | [api process](api-process.md) |
| `pulse api sample` | Return sample rows from a cohort | [api sample](api-sample.md) |
| `pulse cohort filter` | Filter a `.pulse` file to a new `.pulse` file | `--help` |
| `pulse cohort inspect` | Inspect a `.pulse` header and schema | [cohort inspect](cohort-inspect.md) |
| `pulse convert` | Convert between tabular formats, auto-detected from extensions | [import spss](import-spss.md), [export spss](export-spss.md) |
| `pulse convert predict` | Validate a conversion without writing output | [export spss](export-spss.md) |
| `pulse errors list` | List error codes by domain and/or substring | `--help` |
| `pulse errors lookup` | Message + fixups for one error code | `--help` |
| `pulse examples search` | Search the embedded request-example library | `--help` |
| `pulse examples show` | Print a named example's runnable request JSON | `--help` |
| `pulse export arrow` | Export `.pulse` to Arrow IPC | `--help` |
| `pulse export csv` | Export `.pulse` to CSV | `--help` |
| `pulse export excel` | Export `.pulse` to Excel | `--help` |
| `pulse export jsonarray` | Export `.pulse` to a JSON array | `--help` |
| `pulse export ndjson` | Export `.pulse` to NDJSON | `--help` |
| `pulse export parquet` | Export `.pulse` to Parquet | `--help` |
| `pulse export predict` | Validate an export without writing output | [export spss](export-spss.md) |
| `pulse export spss` | Export `.pulse` to SPSS `.sav` | [export spss](export-spss.md) |
| `pulse export tsv` | Export `.pulse` to TSV | `--help` |
| `pulse import arrow` | Import Arrow IPC into `.pulse` | `--help` |
| `pulse import auto` | Auto-detect a source format into the managed pool | `--help` |
| `pulse import csv` | Import CSV into `.pulse` | `--help` |
| `pulse import drop` | Remove a managed-import handle | `--help` |
| `pulse import excel` | Import Excel into `.pulse` | `--help` |
| `pulse import jsonarray` | Import a JSON array into `.pulse` | `--help` |
| `pulse import list` | List managed-import handles and TTL status | `--help` |
| `pulse import ndjson` | Import NDJSON into `.pulse` | `--help` |
| `pulse import parquet` | Import Parquet into `.pulse` | `--help` |
| `pulse import predict` | Validate an import without writing output | [import spss](import-spss.md) |
| `pulse import schema-template` | Emit an editable schema template from input data | [import spss](import-spss.md) |
| `pulse import spss` | Import SPSS `.sav` / `.zsav` into `.pulse` | [import spss](import-spss.md) |
| `pulse import tsv` | Import TSV into `.pulse` | `--help` |
| `pulse index build` | Build a point-lookup sidecar index | [index](index.md) |
| `pulse index drop` | Remove a cohort's sidecar index (destructive, no prompt) | [index](index.md) |
| `pulse index list` | List every sidecar index built for a cohort | [index](index.md) |
| `pulse index verify` | Report whether a cohort's sidecar index is fresh | [index](index.md) |
| `pulse mcp` | Run the MCP server over stdio | [mcp](mcp.md) |
| `pulse profile create` | Create a profile JSON for an existing cohort | [profile create](profile-create.md) |
| `pulse schema` | Print the payload JSON Schema (raw, not envelope-wrapped) | [schema](schema.md) |
| `pulse shard add` | Append a shard to an existing archive | `--help` |
| `pulse shard compact` | Rewrite an archive to reclaim orphan bytes | `--help` |
| `pulse shard create` | Create a shard archive from single-file shards | `--help` |
| `pulse shard extract` | Write a shard's standalone `.pulse` bytes to stdout | `--help` |
| `pulse shard list` | List shards inside an archive | `--help` |
| `pulse shard remove` | Remove a shard from an archive by basename | `--help` |
| `pulse shard verify` | Re-validate every shard against the canonical schema | `--help` |
| `pulse skills list` | List every embedded skill | `--help` |
| `pulse skills show` | Print one skill's markdown | `--help` |
| `pulse synth from-profile` | Generate a synthetic cohort from a captured profile | [synth from-profile](synth-from-profile.md) |
| `pulse synth from-schema` | Generate a cohort from a JSON schema/spec | [synth from-schema](synth-from-schema.md) |

## Help

Every command supports `--help`:

```bash
pulse --help
pulse api --help
pulse api process --help
pulse mcp --help
```

`--help` output is the urfave/cli v3 default — a usage block,
description, flag list, and an examples block where applicable.

## Cross-references

| If you need… | Go to |
|---|---|
| Per-command synopsis & examples | [CLI Tour](../getting-started/cli-tour.md) and each `cli/` page |
| Library-side equivalents | [Library Embedding](../library/overview.md) |
| MCP-side equivalents | [How LLMs Use Pulse](../mcp/index.md) |
| Envelope and error code semantics | [Troubleshooting](../ops/troubleshooting.md) and the `pulse_errors_lookup` MCP tool / `pulse errors lookup CODE` CLI |
