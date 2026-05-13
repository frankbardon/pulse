# pulse api facet

**Audience:** CLI users enumerating distinct values for a single
field — a cheap probe for "what are the regions in this cohort?"
without building a full filter.

`pulse api facet` returns the distinct values of one field in a
`.pulse` file. For categorical fields it reads the dictionary
directly (no record scan). For non-categorical fields it scans
records.

> **LLM agents using MCP:** see the `pulse_facet` MCP tool.

## Synopsis

```
pulse api facet --input PATH --field NAME [--json]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--input` | `-i` | string | (required) | Cohort `.pulse` file path |
| `--field` | `-f` | string | (required) | Field name to facet on |
| `--json`  |      | bool   | false     | Emit the standard envelope |

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
  "format_version": "1.0",
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
