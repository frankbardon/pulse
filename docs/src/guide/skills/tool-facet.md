```yaml
name: tool-facet
kind: tool
description: Return distinct values for one field in a cohort.
type: reference
applies_to: facet, mcp
```

## When to use

CALL TO LIST THE DISTINCT VALUES OF ONE FIELD.
Diagnostic / discovery — before authoring a value filter, to know which categorical labels exist. For multi-field summaries with counts, null tallies, numeric stats, percentiles, histograms, or additive-contribution counts, use `pulse_facet_schema` instead.

## Input

- `path` (string, required): filesystem path to the `.pulse` file.
- `field` (string, required): field name to facet.

## Output

`{values: [...]}` (no envelope), every value a STRING. A categorical field returns its whole dictionary in dictionary order (no truncation, nulls never listed). Any other field streams every record and returns each distinct numeric value in first-seen order, formatted as a plain number: a `date` is its epoch-day count (`"20210"`), a `datetime` its epoch seconds, a `decimal128` drops trailing zeros (`"87.5"`), a `u64` past 2^53 rounds.

## Gotchas

- No counts, no nulls. Use `pulse_facet_schema` for counts, null tallies or several fields; its discrete values carry labels and counts.
- An unknown field is `SERVICE_VALIDATION` (`field "x" not found in schema`).
- A non-categorical facet reads every record, bounded by the instance `max_groups` limit on distinct values.

## See

- [`facet-design`](facet-design.md) — facet endpoint contract.
- [`tool-facet-schema`](tool-facet-schema.md) — rich multi-field variant.
- [`tool-sample`](tool-sample.md) — rows with typed values (`YYYY-MM-DD` dates).
