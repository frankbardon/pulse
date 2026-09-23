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
skill](https://github.com/frankbardon/pulse/blob/main/skills/cohort-schema-design.md)
(Sharded cohorts).

## 1. Create the archive

The first include seeds the canonical schema; remaining includes are
validated against it via structural cohesion + the dict prefix rule.
Atomic temp + rename:

```bash
pulse shard create q1_2019.pulse \
    --include 20190101.pulse \
    --include 20190108.pulse \
    --include 20190115.pulse
```

## 2. Append a shard

Validates cohesion + dict prefix, grows the canonical dict if needed
(rewriting `_schema.pulse` before placing the new shard), then in-place
appends the payload:

```bash
pulse shard add q1_2019.pulse 20190122.pulse
```

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

The rewrite is atomic (temp file + fsync + rename), so a failure
mid-rewrite leaves the original archive byte-identical and openable at
its old rung. It is also **never silent**: every widen emits a
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
canonical metadata (`aggregate_record_count`, `shard_count`):

```bash
pulse shard compact q1_2019.pulse
```

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
| `encoding/archive.go` | Zip64 read / write + EOCD |
| `encoding/schema_doc.go` | `_schema.pulse` parser / writer |
| `encoding/cohesion.go` | Structural + dict-prefix validators |
| `service/shard_iter.go` | Multi-shard row iterator |
| `service/shard_reduce.go` | Parallel reducer for mergeable ops |
| `service/shard_admin.go` | `create` / `add` / `remove` / `list` / `extract`; both `create` and `add` plan set-rung reconciliation before strict cohesion |
| `service/shard_widen.go` | Set-width auto-widen on `create` + `add` + the mandatory warning |
| `encoding/widen.go` | The re-stride engine (`WidenSetFieldBytes` / `WidenSchemaSetField`) |
| `service/shard_compact.go` | `compact` |
| `service/shard_verify.go` | `verify` |
| `service/anchor_overlay.go` | Anchor-syntax overlay |
| `internal/cli/shard.go` | CLI thin adapter |

Width overflow on a categorical dictionary grown by an append surfaces
as `PULSE_SHARD_DICT_WIDTH_OVERFLOW`; the stricter prefix-only
validator (`PULSE_SHARD_DICT_DIVERGENCE`) is retained for the
`pulse shard verify` strict path. The set-width auto-widen planner is
`encoding.PlanSetWidening` and its executor is `service/shard_widen.go`
over E4-S1's `encoding/widen.go` engine; `encoding.SetWidthHeadroomFor`
feeds `verify`'s headroom report.

## Run the gates

```bash
go test ./service/ -run TestShardArchive
go test ./encoding/ -run TestShardArchive
go test ./service/ -run TestCohesion
```
