---
id: U74
slug: memory-bounds
title: "A 50M-row archive run fits in 16 GB without GOMEMLIMIT, with bounded merge windows, streamed output and a soft memory limit"
track: Embedder operations
size: M
status: done
depends_on: [U77, U78, U73]
soft_depends_on: []
blocks: [U32]
todo_items: [494, 501, 502, 503, 361, 707]
branch: memory-bounds
---

# U74 — memory-bounds

**Outcome:** A 50M-row archive run fits in 16 GB without GOMEMLIMIT, with bounded merge windows, streamed output and a soft memory limit.

**Track:** Embedder operations · **Size:** M · **Depends on:** U77, U78, U73 (U77, U78: CI and test hygiene land first; U73 is a hard dependency)

## Summary

Memory bounds on the archive path: `total()` on the parallel arms for grouped, multi-layer and chain requests, a streamed `--json` envelope, a bounded merge window for in-flight shard partials, and a soft memory limit. Closes with #361, the 50M-row / 16 GB acceptance: the retail example's retention and spend chains now finish inside a 16 GiB, 4-CPU container with no soft limit (see [Measurement](#measurement)). U73 is a hard dependency.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [x] **#494** Grouped, multi-layer and `ProcessChain` `total()` on the parallel arms (#399 delivered the ungrouped, one-layer form). Grouped needs per-partition pre-pass publication before phase 2; multi-layer needs one parallel pre-pass per layer (`TotalsPrepass` per layer, `twoPassPlan.needed[L]`); chains need a two-phase stage runtime and a validator twin. Sites: `internal/mergegate/gate.go` (`ParallelTotals`, `ChainRefusal`), `internal/processing/formula_total_merge.go`, `internal/service/totals_prepass.go`, `internal/service/chain.go`, `internal/descriptor/chain.go`. Found in U51 (E3-S1) · [U74](units/U74-memory-bounds.md) Delivered by U74 (E3-S1 grouped, E3-S2 one parallel pre-pass per layer, E3-S3 chain stages two-phase; `ChainRefusal` shares `MergeRefusal`'s rule): the serial bits under every shard / decode worker count, predict fan-out ≡ runtime.
- [x] **#501** Stream the CLI `--json` envelope's `data` rows for `pulse api process`, `process-chain` and `compose` instead of building the whole response and encoding it in one `json.Marshal`: write the envelope head, then each row as it is produced, then the tail, byte-identical to today's output (a digest test on every arm, weighted and `Return` included). The CLI's whole-response encoding is the peak of `pulse api process-chain --json` on the 1/20 retention archive (5.6 GB footprint); an E5-S12 prototype cut it 23%. One of the three holders that keep the 50M retention chain above #361's 16 GB without `GOMEMLIMIT` (owner-accepted miss, U51 E5-S2). Sites: `internal/cli/json.go` (`writeEnvelope*`), `internal/cli/api.go`, `descriptor.NewEnvelope`. Found in U51 (E5-S12) · [U74](units/U74-memory-bounds.md) Delivered by U74 (E1-S1): `pulse api process|process-chain|compose --json` writes the envelope head, each row and the tail as produced, byte-identical to `json.Marshal` of the envelope on every arm (digest tests); in-process encode peak at 50k rows 13.4 MB → 70 KB.
- [x] **#502** Bound the in-flight shard partials to a merge window equal to the shard workers (today `limits.ShardMergeWindow` admits 2 × workers): on the dense 1/20 retention shape max RSS fell 1.90 → 1.82 GB and on spend 1.33 → 0.85 GB. When a shard holds about half the customers, as at 50M, the partials are the stage-0 peak, so this is one of the three holders that keep the 50M retention chain above #361's 16 GB without `GOMEMLIMIT` (owner-accepted miss, U51 E5-S2). Predict's `descx.ShardMergeWindow` and its memory estimate must move with the runtime (a predict ≡ runtime parity test on the window). Sites: `internal/limits/memory.go` (`ShardMergeWindow`, `ShardMergeWindowWithin`), `internal/descriptor/limits.go` (`ShardMergeWindow`), `internal/service/shard_reduce.go`. Found in U51 (E5-S12) · [U74](units/U74-memory-bounds.md) Delivered by U74 (E1-S2): the merge window is the shard worker count (one in-flight partial a worker), predict ≡ runtime on window and budget; no wall-time regression (50M spend in the 16 GiB container 52–93 s against 88–90 s before).
- [x] **#503** A Pulse soft memory limit: a CLI flag and a `pulse.Options` budget (and possibly a default derived from physical RAM) that set the Go runtime soft limit (`debug.SetMemoryLimit`) for the run, so a large grouped run fits the machine without the caller knowing about `GOMEMLIMIT`. On the 50M retention chain `GOMEMLIMIT=14GiB` fit at a 14.0–14.8 GiB peak footprint with identical output and finished faster than the unlimited run (113–117 s against 181–187 s; less paging), against a 17.3–23.1 GiB footprint without it: #361's "16 GB without `GOMEMLIMIT`" was missed and accepted by the owner (U51 E5-S2). Needs an Update Demand pass (CLI leaf flag, `Options` field, env-var rules) and a decision on whether the default may change behaviour. Found in U51 (E5-S12) · [U74](units/U74-memory-bounds.md) Delivered by U74 (E1-S3, E2-S2): a global `--memory-limit <size|N%>` CLI flag sets the Go soft limit (beats `GOMEMLIMIT`; unset leaves the runtime untouched); `N%` is a share of min(RAM, cgroup v2 `memory.max`). By owner decision no `pulse.Options` field, no env var and no default limit.
- [x] **#361** Acceptance: the retail example runs at 50M rows on 4 vCPU / 16 GB without `GOMEMLIMIT`; archive header operations finish in under a second; customer lookups work on the archive; the customer chains run on every customer, not a 1-in-10 panel; the bench program is replaced by CLI flags · [U74](units/U74-memory-bounds.md) — *U51 (E5-S2) met every per-step target, including `api predict` on the archive (0.20 s, 52 MB), but missed the 16 GB part (filed as #501–#503).* Delivered by U74 (E2-S3), measured in `docker run --cpus=4 --memory=16g --memory-swap=16g` with no soft limit (owner metric: no OOM kill + anon peak < 16 GiB + digest equal): retention chain 3/3 runs pass at a 13.1–15.0 GiB anon peak in 162–191 s, spend chain at 9.3 GiB in 52 s, digests equal to the unconstrained reference ([U74 Measurement](units/U74-memory-bounds.md#measurement)); headroom item filed as #723
- [x] **#707** Flaky `TestRecord_LayoutCacheReleasesCollectedSchemas` (`internal/processing`): failed once in a parallel multi-package run, a GC-timing dependency; make it deterministic or move it to the heavy tier. Found in library-gaps E1-S2 (U78 owns the test-hygiene pattern, #555, but is closed; the memory-bounds unit is the nearest owner) · [U74](units/U74-memory-bounds.md) Delivered by U74 (E2-S3): the test follows its own schemas through weak pointers with a bounded `runtime.GC()` retry; clean under `-race -count=200` beside other packages, so it stays in the fast tier.

