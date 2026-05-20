---
name: getting-started
description: Pulse mental model, MCP tool surface, .pulse file format, operator vocabulary. Use first on a new session to establish baseline before calling pulse_inspect, pulse_predict, or pulse_process.
type: guide
applies_to: process, compose, sample, facet, inspect, predict, manifest
---

# Getting Started

<skill_overview>
Pulse is a self-describing tabular processing engine over `.pulse` cohort files. As an LLM you reach Pulse through ten MCP tools. This skill teaches the vocabulary, the request shape, the pipeline order, and the typical session pattern. Invoke it first when onboarding to any other Pulse skill.
</skill_overview>

<reference>
## What Pulse is, in three paragraphs

A `.pulse` file is a binary cohort: a fixed-width header carrying a typed schema, optional categorical dictionaries, and a record region. Every column has a declared type from a closed set of 17 (`u8`..`categorical_u32`, `decimal128`, ...). Pulse never infers types at query time; the schema in the header is the contract.

A Pulse `Request` is a JSON document that names a cohort plus the operators to apply: filterers, features, attributes, groupers, aggregators, windows, sort, and statistical tests. The engine validates the request against the cohort schema before running anything, then executes the pipeline in a fixed order and returns a typed envelope of rows, metadata, and any diagnostics.

Self-description is structural: the engine publishes a `Manifest` that names every operator, accepts/emits type, parameter, and streamability hint, plus the field-type and error-code catalogs. One call to `pulse_manifest` at session start replaces a long discovery dance — the LLM authors against the cached manifest from then on.
</reference>

<reference>
## Vocabulary

| Term | Meaning |
|---|---|
| Cohort | A `.pulse` binary file: schema header + fixed-width records. |
| Schema | Field list (name, type, description) embedded in the cohort header. |
| Field | One column. Typed with one of 13 field types (`u4` ... `decimal128`). Nullability is orthogonal — set `Nullable: true` on any field to opt into the per-record null bitmap. |
| Record | One row. Fixed-width binary block. |
| Aggregation | One of 16 `AGG_*` ops (COUNT, SUM, AVERAGE, ...) producing a per-group scalar. |
| Attribute | One of 6 `ATTR_*` ops producing a per-record derived value. |
| Filterer | One of 5 `FILTER_*` predicates run before grouping. |
| Grouper | One of 5 `GROUP_*` partition strategies run before aggregation. |
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

**`pulse_ask` is the default — use it unless you specifically need to diagnose a failed predict, peek at raw rows (`pulse_sample`), facet a single field (`pulse_facet`), or batch-execute multiple requests (`pulse_compose`).** It collapses import + inspect + predict + process into one round trip, accepts either a structured `request` or a natural-language `query`, and slides the managed-import TTL forward on every call. The four-call chain (`pulse_import` -> `pulse_inspect` -> `pulse_predict` -> `pulse_process`) is the advanced / diagnostic path.

