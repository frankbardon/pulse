# pulse api predict

**Audience:** CLI users validating a request before running it.

`pulse api predict` validates a `types.Request` against a `.pulse`
file's schema **without executing it**. It reads only the header and
schema — never record data — so it's a cheap, safe iteration loop
against arbitrarily large cohorts.

> **LLM agents using MCP:** see the `pulse_predict` MCP tool and the
> `debugging-with-predict` skill. Predict is the LLM's primary
> "would this work?" probe.

## Synopsis

```
pulse api predict --request FILE [--json] [--strict]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--request` | `-r` | string | (required) | Request JSON path |
| `--json`    |      | bool   | false      | Emit the standard envelope |
| `--strict`  |      | bool   | false      | Treat warnings as errors |

## Structural ban

`descriptor/predict.go` cannot import `service/` or `processing/`.
This is enforced by `TestPredictNoExecutionImports`. Predict is
guaranteed to never touch the executor.

## Output (text mode)

```
Valid: true
Schema: 7 fields
Warning [PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL]: AGG_AVG on field region (categorical_u8)
```

Without `--strict`, that warning would still let the command exit 0.
With `--strict`, the warning becomes an error and the command exits
non-zero.

## Output (`--json`)

```json
{
  "format_version": "1.0",
  "data": {
    "valid": true,
    "schema_info": {"field_count": 7},
    "streamable": false,
    "streamable_reasons": [
      "AGG_MEDIAN on field price"
    ],
    "request": { /* the request as predict resolved it, with defaults applied */ }
  },
  "errors":  [],
  "warnings": [
    {"code": "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL", "message": "..."}
  ]
}
```

`streamable` reports whether the request will execute on the
streaming Process path; `streamable_reasons` lists every gate that
forced the buffered path. See [Performance
Notes](../ops/performance.md) for the full streaming/buffered table.

`request` echoes the request **after defaults have been applied** so
you can see what would actually run. To suppress defaults, run with
`--no-defaults` on the executing leaf (`api process`,
`api compose`); predict reports `defaults_applied` regardless.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Valid (or valid with warnings, in non-strict mode) |
| 1 | Invalid, or `--strict` with at least one warning |

## Examples

### Quick validity check

```bash
pulse api predict --request req.json
```

### Programmatic check with envelope

```bash
pulse api predict --request req.json --json | \
    jq -e '.data.valid == true' >/dev/null && echo "OK"
```

### Strict mode for CI

```bash
pulse api predict --request req.json --strict --json
```

### Detect that a request will buffer

```bash
pulse api predict --request req.json --json | \
    jq '.data | {streamable, streamable_reasons}'
```

## Common warning codes

| Code | What to do |
|---|---|
| `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` | Use `AGG_COUNT` / `AGG_FREQUENCY` instead of `AGG_SUM` / `AGG_AVG` on categoricals |
| `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`     | Decimal-typed field; switch to a decimal-aware aggregator |
| `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`      | Edit the schema description; re-import |
| `PULSE_FEAT_TARGET_LEAKAGE_RISK`           | The feature operator references the target column; reorganise the pipeline |

The full code-by-code recovery playbook lives in
`skills/error-code-reference.md` and at
[Troubleshooting](../ops/troubleshooting.md).

## Related

- [`pulse api process`](api-process.md) — executes a validated request
- [`pulse api ask`](api-ask.md) — combined predict + execute
- [Library: pulse.Predict / Ask](../library/ask.md) — Go counterparts
- `skills/debugging-with-predict.md` — LLM-side iteration recipe
