# pulse api sample

**Audience:** CLI users grabbing a quick peek at a few rows from a
cohort — for debugging, sanity-checking an import, or seeding a
template request.

`pulse api sample` returns the first N rows from a `.pulse` file
decoded back to a map of field → value. There is no filter, no
aggregation, no transformation — just a typed view of raw rows.

> **LLM agents using MCP:** see the `pulse_sample` MCP tool. It
> returns the same shape over the MCP transport.

## Synopsis

```
pulse api sample --input PATH [--count N] [--json]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--input` | `-i` | string | (required) | Cohort `.pulse` file path |
| `--count` | `-n` | int    | 10        | Rows to sample |
| `--json`  |      | bool   | false     | Emit the standard envelope |

## Output (text mode)

Pretty-printed JSON of the row array:

```json
[
  {
    "order_id": 1,
    "region": "west",
    "product": "widget",
    "units": 3,
    "revenue": "29.97",
    "sold_on": "2024-01-04"
  },
  ...
]
```

Decimal128 values are serialised as strings to preserve precision.

## Output (`--json`)

```json
{
  "format_version": "1.0",
  "data": [ /* row array */ ],
  "errors": [],
  "warnings": []
}
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | File not found, truncated, or unsupported version |

## Examples

```bash
# 10 rows
pulse api sample --input sales.pulse

# 100 rows, envelope-wrapped
pulse api sample --input sales.pulse --count 100 --json

# Pipe into jq
pulse api sample --input sales.pulse --count 100 | jq '.[] | .revenue'
```

## When `sample` is the wrong tool

- For filtered subsets, use [`pulse api process`](api-process.md) with a
  `FILTER_*` and no aggregation — the result will be one row per
  matching record.
- For distinct values of a single field, use [`pulse api
  facet`](api-facet.md).
- For schema-only views (types, descriptions, dictionaries), use
  [`pulse cohort inspect`](api-inspect.md).

## Related

- [`pulse api facet`](api-facet.md) — distinct values for a single field
- [Library: pulse.Sample](../library/overview.md)
