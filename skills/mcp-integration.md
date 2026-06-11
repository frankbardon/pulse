---
name: mcp-integration
description: Pulse's MCP tool surface (pulse_manifest, pulse_inspect, pulse_predict, pulse_process, pulse_compose, pulse_sample, pulse_facet, pulse_facet_schema, pulse_examples_search/get, pulse_errors_lookup, pulse_synth_*), pulse:// and pulse-skill:// resources, schema-bound field enums on inspect, recommended session bootstrap order.
type: guide
applies_to: process, compose, sample, facet, inspect, predict, manifest
---

# MCP Integration

<skill_overview>
This skill is for the LLM consuming Pulse via MCP. It documents the tool surface you have access to, the deterministic-bootstrap pattern, the schema-binding behavior that constrains your tool-call arguments after the first inspect, and the resource scheme used to list cohort files. Setup instructions for wiring `pulse mcp` into Claude Desktop, Claude Code, or any other MCP client live in mdBook at https://frankbardon.github.io/pulse/mcp/index.html and https://frankbardon.github.io/pulse/cli/mcp.html — point a human there.
</skill_overview>

<reference>
## Tool surface

Every Pulse tool wraps a public library entry point. Inputs are JSON; outputs are JSON-encoded text content (the standard Pulse envelope).

