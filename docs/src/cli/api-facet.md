# pulse api facet

**Audience:** CLI users enumerating distinct values for a single
field — a cheap probe for "what are the regions in this cohort?"
without building a full filter — or, in rich mode, a multi-field
summary of counts, null tallies, percentiles, histograms, and additive
contribution counts.

`pulse api facet` has two modes:

- **Simple mode** (`--input PATH --field NAME`) returns the distinct
  values of one field. Categorical fields read the dictionary
  directly (no record scan); non-categorical fields scan records.
- **Rich mode** (any of `--request`, multiple `--field`, `--top-k`,
  `--percentile`, `--histogram`, `--additive`, `--labels`) returns a
  `FacetResult` covering every named field — counts, null tallies,
  optional percentiles and histograms over numerics, and optional
  additive contribution counts. Prefer rich mode over repeated
  simple calls when summarising more than one field.

> **LLM agents using MCP:** see the `pulse_facet` MCP tool for simple
> mode and `pulse_facet_schema` for rich mode.

## Synopsis

```
pulse api facet --input PATH --field NAME [--json]
                [--request FILE]
                [--field NAME ...] [--top-k N]
                [--percentile P ...] [--histogram]
                [--histogram-bins N] [--histogram-min X] [--histogram-max X]
                [--additive FIELD ...] [--labels FIELD=TABLE[:replace|augment]]
                [--echo-request]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--input`          | `-i` | string | (required for simple mode) | Cohort `.pulse` file path |
| `--field`          | `-f` | string | (required for simple mode) | Field name to facet on; repeat for rich mode |
| `--request`        | `-r` | string | (none) | Full `FacetRequest` JSON file (overrides individual flags) |
| `--top-k`          |      | int    | 0      | Cap discrete values per field (rich mode) |
| `--percentile`     |      | float  | (none) | Numeric percentile in `(0, 1)`; repeatable (rich mode) |
| `--histogram`      |      | bool   | false  | Include numeric histograms (rich mode) |
| `--histogram-bins` |      | int    | 20     | Histogram bin count |
| `--histogram-min`  |      | float  | (none) | Histogram lower bound (required with `--histogram`) |
| `--histogram-max`  |      | float  | (none) | Histogram upper bound (required with `--histogram`) |
| `--additive`       |      | string | (none) | Compute additive contribution counts for this field; repeatable |
| `--labels`         |      | string | (none) | Categorical label binding: `field=table[:replace|augment]`. Repeatable |
| `--json`           |      | bool   | false  | Emit the standard envelope |
| `--echo-request`   |      | bool   | false  | Include the resolved `FacetRequest` on `envelope.request` |

Mode dispatch: simple mode runs when exactly one `--field` is passed
without any rich-mode flag. Any other combination switches to rich
mode and calls `FacetSchema`.

## Output (text mode)

One value per line:

```
east
north
south
west
```

## Output (`--json`)

```json
{
  "format_version": "1.1",
  "data": ["east", "north", "south", "west"],
  "errors": [],
  "warnings": []
}
```

## Performance notes

| Field type | Behaviour |
|---|---|
| `categorical_u8` / `_u16` / `_u32` | Read directly from the schema's inline dictionary; O(distinct values), no record scan |
| Non-categorical | Full scan; values collected into a set, then returned sorted |

For columns with very high cardinality on the non-categorical path,
expect memory proportional to distinct value count.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | File not found, field name not found, or unsupported version |

## Examples

```bash
# Read categorical dictionary
pulse api facet --input sales.pulse --field region

# JSON envelope
pulse api facet --input sales.pulse --field region --json

# Pipe into another command
for r in $(pulse api facet --input sales.pulse --field region); do
    echo "Region: $r"
done
```

## Related

- [`pulse api sample`](api-sample.md) — raw rows preview
- [Format: Dictionary Blocks](../format/dictionaries.md) — how
  categorical dictionaries are encoded
- [Library: pulse.Facet](../library/overview.md)
