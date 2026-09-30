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

A grouped cohort decodes to exactly what its ungrouped twin decodes to;
there are two routes to that, chosen by the decoder:

- **Reuse / buffered scans** (the streaming and buffered `Process`
  paths, parallel decode, shard iteration) read the physical row as it
  is and write each field at its LOGICAL position — row fields from
  their bytes, group members from the group's dictionary entry. A group
  whose index equals the index the record already holds is not touched
  at all, and a changed index copies the members from a per-entry cache
  of decoded values (derived at read time, never stored, bounded per
  reader). Per-row cost therefore tracks the row fields plus the groups
  that CHANGED, not the width of the parent block; a projection that
  retains no member of a group never decodes it.
- **Whole-row consumers** (the map decoder, point lookup, export, the
  SPSS writer, profile run-continuation) read the logical stream: each
  physical row expanded into its logical row — row-field bytes copied,
  each group's entry spliced in by index, null bits merged from the
  narrowed bitmap and the entries — and then the ordinary `0x01`
  decoder runs over it.

An index past its dictionary is `ENCODING_INVALID` on both routes. A
physical stride of zero (every field constant-grouped) is refused at
write and read.

## Global-constant elision

`pulse import <format> --elide-constants` (library:
`io.ImportJob.ElideConstants`, default off) folds every field that holds
one value on every imported row into a single constant group. Constancy
is decided over the FULL row pass (`encoding.ConstantDetector`), never
the bounded inference sample — a column constant across the first 500
rows may vary later. Two cells are the same value iff their on-wire
bytes and null bits match: a column null on every row is a null
constant; a value plus some nulls is not constant. The plan
(`encoding.PlanConstantElision`) elides nothing below two rows, nothing
when the schema-block growth would exceed the per-row saving (the file
is then byte-identical `0x01`), and keeps the lowest-index field in the
row when every field is constant. The elided fields are reported in
`ImportReport.ElidedConstants`.

## Declaring groups at import

`pulse import <format> --group KEY[,KEY...]:MEMBER[,MEMBER...]`
(repeatable; library: `io.ImportJob.Groups []io.GroupDecl{Key, Members}`)
declares one indexed group per flag. The group's members are the key
fields plus the fields they determine; the key fields carry the member
KEY flag in the descriptor. A declaration without a colon is a tuple
group whose every member is key.

- **Key semantics.** Non-key members must be functionally determined by
  the key: two rows with the same key tuple must agree on every other
  member, byte for byte and null bit for null bit. The encoder checks
  every row and fails with `PULSE_GROUP_MEMBER_NOT_CONSTANT` (details:
  `group`, `group_label`, `field`, `row` — the 0-based record — and
  `source_row` — the 1-based source data row) rather than store a second
  entry for the same key.
- **Names.** The format has no group-name slot. Errors and reports name a
  group by its 1-based declaration position and its key
  (`group 2 [key: prod_id]`), or its members when it has no key.
- **When it is checked.** Field names are resolved against the import's
  final schema — inferred, authoritative (`SchemaAwareReader`: SPSS,
  Arrow, Parquet) or explicit alike — before the row pass:
  `PULSE_GROUP_FIELD_UNKNOWN`, `PULSE_GROUP_FIELD_CONFLICT` (details
  `group_labels` names both), `PULSE_GROUP_DECLARATION_INVALID`.
- **One pass.** The source is read once. Each group's dictionary is
  built in a single pass over the imported rows: a row's tuple is looked
  up in the group's dictionary, or appended. `u32` exhaustion is
  `PULSE_GROUP_ENTRIES_EXHAUSTED`, never a wraparound.
- **Composition.** Declared groups come first in the descriptor, in
  declaration order; with `--elide-constants` the constant group follows
  them, and declared members are never elided.
- **Report.** `ImportReport.Groups` lists each declared group's label,
  fields, verdict, `entry_count`, `entry_width`, `member_row_bytes`,
  `index_width`, `dictionary_bytes`, `ratio`, `break_even_ratio`,
  `ratio_floor` and `byte_delta` — see the viability gate below.
- **Zero groups** write the `0x01` cohort byte for byte.

## Viability gate

A badly chosen group does not fail — it quietly makes the file bigger
(every row still pays the 4-byte index, and the dictionary holds one
entry per distinct tuple) and holds its whole dictionary in memory
whenever the cohort is open. Each declared group is therefore judged on
its own numbers (`encoding.DedupGate`, shared by import and
retro-dedup); one group can be admitted while another on the same
import is not.

| Check | When | Outcome |
|---|---|---|
| Width floor: members' bytes per row ≤ the 4-byte index | before the row pass | group **dropped** (members stay in the row), `PULSE_GROUP_TOO_NARROW` with both widths |
| Ratio floor: rows ÷ distinct tuples below the floor (default 2) | after the dictionary is built | group **written**, `PULSE_DEDUP_LOW_RATIO` |
| Grows the file: deduped bytes ≥ undeduped bytes, at any floor | after the dictionary is built | group **written**, `PULSE_DEDUP_LOW_RATIO` with `grows_file: true` |

Per group, `deduped = rows × 4 + entry_count × entry_width + (13 + 3 ×
members)` and `undeduped = rows × member_row_bytes`; `byte_delta` is
their difference (negative is a saving) and `dictionary_bytes =
entry_count × entry_width` is the resident cost. The warning carries
those, the ratio, the floor and the break-even ratio
(`entry_width ÷ (member_row_bytes − 4)`).

Ratio alone never refuses a group: a deliberately deduped low-ratio
group is a legitimate choice. The floor is a judgement — at 2, each
stored tuple is shared by two rows on average and a wide parent block
at least halves; below it most of the parent data becomes resident for
a shrinking saving — and it is overridable with `--dedup-ratio-floor`
(`ImportJob.DedupRatioFloor`; a floor of 1 leaves only the grows-the-file
check). `--strict` (`ImportJob.StrictDedup`) turns either finding into
a fatal error carrying the same code, and nothing is written. Constant
groups from `--elide-constants` are not gated.

Shard archives, set-field widening and the shard categorical rewrite do
not accept grouped cohorts yet: they refuse with a coded error rather
than reinterpret physical bytes.