| MCP tool | Wraps | Input fields |
|---|---|---|
| `pulse_inspect` | Header + schema + dictionaries | `path` (string) |
| `pulse_predict` | Validate a request against schema, no execution | `request` (JSON-encoded `types.Request`) |
| `pulse_process` | Execute one request | `request` (JSON-encoded `types.Request`) |
| `pulse_process_chain` | Execute a source-rooted linear chain of mergeable stages (stage N+1 feeds off stage N's rows) | `request` (JSON-encoded `pulse.ChainRequest`) |
| `pulse_compose` | Execute a batch | `request` (JSON-encoded `types.ComposedRequest`) |
| `pulse_sample` | First N rows | `path` (string), `count` (number, default 10) |
| `pulse_facet` | Distinct values for one field (categorical fast path / numeric scan) | `path` (string), `field` (string) |
| `pulse_facet_schema` | Multi-field rich facet — counts, null tallies, numeric stats, optional percentiles, histograms, additive contribution counts. Prefer over repeated `pulse_facet` calls. | `request` (JSON-encoded `pulse.FacetRequest`) |
| `pulse_skills_list` | Embedded skill metadata | (none) |
| `pulse_skills_get` | Fetch one skill body | `name` (string) |
| `pulse_manifest` | Root self-description: commands, components, cohort types, skills, tests, distributions, error codes | (none) |
| `pulse_examples_search` | Search the embedded request-example library by query, taxonomy tags (ANDed), and/or category | `query` (string, optional), `tags` (array of strings, optional), `category` (string, optional) |
| `pulse_examples_get` | Fetch one example record with runnable request JSON (the `body` field has the `_meta` block stripped) | `name` (string) |
| `pulse_errors_lookup` | Look up Pulse error code metadata. Pass `code` for one-code detail, `domain` to enumerate a domain, `query` for substring search across descriptions and fixup hints. Returns an array of `{code, domain, message, fixups}` records | `code` (string, optional), `domain` (string, optional), `query` (string, optional) |
| `pulse_import` | Import a tabular source (csv, tsv, ndjson, jsonarray, parquet, arrow, excel) into a managed .pulse handle, or pass through an existing .pulse file unchanged. Auto-detects format from the extension; override via `format`. Managed handles live in `$PULSE_DATA_DIR/imports/` with a TTL-tracked sidecar — every subsequent inspect / predict / process / sample / facet against the handle slides expiry forward. Pulse-format sources skip the copy + sidecar; they pass through with `managed=false`. | `source` (string, required), `format` (string, optional), `handle` (string, optional), `ttl` (string, optional — default `"7d"`; Go duration like `"24h"`, day form `"7d"`, or `"pin"` for never-expire), `sheet` (string, optional Excel only), `overwrite` (bool, optional) |
| `pulse_drop` | Drop a managed-import handle from the pool — deletes the `.pulse` file and its sidecar. Errors with `PULSE_IMPORT_SOURCE_MISSING` when the handle is unknown. Pulse-format passthroughs are unaffected (they were never managed). | `handle` (string, required) |
| `pulse_imports_list` | List every managed-import handle in the pool with its sidecar metadata: source path, source format, imported_at, expires_at, ttl, expired flag, pinned flag. Sweep is not invoked — expired entries are flagged via `expired` so callers can render them and decide whether to drop or extend. | (none) |
| `pulse_label_tables` | List the registered label tables (ID→display-name dictionaries for categorical fields): name, row count, and whether the table is enumerable (reverse-searchable). Discovery companion to `pulse_label_resolve`. | (none) |
| `pulse_label_resolve` | Reverse-resolve a human-readable name — **typo-tolerant** — to the raw categorical key(s) a filter / grouper expects. Labels are output-only, so filter / group / sort keys see the raw value — resolve a user-supplied name to its key before authoring a `FILTER_INCLUDE` or `GROUP_CATEGORY`. Returns `{key, value, score}` ranked by `score` (0–1: 1.0 exact, ~0.9+ prefix/near-typo, lower fuzzy via edit-distance + trigram, case/punctuation normalized). Use the top hit when its score is high and clearly ahead; otherwise present the top names to the user and ask. | `table` (string, required), `query` (string, optional — empty browses), `limit` (number, optional — default 10) |

The canonical list is `internal/mcp.RegisteredTools()`. Adding or removing a tool requires updating this skill in the same PR (`TestSkillsCoverAllMCPTools`).

### Managed imports + TTL

`pulse_import` is the entry point for "give Pulse a file in whatever format I have and let me address it from then on as if it were a `.pulse`." Two paths:

- **Convertible format** (csv, tsv, ndjson, jsonarray, parquet, arrow, excel): the server runs the import job, writes `$PULSE_DATA_DIR/imports/<handle>.pulse`, and writes a sidecar `<handle>.pulse.meta.json` carrying `imported_at`, `expires_at`, `ttl_seconds`, source path, source format, and row count. `Result.managed=true`.
- **Pulse passthrough** (`format=pulse` or `.pulse` extension): a `.pulse` source already under `PULSE_DATA_DIR` is not copied — the server just confirms it is readable and returns its relative path verbatim with `managed=false`. A `.pulse` source at an absolute path outside `PULSE_DATA_DIR` is copied into the managed pool (the rooted Pulse fs cannot address external absolute paths via inspect / process otherwise), with a normal TTL sidecar and `managed=true`.

**Source path resolution.** Relative `source` paths resolve against `PULSE_DATA_DIR`. Absolute paths (`/Users/...`, `/home/...`, `/var/...`) read from the host filesystem through a separate source fs — clients can hand the server any file under the import jail without first copying it under `PULSE_DATA_DIR`. The managed `.pulse` always lands inside `PULSE_DATA_DIR/imports/` regardless of where the source came from.

**Import jail.** Absolute source paths are confined to a single directory tree (the *jail root*). By default the jail root is the working directory the MCP server / CLI was launched from — so a `pulse mcp` invocation can only reach files under that tree. Paths that escape the jail (including `..` traversal) return `PULSE_IMPORT_SOURCE_FORBIDDEN`. Override via `pulse.Options.ImportSourceJailRoot` when embedding, or pass an explicit `ImportSourceFS` to manage access yourself (the explicit fs IS the boundary in that case).

After a successful `pulse_import` the server fires the same schema-binding hook as `pulse_inspect` (see below), so subsequent `pulse_process` / `pulse_predict` / `pulse_compose` / `pulse_sample` / `pulse_facet` calls against the new handle pick up typed field enums.

TTL is a sliding window. The default lifetime is `7d` (overridable via `PULSE_IMPORT_TTL`). Every subsequent operation against the handle bumps `expires_at` forward by the same TTL. The pool self-sweeps on every `pulse_import` call — no daemon required — and the operator can introspect with `pulse_imports_list` or evict manually with `pulse_drop`.

Pinned imports (`ttl="pin"`) never expire and are skipped by the sweeper. Use them for handles you want to keep around for the duration of a session regardless of activity (e.g., a reference cohort).

Out of scope (deferred): `pulse_validate` and `pulse_join` — gated on Improvements 05 and 07.
</reference>

<workflow id="bootstrap" name="session-bootstrap">
## Session bootstrap

1. **Call `pulse_manifest` once at session start.** No arguments. Cache the payload.

   The manifest is deterministic for a given binary version and carries every fact needed to author a valid request without further discovery round-trips: per-operator params + accepted field types + emit type + streamability hint, the statistical test catalog (tier-1 row tests and tier-2 post tests as peer slices), the synth distribution catalog, the error code catalog, the MCP tool list, and the cohort field-type catalog with operator cross-references.

2. **Call `pulse_import` when the user hands you a raw tabular source** (csv, tsv, ndjson, jsonarray, parquet, arrow, excel). The server converts to a managed `.pulse` handle with a TTL sidecar and fires the same schema-binding hook as `pulse_inspect`. Skip this step when the cohort is already a `.pulse` file or an existing managed handle.

   ```json
   {"source": "data.csv", "ttl": "7d"}
   ```

3. **Call `pulse_inspect` to bind schema-aware enums** into the session's action tools. After this call the field-name and operator-type arguments on `pulse_process` / `pulse_predict` / `pulse_compose` / `pulse_sample` / `pulse_facet` are restricted to values the cohort schema actually carries.

   ```json
   {"path": "sales.pulse"}
   ```

4. **Call `pulse_predict` to validate a hand-authored request** against the schema before paying for execution. Read `errors`, `warnings`, `data.suggestions[]`, `data.defaults_applied`, and `data.streamable_reasons`.

   ```json
   {"request": "{\"cohort\":{\"filename\":\"sales.pulse\"},\"aggregations\":[{\"type\":\"AGG_COUNT\",\"field\":\"id\",\"label\":\"n\"}]}"}
   ```

5. **Call `pulse_process` (or `pulse_compose` for batches, `pulse_process_chain` for source-rooted linear chains) to execute** once predict is clean.

Subsequent requests in the same session reference the cached manifest. Re-fetch only if the underlying binary changes (a notification you would typically receive out of band). Every operation against a managed handle slides its TTL forward — the handle stays warm for the session.
</workflow>

<reference>
## Schema-bound enums

After a successful `pulse_inspect` (or `pulse_import` of a non-pulse source), the server registers session-scoped variants of the five action tools (`pulse_process`, `pulse_predict`, `pulse_compose`, `pulse_sample`, `pulse_facet`) whose JSON Schemas embed enum constraints on field-name parameters. You pick field names from a typed list instead of free-texting them and discovering on predict that the name was wrong.

What gets constrained on the bound `pulse_process` / `pulse_predict` / `pulse_compose` schemas (within the `request` object):

| Path | Enum |
|---|---|
| `aggregations[].field` | All cohort field names (operator–field-type compatibility is communicated via the `type` description, not a correlated enum — see Limitations) |
| `aggregations[].type` | Full aggregator catalogue (`AGG_*`) |
| `attributes[].field` | Numeric fields only (includes decimal) — attributes have a clean type-class scope |
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
| `pulse_facet_schema` `request.fields[]` / `request.additive_fields[]` / `request.filterers[].field` | All cohort field names |

### Request slot keys vs. catalog names

A request's grouping operations go under the `groups` key and its aggregations under `aggregations`. These are **not** the same identifiers the manifest uses for its operator catalogs, which are `groupers` and `aggregators` — those catalogs only enumerate the available `GROUP_*` / `AGG_*` operators. A common authoring mistake is reusing the catalog name as the request key (`{"groupers": [...]}`), which JSON decoding silently drops, so the request runs as if the grouping were absent.

`pulse_predict`, `pulse_process`, `pulse_compose`, and `pulse_process_chain` guard against this: an unrecognized top-level request key is rejected with `PULSE_REQUEST_UNKNOWN_FIELD`, whose message and `details` carry the offending key, the nearest valid slot (`groupers → groups`, `aggregators → aggregations`), and the full valid-key list. Rename the key to the suggested slot and retry.

| Request slot | Manifest catalog |
|---|---|
| `groups` | `groupers` |
| `aggregations` | `aggregators` |
| `attributes` | `attributes` |
| `filterers` | `filterers` |
| `windows` | `windows` |
| `features` | `features` |

### Trigger and lifecycle

- Binding fires on a successful `pulse_inspect`, not on resource subscription. Inspect is the natural moment: the server has just read the schema.
- `mcp-go` auto-fires `notifications/tools/list_changed` on `AddSessionTools`. Your client refreshes the tool list and picks up the bound schemas on the next list-tools call.
- Bound tools share names with the global tools (`pulse_process`, not `pulse_process_bound`). Session-scoped tools override globals for that session.

### Limitations (v1)

- **Multi-file sessions:** the latest inspect wins. If you inspect `A.pulse` then `B.pulse`, the bound schemas reflect file B. A subsequent process call against A may succeed (if the schemas overlap) or fail predict (if not). Track multiple cohorts client-side; do not assume the server retains per-file binding state.
- **No per-element type→field correlation:** JSON Schema can't easily express "if `aggregations[i].type == AGG_SUM` then `aggregations[i].field` must be numeric." Operator–type compatibility lives in the `type` property description. Strict validation remains predict's job.
- **Transport support:** session-scoped tools require a session that implements `SessionWithTools` (`SetSessionTools` / `GetSessionTools`). On the SSE and Streamable HTTP transports this works; on stdio, binding is a no-op fallback and the global (unbound) schemas remain in effect. The manifest's per-operator AcceptsTypes table is still available via `pulse_manifest`, so authoring is not blocked — just less ergonomic.
- **Empty enums omitted:** when the cohort has zero fields in a category (e.g. no geo fields), the matching enum is omitted entirely rather than emitted as `[]`. Some JSON Schema validators reject empty enums.

### Source

Binding logic: `internal/mcp/schema_bind.go`. Hook: `handleInspect` in `internal/mcp/tools.go`. Field classification by `encoding.FieldType` — mirrors the AcceptsTypes lists in `descriptor/capabilities_*.go`.
</reference>

<reference>
## Resource surface

Resources are registered once at server start. To pick up newly created files, the server has to be restarted (a host-side concern, not yours).

| URI scheme | What | Read returns |
|---|---|---|
| `pulse://<path>` | One per `.pulse` file under the data directory | `descriptor.InspectResult` JSON (header + schema, no record bytes) |
| `pulse-skill://<name>` | One per embedded skill | Raw markdown body |

Path is relative to the configured data directory. The server reads only header bytes for cohort resources, so listing is cheap regardless of cohort size.

If your client supports resource subscription, listing `pulse://*` resources is a discovery shortcut: every cohort under the data root surfaces without needing `pulse_inspect` first. Reading a resource then gives you the schema for that file directly.

## Prompt surface

The server registers MCP prompts so clients can surface a canonical "how to use Pulse" preamble at session start or as a slash command. The discovery flow encoded in tool descriptions is also expressed here, in case the client surfaces prompts more prominently than tool metadata.

| Name | Arguments | What it returns |
|---|---|---|
| `pulse-bootstrap` | none | A short instructions block telling the assistant which Pulse tools to call (and in what order) before authoring any request, and where the authoritative request-shape references live. Inject at the top of a fresh session. |
| `pulse-author-request` | `question` (required) | A guided tool-call sequence for translating an analytical question into a Pulse request — manifest → examples search → predict → process. |

Why this exists: when Pulse is deployed remotely (not co-located with the calling LLM's source tree), the model has no codebase to read. The bootstrap prompt + the "DO NOT infer request shapes from external documentation or source code" framing in `pulse_manifest`, `pulse_examples_search`, and `pulse_process` descriptions steer the assistant toward the manifest + example library, which are authoritative for the deployed Pulse version.
</reference>

<workflow id="agent-flow" name="agent-call-pattern">
## Typical agent flow

1. (Once per session) Call `pulse_manifest`. Cache the result.
2. (When the user hands you a raw tabular source) Call `pulse_import` to land it in the managed pool as a `.pulse` handle. Skip when the cohort is already a `.pulse` file or an existing handle.
3. Call `pulse_inspect` on the cohort to read its schema and bind schema-aware enums into the session's action tools.
4. Author a `types.Request` against the cached manifest and the inspected schema.
5. Call `pulse_predict` to validate — read `errors`, `warnings`, `data.suggestions[]`, `data.defaults_applied`, `data.streamable_reasons` before executing.
6. Call `pulse_process` (or `pulse_compose` for batches, `pulse_process_chain` for source-rooted linear chains) to execute the validated request.

Iterate against the same handle for subsequent questions. Every operation slides its TTL forward — the handle stays warm for the session.

### Other tools by situation

- **Eyeballing data.** Call `pulse_sample` or `pulse_facet` to peek at rows or single-field value distributions; call `pulse_facet_schema` for a multi-field rich summary.
- **Error repair.** Call `pulse_errors_lookup` with any `code` you see in an envelope to fetch its canonical `message` and `fixups[]` template list.
- **Pre-staging a managed handle.** Call `pulse_import` ahead of time when you want to lock in a handle name, TTL, or pinning policy without immediately running a query.
- **Discovering examples.** Call `pulse_examples_search` against the embedded request-example library; fetch a candidate body with `pulse_examples_get`.
</workflow>

<example name="predict-via-mcp">
## Calling pulse_predict

Tool input:

```json
{
  "request": "{\"cohort\":{\"filename\":\"sales.pulse\"},\"aggregations\":[{\"type\":\"AGG_COUNT\",\"field\":\"id\",\"label\":\"n\"}]}"
}
```

The handler accepts either a JSON-encoded string (above) or a structured object — both round-trip to `types.Request`. Output is the standard envelope's `data` field as a text content item.
</example>

<reference>
## Transport caveats that affect you

- **Stdio:** schema-binding is a no-op (see Limitations). Argument validation is still possible via `pulse_predict`. The manifest still carries the operator catalog, so authoring against the cached manifest works fine.
- **SSE / Streamable HTTP:** schema-binding works. After your first `pulse_inspect` your action-tool argument types narrow to the cohort's actual field names — error rates drop sharply.
- **Stdout discipline:** in stdio mode, stdout is the JSON-RPC transport. The server logs to stderr. This is invisible to you but affects what a host can show in its UI.
</reference>

<embedding>
## Embedding the MCP server (for host applications)

`pulse mcp` is the standalone path. A host that has already constructed a
`*pulse.Pulse` — especially one configured with `Options.Extensions` (custom
operators, expression functions, label tables) — serves the same MCP surface
from its own process via the public `mcpserve` package:

```go
p, _ := pulse.New(pulse.Options{DataDir: dir, Extensions: myExtensions})
// Over the process's stdin/stdout (what an MCP client spawns as a subprocess):
_ = mcpserve.ServeStdio(p, mcpserve.Options{BindOnOpen: true})
// Or with an injected transport (tests, custom pipes):
_ = mcpserve.Serve(ctx, p, mcpserve.Options{BindOnOpen: true}, in, out)
```

This is the only way a domain layer's in-process Go extensions reach MCP
clients — the stock `pulse` binary cannot load them. The tool surface,
schema-binding, and resource schemes are identical to `pulse mcp`.
</embedding>

<see_also>
- getting-started — Pulse vocabulary and the 10 MCP tools
- debugging-with-predict — pattern for iterating on a request before processing
- error-code-reference — error codes the tools may return inside the envelope
</see_also>
