---
name: getting-started
description: Pulse vocabulary, MCP tool surface, mental model for an LLM session
type: guide
applies_to: process, compose, sample, facet, inspect, predict, manifest
---

# Getting Started

<skill_overview>
Pulse is a self-describing tabular processing engine over `.pulse` cohort files. As an LLM you reach Pulse through ten MCP tools. This skill teaches the vocabulary, the request shape, the pipeline order, and the typical session pattern. Invoke it first when onboarding to any other Pulse skill.
</skill_overview>

<reference>
## What Pulse is, in three paragraphs

A `.pulse` file is a binary cohort: a fixed-width header carrying a typed schema, optional categorical dictionaries, and a record region. Every column has a declared type from a closed set of 19 (`u8`..`categorical_u32`, `decimal128`, `point_f64`, `h3_cell`, ...). Pulse never infers types at query time; the schema in the header is the contract.

A Pulse `Request` is a JSON document that names a cohort plus the operators to apply: filterers, features, attributes, groupers, aggregators, windows, sort, and statistical tests. The engine validates the request against the cohort schema before running anything, then executes the pipeline in a fixed order and returns a typed envelope of rows, metadata, and any diagnostics.

Self-description is structural: the engine publishes a `Manifest` that names every operator, accepts/emits type, parameter, and streamability hint, plus the field-type and error-code catalogs. One call to `pulse_manifest` at session start replaces a long discovery dance — the LLM authors against the cached manifest from then on.
</reference>

<reference>
## Vocabulary

| Term | Meaning |
|---|---|
| Cohort | A `.pulse` binary file: schema header + fixed-width records. |
| Schema | Field list (name, type, description) embedded in the cohort header. |
| Field | One column. Typed with one of 19 field types (`u8` ... `h3_cell`). |
| Record | One row. Fixed-width binary block. |
| Aggregation | One of 18 `AGG_*` ops (COUNT, SUM, AVERAGE, ...) producing a per-group scalar. |
| Attribute | One of 6 `ATTR_*` ops producing a per-record derived value. |
| Filterer | One of 6 `FILTER_*` predicates run before grouping. |
| Grouper | One of 6 `GROUP_*` partition strategies run before aggregation. |
| Window | One of 10 `WIN_*` operators run after aggregation. |
| Feature | One of 8 `FEAT_*` ML pre-filter operators. |
| Test | One of 20 `TEST_*` operators (tier-1 row tests in `tests[]`, tier-2 post tests in `post_tests[]`). |
| Request | JSON: `{cohort, filterers, features, attributes, groups, aggregations, windows, sort, tests, post_tests, outputs}`. |
| ComposedRequest | `{requests: [Request, ...]}` — batch over a shared cohort. |
| Manifest | Self-description payload: commands, components, tests, synth distributions, error codes, MCP tools, cohort field types, skills. |
</reference>

<reference>
## The 10 MCP tools

Every Pulse capability you have access to is a tool registered against the MCP server.

| Tool | Purpose | Required arguments |
|---|---|---|
| `pulse_manifest` | Self-description: operator catalog, types, error codes, skills index. Call once per session, cache result. | (none) |
| `pulse_skills_list` | List embedded skill metadata. | (none) |
| `pulse_skills_get` | Fetch a skill body by name. | `name` |
| `pulse_inspect` | Read a cohort header: fields, types, descriptions, dictionaries. Also binds schema-aware enums into the session's action tools. | `path` |
| `pulse_sample` | Return N rows from the cohort for eyeballing data. | `path`, `count` |
| `pulse_facet` | Distribution of one field (value -> count). | `path`, `field` |
| `pulse_predict` | Validate a `Request` against the cohort's schema. No execution. Reports `streamable`, `streamable_reasons`, `defaults_applied`, structured `suggestions`, plus the standard envelope. | `request` (JSON-encoded `types.Request`) |
| `pulse_process` | Execute one `Request`. Returns rows + metadata + diagnostics envelope. | `request` |
| `pulse_compose` | Execute a `ComposedRequest` (batch of requests against one cohort). Order-preserving. | `request` (JSON-encoded `types.ComposedRequest`) |
| `pulse_ask` | One-shot inspect -> predict -> process. Accepts either a structured `request` or a natural-language `query` parsed against the cohort's schema. With `on_invalid="suggest"` returns structured `Fixup` entries instead of erroring on predict failure. | `request` (JSON-encoded `pulse.AskRequest`) |

