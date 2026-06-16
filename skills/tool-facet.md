---
name: tool-facet
kind: tool
description: Return distinct values for one field in a cohort.
type: reference
applies_to: facet, mcp
---

## When to use

Diagnostic / discovery tool for the unique-value set of a single column — e.g. before authoring a `FILTER_INCLUDE` to know which categorical labels exist. For multi-field summaries with counts, null tallies, numeric stats, percentiles, histograms, or additive-contribution counts, use `pulse_facet_schema` instead.

## Input

- `path` (string, required): filesystem path to the `.pulse` file.
- `field` (string, required): field name to facet.

## Output

`descriptor.Envelope` wrapping a slice of distinct values typed per the field's schema. For categorical fields, dictionary labels surface as strings; numeric fields surface the raw values; date fields surface ISO-8601 strings. `Response.Components` populates the universal floor (`total_n`, `n_null`) for the facet operation.

## Gotchas

- Single-field, no counts. Use `pulse_facet_schema` when you need histograms, percentiles, null tallies, or multi-field rollups.
- Categorical dictionaries are truncated only when the field uses `DefaultDictionaryLimit` semantics elsewhere; facet always returns the full distinct set encountered in records.
- Unknown field → `PULSE_REQUEST_UNKNOWN_FIELD`.

## See

- `facet-design` — facet endpoint contract.
- `tool-facet-schema` — rich multi-field variant.
- `response-components` — universal floor reported.
