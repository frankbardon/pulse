# MCP Integration

**Audience:** operators wiring Pulse into an MCP-aware AI client (Claude Desktop, Claude Code, Cursor, Zed, custom hosts), and embedders who want to expose Pulse to an LLM agent.

This page is the human-facing guide: what the server does, how to wire it up, what the LLM sees, and how to debug a misbehaving session. Agent-facing guidance ships inside the binary as the `mcp-integration` skill — fetch it via `pulse_skills_get` (or `pulse skills show mcp-integration`).

## What `pulse mcp` is

`pulse mcp` runs the Pulse library as a Model Context Protocol (MCP) server. The host (Claude Desktop, Claude Code, etc.) launches it as a subprocess, speaks JSON-RPC over its stdio streams, and shuts it down on session close. The LLM sees Pulse as a set of **tools** (callable functions), **resources** (browseable URIs), and **prompts** (canned slash commands).

```
┌─────────────┐  stdio JSON-RPC  ┌────────────┐  Go calls  ┌─────────────┐
│  AI client  │ ───────────────→ │ pulse mcp  │ ─────────→ │ pulse.Pulse │
│   (host)    │ ←─────────────── │ (this bin) │ ←───────── │  (library)  │
└─────────────┘                  └────────────┘            └─────────────┘
                                       │
                                       └── stderr ─→ host log pane
```

The server is a thin translator. Every tool wraps a public method on `pulse.Pulse`; the same code path powers the CLI.

## Quickstart

```bash
# 1. Build and place on PATH
make build && cp ./bin/pulse /usr/local/bin/

# 2. Pick a data directory
mkdir -p /var/data/pulse

# 3. Wire into your host (see below) and restart it

# 4. From the LLM session, call:
#    pulse_manifest      → cache once
#    pulse_inspect       → open a cohort
#    pulse_predict       → validate a request
#    pulse_process       → execute
```

## Wiring into a host

### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```jsonc
{
  "mcpServers": {
    "pulse": {
      "command": "/usr/local/bin/pulse",
      "args": ["mcp"],
      "env": {
        "PULSE_DATA_DIR": "/var/data/pulse"
      }
    }
  }
}
```

Restart Claude Desktop. Pulse tools appear in the tool picker.

### Claude Code

```bash
claude mcp add pulse --env PULSE_DATA_DIR=/var/data/pulse -- pulse mcp
```

Or by hand in `~/.claude.json` (or per-project `.claude.json`):

```jsonc
{
  "mcpServers": {
    "pulse": {
      "command": "/usr/local/bin/pulse",
      "args":    ["mcp"],
      "env":     { "PULSE_DATA_DIR": "/var/data/pulse" }
    }
  }
}
```

### Cursor / Zed / generic stdio hosts

Any host that speaks the MCP stdio transport can launch `pulse mcp` the same way — provide the binary path, the `mcp` argument, and the `PULSE_DATA_DIR` env var.

## What the LLM sees

### Tool surface

Fifteen tools, registered at server start. Names and order match `mcp/toolmeta/meta.go`.

| Tool | Purpose |
|---|---|
| `pulse_manifest` | **Call first.** Self-description: commands, operators (with accepted types + streamability), tier-1/tier-2 tests, regressions, synth distributions, error code list, MCP tool list, cohort field types with operator cross-references. Cache once per session. |
| `pulse_inspect` | Read `.pulse` header + schema (no record bytes). Side effect: registers session-scoped schema-bound tool variants (see below). |
| `pulse_predict` | Validate a request against the schema without executing. Returns errors, warnings, applied defaults, streamability reasons. |
| `pulse_process` | Execute one pre-built request. |
| `pulse_compose` | Execute a batch of requests against the same cohort in one round trip. |
| `pulse_sample` | Return up to N rows for preview / diagnostics. |
| `pulse_facet` | Distinct values for a single field. |
| `pulse_import` | Convert a tabular source (csv, tsv, ndjson, jsonarray, parquet, arrow, excel) into a managed `.pulse` handle under `imports/`, with TTL-tracked sidecar. Pulse-format inputs pass through. |
| `pulse_drop` | Delete a managed-import handle and its sidecar. |
| `pulse_imports_list` | Enumerate managed handles with sidecar metadata (source, format, imported_at, expires_at, ttl, expired flag, pinned flag). |
| `pulse_examples_search` | Search the embedded request-example library by query, taxonomy tags (ANDed), or category. |
| `pulse_examples_get` | Fetch one runnable example body by name. |
| `pulse_errors_lookup` | Per-code Message + Fixup detail (kept out of the manifest for context economy). |
| `pulse_skills_list` | Embedded skill metadata. |
| `pulse_skills_get` | Fetch one skill body by name. |

### Resources

| URI scheme | Yields |
|---|---|
| `pulse://<path>` | One resource per `.pulse` file under the data directory. Read returns `descriptor.InspectResult` JSON (header + schema only — no record bytes). |
| `pulse-skill://<name>` | One per embedded skill. Read returns the markdown body. |

Resources are registered once at server start. Files added afterwards do not appear until the server restarts. Listing is cheap because the server only reads header bytes.

### Prompts

