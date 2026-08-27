---
name: request-envelope
description: Request shapes, envelope contract, slot keys, smart defaults, streamability, and the v0.20.0 Response.Components additive field. Use when authoring or adapting any Pulse Request.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [Request, ComposedRequest, ChainRequest, FacetRequest, SampleRequest, Envelope]
---

# Request envelope

The Pulse wire contract — what goes in, what comes back, what the slot keys are, what the engine fills in for you.

## Envelope (response side)

Every `--json` CLI output and every facade response uses `descriptor.Envelope`:

```json
{"format_version": "1.0", "data": {...}, "request": {...}, "errors": [], "warnings": []}
```

- `format_version` — `"1.0"`. Additive `data` fields do NOT bump; renames / removals do.
- `data` — operation-specific payload.
- `request` — opt-in normalized-request echo (`EchoRequest: true` / `--echo-request`). Streaming skips.
- `errors` / `warnings` — always arrays (never null). Each: `{code, message, details}`. Resolve via `pulse_errors_lookup`.

## Request shapes (per command)

| Command | Wire type | Top-level keys |
|---|---|---|
| `pulse_process`, `pulse_predict`, `pulse api process` | `Request` | `cohort, filterers, features, attributes, groups, aggregations, windows, sort, tests, post_tests, joins, crosstab, overlays, outputs` |
| `pulse_compose`, `pulse api compose` | `ComposedRequest` | `requests[]` (each = `Request`) |
| `pulse_process_chain`, `pulse api process-chain` | `ChainRequest` | `cohort, stages[], overlays` (each stage: `{request: Request}`) |
| `pulse_facet`, `pulse api facet` | `FacetRequest` | `cohort, fields[], top_k, percentiles, histogram, additive, overlays` |
| `pulse_sample`, `pulse api sample` | `SampleRequest` | `cohort, count, offset` |

`Request` slot order is pipeline order: `features → filterers → attributes → groups → aggregations → windows → sort`. Tests / joins / crosstab / overlays plug specific stages — see per-skill.

## Canonical process Request

```json
{
  "cohort": {"filename": "sales.pulse"},
  "filterers":    [{"type": "FILTER_INCLUDE", "field": "status", "values": ["active"]}],
  "groups":       [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [
    {"type": "AGG_COUNT",   "field": "id",    "label": "n"},
    {"type": "AGG_AVERAGE", "field": "score", "label": "mean"}
  ]
}
```

## Slot-key gotchas

Unknown keys are silently dropped on decode — the engine does NOT warn.

| Wrong | Right |
|---|---|
| `groupers` | `groups` |
| `aggregators` | `aggregations` |
| `filters` | `filterers` |
| `tests_post` | `post_tests` |
| `output` | `outputs` |
| `request` (Compose top-level) | `requests` |

Per-operator slot: `type` (operator constant, e.g. `"AGG_SUM"`), `field`, `label`, optional `params`. Per-operator key lists: `pulse_examples_*` + the category skill.

## Smart defaults

When a slot names `field` but omits `type`, the engine infers from schema type. Predict reports filled slots under `data.defaults_applied`.

| Field type | Default agg | Default grouper |
|---|---|---|
| numeric (`u4`, `u8`..`u64`, `f32`/`f64`, `decimal128`) | `AGG_SUM` | `GROUP_RANGE` (interval 10) |
| `categorical_u8`/`u16`/`u32` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (explicit only) | `GROUP_DATE` (`"day"`) |
| `datetime` | (explicit only) | `GROUP_DATE` (`"day"`), truncated |
| `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |

Rules: never override explicit `type`; never cross categories; `Nullable` irrelevant; tests / filterers / attrs / features / windows never defaulted. Disable via `pulse.Options{DisableDefaults: true}` / `--no-defaults`.

## Streamability

`pulse_predict` returns `data.streamable: bool` + `data.streamable_reasons: []string`.

- **Streams:** no-group online aggs; grouped when every grouper is `GROUP_CATEGORY`/`RANGE`/`ROUNDED` AND every agg is online; row-local attrs (`ATTR_FORMULA`/`DATE_PART`); two-pass attrs (`ATTR_ZSCORE`/`TSCORE`/`NORMALIZED`) via Welford.
- **Buffers:** median/percentile/ZScore aggs; `ATTR_PERCENTILE`; `GROUP_QUANTILE`/`DATE`; any windows; decimal aggs; two-pass attrs with features or groups; tier-2 post tests.

`streamable_reasons` is authoritative.

## Echo request

`--echo-request` / `pulse.Options.EchoRequest: true` populates `envelope.request` with the *normalized* request (defaults applied, slots validated, schema bound). Streaming skips. Use it to confirm defaults or debug silent slot-key drops.

## Response.Components (v0.20.0, additive)

`Response.Components *ResponseComponents`, additive `omitempty`. Marshals to no `components` key when unpopulated. `format_version` stays `"1.0"` (additive-only).

Per-family shape (one entry per matching Request slot, declared order):

```
data.components.aggregations[i]  -> {n, n_null, operator: {<operator-keys>}}
data.components.groupers[i]      -> {total_n, n_null, operator: {<bucket-layout>}}
data.components.crosstab         -> {cell_counts[r][c], cell_components[r][c], row/column/grand margins, axis_key_components}
data.components.filterers[i]     -> {n_in, n_out, n_null_input}
data.components.run              -> {total_records, filtered_records, null_records, shard_count, partial_cohort_reason}
```

Universal floor filled by the orchestrator. Per-operator keys ride inside `operator` (cell maps for crosstab). Resolve keys via `manifest.components_schemas.{aggregators,groupers,filterers}[name].keys`.

**First sight:** call `pulse_skills_get` with `name: "response-components"` — canonical consumption reference with full key tables, mergeability axis, streaming behaviour.

## Predict-specific data fields

`streamable`, `streamable_reasons`, `defaults_applied`, `suggestions` (when `on_invalid="suggest"`), per-slot `buffered_components` (true for non-mergeable: median, percentile, quantile).

## Cross-links

- `response-components` — full Components contract + per-operator key tables.
- `session-bootstrap` — MCP session order.
- `aggregation-guide` / `grouper-design` / `attribute-composition` / ... — per-category slot shapes.
- `compose-requests` — `ComposedRequest` semantics.
- `facet-design` — `FacetRequest` / `FacetSchemaRequest`.
- `streaming-and-watching` — stream chunks, request hashing, watch loop.
- `debugging-with-predict` — predict iteration loop.