Subsequent calls to `pulse_process` / `pulse_predict` / `pulse_compose` / `pulse_sample` / `pulse_facet` inside a session pick up enum constraints automatically after the first `pulse_inspect` — the field name and operator-type arguments are restricted to values that exist in the cohort's schema. The mcp-integration skill describes the binding behavior.

The full CLI surface — `pulse api process`, `pulse import csv`, `pulse export parquet`, and so on — is for humans driving the binary, not for LLMs. See https://frankbardon.github.io/pulse/cli/ if you need to point a user at it.
</reference>

<reference>
## CLI leaves at a glance

For pointing a human at the right chapter. The MCP tool list above is the LLM-facing surface; the CLI is documented in mdBook.

| Leaf | One-line | mdBook chapter |
|---|---|---|
| `process` | Execute a Request from a JSON file | https://frankbardon.github.io/pulse/cli/api-process.html |
| `compose` | Execute a ComposedRequest batch | https://frankbardon.github.io/pulse/cli/api-compose.html |
| `predict` | Validate a request against the schema | https://frankbardon.github.io/pulse/cli/api-predict.html |
| `inspect` | Print schema and descriptions of a `.pulse` file | https://frankbardon.github.io/pulse/cli/api-inspect.html |
| `sample` | Print N rows from a cohort | https://frankbardon.github.io/pulse/cli/api-sample.html |
| `facet` | Print value distribution for one field | https://frankbardon.github.io/pulse/cli/api-facet.html |
| `manifest` | Print the self-description manifest | https://frankbardon.github.io/pulse/cli/manifest.html |
| `mcp` | Serve the MCP transport over stdio | https://frankbardon.github.io/pulse/cli/mcp.html |
</reference>

<workflow id="typical-session" name="typical-mcp-session">
## A typical session

1. Call `pulse_manifest` once. Cache the payload — it is deterministic for a given binary and covers operator catalogs, field types, error codes, MCP tool list, and the skills index.

2. Call `pulse_inspect` with the cohort path the user (or the conversation) is asking about:

   ```json
   {"path": "sales.pulse"}
   ```

   The response is an `InspectResult` envelope: fields, types, byte offsets, descriptions, and (truncated) categorical dictionaries. After this call the session's action tools are bound — their JSON Schemas now enumerate the actual field names for this cohort.

3. (Optional) Call `pulse_sample` or `pulse_facet` to eyeball data:

   ```json
   {"path": "sales.pulse", "count": 10}
   ```
   ```json
   {"path": "sales.pulse", "field": "region"}
   ```

4. Author a `Request` matching the schema. See `request-recipes` for canonical shapes.

5. Call `pulse_predict` to validate the request. Read `errors`, `warnings`, and especially `suggestions` (machine-actionable fixups). If `streamable` is false, `streamable_reasons` lists every gate that forced buffering.

   ```json
   {"request": "{\"cohort\":{\"filename\":\"sales.pulse\"},\"aggregations\":[{\"type\":\"AGG_COUNT\",\"field\":\"id\",\"label\":\"n\"}]}"}
   ```

6. Once predict is clean, call `pulse_process` (single) or `pulse_compose` (batch). Read `data` for rows, `metadata` for `total_rows`/`filtered_rows`/timing, and the envelope's `errors`/`warnings` for diagnostics.

For the common path, `pulse_ask` collapses steps 5–6 into a single round trip. With `on_invalid="suggest"` and a predict failure, the response carries structured `Fixup` entries so you can repair the request without re-querying the schema. With `query` instead of `request`, the parser maps a natural-language sentence to a structured request — see `query-router-prompt` for the recommended prompt template.
</workflow>

<example name="canonical-process-request">
## Canonical process request

