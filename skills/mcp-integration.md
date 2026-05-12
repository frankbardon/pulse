---
name: mcp-integration
description: What the LLM should know about Pulse's MCP surface — tool catalog, session bootstrap, schema-binding behavior
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
| `pulse_compose` | Execute a batch | `request` (JSON-encoded `types.ComposedRequest`) |
| `pulse_sample` | First N rows | `path` (string), `count` (number, default 10) |
| `pulse_facet` | Value distribution for one field | `path` (string), `field` (string) |
| `pulse_skills_list` | Embedded skill metadata | (none) |
| `pulse_skills_get` | Fetch one skill body | `name` (string) |
| `pulse_manifest` | Root self-description: commands, components, cohort types, skills, tests, distributions, error codes | (none) |
| `pulse_ask` | Unified inspect -> predict -> process one-shot. On predict-invalid with `on_invalid="suggest"`, returns structured `Suggestions` instead of erroring. Accepts either `request` or natural-language `query`. | `request` (JSON-encoded `pulse.AskRequest`) |
| `pulse_examples_search` | Search the embedded request-example library by query, taxonomy tags (ANDed), and/or category | `query` (string, optional), `tags` (array of strings, optional), `category` (string, optional) |
| `pulse_examples_get` | Fetch one example record with runnable request JSON (the `body` field has the `_meta` block stripped) | `name` (string) |

The canonical list is `internal/mcp.RegisteredTools()`. Adding or removing a tool requires updating this skill in the same PR (`TestSkillsCoverAllMCPTools`).

Out of scope (deferred): `pulse_validate` and `pulse_join` — gated on Improvements 05 and 07.
</reference>

<workflow id="bootstrap" name="session-bootstrap">
## Session bootstrap

1. Call `pulse_manifest` once at session start. No arguments. Cache the payload.

   The manifest is deterministic for a given binary version and carries every fact needed to author a valid request without further discovery round-trips: per-operator params + accepted field types + emit type + streamability hint, the statistical test catalog (tier-1 row tests and tier-2 post tests as peer slices), the synth distribution catalog, the error code catalog, the MCP tool list, and the cohort field-type catalog with operator cross-references.

2. Subsequent requests reference the cached manifest. Re-fetch only if the underlying binary changes (a notification you would typically receive out of band).

A single bootstrap call replaces a long discovery dance against `pulse_skills_list`, per-operator round-trips, and per-error-code lookups.
</workflow>

<reference>
## Schema-bound enums

After a successful `pulse_inspect` (or after `pulse_ask` opens a cohort), the server registers session-scoped variants of the five action tools (`pulse_process`, `pulse_predict`, `pulse_compose`, `pulse_sample`, `pulse_facet`) whose JSON Schemas embed enum constraints on field-name parameters. You pick field names from a typed list instead of free-texting them and discovering on predict that the name was wrong.

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
</reference>

<workflow id="agent-flow" name="agent-call-pattern">
## Typical agent flow

1. Call `pulse_skills_list` (optional — the manifest also carries skill metadata) to learn what skills exist.
2. Call `pulse_skills_get` with `name=getting-started` to load the mental model.
3. List `pulse://*` resources (or call `pulse_inspect` on a known path) to learn schemas.
4. Author a request matching a known schema. See `request-recipes`.
5. Call `pulse_predict` with the request JSON. Inspect `errors`, `warnings`, `suggestions`, and `streamable_reasons`.
6. Call `pulse_process` (or `pulse_compose` for batches).

For the common path, `pulse_ask` collapses steps 5–6 into a single round trip: it inspects, validates, and (when valid) executes. On validation failure with `on_invalid="suggest"`, the response carries a de-duplicated list of structured `Fixup` entries derived from each error code's metadata, so you can repair the request without re-querying the schema.

For natural-language input, hand the user's text to `pulse_ask` under the `query` field instead of `request`. See `query-router-prompt` for the recommended system-prompt template.
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

<see_also>
- getting-started — Pulse vocabulary and the 10 MCP tools
- request-recipes — copy-pasteable request JSON skeletons keyed by intent
- query-router-prompt — system-prompt template for natural-language queries
- debugging-with-predict — pattern for iterating on a request before processing
- error-code-reference — error codes the tools may return inside the envelope
</see_also>
