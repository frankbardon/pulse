# pulse cohort inspect

**Audience:** CLI users reading a `.pulse` file's schema without
running a query — the human-side counterpart of the `inspect` library
method and the `pulse_inspect` MCP tool. Defined in
[`internal/cli/cohort.go`](https://github.com/frankbardon/pulse/blob/main/internal/cli/cohort.go).

`pulse cohort inspect` reads only the file's header and schema, plus
the SPSS metadata sidecar beside it when there is one — it never reads
record data. The operation is constant-time regardless of cohort size.

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

A grouped cohort (format `0x02`, written by `import --group` or
`--elide-constants`) additionally marks each member field and reports
the physical layout plus each group's realized figures:

```
Records: 1200
Fields: 6
  line_id                        u16                  Numeric field: line_id
  order_id                       u16                  Numeric field: order_id
    stored in: group 1 (indexed, key)
  order_region                   categorical_u8       Categorical field: order_region
    dictionary: 4 entries
    stored in: group 1 (indexed)
  order_total                    f32                  Numeric field: order_total
    stored in: group 1 (indexed)
  qty                            u4                   Numeric field: qty
  tenant                         categorical_u8       Categorical field: tenant
    dictionary: 1 entries
    stored in: group 2 (constant, key)
Format: 0x02 (record stride 7 bytes physical, 11 logical)
Groups: 2
  group 1 [key: order_id]  indexed  admitted
    fields: order_id, order_region, order_total
    dictionary: 100 entries x 7 bytes = 700 bytes resident
    ratio: 12.00x (break-even 2.33x, floor 2.00x), file delta -2878 bytes
  group 2 [tenant]  constant  admitted
    fields: tenant
    dictionary: 1 entries x 1 bytes = 1 bytes resident
    ratio: 1200.00x (break-even 0.00x, floor 2.00x), file delta -1183 bytes
```

Every figure is header-only — the entry count is in the schema block
and `Records` comes from the file length. A `0x01` cohort prints none
of these lines. Field semantics:
[Format → Parent Groups](../format/parent-groups.md#inspecting-a-grouped-cohort).

## Suggested weight (SPSS cohorts)

A cohort imported from a weighted `.sav` carries the file's weighting
variable in its SPSS metadata sidecar (`cohort.pulse.spss.json`).
Inspect reports it as a suggestion — text mode ends with

```
Suggested weight: WT (spss_sidecar, kind probability; not applied)
```

and `--json` carries `"suggested_weight": {"field": "WT", "source":
"spss_sidecar", "kind": "probability"}`. It is **never applied**: name
the field as a request `weight` to use it. SPSS `WEIGHT BY` replicates
cases, which is frequency-like, so pass `"kind": "frequency"` when you
mean the SPSS semantics. No sidecar, a stale or unreadable one, or a
variable the cohort no longer carries (or cannot weight by) yields no
suggestion and no warning. `pulse_predict` and the library `Predict`
echo the same object while no weight resolves.

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

A grouped (`0x02`) cohort adds `layout` and `groups` to `data` and a
`group` marker to each member field — all three keys are omitted for a
`0x01` cohort, whose envelope is unchanged:

```json
    "layout": {"pulse_format_version": 2, "physical_record_stride": 7, "logical_record_stride": 11},
    "groups": [
      {
        "group": 0, "label": "group 1 [key: order_id]", "kind": "indexed",
        "key": ["order_id"], "members": ["order_region", "order_total"],
        "fields": ["order_id", "order_region", "order_total"],
        "entry_count": 100, "entry_width": 7, "dictionary_bytes": 700,
        "member_row_bytes": 7, "index_width": 4, "ratio": 12,
        "break_even_ratio": 2.3333333333333335, "ratio_floor": 2,
        "byte_delta": -2878, "grows_file": false, "verdict": "admitted"
      }
    ]
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
