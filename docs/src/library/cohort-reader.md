# Reading & Building Cohorts

**Audience:** Go embedders who need the stored records of a cohort
themselves — to copy, verify, re-encode or feed them elsewhere — rather
than an aggregate computed by [`Process`](overview.md).

`Cohort.Reader` opens an index-addressed, record-by-record reader over
any cohort `Pulse.Open` returns: a single-file cohort (ungrouped format
`0x01` or grouped `0x02`), a whole shard archive, or one anchored shard
(`archive.pulse#shard.pulse`). The builder half of the round trip is
described further down.

## Read records by index

```go
c, err := p.Open(ctx, "survey.pulse")
if err != nil {
    log.Fatal(err)
}
r, err := c.Reader()
if err != nil {
    log.Fatal(err)
}
defer r.Close()

fields := r.Schema().Fields
for i := int64(0); i < r.Len(); i++ {
    row, err := r.RecordAt(i) // pulse.CohortRow, one slot per field
    if err != nil {
        log.Fatal(err)
    }
    for f, v := range row {
        fmt.Println(fields[f].Name, v)
    }
}
```

- `Schema()` is the schema the reader decodes against. `CohortRow`
  slot `i` holds `Schema().Fields[i]`, in schema LOGICAL order, so a
  grouped cohort reads exactly like its ungrouped twin.
- `Len()` is the number of addressable records. A trailing partial
  record is never addressable (the count floors, as `CountRecords`
  does).
- `RecordAt(i)` is 0-based. Every returned row is freshly allocated and
  owned by the caller.
- `Close()` releases the file handle, waits for in-flight reads and is
  idempotent.

## Exact values

`RecordAt` returns the EXACT stored value, never a `float64` echo:

| Field type | Go value |
|---|---|
| `u4`, `u8`, `u16`, `u32`, `u64` | `uint64` |
| `f32` / `f64` | `float32` / `float64` |
| `date` | `int32` epoch days |
| `datetime` | `int64` epoch seconds |
| `decimal128` | `encoding.Decimal128` |
| `categorical_*` | `string` (the dictionary label) |
| `set_*` (every rung, `set_u128` / `set_u256` included) | `[]string` labels in dictionary order; an empty non-nil slice is "no selection" |
| `packed_bool` | `bool` |
| null (any type) | `nil` |

## Shard archives and anchors

Over a whole shard archive the index is **global**: shards are
concatenated in archive order (the order `Cohort.Shards` lists them),
`Len()` is the sum of their record counts — unlike
`Cohort.RecordCount`, which counts single-file cohorts only — and
`RecordAt` locates the record's shard by binary search over the prefix
sums. Every record decodes against the archive's **canonical schema**,
the one `Cohort.Schema` returns for the archive.

An anchored shard (`archive.pulse#shard.pulse`) addresses that one
shard, again against the canonical schema, so its rows equal the
whole-archive reader's rows for that shard. A stored shard's own
header schema is structurally identical to the canonical one — same
fields, types, set rungs, offsets and group layout — but its
dictionaries (categorical, set and group entries) may be a **prefix**
of the canonical ones: the seed shard is stored untouched while later
shards carry the union. The values decode identically either way;
the visible difference is that the anchored reader's `Schema()` can
carry longer dictionaries than the anchored `Cohort.Schema()`.

At open the reader checks every shard it addresses with the rules
`pulse shard verify` applies. A shard that is not decode-compatible is
refused with the same code — `PULSE_SHARD_SCHEMA_MISMATCH` or
`PULSE_SHARD_DICT_DIVERGENCE` — never decoded wrongly.

## Errors

| Situation | Code |
|---|---|
| index outside `[0, Len())` | `SERVICE_VALIDATION` |
| `RecordAt` after `Close` | `SERVICE_RESOURCE` |
| a record cut short | `ENCODING_INVALID` |
| a shard not decode-compatible with the canonical schema | `PULSE_SHARD_SCHEMA_MISMATCH` / `PULSE_SHARD_DICT_DIVERGENCE` |

## Concurrency

`RecordAt` is safe for concurrent use from many goroutines, across
shard boundaries included: every call reads through its own
section reader, so no seek cursor is shared. On-disk files are read
with positional reads; other `afero` files are serialised behind a
mutex.

## Building a single-file cohort

`Pulse.NewCohortBuilder` writes a new cohort from rows appended one at
a time — the write half of the round trip. The schema is the contract,
exactly as an explicit-schema import's is.

```go
schema := encoding.Schema{Fields: []encoding.Field{
    {Name: "order", Type: encoding.FieldTypeU32, Description: "Order number."},
    {Name: "cust", Type: encoding.FieldTypeU32, Description: "Customer number."},
    {Name: "tier", Type: encoding.FieldTypeCategoricalU8, Description: "Customer tier."},
    {Name: "amount", Type: encoding.FieldTypeF64, Nullable: true, Description: "Order value."},
}}
b, err := p.NewCohortBuilder(ctx, "orders.pulse", schema, pulse.CohortBuilderOptions{
    Groups:         []pio.GroupDecl{{Key: []string{"cust"}, Members: []string{"tier"}}}, // optional
    ElideConstants: true,                                                                // optional
})
if err != nil {
    log.Fatal(err)
}
for _, o := range orders {
    if err := b.Append(pulse.CohortRow{o.ID, o.Cust, o.Tier, o.Amount}); err != nil {
        log.Println(err) // PULSE_IMPORT_ROW_ERROR: the row is skipped, the builder stays usable
    }
}
res, err := b.Close() // or b.Abort() to discard
```

