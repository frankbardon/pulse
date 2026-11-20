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
                              [--labels FIELD=TABLE[:replace|augment]]
                              [--echo-request]
pulse api sample --request FILE|- [--count N] [--json]
                              [--labels FIELD=TABLE[:replace|augment]]
                              [--echo-request]
```

Name the cohort with `--input`, or pass a whole `SampleRequest` with
`--request` — the only CLI form that samples a [cohort
union](../library/cohort-union.md). Exactly one of the two is required;
both, or neither, is a usage error (exit 2).

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--input`        | `-i` | string | (none)     | Cohort `.pulse` file path. Required unless `--request` |
| `--request`      | `-r` | string | (none)     | A `SampleRequest` JSON file, or `-` to read it from stdin: `cohort` or `union` (+ `union_source_field`), `n`, `labels`. Decoded strictly — a misspelled key is `PULSE_REQUEST_UNKNOWN_FIELD` (exit 1, in the envelope). Never beside `--input` |
| `--count`        | `-n` | int    | 10         | Rows to sample. With `--request`: overrides the file's `n` when set; a file without `n` takes 10 |
| `--json`         |      | bool   | false      | Emit the standard envelope |
| `--labels`       |      | string | (none)     | Label binding on a categorical field, or an integer-coded u4/u8/u16/u32 field in augment mode: `field=table[:replace|augment]`. Repeatable. Requires `PULSE_LABEL_TABLES_DIR` or a programmatically registered label table. With `--request`: replaces the file's `labels` |
| `--echo-request` |      | bool   | false      | Include the resolved `SampleRequest` on `envelope.request` |

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

Decimal128 values are serialised as strings to preserve precision. A
`date` cell is its calendar day (`2024-01-04`) and a `datetime` cell
RFC 3339 UTC (`2024-01-04T09:30:00Z`) — never the stored epoch day /
second, and no zone is applied. A null cell is omitted from its row.

## Output (`--json`)

```json
{
  "format_version": "1.1",
  "data": [ /* row array */ ],
  "errors": [],
  "warnings": []
}
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | File not found, truncated, or unsupported version — in the envelope's `errors` under `--json` |
| 2 | Usage error (unknown flag, malformed value, neither `--input` nor `--request`, or both); no envelope |

## Examples

```bash
# 10 rows
pulse api sample --input sales.pulse

# 100 rows, envelope-wrapped
pulse api sample --input sales.pulse --count 100 --json

# Pipe into jq
pulse api sample --input sales.pulse --count 100 | jq '.[] | .revenue'

# A union: rows in member order (source first), each tagged in _source
cat > union.json <<'JSON'
{"union": [{"cohort": {"filename": "customers.pulse"}, "label": "real"},
           {"cohort": {"filename": "customers_twin.pulse"}, "label": "twin"}],
 "n": 20}
JSON
pulse api sample --request union.json --json

# The same request from stdin
pulse api sample --request - --json < union.json
```

## When `sample` is the wrong tool

- For filtered subsets, use `pulse cohort filter` to write the matching
  records to a new `.pulse` file, then sample or export that. A
  [`pulse api process`](api-process.md) request with only a `FILTER_*`
  returns no rows: `process` emits `data` only for groups, aggregations
  or windows.
- For distinct values of a single field, use [`pulse api
  facet`](api-facet.md).
- For schema-only views (types, descriptions, dictionaries), use
  [`pulse cohort inspect`](cohort-inspect.md).

## Related

- [`pulse api facet`](api-facet.md) — distinct values for a single field
- [Library: pulse.Sample](../library/overview.md)
