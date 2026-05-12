# pulse api ask

**Audience:** CLI users running a one-shot natural-language query
against a cohort, or any caller who wants "predict + process" in one
call.

`pulse api ask` is the unified entry point. It validates a request
(predict), optionally translates a natural-language query into a
request via the built-in parser, and — on success — executes the
request. The MCP server uses the same library facade internally for
the `pulse_ask` tool.

> **LLM agents using MCP:** the LLM-side counterpart is the
> `pulse_ask` MCP tool. The `query-router-prompt` skill gives a
> system-prompt template for routing natural language into Pulse
> requests.

## Synopsis

```
pulse api ask  [--file FILE] [--query "..."] [--request FILE]
               [--on-invalid abort|suggest] [--predict]
               [--json] [--no-defaults]
```

You must pass at least one of `--query` or `--request`.

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--file`        | `-f` | string | (none)   | Cohort `.pulse` file path |
| `--query`       | `-q` | string | (none)   | Natural-language query string |
| `--request`     | `-r` | string | (none)   | Optional structured request JSON path |
| `--on-invalid`  |      | string | `"abort"` | Predict-invalid behaviour: `"abort"` returns an error; `"suggest"` returns the response with `suggestions` populated |
| `--predict`     |      | bool   | false    | Validate without executing |
| `--json`        |      | bool   | false    | Emit the standard envelope |
| `--no-defaults` |      | bool   | false    | Disable smart operator-type inference |

## How the parser fills the request

When `--query` is set, the parser reads the cohort's schema and
synthesises a `types.Request` slot-by-slot. If `--request` is also
provided, explicit fields in that request **always win** on
collision — the parser only fills empty slots.

The parser populates these slots from the query today: `Aggregations`,
`Groups`, `Filterers`, `Windows`, `Sort`, `Tests`. Other slots in the
parsed request are ignored.

## Output

### Text mode

A human-readable summary:

```
Query: average revenue by region
Matched fields: [revenue region]
Confidence: 0.92

Resolved request:
{ ...the synthesised types.Request... }

{ ...result rows, if executed... }
```

### `--json`

Full `AskResponse` envelope:

```json
{
  "format_version": "1.0",
  "predict": { /* PredictResult */ },
  "process": { /* Response, if executed */ },
  "suggestions": [],
  "query_resolution": {
    "query": "average revenue by region",
    "matched_fields": ["revenue", "region"],
    "confidence": 0.92
  },
  "errors": [],
  "warnings": []
}
```

`process` is omitted when `--predict` is set or when predict reported
invalid and execution was skipped.

## Confidence and unresolved queries

`query_resolution.confidence` is in `[0, 1]`. A confidence of `0` means
`PULSE_QUERY_UNRESOLVED` (the parser found no usable structure) and
lands in `errors`. Lower-than-1 confidences with at least one matched
field land their reasons in `warnings`
(`PULSE_QUERY_AMBIGUOUS`). The `query-router-prompt` skill describes
the parser's grammar.

## OnInvalid behaviours

| Value | Behaviour |
|---|---|
| `"abort"` (default) | Return a `SERVICE_VALIDATION` error if predict reports invalid |
| `"suggest"`         | Return the response with `suggestions` populated from `errors/fixup_metadata.go` |

Use `"suggest"` when you want fixup hints (e.g., "did you mean field
`revenue`?") rather than a hard fail.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Validation failed (`abort`), parser failed, or process errored |

## Examples

### Pure natural-language query

```bash
pulse api ask --file sales.pulse --query "average revenue by region" --json
```

### Query plus partial structured request

```bash
cat > partial.json <<'EOF'
{
  "filterers": [{"type": "FILTER_RANGE", "field": "revenue", "values": ["100", "1000"]}]
}
EOF
pulse api ask --file sales.pulse --request partial.json --query "by region" --json
```

### Predict-only probe

```bash
pulse api ask --request req.json --predict --json
```

### Suggest fixups instead of erroring

```bash
pulse api ask --request typo.json --on-invalid suggest --json
```

## Related

- [`pulse api predict`](api-predict.md) — standalone validation
- [`pulse api process`](api-process.md) — execute a pre-validated request
- [Library: pulse.Ask](../library/ask.md) — Go-side counterpart
- `skills/query-router-prompt.md` — LLM prompt template for routing
- `skills/request-recipes.md` — canonical request skeletons
