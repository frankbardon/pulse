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
