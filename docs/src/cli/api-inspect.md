# pulse cohort inspect

**Audience:** CLI users reading a `.pulse` file's schema without
running a query — the human-side counterpart of the `inspect` library
method and the `pulse_inspect` MCP tool.

> **Note on naming.** This site indexes the inspect operation under
> `cli/api-inspect.md` for symmetry with the library facade
> (`Pulse.Inspect`) and the MCP tool (`pulse_inspect`). The actual
> CLI command is `pulse cohort inspect`, defined in
> [`internal/cli/cohort.go`](https://github.com/frankbardon/pulse/blob/main/internal/cli/cohort.go).

`pulse cohort inspect` reads only the file's header and schema — it
never reads record data. The operation is constant-time regardless of
cohort size.

> **LLM agents using MCP:** see the `schema-inspection` (alias
> `cohort-schema-design`) skill and the `pulse_inspect` tool.

## Synopsis

```
pulse cohort inspect PATH [--json] [--full-dict]
```

## Flags

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--json`      | bool | false | Emit the standard envelope |
| `--full-dict` | bool | false | Print every categorical dictionary entry (default truncates at 100) |

## Output (text mode)

```
Fields: 7
  order_id              u64                  Stable order identifier
  region                categorical_u8       Sales region label
    dictionary: 4 entries
  product               categorical_u16      Product SKU
    dictionary: 240 entries (truncated)
  units                 u32                  Units sold per line
  revenue               decimal128           Line revenue (precision 18, scale 2)
  sold_on               date                 Date the order shipped
  ...
```

Dictionaries with > 100 entries are flagged `(truncated)` — pass
`--full-dict` to print every entry.

## Output (`--json`)

```json
{
  "format_version": "1.0",
  "data": {
    "field_count": 7,
    "fields": [
      {
        "name": "order_id",
        "type": "u64",
        "byte_offset": 0,
        "bit_position": 0,
        "description": "Stable order identifier",
        "description_source": "schema"
      },
      {
        "name": "region",
        "type": "categorical_u8",
        "byte_offset": 8,
        "bit_position": 0,
        "description": "Sales region label",
        "description_source": "schema",
        "dictionary": {
          "total_entries": 4,
          "truncated": false,
          "entries": ["east", "west", "north", "south"]
        }
      }
    ]
  },
  "errors": [],
  "warnings": []
}
```

Fields with empty descriptions on disk get a synthesised fallback
(`"Categorical field: <name>"` / `"Numeric field: <name>"`); their
`description_source` is `"synthesized"` rather than `"schema"`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | File not found, truncated, magic-byte mismatch, or unsupported format version |

## Examples

```bash
# Human-readable inspect
pulse cohort inspect data.pulse

# Full envelope for programmatic consumers
pulse cohort inspect data.pulse --json

# Show all categorical entries
pulse cohort inspect data.pulse --full-dict --json | jq '.data.fields[] | select(.dictionary)'
```

## Related

- [Format → Header Layout](../format/header.md)
- [Format → Schema Block](../format/schema-block.md)
- [Format → Dictionary Blocks](../format/dictionaries.md)
- [Library: pulse.Inspect](../library/overview.md) — Go counterpart
- `skills/cohort-schema-design.md` — LLM-facing schema-design skill
