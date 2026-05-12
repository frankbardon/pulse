---
name: mcp-integration
description: Wire Pulse into MCP-aware AI clients (Claude Desktop, Claude Code) and call its tools and resources
type: guide
applies_to: process, compose, sample, facet, inspect, predict, manifest
---

# MCP Integration

<skill_overview>
The `pulse mcp` command serves the Model Context Protocol over stdio so MCP-aware AI clients can discover Pulse's tool surface, list cohort files, fetch skill markdown, and execute requests through a single protocol. Tools wrap the public library facade 1:1; resources expose `.pulse` files and embedded skills as read-only URIs.
</skill_overview>

<reference>
## Server identity

| Property | Value |
|---|---|
| Server name | `pulse` |
| MCP spec version | `1.0.0` (advertised during `initialize`) |
| Transport | stdio |
| Required env | `PULSE_DATA_DIR` (or `--data-dir` flag) — roots the filesystem |
</reference>

<reference>
## Tool surface

Every tool wraps a public library entrypoint. Inputs are JSON; outputs are JSON-encoded text content.

| MCP tool | Wraps | Input fields |
|---|---|---|
| `pulse_inspect` | `pulse.Pulse.Inspect` — header + schema + dictionaries | `path` (string) |
| `pulse_predict` | `pulse.Pulse.Predict` — validate request without executing | `request` (JSON string or object) |
| `pulse_process` | `pulse.Pulse.Process` — execute one request | `request` (JSON string or object) |
| `pulse_compose` | `pulse.Pulse.Compose` — execute a batch | `request` (JSON ComposedRequest) |
| `pulse_sample` | `pulse.Pulse.Sample` — N rows | `path` (string), `count` (number, default 10) |
| `pulse_facet` | `pulse.Pulse.Facet` — distribution of a field | `path` (string), `field` (string) |
| `pulse_skills_list` | `skills.List` — embedded skill metadata | (none) |
| `pulse_skills_get` | `skills.Get` — fetch skill body | `name` (string) |
| `pulse_manifest` | `pulse.Pulse.Manifest` — root self-description (commands, components, cohort types, skills) | (none) |

Call `pulse_manifest` once at session start and cache the result. The
manifest is deterministic and free of cohort data; it contains every
fact an LLM needs to author a valid Pulse request (operator names,
field types, command list, skill index). One bootstrap call replaces
several discovery round-trips.

The canonical list is `internal/mcp.RegisteredTools()`. Adding or removing a tool requires updating this skill in the same PR (`TestSkillsCoverAllMCPTools`).

Out of scope (deferred): `pulse_validate` and `pulse_join` — gated on Improvements 05 and 07.
</reference>

<reference>
## Schema-bound enums

After a successful `pulse_inspect` call, the server registers session-scoped
variants of the five action tools (`pulse_process`, `pulse_predict`,
`pulse_compose`, `pulse_sample`, `pulse_facet`) whose JSON Schemas embed
enum constraints on field-name parameters. The LLM picks field names from
a typed list instead of free-texting them and discovering on predict that
the name was wrong.

What gets constrained on the bound `pulse_process` / `pulse_predict` /
`pulse_compose` schemas (within the `request` object):

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

### Trigger and lifecycle

- Binding fires on a successful `pulse_inspect`, not on resource subscription. Inspect is the natural moment: the server has just read the schema.
- `mcp-go` auto-fires `notifications/tools/list_changed` on `AddSessionTools`. Clients refresh their tool list and pick up the bound schemas on the next list-tools call.
- Bound tools share names with the global tools (`pulse_process`, not `pulse_process_bound`). Session-scoped tools override globals for that session.

### CLI flag

`pulse mcp --bind-on-open=true` (default) enables the binding behaviour. Pass `--bind-on-open=false` for clients that bind tool schemas themselves.

### Limitations (v1)

