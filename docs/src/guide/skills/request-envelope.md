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

Every `--json` output uses `descriptor.Envelope`:

```json
{"format_version": "1.1", "data": {...}, "request": {...}, "errors": [], "warnings": []}
```

- `format_version` — `"1.1"`; additive `data` fields never bump it.
- `data` — the payload.
- `request` — opt-in echo of the *normalized* request (`--echo-request`); streaming skips it.
- `errors` / `warnings` — always arrays of `{code, message, details}` (`pulse_errors_lookup`).

## Request shapes (per command)

| Operation (CLI) | Wire type | Top-level keys |
|---|---|---|
| process, predict (`pulse api process`) | `Request` | `label, cohort, time_zone, time_zone_field, filterers, features, attributes, groups, aggregations, windows, sort, tests, post_tests, regressions, joins, labels, crosstab, overlays, outputs, disable_components, return`, `union`, `union_source_field`, `weight`, `params`, `multiplicity`, `vectors`, `matrices`, `models`, `populations` |
| compose (`pulse api compose`) | `ComposedRequest` | `requests[]` (each a `Request`) plus batch keys ([`compose-requests`](compose-requests.md)) |
| process chain (`pulse api process-chain`) | `ChainRequest` | `cohort, stages[], overlays, params` (each stage: `{request: Request}`) |
| facet (`pulse api facet`) | `FacetRequest` | `cohort, fields[], filterers, additive_fields, discrete_top_k, numeric_percentiles, include_histogram, histogram_bins, histogram_range, labels, overlays, time_zone, time_zone_field` |
| sample (`pulse api sample --request`) | `SampleRequest` | `cohort, n, labels` |

MCP tool per operation: manifest `mcp_tools`.

Row weight: `Request.weight` `{field, kind}`; per-slot `weight` — a field, `{field, kind}` or `null` (`null` opts out, absent inherits); `pulse.Options.DefaultWeight`. See [`weighting`](weighting.md).

Slot order is pipeline order: `features → filterers → attributes → groups → aggregations → windows → sort`; `sort` shares the window comparator (nulls last both ways, grouper keys in key order). A derived name (feature output, attribute / aggregation / window label) exists only DOWNSTREAM of its producer. Runtime and predict refuse (`SERVICE_VALIDATION`) a name nothing produces, a label shadowing a field, and an aggregation label equal to a group field or another aggregation label.

## Canonical process Request

```json cohort=experiment
{
  "cohort": {"filename": "sales.pulse"},
  "filterers": [{"type": "FILTER_INCLUDE", "field": "treatment", "values": ["variant"]}],
  "groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    {"type": "AGG_SUM", "field": "revenue", "label": "rev"}
  ]
}
```

Every `type` is an operator from `pulse_manifest` `components.<category>`; runnable requests: `pulse_examples_search`.

## Slot-key gotchas

Unknown keys (any depth) → `PULSE_REQUEST_UNKNOWN_FIELD`, `details.path`; a null entry (`[null]`) → `PROCESSING_CONFIG` `{slot, index}`.

| Wrong | Right |
|---|---|
| `groupers` | `groups` |
| `aggregators` | `aggregations` |
| `filters` | `filterers` |
| `tests_post` | `post_tests` |
| `output` (not materialize) | `outputs` |
| `request` (Compose top-level) | `requests` |

Per-operator slot: `type`, `field`, `label`, optional `params` (keys: its atomic skill).

## Smart defaults

A slot naming `field` without `type` gets one from the schema type. Predict reports filled slots under `data.defaults_applied`.

| Field type | Default agg | Default grouper |
|---|---|---|
| numeric (`u4`, `u8`..`u64`, `f32`/`f64`, `decimal128`) | `AGG_SUM` | `GROUP_RANGE` (interval 10) |
| `categorical_*`, `packed_bool` | `AGG_MODE_COUNT` | `GROUP_CATEGORY` |
| `date`, `datetime` (truncated) | (explicit only) | `GROUP_DATE` (`"day"`) |

A `type` defaults only on `groups`, `aggregations`, crosstab axes and cell(s); never overridden, never across categories. Disable: `pulse.Options{DisableDefaults: true}` / `--no-defaults`.

## Streamability

`pulse_predict` returns `data.streamable: bool` + `data.streamable_reasons: []string` (authoritative).

Each operator's manifest entry carries `streamable` (+ `streamable_hint`). Windows, order statistics, percentile ranks, decimal aggregations, tier-2 post tests and two-pass attributes beside features or groups buffer.

## Time zones

`time_zone` (or per-row `time_zone_field`) sits on `Request` and `FacetRequest` (a batch root: per inner `Request`). Slot `tz` (or `tz_field`, not both) sits on `groups`, `filterers`, `attributes`, `features` and `crosstab.rows`/`columns` entries — never inside `params`. Names are `UTC` or IANA; `EST`, `Local`, `+05:00` → `PULSE_TIMEZONE_UNKNOWN`.

Precedence per slot: `tz` → `tz_field` → `time_zone_field` → `time_zone` → cohort `zone_field` → `Options.DefaultTimeZone` → `UTC`. Only manifest `zone: "capable"` operators take `tz`; a `zone: "following"` operator inherits its host grouper's zone; extensions never are.

On a `datetime` field instants read on their local day. Refused with `PROCESSING_CONFIG`: `tz` on a non-capable operator; explicit `tz` on a `date` field (even `"UTC"`); a non-UTC zone on a derived field. An inherited zone on a `date` field is not applied. UTC is byte-identical to no zone.

## Predict-specific data fields

`streamable`, `streamable_reasons`, `defaults_applied`, `weights` / `suggested_weight` ([`weighting`](weighting.md)), `resolved_vectors`, `time_zones` (per zone-capable slot: `{slot, operator, field_type, tz, tz_field, source}`; [`time-zones`](time-zones.md)), `suggestions` (`on_invalid="suggest"`), per-slot `buffered_components` (non-mergeable).

## Cross-links

[`response-components`](response-components.md) (the `Response.Components` block) · [`response-shaping`](response-shaping.md) (`return`: trim the response, wire precision) · [`session-bootstrap`](session-bootstrap.md) (MCP session order) · [`aggregation-design`](aggregation-design.md) / [`grouper-design`](grouper-design.md) / [`attribute-composition`](attribute-composition.md) (slot shapes) · [`compose-requests`](compose-requests.md) · [`facet-design`](facet-design.md) · [`streaming-and-watching`](streaming-and-watching.md) (stream chunks, request hashing) · [`multiplicity-correction`](multiplicity-correction.md).