```json
{
  "cohort": {"filename": "data.pulse"},
  "filterers": [
    {"type": "FILTER_INCLUDE", "field": "status", "values": ["active"]}
  ],
  "groups": [
    {"type": "GROUP_CATEGORY", "field": "region"}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    {"type": "AGG_AVERAGE", "field": "score", "label": "mean_score"}
  ],
  "attributes": [],
  "outputs": [{"format": "json"}]
}
```

JSON tags mirror `types.Request`: `cohort`, `filterers`, `features`, `attributes`, `groups`, `aggregations`, `windows`, `sort`, `tests`, `post_tests`, `outputs`. See `request-recipes` for a fuller catalog keyed by intent.
</example>

<reference>
## Pipeline order

Load -> Features -> Filter -> Attributes -> Group -> Aggregate -> Windows -> Sort -> Output.

Features run BEFORE filterers, so derived columns are addressable as filter, group, attribute, and window inputs. Windows run AFTER aggregation, on the post-aggregate row set. `Request.Sort` runs last.
</reference>

<reference>
## Defaults

When an `aggregations[]` or `groups[]` slot names a `field` but omits `type`, Pulse infers the operator from the named field's schema type before running the request. Predict reports the inferred slot under `defaults_applied` so you can echo back what was filled in.

| Field type | Default aggregation | Default grouper |
|---|---|---|
| `u8`..`u64`, `f32`, `f64`, `nullable_u*`, `decimal128`, `nullable_decimal128` | `AGG_SUM` | `GROUP_RANGE` (interval 10) |
| `categorical_u8`/`u16`/`u32` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (none — must be explicit) | `GROUP_DATE` (component `"day"`) |
| `nullable_bool`, `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `point_f64`, `h3_cell` | `AGG_GEO_CENTROID` | `GROUP_H3_CELL` (resolution 7) |

Rules: defaults never override an explicit `type`; they never cross categories (a missing aggregator does not insert a grouper); statistical tests (`tests[]`, `post_tests[]`) are not defaulted; filter expressions, features, attributes, and windows are out of scope.
</reference>

<reference>
## Streaming hint

`pulse_predict` reports `streamable: bool` and `streamable_reasons: []string` so you know whether a request can run through the single-pass streaming aggregator path or whether the engine will buffer the intermediate row set.

What streams today: no-group online aggregations; grouped requests when every grouper is CATEGORY/RANGE/ROUNDED/H3_CELL and every aggregator is online; row-local attributes (FORMULA, DATE_PART); two-pass attributes (ZSCORE, TSCORE, NORMALIZED) via Welford pass 1.

What forces buffering: median/percentile/ZScore aggregators, ATTR_PERCENTILE, GROUP_QUANTILE/GROUP_DATE, any windows, decimal-typed aggregations, geo aggregations, two-pass attributes combined with features or groups, tier-2 post tests.

For very large cohorts, prefer the streaming-eligible shape when possible. The bound enums in the session's tool schemas do not enforce this — predict's `streamable_reasons` is the source of truth.
</reference>

<reference>
## Envelope shape

Every Pulse response is wrapped in:

```json
{"format_version": "1.0", "data": {...}, "errors": [], "warnings": []}
```

- `format_version` is currently `"1.0"`.
- `data` is the operation-specific payload (rows for process, `PredictResult` for predict, `InspectResult` for inspect).
- `errors` and `warnings` are always arrays (never `null`). Each entry: `{"code": "...", "message": "...", "details": {...}}`.
- Predict envelopes additionally carry `data.suggestions[]` — structured fixups derived from each error code's metadata.
</reference>

<see_also>
- request-recipes — copy-pasteable request JSON skeletons keyed by analytical intent; start here when authoring requests quickly
- query-router-prompt — system-prompt template for translating natural-language asks into a structured request
- cohort-schema-design — field types and schema authoring
- aggregation-guide — `AGG_*` operations and filtering
- attribute-composition — `ATTR_*` per-record derivations
- grouper-design — `GROUP_*` partition strategies
- compose-requests — batching with `ComposedRequest`
- debugging-with-predict — iterating on a request before processing
- error-code-reference — every error code by domain
- mcp-integration — what the LLM should know about Pulse's MCP surface
</see_also>
