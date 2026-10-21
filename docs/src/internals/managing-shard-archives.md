# Managing a Shard Archive

**Audience:** Pulse internals contributors and embedders who manage a
multi-shard `.pulse` archive — a Zip64 store-only cohort that fans
out across N standalone `.pulse` shards under union semantics.

A shard archive is byte-distinct from a single-file `.pulse` cohort:
the magic-byte dispatch at `pulse.Open` looks at the first four bytes
and chooses single-file vs archive based on `PULSE` vs `PK\x03\x04`.
Read-side commands (`pulse api process`, `pulse api compose`,
`pulse api sample`, `pulse api facet`, `pulse inspect`, `pulse predict`)
accept either transparently.

For the union semantics, per-shard cohesion, and the memory multiplier
of read paths see the [Cohort schema design
skill](https://github.com/frankbardon/pulse/blob/main/internal/skills/cohort-schema-design.md)
(Sharded cohorts).

## 1. Create the archive

The first include seeds the canonical schema; remaining includes are
validated against it via structural cohesion + the dict prefix rule.
Cohesion compares the type-derived layout (names, types, field widths,
bit positions), never a shard's stored byte offsets, so cohorts written
with all-zero offsets or a pre-canonical bit-packed layout join without
a re-import. Create runs in two passes: a plan read from each include's
header and schema (an include with parent groups is read whole, one at a
time — its group dictionaries come from its rows), then each include
streamed once into the staged archive, so memory is one include plus
the plan, never the sum. An archive holds at most 65535 shards; more is
`PULSE_SHARD_COUNT_LIMIT`, refused before anything is written.
Atomic temp + rename:

```bash
pulse shard create q1_2019.pulse \
    --include 20190101.pulse \
    --include 20190108.pulse \
    --include 20190115.pulse
```

## 2. Append a shard

Validates cohesion + dict prefix, grows the canonical dict if needed,
then streams a new archive into a staged file beside the original — a
refreshed `_schema.pulse`, every stored shard copied raw, the new shard
last — and renames it over the original (see
[The rewrite contract](#the-rewrite-contract)):

```bash
pulse shard add q1_2019.pulse 20190122.pulse
```

Go embedders building shards from rows skip the intermediate files:
`Pulse.NewCohortBuilder` with `ShardSplit` writes a new archive through
one `CreateShardArchive` call, and an anchored target
(`q1_2019.pulse#20190122.pulse`) appends one shard through one
`AddShard` call, with the same cohesion checks and mandatory warnings.
See [Reading & Building Cohorts](../library/cohort-reader.md).

### Vector fields

A `vec_f32` / `vec_f64` field's dim is not in its type byte, so cohesion
checks it explicitly: a shard whose vector has a different dim — or the
other element type — is refused with `PULSE_SHARD_SCHEMA_MISMATCH`
(`details.dimension: vector_dim`), even when the vector is the LAST field
and no later offset moves. Element labels, kind and scale range may
diverge: the shard is accepted, the canonical copy wins, and each
divergence is a `PULSE_SHARD_DESCRIPTION_DIVERGENCE` warning naming
`vector_labels`, `vector_kind` or `vector_scale`.

### Nullability

Every shard decodes under the archive's canonical null flags, so
per-field nullability is part of cohesion and is never widened. A shard
that declares nullable a field the archive declares non-nullable is
refused with `PULSE_SHARD_SCHEMA_MISMATCH` (`details.dimension:
nullable`): the canonical decode would ignore the field's null bit and a
stored null would read back as a value. The reverse — a non-nullable
shard field under a nullable archive field — is admitted as-is only when
both null bitmaps have the same width (equal strides) and the field is
not a parent-group member; every Pulse writer leaves a non-nullable
field's null bit 0, so each row reads non-null. Otherwise it is refused
the same way. `shard add`, `shard create`, `shard verify`, the anchored
`NewCohortBuilder` pre-check and the archive record reader all apply the
one rule (`ValidateStructuralCohesion`).

### Set-width auto-widen

**Two** conditions trigger it, and they compose:

1. **The dictionary union outgrows the bitmask.** The field is promoted
   to the narrowest rung that holds the union.
2. **The arriving shard declares a different rung.** A re-import of a
   new period infers the rung *its own* data needs, so a `set_u128`
   shard legitimately meets a `set_u64` archive with no dictionary
   overflow in sight.

Either way the add does **not** fail. The field is promoted across the
canonical `_schema.pulse` *and* every shard payload already in the
archive, and the merge proceeds. The alternative, refusing, forces a
full re-import to reach a layout a mechanical re-stride already
produces.

The promotion is **only ever wider**. A shard arriving at a *narrower*
rung never narrows the archive — narrowing would drop every selection
above the target's ceiling, and it would do so silently, with the record
still decoding to a smaller and entirely plausible selection. The
arriving shard is promoted to the archive's rung instead, and it is the
only payload rewritten. `SetWidening.From != To` is the test for "the
archive moved"; the warning's `details.archive_widened` says the same.

Only the rung dimension relaxes, and it relaxes by **order**, not by
loosening a validator: `encoding.PlanSetWidening` now runs *before*
`encoding.ValidateStructuralCohesion`, reconciles the rungs, and
cohesion then runs over the reconciled schemas as strictly as ever. That
matters because `pulse shard verify` calls the same validator, and an
archive whose shards genuinely carry different strides is a corruption
signature that must stay fatal there.

**`pulse shard create` widens on the same conditions.** Refusing at seed
while widening at add was an asymmetry with no defence: the same two
files produced an archive when fed one after the other and an error when
fed together. `Pulse.CreateShardArchive` therefore returns a
`*CreateShardArchiveResult`, not a bare `error`, for the same reason
`AddShard` returns a result — so the mandatory warning has nowhere to be
dropped.

The rewrite is atomic ([The rewrite contract](#the-rewrite-contract)),
so a failure or crash mid-rewrite leaves the original archive
byte-identical and openable at its old rung. It is also **never silent**: every widen emits a
mandatory `PULSE_SHARD_SET_WIDENED` warning naming the field, the old
rung, the new rung and the number of shards rewritten, on the `--json`
envelope's `warnings` array and on the text output.

```
Added 20190122.pulse to q1_2019.pulse (now 4 shard(s))
  WARN   [PULSE_SHARD_SET_WIDENED] set field "media" was widened from set_u64 to set_u128 ...
```

A union above `set_u256` has nowhere left to widen to and still fails
with `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. Categorical widths are
untouched — a categorical's width is fixed at folder creation.

`pulse shard verify` reports per-set-field width headroom so a widen is
foreseeable before you pay for it.

### Grouped (`0x02`) shards

Shards may be deduped cohorts (`pulse import --group`, `pulse dedup`).
Every shard is decoded with the canonical schema, so an archive has ONE
parent-group layout, and `create` / `add` share one merge rule
(`internal/service/shard_groups.go`, `mergeShard`):

1. The arriving shard is flattened to its ungrouped twin and reconciled
   exactly as an ungrouped shard — set-rung plan, strict field cohesion,
   categorical union and remap (null placeholders keep their bytes, since
   they are part of a group entry's identity).
2. The archive's layout wins. An ungrouped archive stores a grouped
   arrival flattened; a grouped archive re-encodes an arrival with other
   groups (or none) into its own.
3. Group dictionaries union-merge canonical-first: the encoder is seeded
   with the canonical dictionaries, so stored shards are untouched and
   only the arrival is renumbered. A union past the u32 index space is
   `PULSE_SHARD_DICT_WIDTH_OVERFLOW`.
4. A constant group the arrival disagrees with is promoted to an indexed
   group across every shard (a whole-archive rewrite); a declared key the
   arrival violates is refused with `PULSE_GROUP_MEMBER_NOT_CONSTANT`.
5. Strict cohesion runs over the reconciled grouped schemas.

Layout changes emit the mandatory `PULSE_SHARD_GROUPS_REWRITTEN`
warning (`details.reason` = `incoming_flattened` / `incoming_regrouped` /
`constant_promoted`, `details.archive_rewritten`) and a `regrouped`
entry in `--json` `data`. `pulse shard verify` refuses a shard whose
group layout differs from the canonical one, applies the prefix rule to
group dictionaries, and reports `group_index_headroom`.

## 3. List shards

Reads `_schema.pulse` + central directory, prints basenames + per-shard
record counts:

```bash
pulse shard list q1_2019.pulse
```

## 4. Verify

Re-validates every shard's header + cohesion against the canonical
schema. Useful after manual archive surgery or when a build pipeline
appends shards from multiple producers:

```bash
pulse shard verify q1_2019.pulse
```

## 5. Compact

Reclaims orphan bytes (e.g. after `pulse shard remove`) and refreshes
canonical metadata (`aggregate_record_count`, `shard_count`). Compacting
an archive whose metadata is already current reproduces it byte for
byte:

```bash
pulse shard compact q1_2019.pulse
```

## The rewrite contract

`create`, `add`, `remove` and `compact` (and `cohort filter` writing an
archive) never edit an archive in place, and never build it in memory:

- **Streamed into a stage.** The new archive is written to
  `<archive>.pulse.tmp-staged` in the same directory: the refreshed
  `_schema.pulse` first (its aggregate summed from each shard's own
  header, never a decode), then each stored shard copied **raw** —
  `zip.Writer.CreateRaw` from a section of the old file, source CRC and
  sizes verbatim, no decode — then the new shard. Memory is the arriving
  shard plus fixed copy buffers, whatever the archive's size; I/O is the
  archive's size. A deflated entry (a third-party zip tool's) is inflated
  and re-stored instead.
- **When the archive itself moves** (an archive-level set widen, a
  constant-group promotion), each stored shard is read one at a time and
  re-encoded on its way into the stage; a promotion first runs a
  discovery pass over every shard to fix the final group dictionaries.
  The mandatory warning's `shards_rewritten` / `records_rewritten` are
  exact.
- **Published by one rename.** The stage is flushed and fsynced, renamed
  over the archive, and the directory fsynced. Until that rename the
  original is byte-identical; a crash before it leaves the original
  openable and the stage behind, and the next mutation of that archive
  sweeps every `<archive>.pulse.tmp-*` (other archives' stages are left
  alone).
- **Not verified on the way through.** A raw copy does not re-check a
  stored shard's CRC; `pulse shard verify` is the diagnostic arm.

In-place tail append is not done: it would give up the "original
byte-identical until the rename" guarantee the widen path relies on.

### Point-lookup indexes

An archive with point-lookup indexes (`pulse index build archive.pulse`)
keeps them in step through `add` and `remove`
(`internal/service/index_upkeep.go`):

- **Add builds before it publishes.** After the stage is finished but
  before it is renamed, the new shard is scanned once from the stage and
  its sidecar for every archive index is written to
  `<sidecar>.pulse.tmp-staged`, with the updated manifest beside it. If
  any of that fails, the add returns the error and the archive, every
  sidecar and the manifest are left byte-identical. Otherwise the
  archive is renamed, then each sidecar, then the manifest.
- **Remove touches only the removed shard.** The manifest without the
  shard's record is staged first; after the archive rename it is
  published and that shard's sidecars are deleted.
- Both emit the mandatory `PULSE_SHARD_INDEXES_UPDATED` warning. A
  rename that fails after the archive rename cannot be undone; the
  result warns `PULSE_INDEX_STALE` and lookups refuse until the index is
  rebuilt.
- **A re-encoding add keeps nothing.** When the archive itself moves,
  every stored shard's CRC32 changes, so the add leaves the index as it
  was and lists every per-shard sidecar in `invalidated_sidecars`.
- `compact` copies shards raw, so the sidecars stay valid.

## 6. Anchor syntax

Anchor syntax `archive.pulse#shard.pulse` opens a single shard inside
an archive as a one-shard cohort — useful for diagnostics, debugging,
and tests that exercise the cohesion path against a known-good shard.

## Concurrency caveat

Pulse does **not** provide writer locking. Two processes running
`pulse shard add` against the same archive race; the last writer wins
and the earlier writer's shard is lost. Sharding is single-writer by
design — the caller owns concurrency control (orchestrator coordination,
an external advisory lock, or a single-writer architecture).

## Implementation surface

For maintainers extending the sharding internals, the surface lives in:

| File | Role |
|---|---|
| `internal/encoding/archive.go` | Zip64 read / write + EOCD |
| `internal/encoding/schema_doc.go` | `_schema.pulse` parser / writer |
| `internal/encoding/cohesion.go` | Structural + dict-prefix validators |
| `internal/service/shard_iter.go` | Multi-shard row iterator |
| `internal/service/shard_reduce.go` | Parallel reducer for mergeable ops |
| `internal/service/shard_admin.go` | `create` / `add` / `remove` / `list` / `extract`; both `create` and `add` plan set-rung reconciliation before strict cohesion |
| `internal/service/shard_create.go` | streaming `create`: the header-only plan, then each source replayed once into the stage; the `PULSE_SHARD_COUNT_LIMIT` check |
| `internal/service/shard_widen.go` | Set-width auto-widen on `create` + `add` + the mandatory warning |
| `internal/service/shard_groups.go` | The shared `create` / `add` merge (`mergeShard`), grouped-shard reconciliation + `PULSE_SHARD_GROUPS_REWRITTEN` |
| `internal/encoding/shard_groups.go` | `FlattenCohortBytes`, `ValidateGroupCohesion`, group prefix rule, `GroupIndexHeadroomFor` |
| `internal/encoding/widen.go` | The re-stride engine (`WidenSetFieldBytes` / `WidenSchemaSetField`) |
| `internal/service/shard_compact.go` | `compact` |
| `internal/service/index_upkeep.go` | Point-lookup index upkeep on `add` / `remove` (build before publish) |
| `internal/service/shard_stream.go` | The streaming archive writer (staged file, raw copy, sweep, fsync + rename + directory fsync) shared by every mutation |
| `internal/service/shard_verify.go` | `verify` |
| `internal/service/anchor_overlay.go` | Anchor-syntax overlay |
| `internal/cli/shard.go` | CLI thin adapter |

Width overflow on a categorical dictionary grown by an append surfaces
as `PULSE_SHARD_DICT_WIDTH_OVERFLOW`; the stricter prefix-only
validator (`PULSE_SHARD_DICT_DIVERGENCE`) is retained for the
`pulse shard verify` strict path. The set-width auto-widen planner is
`encoding.PlanSetWidening` and its executor is `internal/service/shard_widen.go`
over E4-S1's `internal/encoding/widen.go` engine; `encoding.SetWidthHeadroomFor`
feeds `verify`'s headroom report.

## Run the gates

```bash
go test ./internal/service/ -run 'TestShardArchive|TestShardGroups'
go test ./internal/encoding/ -run TestShardArchive
go test ./internal/encoding/ -run Cohesion
```
