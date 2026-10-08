```yaml
name: request-envelope
description: Request shapes, envelope contract, slot keys, smart defaults, streamability and time zones. Use when authoring or adapting any Pulse Request.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [Request, ComposedRequest, ChainRequest, FacetRequest, SampleRequest, Envelope]
```

# Request envelope

## Envelope (response side)

Every `--json` output and facade response uses `descriptor.Envelope`:

```json
{"format_version": "1.1", "data": {...}, "request": {...}, "errors": [], "warnings": []}
```

- `format_version` — `"1.1"`; additive `data` fields never bump it.
- `data` — the operation's payload.
- `request` — opt-in echo of the *normalized* request (`--echo-request`); streaming skips it.
- `errors` / `warnings` — always arrays of `{code, message, details}`; resolve via `pulse_errors_lookup`.

## Request shapes (per command)

| Operation (CLI) | Wire type | Top-level keys |
|---|---|---|
| process, predict (`pulse api process`) | `Request` | `cohort, time_zone, filterers, features, attributes, groups, aggregations, windows, sort, tests, post_tests, joins, crosstab, overlays, outputs, return`, `multiplicity`, `vectors`, `matrices` |
| compose (`pulse api compose`) | `ComposedRequest` | `requests[]` (each = `Request`), `return` (overlays only) |
| process chain (`pulse api process-chain`) | `ChainRequest` | `cohort, stages[], overlays` (each stage: `{request: Request}`) |
| facet (`pulse api facet`) | `FacetRequest` | `cohort, time_zone, fields[], top_k, percentiles, histogram, additive, overlays` |
| sample (`pulse api sample`) | `SampleRequest` | `cohort, count, offset` |

Each operation's MCP tool is listed in manifest `mcp_tools`.

Row weight: `Request.weight` `{field, kind}`; per-slot `weight` — a field, `{field, kind}` or `null` (`null` opts out, absent inherits); `pulse.Options.DefaultWeight`. See [`weighting`](weighting.md).

`Request` slot order is pipeline order: `features → filterers → attributes → groups → aggregations → windows → sort`; `sort` shares the window comparator (nulls last both ways). A derived name (feature output, attribute / aggregation / window label) exists only DOWNSTREAM of its producer. Runtime and predict refuse (`SERVICE_VALIDATION`) a name nothing produces, a label shadowing a column, and an aggregation label equal to a group field or another aggregation label.

## Canonical process Request

```json
{
  "cohort": {"filename": "sales.pulse"},
  "filterers":    [{"type": "FILTER_INCLUDE", "field": "status", "values": ["active"]}],
  "groups":       [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id",      "label": "n"},
    {"type": "AGG_SUM",   "field": "revenue", "label": "revenue"}
  ]
}
```

Every `type` is an operator constant from `pulse_manifest` `components.<category>`; runnable requests: `pulse_examples_search`.

## Slot-key gotchas

Unknown keys drop silently on decode.

| Wrong | Right |
|---|---|
| `groupers` | `groups` |
| `aggregators` | `aggregations` |
| `filters` | `filterers` |
| `tests_post` | `post_tests` |
| `output` | `outputs` |
| `request` (Compose top-level) | `requests` |

Per-operator slot: `type`, `field`, `label`, optional `params` (keys per operator: its atomic skill).

## Smart defaults

When a slot names `field` but omits `type`, the engine infers from schema type. Predict reports filled slots under `data.defaults_applied`.

| Field type | Default agg | Default grouper |
|---|---|---|
| numeric (`u4`, `u8`..`u64`, `f32`/`f64`, `decimal128`) | `AGG_SUM` | `GROUP_RANGE` (interval 10) |
| `categorical_*`, `packed_bool` | `AGG_MODE_COUNT` | `GROUP_CATEGORY` |
| `date`, `datetime` (truncated) | (explicit only) | `GROUP_DATE` (`"day"`) |

Never override explicit `type` or cross categories; tests / filterers / attrs / features / windows never defaulted. Disable via `pulse.Options{DisableDefaults: true}` / `--no-defaults`.

## Streamability

`pulse_predict` returns `data.streamable: bool` + `data.streamable_reasons: []string`.

- **Streams:** online aggregators (manifest `streamable: true`), ungrouped or under a streamable grouper; row-local attributes; whole-cohort-statistic attributes (z-scores and the like) in two passes.
- **Buffers:** order-statistic aggregators (median, percentile); percentile-rank attributes; groupers with `streamable: false`; windows; decimal aggregations; two-pass attributes combined with features or groups; tier-2 post tests.

`streamable_reasons` is authoritative.

## Time zones

`time_zone` sits on `Request` and `FacetRequest` (Compose / Chain: per inner Request; `SampleRequest`: none). Slot `tz` sits on `groups`, `filterers`, `attributes`, `features` and `crosstab.rows`/`columns` entries — a slot key, never inside `params`. Names are `UTC` or IANA (`Europe/Berlin`); `EST`, `Local`, `+05:00` → `PULSE_TIMEZONE_UNKNOWN`.

Precedence per slot: `tz` → `time_zone` → `pulse.Options.DefaultTimeZone` → `UTC`. Only manifest `zone: "capable"` operators take `tz`; a `zone: "following"` operator (the year-over-year overlay) inherits its host grouper's zone; extension operators are never capable.

On a `datetime` field instants read on their local day (DST-correct). Refused with `PROCESSING_CONFIG`: `tz` on a non-capable operator; explicit `tz` on a `date` field (even `"UTC"`); a non-UTC zone on a derived field. An inherited zone on a `date` field is not applied. UTC (and aliases like `Etc/UTC`) is byte-identical to no zone.

## Predict-specific data fields

`streamable`, `streamable_reasons`, `defaults_applied`, `weights` / `suggested_weight` ([`weighting`](weighting.md)), `resolved_vectors`, `time_zones` (per zone-capable slot: `{slot, operator, field_type, tz, source}`, `source` ∈ `slot|request|options|default`), `suggestions` (`on_invalid="suggest"`), per-slot `buffered_components` (non-mergeable).

## Cross-links

[`response-components`](response-components.md) (the `Response.Components` block) · [`response-shaping`](response-shaping.md) (`return`: trim the response, wire precision) · [`session-bootstrap`](session-bootstrap.md) (MCP session order) · [`aggregation-design`](aggregation-design.md) / [`grouper-design`](grouper-design.md) / [`attribute-composition`](attribute-composition.md) (slot shapes) · [`compose-requests`](compose-requests.md) · [`facet-design`](facet-design.md) · [`streaming-and-watching`](streaming-and-watching.md) (stream chunks, request hashing) · [`multiplicity-correction`](multiplicity-correction.md).