- **Rows** use the same Go types `RecordAt` returns (table above), so
  a row read from one cohort can be appended to another unchanged. No
  coercion is applied. A row that does not fit — wrong arity or Go
  type, `nil` in a non-nullable field, a value past its type, a new
  label past the dictionary's rung — is `PULSE_IMPORT_ROW_ERROR` with
  `row` (the 1-based `Append` call), `field` and `reason` details; it
  leaves no trace, not even a dictionary entry.
- **Schema.** Field order, names, types, nullability, descriptions,
  decimal precision / scale and optional pre-seeded dictionaries are
  honoured. Layout (`ByteOffset`, `BitPosition`, `CsvColumnIdx`) is
  recomputed, and the caller's schema is never mutated. Dictionaries
  grow from the pre-seeded entries in first-seen order; the declared
  rung is a ceiling, never auto-promoted. Parent groups go in `Groups`,
  never in the schema.
- **Groups and elision.** `Groups` and `ElideConstants` behave exactly
  as `pulse import --group` / `--elide-constants`: the same
  `PULSE_GROUP_*` declaration checks (at `NewCohortBuilder`), the same
  viability gate (a group no wider than its index is dropped with
  `PULSE_GROUP_TOO_NARROW`; one below `RatioFloor` rows per tuple is
  still written, with `PULSE_DEDUP_LOW_RATIO`) and the same encoder. A
  key that does not determine its members fails `Close` with
  `PULSE_GROUP_MEMBER_NOT_CONSTANT` (`source_row` = the `Append` call).
  The header is `0x02` only when a group is written — a build whose
  every group was dropped is a plain `0x01` file.
- **`Strict`** turns `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` and the
  gate's findings into errors; none leaves anything on disk.
- **Close** writes atomically: rows spool to a temp file beside the
  target (the preamble's dictionaries are only final after the last
  row), then preamble + rows go to a second temp file that is fsynced
  and renamed over the target. A failed `Close` or an `Abort` leaves no
  target and no temp files. `CohortBuildResult` carries `Records`, the
  `FormatVersion` written, the final `Schema`, `Warnings`, the
  per-group `Groups` verdicts and `ElidedConstants`.
- **Overwrite.** An existing target is refused unless
  `Overwrite: true`; replacing a cohort reports the sidecars it
  invalidated (`InvalidatedSidecars`), as `Pulse.Dedup` does.

**Parity with import.** A builder cohort with schema S is
byte-identical to the same rows imported with explicit schema S and the
same `Groups` / `ElideConstants` — both run one dictionary-assignment,
row-encoding, group-gate and encoder path. Shared checks are the
description length (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`) and the row
conversion; the builder additionally refuses a malformed schema (no
fields, empty or duplicate names, unknown type, bad decimal precision)
with `SERVICE_VALIDATION`, which import does not check.

## Building a shard archive

Set `Shards` and the same builder writes a **new shard archive**, split
automatically:

```go
b, err := p.NewCohortBuilder(ctx, "orders.pulse", schema, pulse.CohortBuilderOptions{
    Shards: &pulse.ShardSplit{MaxRecords: 1_000_000},
    Groups: []pio.GroupDecl{{Key: []string{"cust"}, Members: []string{"tier"}}}, // optional
})
// ... Append exactly as above ...
res, err := b.Close() // res.Shards: part-00001.pulse, part-00002.pulse, ...
```

- **Split.** Rows are cut, in append order, into consecutive shards of
  `MaxRecords` rows named `part-00001.pulse`, `part-00002.pulse`, …;
  the last shard is partial, and an empty build is one empty shard.
  The archive reads back through `CohortReader` as the appended rows,
  in order. `MaxRecords` must be positive (`SERVICE_VALIDATION`), and a
  build holds at most 65,535 shards — the row that would open one more
  is refused with `SERVICE_VALIDATION` (`reason: shard_limit`).
- **One layout.** `Groups` and `ElideConstants` are decided **once over
  all rows**, then applied to every shard: each shard carries the same
  schema, dictionaries and group layout. A group the gate drops is
  dropped in every shard, and a field constant within each shard but
  not across the build is not elided.
- **All or nothing.** `Close` stages the shard files in a directory
  beside the target and publishes them with **one**
  `Pulse.CreateShardArchive` call, which writes the archive atomically.
  The staging directory is removed whatever the outcome, so a failed
  `Close` leaves no archive, no spool and no shard file — and an
  existing archive being replaced (`Overwrite: true`) stays as it was.
- **Result.** `CohortBuildResult.Shards` lists the shard entries in
  archive order. `Warnings` ends with any warnings `CreateShardArchive`
  raised (`PULSE_SHARD_*`), with their codes and details unchanged.
