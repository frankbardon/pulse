---
id: U73
slug: archive-storage
title: "Shard archives append in place, prune lookup fan-out and carry honest index statistics at scale"
track: Embedder operations
size: L
status: done
depends_on: [U77, U78]
soft_depends_on: []
blocks: [U32]
todo_items: [492, 493, 495, 496, 498, 500]
branch: archive-storage
---

# U73 — archive-storage

**Outcome:** Shard archives append in place, prune lookup fan-out and carry honest index statistics at scale.

**Track:** Embedder operations · **Size:** L · **Depends on:** U77, U78 (U77, U78: CI and test hygiene land first)

## Summary

Archive storage depth: a true in-place tail append for `shard add`, lookup fan-out pruning, per-source schema snapshots, buffered single-file schema decode, archive index statistics that are not per-shard sums, and a recorded per-field distinct-count statistic.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [x] **#492** True in-place tail append: `shard add` rewrites the whole archive's bytes (streamed, O(archive) I/O, O(new shard) memory). Appending only the new shard needs a duplicate `_schema.pulse` entry (last wins) or an equivalent byte-layout change, plus a new crash story. A byte-layout change, so it ships only with `byte-layout.md` and a version decision. Found in U51 (interview) Done in U73 (E2-S1, E2-S2, E2-S3): an ordinary `shard add` appends the new shard, a refreshed `_schema.pulse` and a new Zip64 central directory past the old end (O(new shard + directory) bytes; no `.pulse` version byte change); the append is journalled (`<archive>.append`), an interrupted one is refused `PULSE_SHARD_APPEND_INCOMPLETE` and repaired by `pulse shard verify --repair`; dead bytes are measured, and an add that would leave more than half the archive dead rewrites instead (`PULSE_SHARD_DEAD_BYTES_RECLAIMED`, `shard add --rewrite`). · [U73](units/U73-archive-storage.md)
- [x] **#493** Lookup fan-out pruning: `Lookup` on an archive probes every shard's sidecar. A per-shard Bloom filter or key min/max range recorded in the index manifest's `shards[]` would skip shards that cannot hold the key. Found in U51 (interview, E4-S1) Done in U73 (E1-S1, E1-S2): the index manifest's `shards[]` records carry an order-preserving `key_min` / `key_max`, and each per-shard sidecar gets a `.bloom` filter (integer hashing, stamped with the shard identity); `Lookup` skips a shard unopened only when its range excludes the key or its valid Bloom says absent, else it probes. `VerifyIndex` reports range mismatches. · [U73](units/U73-archive-storage.md)
- [x] **#495** `CreateShardArchive`'s plan keeps one schema snapshot per dictionary-remapping source, so it grows N x dictionary size when sources carry large dictionaries (a categorical with hundreds of thousands of entries across thousands of sources). The cause is create-time, not archive open (open holds one canonical schema): each source's replay closure (`remapOp`) retains the `MergeDictUnion` result, which deep-copied EVERY dictionary even when the union added nothing. Share the snapshot between identical schemas and hold untouched dictionaries once. Found in U51 (E1-S4) Done in U73 (E4-S1): a union that adds no entry returns the canonical schema itself, and a growing union rebuilds only the grown dictionaries, so identical sources share one snapshot; output archives are byte-identical. · [U73](units/U73-archive-storage.md)
- [x] **#496** `Service.Open` decodes a single file's schema unbuffered: two reads per inline dictionary entry (about 0.45 s at 600k entries). Buffer the schema read. It must respect the header-only byte-count gates (`CountRecords` and `Inspect` byte budgets, `fs_counting`), so the buffer cannot read ahead into the record payload on those paths. Found in U51 (E3-S4) Done in U73 (E4-S2): `Service.Open` decodes a single file's header + schema through a bounded chunked buffer (over-read at most one chunk); predict's runtime-side readers and `CountRecords` stay unbuffered (`Service.OpenExact`), so the header-only byte gates hold. · [U73](units/U73-archive-storage.md)
- [x] **#498** Archive index `DistinctKeys` / `IndexedRecords` are per-shard SUMS, so a key present in two shards counts twice. An exact union count needs every key held in memory, which the streaming build avoids. Documented as a known limit; a probabilistic distinct count (HyperLogLog) or an exact external merge is the fix. Found in U51 (E4-S1) Done in U73 (E1-S3): each `shards[]` record carries a HyperLogLog sketch (p = 10, integer registers), and `BuildIndexResult` / `IndexInfo` gain `DistinctKeysEstimate` (the merged estimate, clamped to [largest shard count, sum]; exact for a single file, nil when any sketch is missing). `DistinctKeys` / `IndexedRecords` stay sums. · [U73](units/U73-archive-storage.md)
- [x] **#500** A recorded per-field distinct-count statistic, written at import and at `shard create` / `add` / `compact`, that predict may read as a provable bucket bound. The memory estimate bounds a grouper of data-dependent cardinality by the width of its key word or by the record count (`memoryGroupBound`); a `u32` customer id over 2.52M rows is charged 2.52M buckets against 1,053,033 present (2.4×), the largest remaining share of the retention-shaped estimate (10.55 GB against a 3.4 GB peak after E5-S7). A sidecar index's `distinct_keys` was rejected because its freshness is not provable header-only. The statistic must be exact (or a proven upper bound) and kept fresh by every rewrite, so it is a byte-layout or sidecar contract change: it ships only with `byte-layout.md` and a version decision. Sites: `internal/descriptor/limits.go` (`memoryGroupBound`), the import writers, `internal/service/shard_create.go` / `shard_admin.go` / `shard_compact.go`. Owner-deferred option D of the E5-S6 stop. Found in U51 (E5-S6) Done in U73 (E3-S1, E3-S2): every archive writer records exact per-shard distinct counts of `u32` / `u64` / `date` / `datetime` / `f32` / `f64` fields in a DIST trailer after SHRD in `_schema.pulse` (no `.pulse` version byte change; single files carry none). Under `MaxEstimatedMemory`, predict and the run's pre-flight bound a value-keyed grouper by the sum of the shards' counts (`distinctKeyBound`); an absent, over-limit, opted-out or malformed count keeps the old bound. · [U73](units/U73-archive-storage.md)

## Decisions kept

- No `.pulse` version byte moves: the append rides a duplicate `_schema.pulse` (last wins), and the distinct counts ride a trailer after SHRD in `_schema.pulse`; single-file cohorts carry no counts.
- The grouper bound is advisory: a missing, over-limit, opted-out or malformed count (or a trailer that does not name exactly the archive's shards) falls back to the old bound and never fails predict or the run. It is read only when `MaxEstimatedMemory` is set, on both sides.

## Handed-on

| Item | To | Why |
|---|---|---|
| #720 a shard replaced outside Pulse keeps its carried distinct counts | [U74](U74-memory-bounds.md) | the per-shard CRC fix is blocked on the add rewrite arm's ordering; U74 owns the archive memory estimate |
| #721 one archive-level Bloom / range read instead of a `.bloom` GET per in-range shard | [U74](U74-memory-bounds.md) | archive-path cost at scale; no unit owns lookup after U73 |
| #722 predict's `OpenExact` readers still decode a huge-dictionary single file unbuffered | [U74](U74-memory-bounds.md) | the same buffering under the predict byte gates; U74 owns the scale acceptance runs |
