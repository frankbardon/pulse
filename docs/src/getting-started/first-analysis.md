# Your First Analysis

**Audience:** new CLI users with `sales.pulse` from
[Your First Cohort](first-cohort.md).

## 1. Write a request

A request names a cohort and the operators to run. This one groups by
region and counts orders and sums revenue in each group. Save it as
`request.json`:

```json cohort=experiment
{
  "cohort": {"filename": "sales.pulse"},
  "groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id", "label": "orders"},
    {"type": "AGG_SUM", "field": "revenue", "label": "total_revenue"}
  ]
}
```

To find an operator for a question, see
[Choosing an Operator](../library/operators.md) or
`pulse examples search <words>`.

## 2. Validate it

```bash
pulse api predict --request request.json
```

Predict checks the request against the schema without reading records:
`Valid: true`, then any warnings. The import above wrote placeholder
field descriptions, so it warns `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`
for each field; a schema file with real descriptions clears them.

## 3. Run it

```bash
pulse api process --request request.json --json
```

`data.data` holds one row per region (`orders`, `total_revenue`), and
`data.metadata` the row counts. Every `--json` output uses the same
envelope: `format_version`, `data`, `request`, `errors`, `warnings`
([`--json` envelope](../cli/flags.md#--json-envelope)). For a large result,
`--stream` writes NDJSON rows instead
([Streaming](../library/streaming.md)).

## 4. Export

```bash
pulse export csv --input sales.pulse --output sales.out.csv
```

`pulse convert source.csv target.parquet` converts between formats in
one step.

## Next

[Your First MCP Session](first-mcp-session.md) hands the same cohort to
an AI client. The [CLI Tour](cli-tour.md) maps the rest of the CLI, and
the [Analysis Guide](../guide/index.md) goes from a question to an
operator.
