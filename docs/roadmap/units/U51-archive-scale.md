---
id: U51
slug: archive-scale
title: "A 50-million-row shard archive runs on a 16 GB machine: header-only operations read headers, and appending costs only the new shard"
track: Embedder operations
size: L
status: not-started
depends_on: []
soft_depends_on: [U19]
blocks: [U32]
todo_items: [354, 355, 356, 357, 358, 359, 360, 361, 373, 390, 399]
branch: archive-scale
---

# U51 — archive-scale

**Outcome:** A 50-million-row shard archive runs on a 16 GB machine: header-only operations read headers, and appending costs only the new shard.

**Track:** Embedder operations · **Size:** L · **Depends on:** none · **Soft:** [U19](U19-resource-limits.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

The retail example targeted 50 million order lines and had to stop at 25.55 million: at 47.5 million rows `pulse shard create` was killed at 13.9 GB. Opening a shard archive reads the whole file into memory, sometimes twice (`afero.ReadFile` in the service open, the parallel reducer, the count path, verify, precheck), so the operations documented as header-only (`inspect`, `predict`, `CountRecords`, `shard list`) took 9–23 s and 4–5 GB on a 1.26 GB archive, and an indexed lookup through an archive anchor cost 16 s / 4.1 GB against 0.14 s on a single file. `shard add` rebuilds the whole archive in a memory buffer. The shard-parallel arm decodes every field (`projected_fields=0`), runs at about a fifth of the single-file rate, gains nothing from four workers, and its float sum differs from the serial sum in the last digits although the docs promise byte equality. Grouping 800k customers costs about 1.2 KB per group, multiplied by worker count.

**Source of inspiration:** the showcase examples under `docs/showcase/` ([retail at scale](../../showcase/retail-at-scale/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `.claude/reference/byte-layout.md` (shard archive variant, sidecars, projected decode) and `.claude/reference/execution-modes.md` (parallel shards and decode)
- `skills/cohort-sharding.md`, `skills/cohort-sidecar-index.md`, `skills/streaming-and-watching.md`
- `.claude/reference/observability.md` (log attributes for arms and workers)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#354** (Archive scale) Archives open through `io.ReaderAt` and the zip central directory: `inspect`, `predict`, `CountRecords`, `shard list` and anchor reads touch only headers and the central directory (O(shards)), as CLAUDE.md promises
- [ ] **#355** (Archive scale) `shard add` and `shard create` stream entries (copy existing entries by section, append, rewrite the central directory) so memory and time are O(new shard), keeping atomic replace and crash recovery
- [ ] **#356** (Archive scale) The shard-parallel arm reads shards by section and uses projected decode; worker scaling is measured and documented
- [ ] **#357** (Archive scale) Serial and shard-parallel results for `AGG_SUM` and the other mergeable aggregators are byte-equal as documented (ordered or compensated reduction), or the documented promise is corrected
- [ ] **#358** (Archive scale) High-cardinality grouping memory: a per-group overhead budget, and shard workers merge into one group map instead of each holding a full copy
- [ ] **#359** (Archive scale) Point lookup over archives (per-shard sidecars or an archive-level index) and a `cohort filter` option that flattens an archive into one single-file cohort
- [ ] **#360** (Archive scale) CLI and predict parity: `--shard-workers` / `--decode-workers` flags; `predict-chain` accepts archives; the streaming skill states that grouped output arrives at the end
- [ ] **#361** (Archive scale) Acceptance: the retail example runs at 50M rows on 4 vCPU / 16 GB without `GOMEMLIMIT`; archive header operations finish in under a second; customer lookups work on the archive; the customer chains run on every customer, not a 1-in-10 panel; the bench program is replaced by CLI flags

## Scope

**In scope**
- Archive open, add, create, list, verify paths
- Shard-parallel decode and reduction
- Group-map memory
- Archive lookup and flatten
- Worker flags

**Out of scope**
- Distributed execution across machines
- Compression inside archives (`.pulse.zst` stays transport-only)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(archive-scale/E<n>-S<m>): …`; close each epic with `milestone(archive-scale/E<n>): vertical slice complete — <epic title>`.

### E1 — Header-only means header-only
- S1: ReaderAt/central-directory open for every archive path
- S2: streaming `shard add` / `create`

### E2 — Parallel archives are fast and exact
- S1: sectioned, projected shard decode; worker scaling
- S2: deterministic reduction; group-map merge

### E3 — Archives are first-class
- S1: archive lookup and flatten; worker flags; predict-chain
- S2: convert the retail example at 50M rows

## Acceptance criteria

- [ ] `pulse cohort inspect` on a 2.5 GB archive returns in under a second with peak RSS under 100 MB
- [ ] `shard add` peak memory is bounded by the new shard, not the archive
- [ ] Serial and parallel sums are byte-equal on the retail data
- [ ] Showcase examples simplified where this unit allows (retail at scale (memory workarounds, the 1-in-10 panel, monthly-file lookups, the Go bench program)): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse
- [ ] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- the `TestShardArchive*` suites, `TestShardArchiveLayoutDocumented`, `TestSkillsCoverShardingTopics`, the limits and memory tests, `TestSkillsCoverAllCliLeaves`
- New: archive open memory test, `shard add` memory bound, serial/parallel byte-equality, archive lookup parity, a 10M-row benchmark in CI-optional mode
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- `byte-layout.md` and `execution-modes.md` (load first); `skills/cohort-sharding.md`, `skills/cohort-sidecar-index.md`, `skills/streaming-and-watching.md`
- `docs/src/cli/flags.md`, `commandBindings` and `skills/session-bootstrap.md` for the new flags; `docs/src/library/tuning-limits.md`
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Open:** an archive-level index versus per-shard sidecars for point lookup.
- **Open:** deterministic reduction order (exact) versus compensated summation (fast) for byte equality.

## Inherited from U41

Filed from [U41 handed on](U41-derived-cohort.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#373** — Legacy-offset cohort compatibility: shard cohesion and `rewriteShardDicts` refuse a cohort whose stored `ByteOffset` / `BitPosition` differ from the canonical layout (zero-offset explicit-schema cohorts from before U41 E1-S5, schema-file cohorts with a bit-packed field first from before E1-S3). **Required resolution: full cohort compatibility — such cohorts join and interoperate with new cohorts and shard archives without re-import (compare the type-derived layout, never stored offsets).** Found in U41 (E1-S5).

## Inherited from U42

Filed from [U42 handed on](U42-fit-scoring.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#390** — Predict builds the aggregator capability table once instead of per call (`aggregatorComponentSchemaIndex` → `aggregatorCapabilities` → `withWeightAware`); the off-path predict allocation pin grew from 236 to 251 in U42. Found in U42 (E2-S1).

## Inherited from U43

Filed from [U43 handed on](U43-post-aggregation-ratios.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#399** — Merge `total()` partial sums across partitions so shard and parallel-decode runs stay parallel; today a two-pass `total()` request falls back to the sequential path there. Found in U43 (E1).