- **Multi-file sessions:** the latest inspect wins. If you inspect `A.pulse` then `B.pulse`, the bound schemas reflect file B. A subsequent process call against A may succeed (if the schemas overlap) or fail predict (if not). Track multiple cohorts in the client; do not assume the server retains per-file binding state.
- **No per-element type→field correlation:** JSON Schema can't easily express "if `aggregations[i].type == AGG_SUM` then `aggregations[i].field` must be numeric." Operator–type compatibility lives in the `type` property description. Strict validation remains predict's job.
- **Transport support:** session-scoped tools require a session that implements `SessionWithTools` (`SetSessionTools` / `GetSessionTools`). In mcp-go v0.52.0, that is the SSE and Streamable HTTP transports. The stdio transport does not implement it; on stdio, binding is a no-op fallback and the global (unbound) schemas remain in effect. The LLM still gets the manifest's per-operator AcceptsTypes table via `pulse_manifest`, so authoring is not blocked — just less ergonomic.
- **Empty enums omitted:** when the cohort has zero fields in a category (e.g. no geo fields), the matching enum is omitted entirely rather than emitted as `[]`. Some JSON Schema validators reject empty enums.

### Source

Binding logic: `internal/mcp/schema_bind.go`. Hook: `handleInspect` in
`internal/mcp/tools.go`. Field classification by `encoding.FieldType` —
mirrors the AcceptsTypes lists in `descriptor/capabilities_*.go`.
</reference>

<reference>
## Resource surface

Resources are registered once at server start. To pick up newly created files, restart the server.

| URI scheme | What | Read returns |
|---|---|---|
| `pulse://<path>` | One per `.pulse` file under `PULSE_DATA_DIR` | `descriptor.InspectResult` JSON (header + schema, no record bytes) |
| `pulse-skill://<name>` | One per embedded skill | Raw markdown body |

Path is relative to `PULSE_DATA_DIR`. The server reads only header bytes for cohort resources, so listing is cheap regardless of cohort size.
</reference>

<workflow id="A" name="claude-desktop-config">
### Wire Pulse into Claude Desktop

Add an entry to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "pulse": {
      "command": "pulse",
      "args": ["mcp"],
      "env": { "PULSE_DATA_DIR": "/path/to/cohorts" }
    }
  }
}
```

The `pulse` binary must be on `PATH`, or use an absolute path in `command`. After restart, the Pulse tools and resources show up in the client UI.
</workflow>

<workflow id="B" name="claude-code-config">
### Wire Pulse into Claude Code

In `~/.claude.json` (or `.claude.json` in the project root):

```json
{
  "mcpServers": {
    "pulse": {
      "command": "/usr/local/bin/pulse",
      "args": ["mcp", "--data-dir", "/srv/cohorts"]
    }
  }
}
```

`--data-dir` overrides `PULSE_DATA_DIR` if both are set.
</workflow>

<workflow id="C" name="agent-call-pattern">
### Typical agent flow

1. Call `pulse_skills_list` once at boot to learn what skills exist.
2. Call `pulse_skills_get` with `name=getting-started` to load the mental model.
3. List resources to see available `.pulse` files; read each `pulse://<path>` to learn schemas.
4. Author a request matching a known schema.
5. Call `pulse_predict` with the request JSON. If invalid, fix and retry.
6. Call `pulse_process` (or `pulse_compose` for batches).
</workflow>

<example name="predict-via-mcp">
## Calling pulse_predict via MCP

Tool input:

```json
{
  "request": "{\"cohort\":{\"filename\":\"sales.pulse\"},\"aggregations\":[{\"type\":\"AGG_COUNT\",\"field\":\"id\",\"label\":\"n\"}]}"
}
```

The handler accepts either a JSON-encoded string (above) or a structured object — both round-trip to `types.Request`. Output is the standard envelope's `data` field as a text content item.
</example>

<reference>
## Stdout discipline

In stdio mode, stdout is the JSON-RPC transport. Anything Pulse needs to log goes to stderr (the startup banner, MCP-go internal hooks). Embedders writing custom hooks must follow the same rule — writing to stdout corrupts the protocol stream.
</reference>

<see_also>
- getting-started — Pulse vocabulary and CLI command tree
- debugging-with-predict — pattern for iterating on a request before processing
- error-code-reference — error codes the tools may return inside the envelope
</see_also>
