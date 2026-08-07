---
name: session-bootstrap
description: Canonical MCP session order — manifest once, then examples → predict → process → errors_lookup. Skill-name derivation from operator names. Use first on every new MCP session.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [pulse_manifest, pulse_examples_search, pulse_examples_get, pulse_skills_list, pulse_skills_get, pulse_inspect, pulse_predict, pulse_process, pulse_errors_lookup]
---

# Session bootstrap

The canonical order for an LLM driving Pulse over MCP. Do this once per session; cache; re-enter from step 3 for every user question.

## The order

1. **`pulse_manifest`** — once per session. Cache. Deterministic for a binary version. Carries: operator catalogs, field types, error codes, MCP tool list, `components_schemas`, skills index, extensions, capability blocks (Facet, Join, ProcessChain, Crosstab, Overlays).

2. **`pulse_inspect`** — once per cohort path. Returns schema (fields, types, descriptions, dictionaries). Side-effect: binds schema-aware enums into the session's action tools (`pulse_process`, `pulse_predict`, `pulse_compose`, `pulse_sample`, `pulse_facet`) so field-name arguments stay constrained to schema-resident values.

3. **`pulse_examples_search`** — for each user question. Search by `query` + `tags` + `category`. Returns name + summary. Examples are the canonical source of runnable Request JSON.

4. **`pulse_examples_get`** — fetch the candidate `body` (runnable Request JSON; `_meta` block stripped). Adapt cohort filename, field names, labels.

5. **`pulse_skills_get`** — on demand. Skills carry shape, gotchas, and design contract. Runnable JSON comes from examples, not skills. Default to `getting-started` if cold; otherwise use the skill-name derivation rules below.

6. **`pulse_predict`** — validate the adapted Request against the schema. Read `errors`, `warnings`, `data.suggestions`, `data.defaults_applied`, `data.streamable`, `data.streamable_reasons`. Iterate until clean.

7. **`pulse_process`** (or `pulse_compose` / `pulse_process_chain` / `pulse_facet` / `pulse_facet_schema` / `pulse_sample` / `pulse_lookup`) — execute. Returns `{format_version, data, errors, warnings}` plus the additive `data.components` family. `format_version` is `"1.1"`. For `pulse_compose` `data` is a `ComposedResponse` `{responses[], overlays[]}` — not the legacy `[]*Response`; per-slot Components rides each `responses[i]`, overlay diagnostics ride `overlays[i].warnings`.

8. **`pulse_errors_lookup`** — on any non-empty `errors[]` entry. Resolves `code` to canonical `message` + structured `fixups[]`. Do not paraphrase from memory — the lookup is authoritative.

## Skill-name derivation

When a manifest operator name or response key surfaces a topic, derive the skill name to fetch:

| Trigger | Skill to fetch |
|---|---|
| `AGG_*` operator name | `aggregation-guide` |
| `ATTR_*` | `attribute-composition` |
| `FILTER_*` | `aggregation-guide` (filtering section) |
| `GROUP_*` | `grouper-design` |
| `WIN_*` | `window-operations` |
| `FEAT_*` | `feature-engineering` |
| `TEST_*` (tier-1 or tier-2) | `statistical-testing` |
| `REG_*` | `regression-modeling` |
| `OVERLAY_*` | `overlay-system` |
| `synth_distributions[i].kind` | `synthetic-data` |
| `cohort_types[i].name` (field type) | `cohort-schema-design` |
| `mcp_tools[i].name` | `mcp-integration` |
| `pulse_lookup` / CLI `pulse index {build,list,verify,drop}` / `pulse api lookup` | `tool-lookup` (MCP surface), `cohort-schema-design` (sidecar format) |
| `error_codes[i]` | `pulse_errors_lookup` (not a skill — the tool is the surface) |
| Request slot: `Joins` | `join-design` |
| Request slot: `Crosstab` | `crosstab-guide` |
| Request slot: `Overlays` | `overlay-system` |
| Response slot: `data.components` (first sight) | `response-components` |
| Response slot: `Metadata.LabelBindings` | `label-display` |
| `ComposedRequest` | `compose-requests` |
| `ChainRequest` | `contributor-workflow` (ProcessChain section) |
| `FacetRequest` / `FacetSchemaRequest` | `facet-design` |
| Streaming, watch, request hashing | `streaming-and-watching` |
| Extension operator (anything in `manifest.extensions`) | `extension-points` |
| Predict failed — fix loop | `debugging-with-predict` |

The general rule: lowercase the operator family prefix (`AGG`, `ATTR`, `FILTER`, ...) and map to the target skill via the table above. Skills do NOT carry runnable JSON; examples do.

## `Response.Components` — v0.20.0 first-sight protocol

When you see `data.components` (or `Response.Components` in a typed envelope) for the first time in a session:

1. Call `pulse_skills_get` with `name: "response-components"`.
2. It is the canonical reference for the per-family shape: `aggregations[]`, `groupers[]`, `crosstab`, `filterers[]`, `run`.
3. `format_version` is `"1.1"` — Components itself is additive-only and never bumps; the bump came from the Compose facade lift.
4. Universal floor `{n, n_null}` (or `{total_n, n_null}` / `{n_in, n_out, n_null_input}` depending on family) is filled by the orchestrator and is NOT enumerated in `manifest.components_schemas[*].keys`.
5. Per-operator keys appear inside `Operator map[string]any` (aggregations / groupers) or directly inside the cell map (crosstab cells). Look them up via `manifest.components_schemas.aggregators[name].keys` / `.groupers[name].keys` / `.filterers[name].keys`.

Do not infer Components shape from memory or external documentation — it is bound to the binary version and lives in the manifest plus the `response-components` skill.

## Authoring layers

- Manifest is the contract: operator names, parameter shapes, streamable hints, error codes.
- `pulse_inspect` is the field-name + value source.
- `pulse_examples_*` is the runnable-JSON source. Adapt; do not invent.
- Skills carry gotchas, slot-key naming, design rationale.
- `pulse_errors_lookup` is the per-code prose source.

## On failure

1. Read every `errors[]` / `warnings[]` entry — `{code, message, details}`.
2. `pulse_errors_lookup` each unique `code`. Cache.
3. Apply returned `fixups[]`. Re-`pulse_predict`. Iterate.
4. Re-`pulse_process` only when predict is clean.

## Environment

Directories auto-loaded from disk at `pulse.New` time:

- `PULSE_LABEL_TABLES_DIR` — output-time label tables.
- `PULSE_RANGE_TABLES_DIR` — named labeled-date-range tables (`{label,start,end}` sets referenced by `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES`).
- `PULSE_TEMPLATES_DIR` — parameterised request templates; `os.PathListSeparator`-separated roots in precedence order, first root wins. Render via `RenderTemplate` / `RenderTemplateRequest`, then predict the rendered request. See the `request-templating` skill.

Both table kinds surface under `manifest.extensions.{label_tables,range_tables}` — check there before assuming a named table exists. Templates are not manifest-projected; enumerate them with `ListTemplates` (`Summary.Broken` flags a file that has gone malformed since load).

## Cross-links

- `request-envelope` — envelope shape, slot keys, smart defaults, streamability flag.
- `response-components` — v0.20.0 Components family + per-operator key tables.
- `mcp-integration` — every registered tool, full per-tool argument shape.
- `debugging-with-predict` — predict loop in detail.
- `getting-started` — vocabulary + pipeline order primer (cold-start fallback).