| Tool | Purpose | Required arguments |
|---|---|---|
| `pulse_manifest` | Self-description: operator catalog, types, error codes, skills index. Call once per session, cache result. | (none) |
| `pulse_skills_list` | List embedded skill metadata. | (none) |
| `pulse_skills_get` | Fetch a skill body by name. | `name` |
| `pulse_inspect` | Read a cohort header: fields, types, descriptions, dictionaries. Also binds schema-aware enums into the session's action tools. | `path` |
| `pulse_sample` | Return N rows from the cohort for eyeballing data. | `path`, `count` |
| `pulse_facet` | Distinct values for one field (categorical fast path / numeric scan). | `path`, `field` |
| `pulse_facet_schema` | Multi-field rich facet — counts, null tallies, numeric stats, optional percentiles, histograms, and additive contribution counts. Prefer over repeated `pulse_facet` calls when summarising more than one field. | `request` (JSON-encoded `pulse.FacetRequest`) |
| `pulse_predict` | Validate a `Request` against the cohort's schema. No execution. Reports `streamable`, `streamable_reasons`, `defaults_applied`, structured `suggestions`, plus the standard envelope. | `request` (JSON-encoded `types.Request`) |
| `pulse_process` | Execute one `Request`. Returns rows + metadata + diagnostics envelope. | `request` |
| `pulse_process_chain` | Execute a source-rooted linear chain (`ChainRequest`) of mergeable stages — stage N+1's input cohort is stage N's output rows. Collapses N round-trips into one open + N stage validations. Mergeable-only (`processing.CanChainRequest`); non-mergeable stages return `PULSE_CHAIN_NOT_MERGEABLE`. | `request` (JSON-encoded `pulse.ChainRequest`) |
| `pulse_compose` | Execute a `ComposedRequest` (batch of requests against one cohort). Order-preserving. | `request` (JSON-encoded `types.ComposedRequest`) |
| `pulse_ask` | **PREFERRED entry point.** One-shot import -> inspect -> predict -> process. Pass `source` (raw file path) to auto-import; pass `cohort` for an existing `.pulse` or managed handle. Accepts either a structured `request` or a natural-language `query` parsed against the cohort's schema. Optional `source`-side fields: `source_format`, `source_handle`, `source_ttl` (default `"7d"`, accepts `"pin"`), `source_sheet`, `source_overwrite`. With `on_invalid="suggest"` returns structured `Fixup` entries instead of erroring on predict failure. | `request` (JSON-encoded `pulse.AskRequest`) |

Subsequent calls to `pulse_process` / `pulse_predict` / `pulse_compose` / `pulse_sample` / `pulse_facet` inside a session pick up enum constraints automatically after the first `pulse_inspect` — the field name and operator-type arguments are restricted to values that exist in the cohort's schema. The mcp-integration skill describes the binding behavior.

The full CLI surface — `pulse api process`, `pulse import csv`, `pulse export parquet`, and so on — is for humans driving the binary, not for LLMs. See https://frankbardon.github.io/pulse/cli/ if you need to point a user at it.
</reference>

<reference>
## CLI leaves at a glance

For pointing a human at the right chapter. The MCP tool list above is the LLM-facing surface; the CLI is documented in mdBook.

