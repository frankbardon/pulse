# Flag Reference

**Audience:** CLI users who want one page that lists every flag and
every environment variable in scope across the binary.

The per-command pages list each command's full flag set; this page is
the cross-cutting reference for flags that appear on multiple
commands and for the environment variables Pulse reads.

> **LLM agents using MCP:** there is no LLM-facing skill for the CLI
> surface. Agents go via MCP tools (`pulse_process`, `pulse_inspect`,
> ...) — see `skills/mcp-integration.md`.

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
| `PULSE_DATA_DIR` | All commands when no path override is given; required by `pulse mcp` | conditionally | Base directory for cohort files. Relative cohort paths resolve against it. |

`PULSE_DATA_DIR` is the only `PULSE_*` environment variable today. The
Makefile auto-loads a repo-root `.env` file so you can keep it (and
any future env vars) there for development.

When embedding the library, you can bypass the env var entirely by
passing `pulse.Options{DataDir: "/path"}` or `pulse.Options{FS:
myFs}`.

## `--json` envelope

Almost every leaf command accepts `--json`, which switches output
from human prose to a structured envelope. The envelope shape is
fixed and documented in CLAUDE.md → Output Format Contract:

```json
{
  "format_version": "1.0",
  "data":     { /* operation-specific result */ },
  "errors":   [ /* {"code": "...", "message": "...", "details": {...}} */ ],
  "warnings": [ /* same shape */ ]
}
```

`format_version` is currently `"1.0"`. `errors` and `warnings` are
always arrays (never null) so JSON consumers can index without
nullable-check overhead.

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

Available on: `api predict`.

Treat warnings (e.g. low-quality field description) as errors.
Useful in CI gates that want the strictest possible validation.

### `--full-dict`

Available on: `cohort inspect`.

Print full categorical dictionaries instead of truncating after 100
entries. Pair with `--json` for programmatic consumption.

### `--strict` / `--seed` / `--rows`

`synth from-schema` and `synth from-profile` use `--seed` (for
deterministic RNG) and `--rows` (override the spec's row count). See
the per-command pages.

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
| Envelope and error code semantics | [Troubleshooting](../ops/troubleshooting.md) and `skills/error-code-reference.md` |
