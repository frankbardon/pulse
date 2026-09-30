# Parent Groups (format 0x02)

**Audience:** anyone decoding a grouped `.pulse` file by hand, writing a
non-Go reader, or building on the group descriptor. Source of truth:
[`encoding/group.go`](https://github.com/frankbardon/pulse/blob/main/encoding/group.go),
[`group_wire.go`](https://github.com/frankbardon/pulse/blob/main/encoding/group_wire.go),
[`group_stream.go`](https://github.com/frankbardon/pulse/blob/main/encoding/group_stream.go).

A denormalised join repeats its parent block on every child row. A
**parent group** stores each distinct tuple of its member fields ONCE,
in a dictionary inside the schema block, and every row carries a
fixed-width `u32` index into it. A cohort may declare N independent
groups; groups never reference each other. A **constant group** holds
exactly one entry and no per-row index — how a column with one value
across the whole cohort is stored once. A cohort with at least one
group is written at format `0x02`; one without is `0x01`,
byte-identical to every file written before groups existed.

## Logical vs physical

| Term | Meaning |
|---|---|
| Logical schema | `Schema.Fields` — every field, original order. Field indices, records, operators, dictionaries and inspect all use it. |
| Logical row | The `0x01` row of the logical schema: each field's on-wire bytes in order (bit-packed = one whole byte), then a `ceil(field_count/8)` null bitmap when any field is nullable. |
| Physical row | What a grouped cohort stores: one `u32` index per **indexed** group (group order), then the **row fields** (fields in no group) in logical order, then a **narrowed** null bitmap over the row fields only. |
| Entry | One dictionary tuple: each member's logical on-wire bytes in member order, then a `ceil(member_count/8)` member null bitmap when any member is nullable. |

`Schema.RecordByteSize()`, `BitmapByteSize()` and `HasBitmap()` describe
the PHYSICAL row, so stride is still fixed and derivable from the schema
alone, and `RecordCountForPayload`, the record locator, parallel
segment splitting and lookup offsets are unchanged in form.
`Schema.Logical()` is the ungrouped view (same `Fields` slice).
A cohort whose every nullable field is a group member has no per-row
bitmap at all.

## Schema extension block

After the `0x01` field descriptors:

```
u64 extension_length
  u16 section_count
  per section: u16 tag, u8 flags (bit0 REQUIRED), u64 length, bytes
```

An unknown REQUIRED section is refused (`ENCODING_INVALID`); an unknown
optional one is skipped. Reserved flag bits, a duplicated tag, or a
payload not consumed exactly are refused. Section tag `1` is GROUPS
(always REQUIRED):

```
u16 group_count (>= 1)
per group:
  u8  kind          0 = indexed, 1 = constant
  u8  index_width   4 for indexed, 0 for constant
  u8  flags         reserved, 0
  u16 member_count  (>= 1)
  per member: u16 field_index (logical, strictly ascending), u8 flags (bit0 = key)
  u32 entry_width   must equal the members' widths (+ member bitmap)
  u32 entry_count   constant: exactly 1
  entry_count x entry_width bytes
```

Entries hold raw encoded values, never strings: a categorical member's
bytes are its own dictionary ID, and every member keeps its own inline
dictionary exactly as in the ungrouped cohort. A bit-packed member
(`u4`, `packed_bool`) stores its one whole on-wire byte verbatim — the
row never shares a byte between fields, so splitting a bit-packed run
across a group boundary needs no repacking.

## Decode

A reader expands each physical row into its logical row — row-field
bytes copied, each group's entry spliced in by index, null bits merged
from the narrowed bitmap and the entries — and then runs the ordinary
`0x01` decoder. A grouped cohort therefore decodes to exactly what its
ungrouped twin decodes to. An index past its dictionary is
`ENCODING_INVALID`. A physical stride of zero (every field
constant-grouped) is refused at write and read.

Shard archives, set-field widening and the shard categorical rewrite do
not accept grouped cohorts yet: they refuse with a coded error rather
than reinterpret physical bytes.