| Leaf | One-line | mdBook chapter |
|---|---|---|
| `process` | Execute a Request from a JSON file. Flags: `--request`, `--json`, `--stream`, `--no-defaults`, `--strict` (promote request-validation warnings into hard errors, e.g. numeric aggregation on a categorical field). | https://frankbardon.github.io/pulse/cli/api-process.html |
| `process-chain` | Execute a source-rooted linear chain of mergeable processing stages. Flags: `--request`, `--json`, `--no-defaults`. Mergeable-only — rejected stages return `PULSE_CHAIN_NOT_MERGEABLE` so callers can fall back to per-stage `process`. | https://frankbardon.github.io/pulse/cli/api-process-chain.html |
| `compose` | Execute a ComposedRequest batch | https://frankbardon.github.io/pulse/cli/api-compose.html |
| `predict` | Validate a request against the schema | https://frankbardon.github.io/pulse/cli/api-predict.html |
| `cohort inspect` | Print schema and descriptions of a `.pulse` file | https://frankbardon.github.io/pulse/cli/cohort-inspect.html |
| `cohort filter` | Filter a `.pulse` cohort (single-file, shard archive, or `archive.pulse#shard.pulse` anchor) to a new `.pulse` file. Accepts `--filter EXPR` (FILTER_EXPRESSION semantics), a file-backed include-set via `--include-from PATH --include-field NAME`, or both AND-combined (include-set tested first to short-circuit on misses). Include-set picks the most performant impl for the field type: bitset for categorical, `map[uint64]struct{}` for integer / date, `map[string]struct{}` for decimal / fallback. Float fields rejected — use `--filter` for numeric ranges. | https://frankbardon.github.io/pulse/cli/cohort-filter.html |
| `sample` | Print N rows from a cohort | https://frankbardon.github.io/pulse/cli/api-sample.html |
| `facet` | Print distinct values for one field (or a rich multi-field summary via `--request` / repeat `--field` / `--top-k` / `--percentile` / `--histogram` / `--additive`). | https://frankbardon.github.io/pulse/cli/api-facet.html |
| `manifest` | Print the self-description manifest | https://frankbardon.github.io/pulse/cli/manifest.html |
| `mcp` | Serve the MCP transport over stdio | https://frankbardon.github.io/pulse/cli/mcp.html |
| `import auto SOURCE` | Auto-detect a source format, convert into the managed `.pulse` pool, and track lifetime via TTL sidecar. Flags: `--format`, `--handle`, `--ttl` (default `7d`; accepts Go duration, day form `7d`, or `pin`), `--sheet`, `--overwrite`. Pulse-format sources pass through unchanged with no sidecar. | https://frankbardon.github.io/pulse/cli/import-auto.html |
| `import list` | List managed-import handles with TTL status. Expired and pinned entries are flagged. | https://frankbardon.github.io/pulse/cli/import-list.html |
| `import drop HANDLE` | Remove one managed handle (file + sidecar) from the pool. | https://frankbardon.github.io/pulse/cli/import-drop.html |
| `shard create ARCHIVE --include SHARD ...` | Create a new shard archive from one or more single-file `.pulse` shards. Atomic temp+rename; first include seeds the canonical schema. | https://frankbardon.github.io/pulse/cli/shard-create.html |
| `shard add ARCHIVE SHARD` | Append a shard to an existing archive (validated for structural cohesion + dict prefix rule). | https://frankbardon.github.io/pulse/cli/shard-add.html |
| `shard remove ARCHIVE BASENAME` | Remove a shard from an archive by basename. Canonical schema is preserved. | https://frankbardon.github.io/pulse/cli/shard-remove.html |
| `shard list ARCHIVE` | List shards inside an archive with per-shard record counts. | https://frankbardon.github.io/pulse/cli/shard-list.html |
| `shard extract ARCHIVE BASENAME` | Write one shard's standalone `.pulse` bytes to stdout. | https://frankbardon.github.io/pulse/cli/shard-extract.html |
</reference>

<workflow id="typical-session" name="typical-mcp-session">
## A typical session

**Two calls do the job.** USE `pulse_ask` FIRST.

1. Call `pulse_manifest` once. Cache the payload — it is deterministic for a given binary and covers operator catalogs, field types, error codes, MCP tool list, and the skills index.

