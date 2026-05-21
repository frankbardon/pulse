---
name: export-format-selection
description: Pick the right export format — CSV (universal), TSV, NDJSON, JSON array, Parquet (columnar analytics), Arrow IPC (zero-copy handoff), Excel (humans). Use when wiring Process output into a downstream tool or warehouse.
type: guide
applies_to: process, compose
---

# Export Format Selection

<skill_overview>
After processing, Pulse can export results in multiple formats with tradeoffs in type fidelity, file size, tooling compatibility, and categorical representation. Invoke this skill when choosing an export target for a downstream consumer.
</skill_overview>

<reference>
## Format Comparison

| Feature | CSV | TSV | NDJSON | JSON Array | Parquet | Arrow IPC | Excel |
|---------|-----|-----|--------|------------|---------|-----------|-------|
| Type fidelity | Low (all text) | Low (all text) | Medium (JSON types) | Medium (JSON types) | High (native types) | High (native Arrow types) | Medium |
| Null representation | Empty string | Empty string | `null` | `null` | Native null | Native null | Empty cell |
| Categorical | String labels | String labels | String labels | String labels | Dictionary-encoded | Dictionary-encodable | String labels |
| File size | Medium | Medium | Large | Large | Small (compressed) | Small (LZ4 by default) | Medium |
| Streaming | Yes | Yes | Yes | Producer streams; consumers usually parse whole file | No (columnar) | Batch-by-batch | No |
| Human-readable | Yes | Yes | Yes | Yes | No | No | Yes (GUI) |
| Schema included | No | No | No | No | Yes | Yes | No |
</reference>

<reference>
## When to Use Each Format

### CSV

Use CSV when:
- The downstream tool expects comma-separated text (spreadsheets, most data tools)
- Human readability is important
- Type information will be re-inferred by the consumer

Limitations:
- No native null; empty strings are ambiguous
- No type metadata; consumers must infer types
- Quoting rules can cause issues with embedded commas

### TSV

Use TSV when:
- The downstream tool expects tab-separated text
- Data contains many commas (avoiding quoting complexity)
- Otherwise, same tradeoffs as CSV

### NDJSON

Use NDJSON (newline-delimited JSON) when:
- The consumer is a JSON-native tool or API
- You need one-record-per-line streaming
- Null values must be explicitly represented

Limitations:
- Larger file size than CSV due to repeated keys
- No schema metadata in the file itself

### JSON Array

Use JSON Array (`.json`) when:
- The consumer is a browser, REST API, or scripting environment that expects a single top-level array (`JSON.parse`, Python `json.load`, etc.)
- The dataset is small enough that whole-file parsing is acceptable
- You want a single self-contained JSON document rather than line-delimited records

Limitations:
- Most consumers parse the whole array at once; not as memory-friendly as NDJSON for very large datasets
- No schema metadata in the file
- Only flat objects are supported on import (nested objects/arrays are rejected with `PULSE_IMPORT_ROW_ERROR`, same rule as NDJSON)
- The `.json` extension always means "top-level array of flat objects"; NDJSON should use `.ndjson` or `.jsonl`

### Parquet

Use Parquet when:
- The downstream tool supports Parquet natively (Spark, DuckDB, Pandas, Arrow)
- Type fidelity matters (integers stay integers, nulls stay nulls)
- File size matters (Parquet uses columnar compression)
- Categorical fields should remain dictionary-encoded

Parquet is the best format for machine-to-machine data transfer.

### Arrow IPC (Feather V2)

Use Arrow IPC (`.arrow`, `.feather`) when:
- The downstream tool already speaks Arrow natively (Polars, DuckDB, pandas via `pyarrow`, R `arrow`)
- You want zero-copy columnar interchange without the Parquet encode/decode overhead
- You need batch-level streaming on read while still keeping schema metadata in the file
- You want to round-trip a dataset through an Arrow-native pipeline and back into Pulse

The `.arrow` and `.feather` extensions both produce the same Arrow IPC file format. Feather V2 is a re-branding of the IPC file format on disk.

Limitations:
- Not as widely consumed as Parquet by non-Arrow tooling
- Pulse's writer emits all columns as Arrow `String`; consumers that need native numeric Arrow types should round-trip through Parquet or import the data with a typed schema first
- Stream-format Arrow IPC (`.arrows`) is not supported — use the file format only

### Excel

Use Excel when:
- The consumer is a non-technical user working in Microsoft Excel or Google Sheets
- You need a single-file deliverable with formatted output

Limitations:
- Row limit of ~1 million rows
- No native categorical encoding (values appear as strings)
- Larger file size than Parquet
</reference>

<reference>
## Categorical Behavior per Format

- **CSV/TSV**: Categorical fields are exported as their string labels. The dictionary is lost.
- **NDJSON**: Categorical fields appear as string values in JSON.
- **JSON Array**: Categorical fields appear as string values; the dictionary is lost.
- **Parquet**: Categorical fields are exported as dictionary-encoded columns, preserving the encoding.
- **Arrow IPC**: Categorical fields are exported as Arrow `String` columns. Consumers that need dictionary encoding can call `.dictionary_encode()` after read.
- **Excel**: Categorical fields appear as string values in cells.
</reference>

<rule severity="should" topic="round-trip-preservation">
If you need to round-trip data back into Pulse, use Parquet to preserve categorical encoding.
</rule>

<rule severity="note" topic="field-projection">
## Projecting Fields on Export

`ExportJob.Includes []string` (and `ConvertJob.Includes`) restricts the output to the named source-schema fields. Nil / empty selects every field — pre-projection behaviour, unchanged. Output column order always follows the source schema, never the include order, so the on-disk byte layout stays the source of truth and downstream consumers see a stable layout regardless of CLI invocation order.

CLI surface: the `pulse export {csv|tsv|ndjson|jsonarray|parquet|arrow|excel}` subcommands accept a repeatable `--include` flag (e.g. `--include country --include amount`). Unknown names return `PULSE_EXPORT_FIELD_UNKNOWN` — run `pulse inspect` first to discover valid field names.

Label interaction: `--labels field=table:augment` still emits the sibling `<field>_label` column, but only when the source field is itself included. Excluding a source field also drops its augment sibling — the sibling has nothing to attach to. Replace-mode bindings apply only to included fields. Convert: when `KeepPulseAt` is set, the intermediate `.pulse` file always carries the full source schema — projection is an output-time overlay, not an on-disk schema change.

Embedder API: set `pio.ExportJob{Includes: []string{"a", "b"}}` (or `pio.ConvertJob`); duplicates dedupe; the in-memory order matches schema order in the emitted row.
</rule>

<rule severity="note" topic="no-mcp-tool">
## Export has no MCP tool today

The export and convert operations are CLI / library facilities — there is no `pulse_export` MCP tool. When advising on format selection, decide based on the table above and point a human at the relevant mdBook chapter for invocation:

- CSV / TSV / NDJSON / JSON Array — see the export chapters under https://frankbardon.github.io/pulse/cli/
- Parquet — same.
- Arrow IPC / Feather V2 — produced via the convert command. See https://frankbardon.github.io/pulse/cli/
- Excel — same.

Validation before writing is available from the CLI side (`pulse export predict`). It is not surfaced through MCP today.
</rule>
