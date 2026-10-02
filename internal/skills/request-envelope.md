---
name: request-envelope
description: Request shapes, envelope contract, slot keys, smart defaults, streamability, and the v0.20.0 Response.Components additive field. Use when authoring or adapting any Pulse Request.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [Request, ComposedRequest, ChainRequest, FacetRequest, SampleRequest, Envelope]
---

# Request envelope

## Envelope (response side)

Every `--json` CLI output and every facade response uses `descriptor.Envelope`:

```json
{"format_version": "1.1", "data": {...}, "request": {...}, "errors": [], "warnings": []}
```

- `format_version` — `"1.1"`. Additive `data` fields do NOT bump; renames / removals do.
- `data` — operation-specific payload.
- `request` — opt-in echo of the *normalized* request (defaults applied; `EchoRequest: true` / `--echo-request`) — confirms defaults, debugs silent slot-key drops. Streaming skips.
- `errors` / `warnings` — always arrays (never null). Each: `{code, message, details}`. Resolve via `pulse_errors_lookup`.

## Request shapes (per command)

| Command | Wire type | Top-level keys |
|---|---|---|
| `pulse_process`, `pulse_predict`, `pulse api process` | `Request` | `cohort, time_zone, filterers, features, attributes, groups, aggregations, windows, sort, tests, post_tests, joins, crosstab, overlays, outputs` |
| `pulse_compose`, `pulse api compose` | `ComposedRequest` | `requests[]` (each = `Request`) |
| `pulse_process_chain`, `pulse api process-chain` | `ChainRequest` | `cohort, stages[], overlays` (each stage: `{request: Request}`) |
| `pulse_facet`, `pulse api facet` | `FacetRequest` | `cohort, time_zone, fields[], top_k, percentiles, histogram, additive, overlays` |
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

- **Streams:** online aggs, ungrouped or under `GROUP_CATEGORY`/`RANGE`/`ROUNDED`; row-local attrs; two-pass attrs (`ATTR_ZSCORE`/…) via Welford.
- **Buffers:** median/percentile aggs; `ATTR_PERCENTILE`; `GROUP_QUANTILE`/`DATE`; windows; decimal aggs; two-pass attrs with features/groups; tier-2 post tests.

`streamable_reasons` is authoritative.

## Response.Components

`Response.Components` is additive `omitempty`: `aggregations[i]` `{n, n_null, operator}`, `groupers[i]` `{total_n, n_null, operator}`, `crosstab`, `filterers[i]` `{n_in, n_out, n_null_input}`, `run`. Per-operator keys: `manifest.components_schemas`. Full contract: `pulse_skills_get` `name: "response-components"`.

## Time zones

`time_zone` sits on `Request` and `FacetRequest` (Compose / Chain inherit it per inner Request; `SampleRequest` has none). Slot `tz` sits on `groups`, `filterers`, `attributes`, `features` and `crosstab.rows`/`columns` entries — a slot key, never inside `params`. Names are `UTC` or IANA `Area/Location` (`Europe/Berlin`, `Etc/GMT-5`); `EST`, `Local`, `+05:00` → `PULSE_TIMEZONE_UNKNOWN`.

Precedence per slot: `tz` → `time_zone` → `pulse.Options.DefaultTimeZone` → `UTC`. Only manifest `zone: "capable"` operators take `tz`; `OVERLAY_YOY` is `zone: "following"` (inherits its host grouper); extension operators are never capable.

Refused with `PROCESSING_CONFIG`: `tz` on a non-capable operator; an explicit `tz` on a `date` field (even `"UTC"`); and — until zone-aware operator math lands — any non-UTC zone reaching a `datetime` (or derived / joined) field. An inherited zone on a `date` field is not applied. UTC (and fixed-zero aliases like `Etc/UTC`) is byte-identical to no zone.

## Predict-specific data fields

`streamable`, `streamable_reasons`, `defaults_applied`, `time_zones` (per zone-capable slot: `{slot, operator, field_type, tz, source}`, `source` ∈ `slot|request|options|default`), `suggestions` (when `on_invalid="suggest"`), per-slot `buffered_components` (true for non-mergeable: median, percentile, quantile).

## Cross-links

- `response-components` — full Components contract + per-operator keys.
- `session-bootstrap` — MCP session order.
- `aggregation-design` / `grouper-design` / `attribute-composition` — per-category slot shapes.
- `compose-requests` — `ComposedRequest` semantics; `facet-design` — `FacetRequest` / `FacetSchemaRequest`.
- `streaming-and-watching` — stream chunks, request hashing, watch loop.
- `docs/src/internals/debugging-predict.md` — predict iteration loop.