2. Call `pulse_ask` with the user's question. Two shapes cover almost everything:

   **Raw file + natural language** (auto-imports first, then runs):

   ```json
   {
     "request": "{\"source\":\"data.csv\",\"query\":\"average revenue by month\"}"
   }
   ```

   **Existing cohort + structured request** (you already have a `.pulse` or managed handle):

   ```json
   {
     "request": "{\"cohort\":{\"filename\":\"sales.pulse\"},\"aggregations\":[{\"type\":\"AGG_COUNT\",\"field\":\"id\",\"label\":\"n\"}]}"
   }
   ```

   The server inspects (binding schema-aware enums into the session's action tools), validates, executes, and returns import metadata, the predict envelope, and the result rows in one response. On predict failure with `on_invalid="suggest"`, the response carries structured `Fixup` entries so you can repair the request without re-querying the schema. For natural-language input, the `query` parser maps prose to a structured request against the cohort's schema — see `query-router-prompt` for the prompt template.

3. Iterate against the same handle in subsequent `pulse_ask` calls. Every call slides the managed-import TTL forward (default `7d`); pass `source_ttl: "pin"` if you want the handle to outlive activity-based expiry.

### When to use lower-level tools

The four-call chain is still supported and is the right reach in these situations:

- **Diagnosing a failed predict** — call `pulse_predict` directly to read the full envelope (`errors`, `warnings`, `suggestions`, `defaults_applied`, `streamable_reasons`) without execution.
- **Eyeballing data** — `pulse_sample` for N rows, `pulse_facet` for a one-field distribution.
- **Schema discovery before authoring** — `pulse_inspect` reads the cohort header explicitly. `pulse_ask` does this for you under the hood.
- **Batch execution** — `pulse_compose` runs many requests against a single cohort in one shot.
- **Pre-staging a handle** — `pulse_import` when you want to lock in a handle name / TTL / pinning policy without immediately running a query.

Legacy multi-step shape:

```json
// 1. (Optional) pulse_import — converts raw source into a managed handle
{"source": "data.csv", "ttl": "7d"}

// 2. pulse_inspect — read cohort schema, bind enums
{"path": "sales.pulse"}

// 3. pulse_predict — validate a hand-authored request
{"request": "{\"cohort\":{\"filename\":\"sales.pulse\"},\"aggregations\":[{\"type\":\"AGG_COUNT\",\"field\":\"id\",\"label\":\"n\"}]}"}

// 4. pulse_process — execute
{"request": "..."}
```
</workflow>

<example name="shard-archive-workflow">
## Shard archives

A `.pulse` path can be either a single-file cohort or a **shard archive** (uncompressed Zip64, magic `PK\x03\x04`) containing one canonical `_schema.pulse` entry plus N standalone shard payloads. Every facade method (`Process`, `Compose`, `Sample`, `Facet`, `Inspect`, `Predict`, `ProcessStream`) operates transparently on the union of shards — there is no separate facade for archives.

Build the archive from existing shards (atomic temp+rename), then run requests against it like any other cohort:

```bash
$ pulse shard create q1_2019.pulse \
    --include 20190101.pulse \
    --include 20190108.pulse
$ pulse api process --request q1.json --cohort q1_2019.pulse
$ pulse inspect q1_2019.pulse#20190101.pulse
```

The `archive.pulse#shard.pulse` anchor opens one shard as a one-shard cohort — useful for inspecting a single wave inside a quarterly archive, or for mixing whole-archive and per-shard slots inside a `Compose`:

```json
{
  "requests": [
    {"cohort": {"filename": "Q1_2019.pulse"},                "aggregations": [...]},
    {"cohort": {"filename": "Q1_2019.pulse#20190101.pulse"}, "aggregations": [...]},
    {"cohort": {"filename": "wave_2018.pulse"},              "aggregations": [...]}
  ]
}
```

The first slot fans out across every shard in the archive; the second targets just one shard via anchor syntax; the third is a legacy single-file cohort. Compose is order-preserving by slot.

See `skills/cohort-schema-design.md` (Sharded cohorts) for archive layout, dict cohesion rules, memory multiplier on forced-buffered ops, and the concurrency contract.
</example>

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
| `u4`, `u8`..`u64`, `f32`, `f64`, `decimal128` | `AGG_SUM` | `GROUP_RANGE` (interval 10) |
| `categorical_u8`/`u16`/`u32` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (none — must be explicit) | `GROUP_DATE` (component `"day"`) |
| `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |

The `Nullable` flag on a field never changes its default operator — it only controls bitmap participation.

Rules: defaults never override an explicit `type`; they never cross categories (a missing aggregator does not insert a grouper); statistical tests (`tests[]`, `post_tests[]`) are not defaulted; filter expressions, features, attributes, and windows are out of scope.
</reference>

<reference>
## Streaming hint

`pulse_predict` reports `streamable: bool` and `streamable_reasons: []string` so you know whether a request can run through the single-pass streaming aggregator path or whether the engine will buffer the intermediate row set.

What streams today: no-group online aggregations; grouped requests when every grouper is CATEGORY/RANGE/ROUNDED and every aggregator is online; row-local attributes (FORMULA, DATE_PART); two-pass attributes (ZSCORE, TSCORE, NORMALIZED) via Welford pass 1.

What forces buffering: median/percentile/ZScore aggregators, ATTR_PERCENTILE, GROUP_QUANTILE/GROUP_DATE, any windows, decimal-typed aggregations, two-pass attributes combined with features or groups, tier-2 post tests.

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
