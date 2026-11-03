---
id: U73
slug: archive-storage
title: "Shard archives append in place, prune lookup fan-out and carry honest index statistics at scale"
track: Embedder operations
size: L
status: not-started
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

- [ ] **#492** True in-place tail append: `shard add` rewrites the whole archive's bytes (streamed, O(archive) I/O, O(new shard) memory). Appending only the new shard needs a duplicate `_schema.pulse` entry (last wins) or an equivalent byte-layout change, plus a new crash story. A byte-layout change, so it ships only with `byte-layout.md` and a version decision. Found in U51 (interview) · [U73](units/U73-archive-storage.md)
- [ ] **#493** Lookup fan-out pruning: `Lookup` on an archive probes every shard's sidecar. A per-shard Bloom filter or key min/max range recorded in the index manifest's `shards[]` would skip shards that cannot hold the key. Found in U51 (interview, E4-S1) · [U73](units/U73-archive-storage.md)
- [ ] **#495** `CreateShardArchive` / archive open keep one schema snapshot per source, so the plan grows N x dictionary size when sources carry large dictionaries (a categorical with hundreds of thousands of entries across thousands of sources). Share the snapshot between identical schemas or hold dictionaries once. Found in U51 (E1-S4) · [U73](units/U73-archive-storage.md)
- [ ] **#496** `Service.Open` decodes a single file's schema unbuffered: two reads per inline dictionary entry (about 0.45 s at 600k entries). Buffer the schema read. It must respect the header-only byte-count gates (`CountRecords` and `Inspect` byte budgets, `fs_counting`), so the buffer cannot read ahead into the record payload on those paths. Found in U51 (E3-S4) · [U73](units/U73-archive-storage.md)
- [ ] **#498** Archive index `DistinctKeys` / `IndexedRecords` are per-shard SUMS, so a key present in two shards counts twice. An exact union count needs every key held in memory, which the streaming build avoids. Documented as a known limit; a probabilistic distinct count (HyperLogLog) or an exact external merge is the fix. Found in U51 (E4-S1) · [U73](units/U73-archive-storage.md)
- [ ] **#500** A recorded per-field distinct-count statistic, written at import and at `shard create` / `add` / `compact`, that predict may read as a provable bucket bound. The memory estimate bounds a grouper of data-dependent cardinality by the width of its key word or by the record count (`memoryGroupBound`); a `u32` customer id over 2.52M rows is charged 2.52M buckets against 1,053,033 present (2.4×), the largest remaining share of the retention-shaped estimate (10.55 GB against a 3.4 GB peak after E5-S7). A sidecar index's `distinct_keys` was rejected because its freshness is not provable header-only. The statistic must be exact (or a proven upper bound) and kept fresh by every rewrite, so it is a byte-layout or sidecar contract change: it ships only with `byte-layout.md` and a version decision. Sites: `internal/descriptor/limits.go` (`memoryGroupBound`), the import writers, `internal/service/shard_create.go` / `shard_admin.go` / `shard_compact.go`. Owner-deferred option D of the E5-S6 stop. Found in U51 (E5-S6) · [U73](units/U73-archive-storage.md)
