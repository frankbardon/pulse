---
name: cohort-sharding
description: Shard archives in depth — strict structural cohesion, dictionary union-merge, set-rung auto-widen on create and add, grouped shards, the anchor syntax, union-wide memory for materializing operations, single-writer concurrency, and transport-only compression for moving a cohort.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [shard archives, cohesion, auto-widen, anchor syntax, transfer, zstd]
---

# Sharded cohorts in depth

Part of the `.pulse` schema surface; entry skill `cohort-schema-design` (Sharded cohorts — the two on-disk shapes). Operations guide: `docs/src/internals/managing-shard-archives.md`.

## Sharded cohorts — cohesion

- Structural: **strict** at insert, on every dimension but one. Field count, names, type bytes, byte offsets, bit positions must match canonical. Mismatch → `PULSE_SHARD_SCHEMA_MISMATCH`. The single exception is the `set_*` RUNG — see auto-widen below.
- Descriptions: **tolerant**. Divergence → `PULSE_SHARD_DESCRIPTION_DIVERGENCE` (warning); canonical wins.
- Categorical / set dictionaries: union-merge. Canonical entries first; new entries appended; incoming records byte-rewritten with remapped indices.
- Prefix-only validator raises `PULSE_SHARD_DICT_DIVERGENCE` when embedders coordinate dicts upstream. Unchanged by auto-widen.
- **`set_*` auto-widen.** Two forces trigger it, and they compose. (1) A dictionary union that outgrows a set field's bitmask. (2) The arriving shard **declaring a different rung** — a re-import of a new period infers the rung its own data needs, so a `set_u128` shard legitimately meets a `set_u64` archive. Either way the field is promoted to the narrowest rung that holds every member, across `_schema.pulse` and **every** shard payload, instead of the shard being refused. Refusing (2) forced a full re-import of every prior period to reach a layout a mechanical re-stride already produces.
- **Only ever wider.** A shard arriving at a NARROWER rung never narrows the archive — narrowing would drop every selection above the target's ceiling, silently, with the record still decoding to a smaller and entirely plausible selection. The arriving shard is promoted to the archive's rung instead, and only that one shard is rewritten.
- **Create widens too.** `pulse shard create` / `Pulse.CreateShardArchive` auto-widens on exactly the same conditions as `shard add`. One rule, no asymmetry: the same two files must not produce an archive when passed together and an error when passed one after the other. `CreateShardArchive` returns a `*CreateShardArchiveResult` rather than a bare error for the same reason `AddShard` does — so the warning has nowhere to be dropped.
- The whole-archive rewrite is atomic (temp + fsync + rename) and emits a mandatory `PULSE_SHARD_SET_WIDENED` warning naming field, old rung, new rung, the rung the arriving shard declared and the shards rewritten — read it: an archive-wide re-stride costs far more than an append. `details.archive_widened` tells the two directions apart (`from != to` ⇒ the archive moved; equal ⇒ only the arriving shard did). Past `set_u256` there is nowhere to widen to and `PULSE_SHARD_DICT_WIDTH_OVERFLOW` stands. Categorical widths are unaffected; they stay fixed at folder creation.
- `pulse shard verify` reports **set-width headroom** per set field (entries used, capacity, headroom, next rung) so an impending widen is foreseeable rather than a surprise the next `shard add` bills for.
- **Grouped (`0x02`) shards.** An archive has ONE parent-group layout — part of strict cohesion, so `shard verify` refuses a `0x01` shard in a `0x02` archive. On create/add the ARCHIVE's layout wins: a grouped arrival into an ungrouped archive is stored flattened; an arrival with other groups (or none) is re-encoded into the archive's. Group dictionaries union-merge canonical-first (stored shards untouched, only the arrival renumbered); past the u32 index space → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. An arrival that disagrees with a CONSTANT group promotes it to indexed across every shard; one that violates a declared key is refused (`PULSE_GROUP_MEMBER_NOT_CONSTANT`). Layout changes emit the mandatory `PULSE_SHARD_GROUPS_REWRITTEN` (`details.reason`, `archive_rewritten`); `shard verify` adds `group_index_headroom` (a constant group always shows 0). To keep appends cheap, import every period with the same `--group` flags and don't elide a column that varies between periods. `pulse dedup` refuses archives — dedup each shard, then `shard create`.

## Anchor syntax

```
respondents/Q1.pulse                 → full sharded cohort (union semantics)
respondents/Q1.pulse#20190101.pulse  → named shard, as one-shard cohort
```

Anchor against single-file → `PULSE_ARCHIVE_MAGIC_INVALID`. Missing anchor → `PULSE_SHARD_MISSING`.

## Memory shape

Materializing ops (percentile / median aggs, the percentile attribute, quantile and date groupers, windows, decimal paths, tier-1 tests + groupers/features/two-pass attrs, tier-2 post tests) materialize across the **union** of shards. Median-of-medians is not the median. Memory scales with shard count.

## Concurrency

No concurrent-writer protection: two writers race, last wins. Readers snapshot at open. Caller owns single-writer architecture or an advisory lock.

## Moving a cohort (transport-only compression)

Compress only to move it: `pulse export transfer` writes `<cohort>.zst` (one standard zstd stream of the exact bytes — single-file or archive, any format version); `pulse import transfer` restores a byte-identical `.pulse` (sha256 in both reports). Never at rest — random access, mmap, lookup and parallel decode need the raw bytes, so any read surface refuses an artifact with `PULSE_COHORT_COMPRESSED`. CLI/library only, no MCP tool. Parent groups and zstd remove the same repetition: a sorted flat cohort compresses to about what a grouped one does. Detail: `docs/src/format/transfer.md`.
