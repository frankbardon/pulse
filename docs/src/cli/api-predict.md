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
pulse api predict --request FILE [--json] [--strict] [--echo-request]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--request`      | `-r` | string | (required) | Request JSON path |
| `--json`         |      | bool   | false      | Emit the standard envelope |
| `--strict`       |      | bool   | false      | Treat warnings as errors |
| `--echo-request` |      | bool   | false      | Include the normalized request on `envelope.request` (distinct from `PredictResult.Request`, which echoes the raw input) |

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
  "format_version": "1.1",
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

## Smart defaults

When an `aggregations[]` or `groups[]` slot names a `field` but omits
`type`, Pulse infers the operator from the named field's schema type
before running the request. Predict reports the inferred slot under
`data.defaults_applied` so you can echo back what was filled in:

| Field type | Default aggregation | Default grouper |
|---|---|---|
| `u4`, `u8`..`u64`, `f32`, `f64`, `decimal128` | `AGG_SUM` | `GROUP_RANGE` (interval 10) |
| `categorical_u8`/`u16`/`u32` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (none — must be explicit) | `GROUP_DATE` (component `"day"`) |
| `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |

The `Nullable` flag on a field never changes its default operator — it
only controls per-record null-bitmap participation.

Rules: defaults never override an explicit `type`; they never cross
categories (a missing aggregator does not insert a grouper); statistical
tests (`tests[]`, `post_tests[]`) are not defaulted; filter expressions,
features, attributes, and windows are out of scope.

`--no-defaults` on `pulse api process` /
[`pulse api compose`](api-compose.md) disables the inference pass
entirely and forces every slot to be source-of-truth. Predict still
reports `defaults_applied` so the caller can see what would have been
filled in.

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

The full code-by-code recovery playbook is reachable per-code via
the MCP `pulse_errors_lookup` tool or the `pulse errors lookup CODE`
CLI; see also [Troubleshooting](../ops/troubleshooting.md).

## Related

- [`pulse api process`](api-process.md) — executes a validated request
- [Library: pulse.Predict](../library/overview.md) — Go counterpart
- [Debugging Predict](../internals/debugging-predict.md) — LLM-side iteration recipe
