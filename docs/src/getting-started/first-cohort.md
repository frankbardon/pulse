# Your First Cohort

**Audience:** new CLI users. This is a five-minute tour: import a CSV,
inspect the resulting `.pulse` file, run an aggregation, and export the
result back.

> **LLM agents using MCP:** the equivalent tour for an agent is the
> `getting-started` skill, fetched via `pulse_skills_get`. That skill
> speaks in tool calls and JSON payloads; this page speaks in shell
> commands.

## 1. Pick a CSV

For this walkthrough we'll assume a file called `sales.csv` with columns
like:

```csv
order_id,region,product,units,revenue,sold_on
1,west,widget,3,29.97,2024-01-04
2,east,gadget,1,19.99,2024-01-04
3,west,widget,7,69.93,2024-01-05
...
```

Any CSV with a header row works. Pulse also imports TSV, NDJSON,
JSON-array, Parquet, Arrow IPC, and Excel — see [Flag
Reference](../cli/flags.md) for per-format flags.

## 2. Import to a `.pulse` file

```bash
pulse import csv --input sales.csv --output sales.pulse
```

Pulse samples up to 500 rows by default to infer a schema (you can change
that with `--sample-rows`). Each column gets a typed binary representation
and, if it looks like a low-cardinality string, a categorical dictionary.

Want to control the schema explicitly? Generate a template, edit it, and
re-import:

```bash
# Editable schema template
pulse import schema-template sales.csv > sales.schema.json

# Edit sales.schema.json — set types, add descriptions
# Then import with the schema
pulse import csv --input sales.csv --schema sales.schema.json --output sales.pulse
```

See [Field Types](../format/field-types.md) for the type catalog and
[Dictionary Blocks](../format/dictionaries.md) for how categoricals are
encoded.

## 3. Inspect

The `.pulse` file is fully self-describing. Read it back:

```bash
pulse cohort inspect sales.pulse
```

Output is a table of fields, their types, and the description string
stored in the header. Add `--json` for the structured envelope, or
`--full-dict` to print every categorical entry instead of truncating
after 100.

```bash
pulse cohort inspect sales.pulse --json
```

The envelope is documented in [`pulse cohort inspect`](../cli/cohort-inspect.md).

## 4. Validate a request before running it

Pulse separates validation from execution. Write a tiny request file:

```json
{
  "cohort": {"filename": "sales.pulse"},
  "groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "order_id", "label": "orders"},
    {"type": "AGG_SUM", "field": "revenue", "label": "total_revenue"}
  ]
}
```

Save it as `request.json`, then check whether it makes sense against the
cohort's schema:

```bash
pulse api predict --request request.json
```

You'll see `Valid: true`, the schema's field count, and any warnings
(e.g., aggregating something numeric on a categorical field). Predict
never reads record data, so it's safe to iterate on a request without
touching a multi-GB cohort.

See [`pulse api predict`](../cli/api-predict.md) and the
`debugging-with-predict` skill for the full predict loop.

## 5. Execute

```bash
pulse api process --request request.json --json
```

The response is wrapped in the standard envelope (`format_version`,
`data`, `errors`, `warnings`). `data` carries the result rows and a
metadata block with `total_rows` and `filtered_rows`.

If your result is large, swap `--json` for `--stream` to receive rows as
NDJSON, one line at a time — useful for pipelines that don't want to
buffer the whole result. See [Streaming &
ProcessStream](../library/streaming.md) for which request shapes
actually stream end-to-end inside the engine vs which buffer.

## 6. Export

You're done with the `.pulse` file? Export to whatever your downstream
tool understands:

```bash
pulse export csv     --input sales.pulse --output sales.out.csv
pulse export parquet --input sales.pulse --output sales.out.parquet
pulse export excel   --input sales.pulse --output sales.out.xlsx
```

To skip the intermediate `.pulse` entirely and convert in one shot, use
`pulse convert source.csv target.parquet` — see the top-level
[README](https://github.com/frankbardon/pulse/blob/main/README.md) for
the full convert recipe.

## What you didn't see

- **Compose**: batch multiple requests in one call —
  [`pulse api compose`](../cli/api-compose.md).
- **Ask**: natural-language one-shot — [`pulse api ask`](../cli/api-ask.md).
- **Sample / Facet**: cheap read-only probes — [`api sample`](../cli/api-sample.md),
  [`api facet`](../cli/api-facet.md).
- **Window / Feature / Test operators**: pull from the skill pack
  (`window-operations`, `feature-engineering`, `statistical-testing`)
  via `pulse skills show <name>`.

For a full map of the CLI, see the [CLI Tour](cli-tour.md).