| Name | Args | Returns |
|---|---|---|
| `pulse-bootstrap` | none | A short instructions block telling the assistant what to call (and in what order) before authoring any request, and where the authoritative references live. Inject at session start. |
| `pulse-author-request` | `question` | A guided tool-call sequence for translating an analytical question into a Pulse request: manifest → examples search → inspect → predict → process. |

Hosts that surface prompts as slash commands let users trigger these directly.

## Recommended session flow

The default sequence for nearly every user request:

1. **`pulse_manifest`** once at session start. No arguments. Cache the payload — it is deterministic for a binary version and carries every fact needed to author a valid request.
2. **`pulse_import`** when the user hands the LLM a raw tabular file (CSV/TSV/NDJSON/JSONArray/Parquet/Arrow/Excel). Returns a managed handle that subsequent tools address as if it were a `.pulse`. Skip when the cohort already exists as a managed handle or `.pulse` file in `PULSE_DATA_DIR`.
3. **`pulse_inspect`** on the handle (or path). Reads header + schema only — no record bytes — and registers session-scoped, schema-bound variants of the action tools (see below).
4. **`pulse_predict`** with the authored request. Validates against the schema, surfaces applied defaults, streamability, and any structural errors / warnings. Each error code carries Fixup metadata so the LLM can repair the request without another round trip.
5. **`pulse_process`** to execute. Use `pulse_compose` to batch multiple requests against the same cohort in one round trip.

Cheaper probes are available without going through `pulse_process`:

- **`pulse_sample`** for a row preview.
- **`pulse_facet`** for distinct values of a single field.
- **`pulse_examples_search`** / **`pulse_examples_get`** to crib a runnable request template from the embedded library.

## Managed imports + TTL

`pulse_import` lets the LLM hand the server any tabular file and address it from then on as if it were a `.pulse`.

- **Convertible formats** (csv, tsv, ndjson, jsonarray, parquet, arrow, excel) are imported into `$PULSE_DATA_DIR/imports/<handle>.pulse` with a sidecar `<handle>.pulse.meta.json` carrying `imported_at`, `expires_at`, `ttl_seconds`, source path, source format, and row count. `result.managed=true`.
- **Pulse passthroughs** (`.pulse` extension) under `PULSE_DATA_DIR` are not copied — the server returns the relative path verbatim with `managed=false`. A `.pulse` outside `PULSE_DATA_DIR` is copied into the managed pool.

**Source path resolution.** Relative `source` paths resolve against `PULSE_DATA_DIR`. Absolute paths read from the host filesystem through a separate "source fs."

**Import jail.** Absolute source paths are confined to a single directory tree (the *jail root*). Default: the working directory the MCP server was launched from. Paths that escape the jail (including `..`) return `PULSE_IMPORT_SOURCE_FORBIDDEN`. Override via `pulse.Options.ImportSourceJailRoot` when embedding.

**Sliding TTL.** Default lifetime is `7d` (overridable via `PULSE_IMPORT_TTL`, or per-import via the `ttl` field — accepts Go duration like `"24h"`, day form like `"7d"`, or `"pin"` for never-expire). Every subsequent inspect/predict/process/sample/facet against the handle slides `expires_at` forward. The pool self-sweeps on every `pulse_import` call — no daemon required. Inspect with `pulse_imports_list`; evict manually with `pulse_drop`.

## Schema-bound enums

After a successful `pulse_inspect`, the server registers session-scoped variants of the action tools (`pulse_process`, `pulse_predict`, `pulse_compose`, `pulse_sample`, `pulse_facet`) whose JSON Schemas embed enum constraints on field-name parameters. The LLM picks field names from a typed list rather than free-texting and discovering on predict that the name was wrong.

What gets constrained on bound `pulse_process` / `pulse_predict` / `pulse_compose` schemas:

| Path | Enum |
|---|---|
| `aggregations[].field` | All cohort field names |
| `aggregations[].type` | Full aggregator catalogue (`AGG_*`) |
| `attributes[].field` | Numeric fields only (includes decimal) |
| `attributes[].type` | Full attribute catalogue (`ATTR_*`) |
| `filterers[].field` | All cohort field names |
| `filterers[].type` | Full filterer catalogue (`FILTER_*`) |
| `groups[].field` | All cohort field names |
| `groups[].type` | Full grouper catalogue (`GROUP_*`) |
| `windows[].field`, `windows[].partition_by[]` | All cohort field names |
| `windows[].order_by[].field` | Numeric and date fields |
| `windows[].type` | Full window catalogue (`WIN_*`) |
| `tests[].field`, `tests[].field2` | Numeric fields only |
| `tests[].split_by` / `rows` / `cols` / `subject_field` | All cohort field names |
| `tests[].type` | Full test catalogue (`TEST_*`) |
| `pulse_facet` `field` arg | All cohort field names |

**Trigger and lifecycle.** Binding fires on a successful `pulse_inspect`. The go-sdk server auto-fires `notifications/tools/list_changed` on the post-serve `AddTool` / `RemoveTools` swap; the host refreshes its tool list and picks up the bound schemas on the next list. Bound tools share names with the global tools — the session-scoped variants override globals for that session.