## Measurement

Manual, never CI (owner decision); raw harness and per-run rows are kept with the effort's planning notes.

**Setup.** Local Docker Desktop on an arm64 Mac: Linux VM with 23.4 GiB RAM and 10 CPUs, cgroup v2. Every run is `docker run --platform linux/arm64 --cpus=4 --memory=16g --memory-swap=16g` (no swap past the cap), with no `GOMEMLIMIT` and no `--memory-limit`, running `pulse --shard-workers 4 --decode-workers 4 api process-chain -r <chain> --json --no-components` and digesting stdout with `sha256sum`. Binaries are linux/arm64, `CGO_ENABLED=0 -trimpath` with the Makefile ldflags. The archive is built with `pulse synth from-schema` and `pulse shard create` from the casebook retail-at-scale specs at 2× row counts with the panel dropped (the U51 E5-S2 recipe): **47.5M rows, 12 monthly shards, 2.52 GB**. The reference digests come from an unconstrained run (bounded only by the 23.4 GiB VM) and match the U51 E5-S2 answers.

**Metric (owner revision, 2026-11-12).** #361 passes when the run is not OOM-killed (`memory.events oom_kill` 0), its anon peak (`memory.stat anon`, sampled every 200 ms: the process's own memory) stays under 16 GiB, and its digest equals the reference. The planned metric was cgroup `memory.peak` < 16 GiB, but `memory.peak` also counts about 2.5 GiB of reclaimable page cache from reading the 2.5 GB archive: the kernel reclaims that cache under pressure instead of killing, so a run whose process memory fits can still read 16.00 GiB there. `memory.peak` is recorded alongside.

