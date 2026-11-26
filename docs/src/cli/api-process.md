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

Each flag's type and help text is in the
[Command & flag reference](reference.md#pulse-api-process). The ones
that change what runs:

- `--no-defaults` turns off [smart defaults](#smart-defaults).
- `--no-components` suppresses `Response.Components` (the counts behind
  each figure); output is otherwise byte-identical. It wins over a
  request `return` block (the block gains `exclude: ["components"]`);
  only a request `"disable_components": false` re-opens them.
- `--no-project` forces full-record decode (see
  [Field projection](#field-projection)).
- `--echo-request` puts the normalized (post-defaults) request on
  `envelope.request`; ignored under `--stream`, which has no envelope.

`--stream` and `--json` are mutually exclusive in spirit — `--stream`
emits one JSON object per line; `--json` emits the full envelope.

`--strict` is the post-execute companion to
[`pulse api predict --strict`](api-predict.md): predict refuses to
declare the request valid in the face of any warning; process refuses
to run it.

## Request file shape

The request file is a [`types.Request`](../library/overview.md)
serialised to JSON. Minimal example:

```json cohort=orders
{
  "cohort": {"filename": "orders.pulse"},
  "aggregations": [
    {"type": "AGG_SUM", "field": "revenue", "label": "total_revenue"}
  ]
}
```

The request is one JSON object whose top-level keys are the
`types.Request` slots (`pulse schema | jq '."$defs".Request.properties | keys'`
lists them). A canonical filter-group-aggregate example:

```json cohort=experiment
{
  "cohort": {"filename": "experiment.pulse"},
  "filterers": [
    {"type": "FILTER_INCLUDE", "field": "treatment", "values": ["variant"]}
  ],
  "groups": [
    {"type": "GROUP_CATEGORY", "field": "region"}
  ],
  "aggregations": [
    {"type": "AGG_COUNT",   "field": "id",      "label": "n"},
    {"type": "AGG_AVERAGE", "field": "revenue", "label": "mean_revenue"}
  ],
  "outputs": [{"format": "json"}]
}
```

The full grammar is in the [payload schema](../contract/payload-schema.md)
and [`types.Request`](https://github.com/frankbardon/pulse/blob/main/types/types.go);
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
narrow projection of a wide cohort). The gain depends on the schema: it
grows with the share of record bytes the request skips, so a narrow
cohort gains little (a 15-field, 53-byte retail cohort measured no
difference), and a request reading most fields gains nothing. `--no-project` (or
`pulse.Options{DisableProjection: true}`) forces a full-record decode;
use it only for debugging or when you need every field materialised.
The flag exists on `pulse api process` only; `process-chain` and
`compose` always project.

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

Every run-time warning the run raised (`data.warnings`: target-encode
leakage, labels, vectors, …) is also written to the envelope's
top-level `warnings`; `data` keeps its copy. Under `--strict` the
warnings `Options.Strict` covers refuse the run instead (exit 1).

`data.components` is the optional `Response.Components` slot — the
counts behind each figure (`skills/response-components.md`). It is
omitted when a run emits no components-shaped state.

### `--stream`

NDJSON of result rows, one per line. No envelope, no metadata footer.
A run that warned ends with one `{"warnings": [{code, message,
details}, ...]}` line — the entries a buffered run puts on
`data.warnings` (`PULSE_RETURN_PATH_UNMATCHED` excepted: buffered
only); a warning-free run has none.
Pair with [`pulse api predict`](api-predict.md) ahead of time to
confirm `Streamable=true`; predict-buffered shapes still emit via
this path, but they materialise inside the engine first.

A request `return` block shapes each line exactly like the buffered
`data` element: only the selected columns, measures at `precision`
(count columns exact). A request without `return` streams the
unchanged rows.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (warnings allowed) |
| 1 | Any error — wrapped in the envelope's `errors` array under `--json` (a warning `--strict` escalated included; nothing added to stderr), or printed to stderr otherwise |
| 2 | Usage error (unknown flag, malformed value, missing required flag); no envelope |

`pulse api process -r bad.json --json; echo $?` prints the envelope, then `1`. See [Exit status](flags.md#exit-status).

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