**Limitations.**
- *Multi-file sessions:* the latest inspect wins. Track multiple cohorts client-side.
- *No per-element type ↔ field correlation:* JSON Schema can't easily express "if `aggregations[i].type == AGG_SUM` then `aggregations[i].field` must be numeric." Operator–type compatibility lives in the `type` property description; strict validation remains `pulse_predict`'s job.
- *Transport support:* binding mutates the single stdio-session server post-serve (`AddTool` / `RemoveTools` auto-emit `list_changed`), so stdio — the transport `pulse mcp` ships — honours it. There is no per-session schema override for a shared HTTP server, a documented limitation. The manifest's `accepts_types` table is still authoritative, so authoring is never blocked regardless of transport.
- *Empty enums omitted:* when the cohort has zero fields in a category (e.g. no geo fields), the enum is omitted entirely rather than emitted as `[]`.

Disable binding entirely with `--bind-on-open=false`.

## Configuration

| Env var | Purpose | Default |
|---|---|---|
| `PULSE_DATA_DIR` | Cohort base directory. Required. | (none — server fails to start without it) |
| `PULSE_IMPORTS_DIR` | Subdirectory for managed-import handles. | `imports` |
| `PULSE_IMPORT_TTL` | Default TTL for managed handles. Accepts Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin`. | `7d` |

Embedders can override per-instance via `pulse.Options{DataDir, ImportsDir, ImportTTL, ImportSourceJailRoot, FS, ImportSourceFS, BindOnOpen}` — see `pulse.go`.

## Transport caveats

- **Stdio.** The default and only transport `pulse mcp` ships today. Schema bind-on-inspect works on the single stdio session (see Limitations). Stdout is the JSON-RPC channel; stderr is the log channel — never write structured output to stdout outside the protocol.
- **Streamable HTTP.** Not exposed by the `mcp` CLI leaf yet. The underlying go-sdk server supports it; embedders build their own `go-sdk` server and call `gosdk.Register(server, p, cfg)` (or use `mcpserve.Serve`), then serve over the transport of their choice. Bind-on-inspect has no per-session override on a shared HTTP server (see Limitations).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `data directory required: set PULSE_DATA_DIR or pass --data-dir` | Neither env var nor flag set | Pass `PULSE_DATA_DIR` in the host's `env` block, or `--data-dir` in `args` |
| Tools don't appear in the host UI after editing config | Host caches tool list | Restart the host fully (not just the conversation) |
| `pulse_import` returns `PULSE_IMPORT_SOURCE_FORBIDDEN` for an absolute path | Path escapes the import jail (default = server's working dir) | Either move the file under the jail, launch the server from a higher-level directory, or set `pulse.Options.ImportSourceJailRoot` when embedding |
| `pulse_inspect` succeeds but bound enums never fire | Stdio session — binding is a no-op there | Use `pulse_predict` for validation; the manifest's `accepts_types` lists give the LLM the same information |
| Tool calls hang | Host wrote non-protocol bytes to the server's stdin, or server wrote non-protocol bytes to stdout | Check server stderr; restart the session. `pulse mcp` itself only writes a one-line startup notice to stderr at boot |

To see what the server registers without launching the host:

```bash
pulse --json | jq '.data.mcp_tools[]'
pulse manifest --json | jq '.data.skills[]'
```

## Skill cross-reference for LLM agents

If you are writing a system prompt for an LLM agent that uses Pulse, point it at these skills rather than at this site:

| LLM task | Skill |
|---|---|
| MCP wiring, tool surface, schema binding | `mcp-integration` |
| Author a `Process` request | `getting-started`, `aggregation-guide` |
| Compose multiple sub-requests in one call | `compose-requests` |
| Iterate on a request with `pulse_predict` | `debugging-with-predict` |
| Look up an error code or warning | `error-code-reference` |
| Pick an aggregator / filterer | `aggregation-guide` |
| Pick an attribute (z-score, percentile, formula, ...) | `attribute-composition` |
| Design a grouper | `grouper-design` |
| Use a window operator (`WIN_*`) | `window-operations` |
| Use a feature engineer (`FEAT_*`) | `feature-engineering` |
| Run a statistical test (tier-1 or tier-2) | `statistical-testing` |
| Fit a regression (OLS, GLM, Bayesian) | `regression-modeling` |
| Generate synthetic data | `synthetic-data` |
| Understand a cohort's schema layout | `cohort-schema-design` |
| Import a tabular source into `.pulse` | `import-best-practices` |
| Pick an export format | `export-format-selection` |
| Work with `decimal128` (currency, precise arithmetic) | `financial-cohorts` |
| Get started end-to-end (LLM walkthrough) | `getting-started` |

The agent should call `pulse_skills_list` once at session start to enumerate the catalog, then `pulse_skills_get` on demand. The returned text is authoritative; this site does not duplicate it and may lag.

## Related

- [`mcp` (CLI leaf)](../cli/mcp.md) — flag reference and exit codes for the server binary
- [Deployment](../ops/deployment.md) — production hardening notes
- [Troubleshooting](../ops/troubleshooting.md) — non-MCP failure modes
