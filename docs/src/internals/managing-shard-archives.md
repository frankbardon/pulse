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
| `service/shard_admin.go` | `create` / `add` / `remove` / `list` / `extract` |
| `service/shard_compact.go` | `compact` |
| `service/shard_verify.go` | `verify` |
| `service/anchor_overlay.go` | Anchor-syntax overlay |
| `internal/cli/shard.go` | CLI thin adapter |

Width overflow on a categorical dictionary grown by an append surfaces
as `PULSE_SHARD_DICT_WIDTH_OVERFLOW`; the stricter prefix-only
validator (`PULSE_SHARD_DICT_DIVERGENCE`) is retained for the
`pulse shard verify` strict path.

## Run the gates

```bash
go test ./service/ -run TestShardArchive
go test ./encoding/ -run TestShardArchive
go test ./service/ -run TestCohesion
```
