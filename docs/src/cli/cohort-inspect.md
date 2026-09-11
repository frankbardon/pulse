# pulse cohort inspect

**Audience:** CLI users reading a `.pulse` file's schema without
running a query — the human-side counterpart of the `inspect` library
method and the `pulse_inspect` MCP tool. Defined in
[`internal/cli/cohort.go`](https://github.com/frankbardon/pulse/blob/main/internal/cli/cohort.go).

`pulse cohort inspect` reads only the file's header and schema — it
never reads record data. The operation is constant-time regardless of
cohort size.

> **LLM agents using MCP:** see the `cohort-schema-design` skill and
> the `pulse_inspect` tool.

## Synopsis

```
pulse cohort inspect PATH [--json] [--full-dict]
```

`PATH` is a single-file cohort, a shard archive, or the anchor form
`archive.pulse#shard.pulse`, which inspects one shard as a one-shard
cohort. Every mode resolves the anchor — text, `--json` and
`--full-dict` alike.

## Flags

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--json`      | bool | false | Emit the standard envelope |
| `--full-dict` | bool | false | Print every categorical dictionary entry (default truncates at 100) |

## Output (text mode)

Every example below is verbatim CLI output.

```
Records: 1200
Fields: 3
  order_id                       u32                  Numeric field: order_id
  region                         categorical_u8       Categorical field: region
    dictionary: 4 entries
  units                          u4                   Numeric field: units
```

`Records` is derived from the file length (`payload_bytes /
record_stride`), never by reading a record, so it stays constant-time.
Dictionaries with > 100 entries are flagged `(truncated)` — pass
`--full-dict` to print every entry.

A shard archive additionally prints the per-shard breakdown under the
aggregate:

```
Records: 200
Shards: 2
  s1.pulse                       120 records
  s2.pulse                       80 records
Fields: 3
  respondent                     u32                  Numeric field: respondent
  region                         categorical_u8       Categorical field: region
    dictionary: 4 entries
  score                          u8                   Numeric field: score
```

An anchor reports that shard's own count, not the archive aggregate —
`pulse cohort inspect arch.pulse#s2.pulse` opens on `Records: 80`.
Single-file cohorts print no `Shards:` line at all.

## Output (`--json`)

```json
{
  "format_version": "1.1",
  "data": {
    "field_count": 3,
    "fields": [
      {
        "name": "order_id",
        "type": "u32",
        "byte_offset": 0,
        "bit_position": 0,
        "description": "Numeric field: order_id",
        "description_source": "synthesized",
        "categorical": false
      },
      {
        "name": "region",
        "type": "categorical_u8",
        "byte_offset": 4,
        "bit_position": 0,
        "description": "Categorical field: region",
        "description_source": "synthesized",
        "categorical": true,
        "dictionary": {
          "total_entries": 4,
          "truncated": false,
          "values": ["east", "west", "north", "south"]
        }
      },
      {
        "name": "units",
        "type": "u4",
        "byte_offset": 5,
        "bit_position": 0,
        "description": "Numeric field: units",
        "description_source": "synthesized",
        "categorical": false
      }
    ],
    "shards": [],
    "record_count": 1200
  },
  "errors": [],
  "warnings": []
}
```

`record_count` and `shards` are always present. `shards` is `[]` (never
`null`) for a single-file cohort and for an anchor; for an archive it
carries one entry per shard in insertion order, with `record_count` the
cumulative sum:

```json
  "data": {
    "field_count": 3,
    "fields": ["..."],
    "shards": [
      {"filename": "s1.pulse", "record_count": 120},
      {"filename": "s2.pulse", "record_count": 80}
    ],
    "record_count": 200
  }
```

A `record_count` of `0` means an empty cohort. When the payload length
is not a whole multiple of the record stride — a truncated tail — the
count is the FLOOR and the envelope says so:

```json
  "record_count": 120,
  "warnings": [
    {
      "code": "ENCODING_INVALID",
      "message": "cohort payload length is not a whole multiple of the record stride; record_count is the floor",
      "details": {"record_stride": 6, "trailing_bytes": 3}
    }
  ]
```

`pulse.CountRecords` floors the same bytes to the same number and
raises no warning: it has no warning channel, and a half-written
trailing record must not stop a cohort that still processes from
reporting its whole-record count. `inspect` is the arm that tells you.

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

# Record count alone
pulse cohort inspect data.pulse --json | jq '.data.record_count'

# One shard of an archive, as a one-shard cohort
pulse cohort inspect 'archive.pulse#20190101.pulse'
```

## Related

- [Format → Header Layout](../format/header.md)
- [Format → Schema Block](../format/schema-block.md)
- [Format → Dictionary Blocks](../format/dictionaries.md)
- [Library: pulse.Inspect](../library/overview.md) — Go counterpart
- `skills/cohort-schema-design.md` — LLM-facing schema-design skill
