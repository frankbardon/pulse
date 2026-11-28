# Dictionary Blocks

**Audience:** anyone decoding categorical or set fields, sizing one of
those types during import, or chasing a dictionary-overflow error.

Dictionary-bearing fields — the three `categorical_*` types and all six
`set_*` rungs (`FieldType.HasDictionary()`) — store their string-to-ID
mapping inline, immediately after the field's schema entry. The
dictionary is part of the schema block, not the record data, and the
BLOCK is byte-identical for both families; only what the record stores
against it differs.

> **LLM agents using MCP:** the `cohort-schema-design` skill covers
> when to pick which categorical width; the `tool-import`
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

Inference samples the source (`--sample-rows`, default 500), picks the
smallest width that fits and promotes it if later rows outgrow it. To
force a width, edit the schema template (`pulse import schema-template
SOURCE`) and import with `--schema`.

## Set fields share the block, not the capacity

A `categorical_*` record value is one dictionary **ID**; a `set_*`
record value is a fixed-width **bitmask** where bit `i` means entry `i`
is selected. So a set's dictionary capacity is its mask width, not an
ID range (`FieldType.MaxSetEntries()`):

| Type | Max entries | Bytes per record value |
|---|---|---|
| `set_u8`   | 8   | 1 |
| `set_u16`  | 16  | 2 |
| `set_u32`  | 32  | 4 |
| `set_u64`  | 64  | 8 |
| `set_u128` | 128 | 16 |
| `set_u256` | 256 | 32 |

`set_u256` is a hard ceiling — there is deliberately no wider rung, so
a 257-option column has no set type and stays as its constituent
columns. An inferred set rung a later cell outgrows promotes up this
ladder, exactly as a categorical rung does (below); a declared rung, or
`set_u256`, overflows as `PULSE_IMPORT_SET_OVERFLOW` (not the
categorical code below). Inference picks the smallest rung that fits,
so a dictionary can be narrower than its rung's capacity — a 206-option
`set_u256` wastes 50 bit slots, accepted knowingly over a
variable-width set type.

## Overflow and unbounded errors

What happens when a column outgrows its dictionary depends on where its
type came from:

- **Inferred** (`import <format>` without `--schema`, `import auto`,
  `convert`): the rung promotes `categorical_u8` → `u16` → `u32`
  (a set: `set_u8` → … → `set_u256`) and warns
  `PULSE_IMPORT_WIDTH_PROMOTED`
  ([Width promotion](../cli/flags.md#width-promotion-inferred-imports-and-converts)).
  Only a dictionary past `categorical_u32` refuses the import; a set
  past `set_u256` drops its row (`PULSE_IMPORT_ROW_ERROR`, reason
  `PULSE_IMPORT_SET_OVERFLOW`).
- **Declared** (`--schema`, an SPSS schema): a value past the cap is
  `PULSE_IMPORT_CATEGORICAL_OVERFLOW`, and its row is skipped as a
  `PULSE_IMPORT_ROW_ERROR`. The good rows are written with a
  `PULSE_IMPORT_ROWS_DROPPED` warning; `--strict` makes it fatal and
  writes nothing ([`--strict`](../cli/flags.md#--strict-import)). Fix it
  by declaring a wider rung.
- **All sampled values unique:** inference refuses the column with
  `PULSE_IMPORT_CATEGORICAL_UNBOUNDED` (an ID or free-text column, not a
  category). Pulse has no variable-width string type: drop or bucket
  the column before import, or raise `--sample-rows` if its cardinality
  really is bounded.

## Inspect behaviour

`pulse cohort inspect --json` reports each categorical and set field's
dictionary entry count and sample values. By default the inline list
is capped at 100 entries (`DefaultDictionaryLimit`); pass `--full-dict`
to print the full dictionary:

```bash
pulse cohort inspect data.pulse --full-dict --json
```

Both forms include a `truncated: true|false` flag and a `total_entries`
count for programmatic consumers.

## Performance notes

The reader reads every label into one reused scratch buffer and copies
it once into a string, so decoding a dictionary costs one allocation per
entry and no per-entry read buffer. A forged count over a short tail
fails `ENCODING_INVALID` after allocating only for the bytes present.

For very large dictionaries, the `categorical_u32` path is still O(N)
to deserialise; if you find yourself near the 32-bit cap, you almost
certainly want a different model (a separate lookup table, or a
plain integer column with the strings stored externally).
