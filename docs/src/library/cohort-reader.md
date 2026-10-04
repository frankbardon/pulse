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

*To be documented with the cohort builder.*

## Building a shard archive

*To be documented with the shard-archive builder.*
