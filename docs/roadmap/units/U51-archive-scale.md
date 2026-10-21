---
id: U51
slug: archive-scale
title: "A 50-million-row shard archive runs on a 16 GB machine: header-only operations read headers, and appending costs only the new shard"
track: Embedder operations
size: L
status: done
depends_on: []
soft_depends_on: [U19]
blocks: [U32]
todo_items: [354, 355, 356, 357, 358, 359, 360, 361, 373, 390, 399, 455, 456, 458, 461, 491, 492, 493, 494, 495, 496, 498, 499, 500, 501, 502, 503]
branch: archive-scale
---

# U51 — archive-scale

**Outcome:** A 50-million-row shard archive runs on a 16 GB machine: header-only operations read headers, and appending costs only the new shard.

**Track:** Embedder operations · **Size:** L · **Depends on:** none · **Soft:** [U19](U19-resource-limits.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

The retail example targeted 50 million order lines and had to stop at 25.55 million: at 47.5 million rows `pulse shard create` was killed at 13.9 GB. Opening a shard archive reads the whole file into memory, sometimes twice (`afero.ReadFile` in the service open, the parallel reducer, the count path, verify, precheck), so the operations documented as header-only (`inspect`, `predict`, `CountRecords`, `shard list`) took 9–23 s and 4–5 GB on a 1.26 GB archive, and an indexed lookup through an archive anchor cost 16 s / 4.1 GB against 0.14 s on a single file. `shard add` rebuilds the whole archive in a memory buffer. The shard-parallel arm decodes every field (`projected_fields=0`), runs at about a fifth of the single-file rate, gains nothing from four workers, and its float sum differs from the serial sum in the last digits although the docs promise byte equality. Grouping 800k customers costs about 1.2 KB per group, multiplied by worker count.

**Source of inspiration:** the showcase examples under the `pulse-casebook` repository ([retail at scale](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/retail-at-scale/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `.claude/reference/byte-layout.md` (shard archive variant, sidecars, projected decode) and `.claude/reference/execution-modes.md` (parallel shards and decode)
- `skills/cohort-sharding.md`, `skills/cohort-sidecar-index.md`, `skills/streaming-and-watching.md`
- `.claude/reference/observability.md` (log attributes for arms and workers)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#354** (Archive scale) Archives open through `io.ReaderAt` and the zip central directory: `inspect`, `predict`, `CountRecords`, `shard list` and anchor reads touch only headers and the central directory (O(shards)), as CLAUDE.md promises
- [x] **#355** (Archive scale) `shard add` and `shard create` stream entries (copy existing entries by section, append, rewrite the central directory) so memory and time are O(new shard), keeping atomic replace and crash recovery
- [x] **#356** (Archive scale) The shard-parallel arm reads shards by section and uses projected decode; worker scaling is measured and documented
- [x] **#357** (Archive scale) Serial and shard-parallel results for `AGG_SUM` and the other mergeable aggregators are byte-equal as documented (ordered or compensated reduction), or the documented promise is corrected
- [x] **#358** (Archive scale) High-cardinality grouping memory: a per-group overhead budget, and shard workers merge into one group map instead of each holding a full copy
- [x] **#359** (Archive scale) Point lookup over archives (per-shard sidecars or an archive-level index) and a `cohort filter` option that flattens an archive into one single-file cohort
- [x] **#360** (Archive scale) CLI and predict parity: `--shard-workers` / `--decode-workers` flags; `predict-chain` over archives (already accepted since U35 #248 — a regression test, `TestPredictRoots_ChainAcceptsArchive`, pins it); the streaming skill states that grouped output arrives at the end
- [ ] **#361** (Archive scale) Acceptance: the retail example runs at 50M rows on 4 vCPU / 16 GB without `GOMEMLIMIT`; archive header operations finish in under a second; customer lookups work on the archive; the customer chains run on every customer, not a 1-in-10 panel; the bench program is replaced by CLI flags — *partial, 16 GB part missed and accepted by the owner (E5-S2 at 9fb6872b): every per-step target is met (archive header operations including `api predict` 0.11–0.20 s at ~50 MB, customer lookups on the archive, both customer chains over all 2,603,647 customers, serial and parallel byte-equal, the bench program replaced by `--shard-workers` / `--decode-workers`), but without `GOMEMLIMIT` the retention chain peaks at a 17.3–23.1 GiB footprint (13.0–14.9 GB RSS), more than 16 GB; with `GOMEMLIMIT=14GiB` it peaks at 14.8 GiB in 117 s with identical output. The box stays open because its text says "16 GB without `GOMEMLIMIT`"; #501, #502 and #503 carry the remaining work (see the Missed-target note)*

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

Each epic is a vertical slice. Commit with `feat|fix|test|docs(archive-scale/E<n>-S<m>): …`; close each epic with `milestone(archive-scale/E<n>): vertical slice complete — <epic title>`. Stories marked (owner) were added during the effort by owner decision.

### E1 — Archives open and grow without loading them
- S1: every archive read path opens header-only (`io.ReaderAt` + the zip central directory)
- S2: predict's archive memory model matches header-only open
- S3: `shard add` / `remove` / `compact` stream with bounded memory
- S4: `shard create` streams; legacy-offset cohorts interoperate; more than 65535 shards refused

### E2 — Parallel archives are fast and exact
- S1: binned reproducible summation kernel (`internal/reprosum`)
- S2: projected shard arm; global `--shard-workers` / `--decode-workers`
- S3: sums are bit-identical across serial and parallel
- S4: incremental shard merge; grouped runs stay under 512 B/group

### E3 — More requests stay parallel
- S1: `total()` runs two-phase on the parallel arms
- S2: tally attributes merge, built-in and extension (`MergeTally`)
- S3 (owner): `GROUP_ROUNDED` merges on the parallel arms
- S4 (owner): grouped fan-out cardinality sweep, no routing rule; decode dispatch counts records without a second schema read

### E4 — Archives answer lookups
- S1: point lookup on archives; exact `u64` index and join keys
- S2: `shard add` / `remove` keep indexes in step
- S3: `cohort filter --flatten`; filter-to-file archive output streams
- S4 (owner): `u64` cells render exactly in lookup and sample rows
- S5 (owner): single-file filter-to-file streams and writes atomically
- S6 (owner): `u64` cells render exactly in `Process` record rows
- S7 (owner): `WIN_LAG` / `WIN_LEAD` copies of `u64` cells render exactly

### E5 — Proven at 50M rows
- S1: roadmap, migration doc and docs close-out
- S2: 50M retail acceptance run and casebook conversion

## Acceptance criteria

- [x] `pulse cohort inspect` on a 2.5 GB archive returns in under a second with peak RSS under 100 MB (header-only reads; measured in E5-S2: 0.12 s and 51 MB peak RSS on a 2.71 GB, 51.1M-row archive)
- [x] `shard add` peak memory is bounded by the new shard, not the archive
- [x] Serial and parallel sums are byte-equal on the retail data
- [ ] Showcase examples simplified where this unit allows (retail at scale (memory workarounds, the 1-in-10 panel, monthly-file lookups, the Go bench program)): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse — *partial (E5-S2): the 1-in-10 panel is dropped, lookups run on the archive, the Go bench program is replaced by `--shard-workers` / `--decode-workers`, the README and REPRODUCE.md numbers are refreshed from the 50M run and every step is pure Pulse (casebook branch `retail-at-scale-alpha22`, PR waits on the `v1.0.0-alpha.22` tag). One memory workaround remains: the README recommends `GOMEMLIMIT=14GiB` for the retention chain on a 16 GB machine (#361 miss, #503)*
- [x] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- the `TestShardArchive*` suites, `TestShardArchiveLayoutDocumented`, `TestSkillsCoverShardingTopics`, the limits and memory tests, `TestSkillsCoverAllCliLeaves`
- New: archive open memory test, `shard add` memory bound, serial/parallel byte-equality, archive lookup parity, a 10M-row benchmark in CI-optional mode
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- `byte-layout.md` and `execution-modes.md` (load first); `skills/cohort-sharding.md`, `skills/cohort-sidecar-index.md`, `skills/streaming-and-watching.md`
- `docs/src/cli/flags.md`, `commandBindings` and `skills/session-bootstrap.md` for the new flags; `docs/src/library/tuning-limits.md`
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)


## Handed on from U30

| Item | Source |
|---|---|
| Process runs open a shard archive by section (`io.ReaderAt`), not `afero.ReadFile` of the whole archive; then drop predict's archive byte-length term (`archiveTerms`, since U30 E8-S3 / E8-S4) and re-take the `ShardWorkers` sweep | [U30](U30-matrix-extensions-hardening.md) #455 (owner decision 2026-10-14) |

## Human inputs & decisions

- **Decided (interview):** point lookup is design C: one sidecar per shard at the shard's anchor path (byte-identical to an anchored build) plus a coverage record (`shards[]` name, size, CRC32) in `archive.pulse.indexes.json`. Nothing is written inside the zip, so no second reserved entry and old readers are unaffected. An archive-level single index (design B) was rejected.
- **Decided (interview):** byte equality comes from a binned reproducible summation (ReproBLAS-style, K = 3, W = 40, `internal/reprosum`) on every sum path, not from an ordered reduction or compensated sums. Welford variance / stddev stay "within ULP" and the docs say so.
- **Decided (interview):** archive mutations are a streaming rewrite (raw copy of stored entries into `<archive>.pulse.tmp-staged`, fsync + rename + directory fsync), O(new shard) memory and O(archive) I/O. True in-place tail append needs a byte-layout change and a new crash story, so it is a v1.x TODO (#492).
- **Decided (owner):** more than 65535 shards is REFUSED with `PULSE_SHARD_COUNT_LIMIT` before writing; no layout change.
- **Decided (owner):** `shard add` BUILDS BEFORE IT PUBLISHES: new-shard sidecars and the updated manifest are built in staging, the archive is renamed only after all succeed, then the sidecars; any failure aborts the whole add and leaves the original untouched.
- **Decided (owner):** a `u64` join key paired with an `f32` / `f64` key is REFUSED (`PULSE_JOIN_TYPE_MISMATCH`, `details.reason` `u64_float_key`): exact or nothing.
- **Decided (owner):** grouped fan-out has NO cardinality routing rule. The calibration showed no honest single threshold (only value-keyed groupers such as `GROUP_ROUNDED` lose time, and their cardinality is invisible to predict); the docs state that very high cardinality may not gain.
- **Decided (owner, fix in effort):** stories added instead of filing follow-ups: E3-S3 (`GROUP_ROUNDED` mergeable), E3-S4 (cardinality sweep, decode-dispatch record count), E4-S4 (exact `u64` in lookup / sample rows), E4-S5 (single-file filter-to-file atomic and streaming), E4-S6 (exact `u64` in `Process` record rows), E4-S7 (exact `u64` in `WIN_LAG` / `WIN_LEAD` copies). Each carries a documented Go type break or behaviour change in the migration doc.
- **Decided:** `PULSE_INDEX_UNSUPPORTED_SHARDED` is KEPT as "No longer raised" (the frozen-constant precedent of `PULSE_CROSSTAB_EMPTY_COLUMNS`), not removed.
- **Missed target (owner, E5-S4; #499 fixed in E5-S12):** the retention-shaped chain was to peak at no more than 1.5× the `spend` chain's max RSS. At 1/20 scale it peaked at 4.66 GB against 1.99 GB, 2.3×, because the merged buckets (1,171 MB) and `FinalizeGroupedStream`'s per-row `Data` maps (1,327 MB) were live together. E5-S12 fixed #499: the grouped tail releases each bucket as its row is written, and a later chain stage reads the previous stage's rows lazily (`processing.ChainRowIterator`), output byte-identical. Max RSS fell ~17% (4.50–5.03 → 3.86–3.97 GB), still 2.0–2.1× `spend`'s: the returned stage-0 rows alone (1.32 GB, the `stages[0].data` echo) are 2.3× spend's whole peak live heap, so 1.5× is out of reach without dropping that echo. Two holders remain beyond this fix: the shard partials (the peak when a shard holds about half the customers, as at 50M; the `-dense` bench shapes) and the CLI's whole-response JSON encoding (the peak of `pulse api process-chain --json` on the 1/20 archive, 5.6 GB footprint). Figures: `docs/internals/BENCHMARKS_BASELINE.md` (Retention-shaped chain).
- **Missed target (owner, E5-S2): #361's "16 GB without `GOMEMLIMIT`".** Re-measured at 9fb6872b (after E5-S12) on the 47.5M-row, 12-shard archive with 4 shard and 4 decode workers, no `GOMEMLIMIT`: the retention chain runs in 181–187 s at a 17.3–23.1 GiB peak footprint (13.0–14.9 GB max RSS; Go heap peak 15.1–15.6 GiB, 8.3 GiB live), the spend chain in 41–42 s at 10.6 GiB (11.2–11.9 GB RSS). Before E5-S12 the retention chain was 448–725 s at 25.7 GiB. With `GOMEMLIMIT=14GiB` the retention chain peaks at 14.8 GiB footprint in 117 s and returns byte-identical data. Every other #361 target is met. The owner accepted the miss and did not loosen the target: #361 stays open and the three holders are filed as #501 (the CLI encodes the whole `--json` response at once), #502 (in-flight shard partials, merge window 2 × workers) and #503 (a Pulse-owned soft memory limit). The casebook README tells a 16 GB machine to set `GOMEMLIMIT`.

## Shipped

Epics E1 (header-only open, streaming add / remove / compact / create), E2 (binned sums, projected shard arm, worker flags, incremental merge, 422 B/group), E3 (`total()`, tally attributes, `GROUP_ROUNDED` on the parallel arms), E4 (archive point lookup, index upkeep, `cohort filter --flatten`, exact `u64` cells), E5 (close-out, 50M acceptance). Behaviour changes are the "Changes from U51" section of the [embedder migration guide](../v1.0.0-api-and-release/03-embedder-migration.md#changes-from-u51-archive-scale).

## Handed on

Filed in [`TODO.md`](../TODO.md) (ids are TODO ids, not GitHub issues), v1.x:

| Item | To | Why |
|---|---|---|
| #492 true in-place tail append | v1.x, owner U51 | needs a byte-layout change (duplicate `_schema.pulse`, last wins) and a new crash story |
| #493 lookup fan-out pruning (Bloom / min-max per shard) | v1.x, owner U51 | lookup probes every shard's sidecar; pruning needs per-shard key summaries in the manifest |
| #494 grouped, multi-layer and chain `total()` on the parallel arms | v1.x, owner U51 | #399 delivered the ungrouped one-layer form; the rest need per-partition pre-pass publication, one pre-pass per layer and a two-phase chain stage |
| #495 per-source schema snapshots grow N x dictionary size in `CreateShardArchive` / open | v1.x, owner U51 | share snapshots between identical schemas (E1-S4 handoff) |
| #496 `Service.Open` single-file schema decode is unbuffered | v1.x, owner U51 | two reads per inline dictionary entry; buffering must respect the header-only byte-count gates (E3-S4) |
| #497 `FilterToFileResult` has no warnings field | [U33](U33-v1-release.md) | a failure to enumerate `InvalidatedSidecars` is dropped silently; adding the field is a public-API call that must precede the freeze (E4-S3) |
| #498 archive index `DistinctKeys` / `IndexedRecords` are per-shard sums | v1.x, owner U51 | an exact union count would hold every key in memory; documented as a known limit (E4-S1) |
| #499 compact grouped rows / chain stage hand-off without per-row `Data` maps | done in U51 (E5-S12) | buckets released as rows are written, lazy stage input; the 1/20 retention RSS is still 2.0–2.1× `spend`'s because the rows are the `stages[0].data` echo (E5-S4 miss, figures in the Missed-target note above) |
| #500 recorded per-field distinct-count statistic as a provable bucket bound | v1.x, owner U51 | the memory estimate charges a `u32`-keyed grouper its record count (2.4× the customers present on the retention shape); a fresh, exact statistic needs a byte-layout or sidecar contract change (owner-deferred option D of the E5-S6 stop; E5-S7 took B + C) |
| #501 stream the CLI `--json` envelope's `data` rows (process, chain, compose), byte-identical | v1.x, owner U51 | the whole-response encode is the peak of `pulse api process-chain --json` (5.6 GB footprint at 1/20; an E5-S12 prototype cut 23%); one reason the 50M retention chain needs more than 16 GB without `GOMEMLIMIT` (#361 miss, owner-accepted in E5-S2) |
| #502 bound in-flight shard partials to a merge window equal to the workers, predict in parity | v1.x, owner U51 | the partials are the stage-0 peak when a shard holds about half the customers, as at 50M (dense 1/20 retention 1.90 → 1.82 GB, spend 1.33 → 0.85 GB); one reason the 50M retention chain needs more than 16 GB without `GOMEMLIMIT` (#361 miss) |
| #503 a Pulse soft memory limit (CLI flag, `Options` budget, possibly a RAM-derived default) | v1.x, owner U51 | `GOMEMLIMIT=14GiB` fits the 50M retention chain at a 14.0–14.8 GiB footprint with identical output (17.3–23.1 GiB without it); a Pulse-owned limit would let the showcase meet 16 GB without the caller setting an environment variable (#361 miss) |

## Inherited from U41

Filed from [U41 handed on](U41-derived-cohort.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#373** — Legacy-offset cohort compatibility: shard cohesion and `rewriteShardDicts` refuse a cohort whose stored `ByteOffset` / `BitPosition` differ from the canonical layout (zero-offset explicit-schema cohorts from before U41 E1-S5, schema-file cohorts with a bit-packed field first from before E1-S3). **Required resolution: full cohort compatibility — such cohorts join and interoperate with new cohorts and shard archives without re-import (compare the type-derived layout, never stored offsets).** Found in U41 (E1-S5).

## Inherited from U42

Filed from [U42 handed on](U42-fit-scoring.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#390** — Predict builds the aggregator capability table once instead of per call (`aggregatorComponentSchemaIndex` → `aggregatorCapabilities` → `withWeightAware`); the off-path predict allocation pin grew from 236 to 251 in U42. Found in U42 (E2-S1).

## Inherited from U43

Filed from [U43 handed on](U43-post-aggregation-ratios.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#399** — Merge `total()` partial sums across partitions so shard and parallel-decode runs stay parallel; today a two-pass `total()` request falls back to the sequential path there. Found in U43 (E1).

## Handed on from U35

Filed from [U35](U35-predict-runtime-parity.md); tick in [`TODO.md`](../TODO.md) when done.

- [x] **#456** `ATTR_SCALE_SCORE` / `ATTR_PROFILE_SIMILARITY` are not merge-admitted: their per-run Components / warning tallies need a tally-merge hook on the shard-archive and parallel-decode reducers (~150 lines), so a Process carrying one runs the serial path (correct, slower) and a `ProcessChain` stage carrying one is refused `PULSE_CHAIN_NOT_MERGEABLE`. Perf only. Found in U35 (E5-S3, #391)
- [x] **#461** `PredictCompose` archive slots are archive-aware but read the whole archive (`Service.Open` does `afero.ReadFile`), not header-only like `Predict` / `Inspect`; resolves with #455. Found in U35 (E7-S3)

## Handed on from U34

Filed from [U34](U34-extension-validation.md); tick in [`TODO.md`](../TODO.md) when done.

- [x] **#458** A join `ON` key over a `u64` field above 2^53 still matches on its float64 echo (`encoding/join_keys.go` documents it): two distinct large keys can collide. Give the join key an exact `u64` path, or refuse such a key. #427 fixed the attribute keys only. Found in U35 (E1-S2); re-homed from U34

## Filed in U51

Found while building this unit; ticked in [`TODO.md`](../TODO.md) by the story that fixed it.

- [x] **#491** The sidecar index key of a `u64` field above 2^53 rode its float64 echo (build `uint64(NumericValue)`, probe `ParseFloat`), so two ids one float apart shared key bytes. Built from the exact word, probed with `ParseUint`; indexes over keys below 2^53 stay byte-identical, larger ones need a rebuild. Found in U51 (E4-S1)
