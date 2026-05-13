# pulse api process

**Audience:** CLI users running a single processing request against a
cohort.

`pulse api process` executes one [`types.Request`](https://github.com/frankbardon/pulse/blob/main/types/types.go)
against a `.pulse` file and prints the result. It's the most-used
leaf in the binary.

> **LLM agents using MCP:** the equivalent surface is the
> `pulse_process` MCP tool — see `skills/request-recipes.md` for
> request skeletons.

## Synopsis

```
pulse api process --request FILE [--json] [--stream] [--no-defaults]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--request`       | `-r` | string | (required) | Path to the request JSON file |
| `--json`          |      | bool   | false      | Emit the result wrapped in the JSON envelope |
| `--stream`        |      | bool   | false      | Stream rows as NDJSON (one per line) instead of buffering |
| `--no-defaults`   |      | bool   | false      | Disable smart operator-type inference; require explicit `Type` on every aggregation and grouper |

`--stream` and `--json` are mutually exclusive in spirit — `--stream`
emits one JSON object per line; `--json` emits the full envelope.

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

The full request grammar — filterers, groupers, attributes, window
operators, features, sort, tests, post-tests — is documented in
[`types.Request`](https://github.com/frankbardon/pulse/blob/main/types/types.go);
the LLM-facing companion is `skills/request-recipes.md`.

## Output

### Text mode (default)

Pretty-printed JSON of the `Response` struct: a `data` array of
result rows plus a `metadata` block with `total_rows`, `filtered_rows`,
and `cohort_file`.

### `--json`

The standard envelope:

```json
{
  "format_version": "1.0",
  "data": {
    "data": [ /* result rows */ ],
    "metadata": { "total_rows": 1000, "filtered_rows": 800, "cohort_file": "sales.pulse" }
  },
  "errors": [],
  "warnings": []
}
```

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
- [`pulse api ask`](api-ask.md) — natural-language one-shot
- [`pulse api predict`](api-predict.md) — validate without executing
- [`pulse api sample`](api-sample.md) — quick row preview
- [Library: pulse.New & Options](../library/options.md) — the Go-side
  equivalent of `--no-defaults`
- [Library: Streaming & ProcessStream](../library/streaming.md) —
  what streams vs what buffers
