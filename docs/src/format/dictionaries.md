# Dictionary Blocks

**Audience:** anyone decoding categorical fields, sizing a categorical
type during import, or chasing a dictionary-overflow error.

Categorical fields (`categorical_u8`, `categorical_u16`,
`categorical_u32`) store their string-to-ID mapping inline, immediately
after the field's schema entry. The dictionary is part of the schema
block, not the record data.

> **LLM agents using MCP:** the `cohort-schema-design` skill covers
> when to pick which categorical width; the `import-best-practices`
> skill covers fail-closed semantics on overflow.

## On-disk layout

From [`encoding/dictionary.go`](https://github.com/frankbardon/pulse/blob/main/encoding/dictionary.go):

```
u32 count
(u16 strlen + utf8 bytes) × count
```

Sizes are little-endian. Each entry's ID is its insertion index
(`0..count-1`); ID lookups during decode use the ID found in the
record byte(s) and resolve to the string at that index.

## Sizing the type

| Type | Max entries | Bytes per record value |
|---|---|---|
| `categorical_u8`  | 256          | 1 |
| `categorical_u16` | 65,536       | 2 |
| `categorical_u32` | 4,294,967,295 | 4 |

The import path samples the source (`--sample-rows`, default 500) to
estimate cardinality and picks the smallest width that fits. You can
also force a width by editing the schema template (`pulse import
schema-template SOURCE`).

## Overflow and unbounded errors

`AddWithLimit` enforces the per-type cap and returns
`PULSE_IMPORT_CATEGORICAL_OVERFLOW` when the source has more distinct
values than the dictionary can hold:

```json
{
  "code": "PULSE_IMPORT_CATEGORICAL_OVERFLOW",
  "message": "categorical dictionary overflow: max 256 entries",
  "details": {"max_entries": 256, "value": "the_257th_distinct_string"}
}
```

The companion code `PULSE_IMPORT_CATEGORICAL_UNBOUNDED` fires when the
import path detects an effectively unbounded categorical column (the
schema declared `categorical_u32` and the column still grew past the
caller-provided guardrails). Both errors halt the import — fail-closed,
no partial output.

Recovery options, in order of preference:

1. Re-import with a wider categorical type
   (`categorical_u8` → `categorical_u16` → `categorical_u32`).
2. Drop the categorical encoding (treat the column as a plain string
   field — but Pulse has no native variable-string type; you'd add a
   pre-import transform to bucket values).
3. Pre-filter the source to a smaller distinct set and re-import.

## Inspect behaviour

`pulse cohort inspect --json` reports each categorical field's
dictionary entry count and sample values. By default the inline list
is capped at 100 entries (`DefaultDictionaryLimit`); pass `--full-dict`
to print the full dictionary:

```bash
pulse cohort inspect data.pulse --full-dict --json
```

Both forms include a `truncated: true|false` flag and a `total_entries`
count for programmatic consumers.

## Performance notes

Dictionary reads are amortised: the reader allocates one shared byte
buffer for all string payloads, then does one `string(...)` copy per
entry. This avoids the "one allocation per entry" overhead that
naively reading length-prefixed strings would produce. The dictionary
itself is held in memory for the life of the cohort's schema parse.

For very large dictionaries, the `categorical_u32` path is still O(N)
to deserialise; if you find yourself near the 32-bit cap, you almost
certainly want a different model (a separate lookup table, or a
plain integer column with the strings stored externally).
