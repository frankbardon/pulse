```yaml
name: op-filter-null
description: Keep or drop records based on null state of a field.
kind: operator
category: FILTER
operator: FILTER_NULL
type: reference
applies_to: process, compose, predict
examples_tags: [data-quality, cohort-analysis, streaming-friendly]
```

## Use when

Keeps either the rows where a field is missing or the rows where it is present, to find gaps or set them aside.

Questions it answers:

- Who skipped the income question, so their other answers can be compared with those who answered?
- What are the averages over only the rows that have a value?

Use something else:

- `AGG_NULL_COUNT` when you only want to count the missing values, not filter on them.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[1]string` | (required) | `"is_null"` keeps null-valued rows; `"is_not_null"` keeps non-null rows. Any other value → `PROCESSING_CONFIG`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type — null state is read from the per-record null bitmap. |

## Output

Row-level predicate. Pass per the configured mode. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per [`response-components`](response-components.md) contract. `n_null_input` matches `n_out` when mode is `is_null`, and `n_in − n_out` when mode is `is_not_null` (book-keeping mirrors the predicate). Mergeable across chunks.

## Gotchas

- `Field` is required; empty → `PROCESSING_CONFIG`.
- Reads the bitmap only — the underlying type's sentinel value (e.g. `NaN`, empty `set_*`) is NOT treated as null.
- For "non-null AND in a set", put this filter (`is_not_null`) before the value filter.

## See

- `pulse_examples_search tags=[data-quality]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`cohort-schema-design`](cohort-schema-design.md)
