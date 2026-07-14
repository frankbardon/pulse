# pulse api process

**Audience:** CLI users running a single processing request against a
cohort.

`pulse api process` executes one [`types.Request`](https://github.com/frankbardon/pulse/blob/main/types/types.go)
against a `.pulse` file and prints the result. It's the most-used
leaf in the binary.

> **LLM agents using MCP:** the equivalent surface is the
> `pulse_process` MCP tool — see `skills/session-bootstrap.md` and
> `skills/aggregation-design.md` for request authoring guidance.

## Synopsis

```
pulse api process --request FILE [--json] [--stream] [--no-defaults]
                                 [--no-components] [--no-project]
                                 [--strict] [--echo-request]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--request`       | `-r` | string | (required) | Path to the request JSON file |
| `--json`          |      | bool   | false      | Emit the result wrapped in the JSON envelope |
| `--stream`        |      | bool   | false      | Stream rows as NDJSON (one per line) instead of buffering |
| `--no-defaults`   |      | bool   | false      | Disable smart operator-type inference; require explicit `Type` on every aggregation and grouper |
| `--no-components` |      | bool   | false      | Suppress `Response.Components` (per-aggregator / per-grouper / per-filterer / crosstab / run constituent-parts metadata). Wire form is byte-identical to the pre-Components baseline |
| `--no-project`    |      | bool   | false      | Disable buffered-decode field projection; force full-record decode. Projection is on by default and output-transparent (same result, only faster), so this is an opt-out for debugging or full-map decode |
| `--strict`        |      | bool   | false      | Promote request-validation warnings (e.g. numeric aggregation on a categorical field) into hard errors |
| `--echo-request`  |      | bool   | false      | Include the normalized (post-defaults) request on `envelope.request`. Ignored under `--stream` because NDJSON has no envelope |

`--stream` and `--json` are mutually exclusive in spirit — `--stream`
emits one JSON object per line; `--json` emits the full envelope.

`--strict` is the post-execute companion to
[`pulse api predict --strict`](api-predict.md): predict refuses to
declare the request valid in the face of any warning; process refuses
to run it.

## Request file shape

The request file is a [`types.Request`](../library/overview.md)
serialised to JSON. Minimal example:

```json
{
  "cohort": {"filename": "sales.pulse"},
  "aggregations": [
    {"type": "AGG_SUM", "field": "revenue", "label": "total_revenue"}
  ]
}
```

The full request grammar is one JSON object whose top-level keys mirror
`types.Request`: `cohort`, `filterers`, `features`, `attributes`,
`groups`, `aggregations`, `windows`, `sort`, `tests`, `post_tests`,
`outputs`. A canonical filter-group-aggregate example:

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
    {"type": "AGG_COUNT",   "field": "id",    "label": "n"},
    {"type": "AGG_AVERAGE", "field": "score", "label": "mean_score"}
  ],
  "outputs": [{"format": "json"}]
}
```

The full grammar — windows, sort, tests, post-tests — is documented in
[`types.Request`](https://github.com/frankbardon/pulse/blob/main/types/types.go);
the LLM-facing companion is `skills/aggregation-design.md`.

## Pipeline order

Pulse executes a request in a fixed sequence:

```
Load -> Features -> Filter -> Attributes -> Group -> Aggregate -> Windows -> Sort -> Output
```

Features run **before** filterers, so derived columns are addressable
as filter, group, attribute, and window inputs. Windows run **after**
aggregation, on the post-aggregate row set. `Request.Sort` runs last.

## Smart defaults

When an `aggregations[]` or `groups[]` slot names a `field` but omits
`type`, the engine infers the operator from the named field's schema
type at request time. `--no-defaults` (or
`pulse.Options{DisableDefaults: true}`) turns this off and requires
every slot to be source-of-truth. The full defaults table is documented
on [`pulse api predict`](api-predict.md#smart-defaults).

## Field projection

Buffered-decode field projection is **on by default** and
output-transparent: the engine decodes only the source fields the
request actually reads, so the result payload is byte-identical to a
full decode — just faster and lighter per record (measured ~7× on a
narrow projection of a wide cohort). `--no-project` (or
`pulse.Options{DisableProjection: true}`) forces a full-record decode;
use it only for debugging or when you need every field materialised.
`pulse.Options.ProjectBufferedFields` is retained but deprecated (a
harmless no-op). This flag is scoped to `pulse api process` in v1 —
`pulse api process-chain` and `pulse api compose` do not expose it yet.

## Output

### Text mode (default)

Pretty-printed JSON of the `Response` struct: a `data` array of
result rows plus a `metadata` block with `total_rows`, `filtered_rows`,
and `cohort_file`.

### `--json`

The standard envelope:

```json
{
  "format_version": "1.1",
  "data": {
    "data": [ /* result rows */ ],
    "metadata": { "total_rows": 1000, "filtered_rows": 800, "cohort_file": "sales.pulse" },
    "components": {
      "aggregations": [{"label": "n", "n": 800, "n_null": 0}],
      "filterers":    [{"n_in": 1000, "n_out": 800, "n_null_input": 0}],
      "run":          {"total_records": 1000, "filtered_records": 800, "null_records": 0}
    }
  },
  "errors": [],
  "warnings": []
}
```

`data.components` is the additive `Response.Components` slot
documented in CLAUDE.md → Output Format Contract and
`skills/response-components.md`. The slot is `omitempty`: a run that
emits no components-shaped state (a streaming `--stream` chunk between
the first and last on a non-mergeable aggregator, for example) marshals
without the `components` key at all — byte-identical to the pre-0.20
wire shape. `format_version` stays at `"1.0"` because the slot is
additive.

### `--stream`

NDJSON of result rows, one per line. No envelope, no metadata footer.
Pair with [`pulse api predict`](api-predict.md) ahead of time to
confirm `Streamable=true`; predict-buffered shapes still emit via
this path, but they materialise inside the engine first.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Any error — wrapped in the envelope's `errors` array under `--json`, or printed to stderr otherwise |

## Examples

### Quick aggregation

```bash
cat > req.json <<'EOF'
{
  "cohort": {"filename": "sales.pulse"},
  "aggregations": [{"type": "AGG_COUNT", "field": "id", "label": "n"}]
}
EOF

pulse api process --request req.json
```

### Filter, group, and aggregate

```bash
cat > req.json <<'EOF'
{
  "cohort": {"filename": "sales.pulse"},
  "filterers": [{"type": "FILTER_RANGE", "field": "revenue", "values": ["100", "10000"]}],
  "groups":    [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [
    {"type": "AGG_COUNT",   "field": "id",      "label": "orders"},
    {"type": "AGG_AVERAGE", "field": "revenue", "label": "avg_rev"}
  ]
}
EOF

pulse api process --request req.json --json
```

### Stream rows into a downstream pipeline

```bash
pulse api process --request req.json --stream | \
    jq -c 'select(.avg_rev > 500)'
```

## Related

- [`pulse api compose`](api-compose.md) — batch of requests in one call
- [`pulse api predict`](api-predict.md) — validate without executing
- [`pulse api sample`](api-sample.md) — quick row preview
- [Library: pulse.New & Options](../library/options.md) — the Go-side
  equivalent of `--no-defaults`
- [Library: Streaming & ProcessStream](../library/streaming.md) —
  what streams vs what buffers
