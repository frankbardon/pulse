---
name: export-format-selection
description: CSV vs Parquet vs Excel for downstream tools
type: guide
applies_to: process, compose
---

# Export Format Selection

## Overview

After processing, Pulse can export results in multiple formats. Each format has tradeoffs in terms of type fidelity, file size, tooling compatibility, and categorical representation.

## Format Comparison

| Feature | CSV | TSV | NDJSON | Parquet | Excel |
|---------|-----|-----|--------|---------|-------|
| Type fidelity | Low (all text) | Low (all text) | Medium (JSON types) | High (native types) | Medium |
| Null representation | Empty string | Empty string | `null` | Native null | Empty cell |
| Categorical | String labels | String labels | String labels | Dictionary-encoded | String labels |
| File size | Medium | Medium | Large | Small (compressed) | Medium |
| Streaming | Yes | Yes | Yes | No (columnar) | No |
| Human-readable | Yes | Yes | Yes | No | Yes (GUI) |
| Schema included | No | No | No | Yes | No |

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

### Parquet

Use Parquet when:
- The downstream tool supports Parquet natively (Spark, DuckDB, Pandas, Arrow)
- Type fidelity matters (integers stay integers, nulls stay nulls)
- File size matters (Parquet uses columnar compression)
- Categorical fields should remain dictionary-encoded

Parquet is the best format for machine-to-machine data transfer.

### Excel

Use Excel when:
- The consumer is a non-technical user working in Microsoft Excel or Google Sheets
- You need a single-file deliverable with formatted output

Limitations:
- Row limit of ~1 million rows
- No native categorical encoding (values appear as strings)
- Larger file size than Parquet

## Categorical Behavior per Format

- **CSV/TSV**: Categorical fields are exported as their string labels. The dictionary is lost.
- **NDJSON**: Categorical fields appear as string values in JSON.
- **Parquet**: Categorical fields are exported as dictionary-encoded columns, preserving the encoding.
- **Excel**: Categorical fields appear as string values in cells.

If you need to round-trip data back into Pulse, use Parquet to preserve categorical encoding.

## CLI Usage

```
pulse export csv --input data.pulse --output results.csv
pulse export parquet --input data.pulse --output results.parquet
pulse export excel --input data.pulse --output results.xlsx
```

Use `pulse export predict` to validate the export configuration before writing.