GiB = 2^30. memory.peak / anon peak in GiB, wall in seconds.

| Build | Retention (05): memory.peak / anon / OOM / wall | Spend (06): memory.peak / anon / OOM / wall |
|---|---|---|
| pre-effort (`da19717f6`) | 16.00 / 15.96 / **killed** at 299 s (1/1 runs) | 15.83 / 12.48 / 0 / 90.0 |
| pre-effort + `GOMEMLIMIT=14GiB` | 16.00 / 14.39 / 0 / 984.7 | — |
| post-E1 (`c887d3e72`: #501, #502, #503, #494) | 16.00 / 15.96 / **killed** at 306–449 s (3/3 runs) | 11.46–11.72 / 9.04–9.30 / 0 / 62.0–84.8 |
| post-E1 + `--memory-limit 14GiB` | 16.00 / 13.99 / 0 / 591.4 | — |
| post-E2-S2 (`5330a3642`) | 15.55 / 13.10 / 0 / 161.9 | — |
| **final** (`5330a3642`, rebuilt, byte-identical binary) | 16.00 / 15.03 / 0 / 181.2 and 16.00 / 14.10 / 0 / 191.2 | 11.70 / 9.27 / 0 / 52.5 |

Every finished run returns the reference digest (pre-effort and post-E1 under a soft limit included). **Verdict: #361 passes** — retention 3/3 runs of the final tree, spend 1/1 (and every post-E1 run). Without a soft limit the pre-effort spend chain fits too, but its retention chain is killed even by the 23.4 GiB VM.

**Root cause: GC headroom, not live state.** At post-E1 the retention chain's post-GC live heap peaked at 9.2–9.6 GiB. With `GOGC=100` the next heap goal is about twice the live heap (≈ 18.3 GiB, above the 16 GiB cap), and at a pointer-dense ~10 GB heap on 4 CPUs one concurrent mark took 30–50 s, during which allocation ran on past the goal; the kernel killed the process before the cycle ended. The live state alone fit, which is why every soft-limit run passed.

**Holders** (heap profile at the post-E1 live peak, retention stage 0, all under the per-shard reducer: the merged per-customer state plus up to 4 in-flight shard partials, ≈ 7.8M live group buckets at ≈ 1 KB each):

| # | Holder | In use |
|---|---|---|
| 1 | Per-customer group buckets (`NewGroupBucket`: the aggregator slice, floors and the aggregators it mints) | 4.35 GB (57 %) |
| 1a | — of which 12 `AGG_MAX` state objects a bucket (`active_m01..m12`), one heap object each | 1.82 GB (24 %) |
| 2 | `AGG_DISTINCT_COUNT(order_id)` per-bucket value sets | 1.40 GB (18 %) |
| 3 | Decoded records alive in the shard workers | 0.93 GB (12 %) |
| 4 | Formula attribute values (12 `ATTR_FORMULA` month flags) | 0.39 GB (5 %) |
| 5 | Group keys and the bucket map | 0.62 GB |

**What fixed it.** The E1 and E3 work — #501 (streamed `--json`, no whole-response encode), #502 (merge window = workers, which took the spend chain's anon peak down 2–3 GiB) and #494 (`total()` stays parallel) — made the soft-limited retention run fast (985 s → 591 s under a 14 GiB limit) but left the unlimited run killed. E2-S2 removed holder 1a: `AGG_MAX` / `AGG_MIN` state became one 8-byte word slab-minted with its bucket, cutting a 12 MAX + 1 MIN bucket from 644.7 to 437.9 B (`TestGroupMemory_ExtremumBytesPerGroup`), about 1.6 GB of live state over 7.8M buckets, which brought twice the live heap under the cap. E2-S2 also made `--memory-limit N%` read the cgroup v2 `memory.max`, so a percentage in a container is a share of the container; neither is needed for #361.

**Next holder.** `AGG_DISTINCT_COUNT` value sets (~1.4 GB) are now the largest single holder. The final retention anon peak is 13.1–15.0 GiB, a 1.0–2.9 GiB margin that varies with where the GC cycles land, so a compact exact set is filed as #723 (see [Handed-on](#handed-on)).

**Wall time (#502's no-regression bar).** No regression: spend 52–93 s across post-E1 and final against 88–90 s pre-effort, in the same container. Retention cannot be compared unlimited (pre-effort is killed); under the same 14 GiB soft limit post-E1 took 591 s against 985 s, and the final tree finishes unlimited in 162–191 s.

## Handed-on

| Item | Owner | Why |
|---|---|---|
| #723 compact `AGG_DISTINCT_COUNT` value sets (~1.4 GB holder at 50M) | [U33](U33-v1-release.md) | #361 passes without it; it widens the 1.0–2.9 GiB margin in the 16 GiB box. Engine-wide (aggregator state, merge path, memory estimate), beyond this unit's last story |

## Handed on from U73

Filed from [U73](U73-archive-storage.md#handed-on), then pulled back and closed inside U73 (E5, owner call 2026-11-12); kept here as a record.

- [x] **#720** A shard entry replaced outside Pulse under the same name keeps its carried per-shard distinct counts (add, remove and compact carry a known entry by name), so the memory estimate's grouper bound can fall below the replaced shard's keys. Fix: key the carry on a per-shard CRC (as the index manifest's shard identity does) and recount on a mismatch; blocked on the add rewrite arm's ordering, which writes `_schema.pulse` before the arriving shard's CRC is known. Sites: `internal/encoding/shard_distinct.go` (`NextArchiveDistinct`), `internal/service/shard_admin.go`, `shard_compact.go`, `shard_append.go`. Found in U73 (E3-S1) · [U73](units/U73-archive-storage.md) (landed in U73 (archive-storage, E5-S2): the DIST trailer v2 stamps each shard's size + CRC-32; add, remove and compact carry a count only when the identity matches and recount otherwise; predict gives no bound for a mismatched or v1 trailer)
- [x] **#721** An archive-level Bloom / key-range read for `Lookup`: today the key-range check reads the index manifest once, but every in-range shard costs one `.bloom` sidecar read, which on object storage is one GET per shard. Hold the shards' Blooms (or a merged filter) in one artifact read once per lookup, keeping the shard-identity stamp so a stale filter still means probe. Sites: `internal/service/index_archive.go` (`lookupArchiveHits`), the Bloom sidecar writer, `.claude/reference/byte-layout/archive-index.md`. Found in U73 (E1-S2) · [U73](units/U73-archive-storage.md) (landed in U73 (archive-storage, E5-S3): one `<archive>.<keyhash>.blooms` set per index, read once per lookup, each record identity-stamped so a stale record probes; per-shard `.bloom` files read only when no set exists, and `shard add` migrates them)
- [x] **#722** Predict over a single file with a huge inline dictionary still decodes its schema unbuffered: E4-S2 buffered `Service.Open`, but predict's runtime-side readers (`SchemaLoader`, `CohortCounter`, `DecodeMappable`) use `Service.OpenExact` so predict reads no record, keeping two reads per dictionary entry (about 0.45 s at 600k entries). Buffer them within the schema bytes (a reader that knows the schema end, or a bounded buffer the byte gates accept). Sites: `internal/service/service.go` (`OpenExact`, `headReader`), `internal/service/limits.go`, the facade's predict wiring; gates `TestExplain_FacadeReadsNoRecord`, the `CountRecords` / `Inspect` byte budgets. Found in U73 (E4-S2) · [U73](units/U73-archive-storage.md) (landed in U73 (archive-storage, E5-S4): every schema decode buffers within bounds the decoded lengths prove are schema bytes, so predict reads 111 times instead of ~1.2M at 600k entries and never reaches a record)
