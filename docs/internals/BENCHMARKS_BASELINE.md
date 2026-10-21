# Benchmark baselines

Committed `benchstat` inputs for the in-tree benchmarks. Benchmarks never
run in CI and assert no threshold; these files exist so a local run has
something to compare against:

```sh
make bench > new.txt            # or one package / -bench pattern
benchstat docs/internals/matrix-bench-baseline.txt new.txt
```

Timings are host-specific. Compare only against a baseline taken on the
same machine class and architecture, and re-take the baseline (same
commands, same host) in the PR that moves a number on purpose.

## Recorded host

| | |
|---|---|
| Host | Apple M1 Max, 10 cores, darwin/arm64 |
| Go | go1.27.2 |
| Commit | `2554d810` + the matrix-extensions-hardening E8-S1 change; the `linalg` section re-taken at `aa2a1146` + E8-S5; `BenchmarkMatrixScaleWorkers` re-taken at `9fad347b` + E8-S2 |
| Date | 2026-10-14 |

The FMA-free kernels (`linalg.CoMoment`, the SPD reference kernels) give
the same bits on amd64; their timings do not carry across architectures.

## Files

| File | Benchmarks | Command |
|---|---|---|
| `matrix-bench-baseline.txt` | `BenchmarkMatrixScale*` and `BenchmarkArchiveWorkerScaling` (`internal/service/matrix_scale_bench_test.go`), default tier 1M rows × p = 50, one run each (the `Workers` and `ArchiveWorkerScaling` rows re-taken in U51 E2-S2); `BenchmarkCoMomentAdd` / `BenchmarkCoMomentMerge` (`linalg/comoment_bench_test.go`), p = 8 / 50 / 256, `-count=6` | `go test -run='^$' -bench='MatrixScale|ArchiveWorkerScaling' -benchmem -count=1 -timeout=0 ./internal/service/` and `go test -run='^$' -bench=CoMoment -benchmem -count=6 ./linalg/` |
| `scan-bench-baseline.txt` | `BenchmarkScan_*`, `BenchmarkProcess_StreamingSum*`, `BenchmarkProcess_GroupedByCategorical`, `BenchmarkProcess_StreamingMulti` (`internal/service/scan_bench_test.go`), 10K / 100K / 1M rows | `go test -run='^$' -bench='^(BenchmarkScan_\|BenchmarkProcess_StreamingSum\|BenchmarkProcess_GroupedByCategorical\|BenchmarkProcess_StreamingMulti)' -benchmem -count=1 -timeout=0 ./internal/service/` |

The opt-in tiers (1M × p = 256, 10M × p = 50, 10M × p = 256 —
`make bench-scale`) have no committed baseline: they need ~55 GB of
generated cohorts and tens of minutes, so record them alongside the
change that needs them.

## Matrix scale benchmarks

Cohorts are stream-generated once into `$PULSE_BENCH_DIR` (default
`<os.TempDir()>/pulse-bench`) and reused; the file name carries the
generator version, so a generator change never reads a stale cohort.

Custom metrics:

- `peak-heap-MB` — the peak `HeapAlloc` above the post-GC baseline over
  one untimed pass (`measurePeakHeap`, sampled every 1 ms). Peak live
  heap, not `B/op`, which is cumulative.
- `pred-state-MB` — predict's `estimated_bytes` for the matrix (merge
  blocks × buckets × `accumulator_bytes`), single-file runs only.

What the default-tier numbers show:

- **Predicted state vs measured peak.** Ungrouped pairwise: 21.5 MB
  predicted, ~60 MB peak; grouped by four buckets: 85.8 MB predicted,
  ~152 MB peak. The rest of the peak is decode buffers and transient
  garbage the estimate does not model. `TestMatrixScale_EstimatedBytesRatio`
  pins the ratio in a loose band on a ~100K-row cohort.
- **`DecodeWorkers` sweep** (single file; re-taken in U51 E2-S2):
  6.4 s at 1 worker, 3.2 s at 2, 1.6 s at 4, 1.0 s at 8, with the peak
  heap 35–45 MB.
- **`ShardWorkers` sweep** (8-shard archive of the same rows; re-taken in
  U51 E2-S2, after the archive stopped being read whole — #455): 6.1 s at
  1 worker, 3.3 s at 2, 1.7 s at 4, 1.1 s at 8, with the peak heap 48 MB
  at 1 worker and 61–69 MB at 2 or more (463 MB and 778 MB before U51,
  when `Service.Open` read the archive into memory). The archive is
  opened by its central directory and each shard streams through a
  bounded read window, so the peak is the merge-block state plus one
  shard's window per worker and the per-shard partials. Predict's
  `estimated_bytes` and `MaxEstimatedMemory` charge the largest shard's
  payload, the directory, one schema decode (the canonical entry) and,
  when `ShardWorkers` fans out, the per-shard partial buckets
  (`TestArchiveMemory_EstimatedBytesRatio`).

## Archive worker scaling (U51 E2-S2)

`BenchmarkArchiveWorkerScaling` (`internal/service/matrix_scale_bench_test.go`,
same cached cohorts) runs a projected scalar request — `AGG_SUM` and
`AGG_AVERAGE` over two of the 50 members, grouped by `seg` — so the run
decodes three of the 51 fields. Command:
`go test -run='^$' -bench=ArchiveWorkerScaling -benchmem -count=1 -timeout=0 ./internal/service/`.
Apple M1 Max (10 cores), 1M rows × p = 50, one run each:

| Workers | Archive (8 shards): wall | peak heap | Single file: wall | peak heap |
|---|---|---|---|---|
| 1 | 423 ms | 1.9 MB | 411 ms | 0.04 MB |
| 2 | 268 ms | 3.2 MB | 267 ms | 2.3 MB |
| 4 | 157 ms | 4.5 MB | 177 ms | 2.5 MB |
| 8 | 107 ms | 15.3 MB | 125 ms | 12.3 MB |

At 1 worker both run the serial arm. The per-shard parallel arm now
decodes through the request's projection (#356) — the same plan the
serial arm and parallel decode install. Before it, the same archive
sweep took 638 / 429 / 283 / 196 ms at 1 / 2 / 4 / 8 workers with
546 MB allocated per run against 154 MB now. Scaling flattens past 4
workers on this cohort as the per-worker partials and the merge take a
larger share of a sub-200 ms run; the peak heap at 8 workers is the
per-shard partials and read windows, not the cohort.

After the binned-sum switch (U51 E2-S3; two alternating runs of the
pre-switch and post-switch binaries, `-benchtime=5x`): archive 417 → 447
ms at 1 worker and 108–110 → 114–116 ms at 8 (~1.06×); single file 399–400
→ 425–426 ms at 1 worker and 121–123 → 121–147 ms at 8. Peak heap
unchanged.

## CoMoment benchmarks

`BenchmarkCoMomentAdd` folds a fixed weighted sample (about a fifth of
rows incomplete) into a fresh accumulator; `BenchmarkCoMomentMerge` runs
the fixed merge tree over 64 (16 at p = 256) block partials.

**Listwise `Add` allocates nothing per row (matrix-extensions-hardening
E8-S5).** The fold used to allocate a p-word slice per massed row. It now
walks the variables from last to first so each weighted deviation lives in
a register, and every stored figure stays bit-identical
(`TestCoMomentAddListwise_MatchesPreScratchReference`, a live comparison
against the retained pre-change fold). Re-taken baseline against the
E8-S1 one (`benchstat`, n = 6, same host):

| Listwise `Add` | sec/op | B/op | allocs/op |
|---|---|---|---|
| p = 8 | 228.1 µs → 162.4 µs (−29%) | 203,360 → 480 | 3,173 → 3 |
| p = 50 | 4.71 ms → 3.84 ms (−18%) | 1,337,411 → 10,784 | 3,192 → 3 |
| p = 256 | 14.37 ms → 12.35 ms (−14%) | 1,077,378 → 272,513 | 396 → 3 |

The 3 remaining allocations are the accumulator itself. Pairwise `Add`
and both `Merge` modes are unchanged code; their 2–8% drift between the
two takes is host noise.

## Scan benchmarks

`scan_bench_test.go` compares raw iterator throughput and streaming
aggregation over 1-numeric and 5-numeric + 2-categorical shapes at 10K /
100K / 1M rows. `scan-bench-baseline.txt` is the reference that file's
header cites.

## Reproducible summation benchmarks

`internal/reprosum/bench_test.go` and
`internal/processing/reprosum_proxy_bench_test.go` price the binned
reproducible sum (`internal/reprosum.Sum`, ReproBLAS fold 3) against naive
`+=`, before any aggregator uses it (archive-scale E2-S1). Same host,
go1.27.2, `b3c8a3f3` + E2-S1, 2026-10-20, `-count=3`, median:

| Benchmark | naive | binned | ratio |
|---|---|---|---|
| `ReproSum_Add` in-cache, sum in a register | 1.04 ns/add | 3.84 ns/add | 3.7× |
| `ReproSum_Add` in-cache, through a non-inlined pointer call (`*_state`) | 3.82 ns/add | 4.01 ns/add | 1.05× |
| `ReproSum_RawScan`, 128 MiB of 64-byte rows | 1.47 ns/row | 4.38 ns/row | 3.0× |
| `ReproSum_RawScanGrouped`, 800k groups, random keys | 3.88 ns/row | 22.4 ns/row | 5.8× |
| `ReproSum_EngineProxy`, `UpdateRow` over 2^20 records | 13.7 ns/row | 17.0 ns/row | 1.24× |
| `ReproSum_Merge` | — | 16.7 ns/op | — |

The register-resident naive arm is a floor no engine aggregator reaches
(they accumulate through a pointer per record); the engine proxy is the
end-to-end figure. The grouped raw scan prices the 56-byte state against
an 8-byte float64 (45 MB versus 6.4 MB of accumulators at 800k groups).

Command: `go test -run='^$' -bench=ReproSum -count=3 ./internal/reprosum/ ./internal/processing/`

Since U51 E2-S3 `AGG_SUM` itself is binned: the proxy's `naive` arm is a
local copy of the pre-switch `+=` aggregator and `binned` the real
`sumAggregator` (14.0 vs 16.8 ns/row, `-benchtime=20x`, 1.2×).

### Grouped engine path (U51 E2-S3)

`BenchmarkGroupedSumScale` (`internal/service/grouped_sum_bench_test.go`)
is the stop-rule measurement for switching GROUPED sums: `AGG_SUM` +
`AGG_AVERAGE` grouped by `GROUP_ROUNDED` (interval 1) over a u32 key with
100k or 800k distinct values, 4M rows, non-dyadic mixed-magnitude
measures, through the real serial streaming-grouped path
(`processStreamingGrouped`; at the time `GROUP_ROUNDED` was not
mergeable, so the `decode=8` rows ran the same arm — the fanned-out
figures are the next section). Three alternating runs of the
pre-switch and post-switch test binaries, `-benchtime=3x`, Apple M1 Max:

| Groups | naive (pre-switch) | binned | ratio (medians) | peak heap |
|---|---|---|---|---|
| 100k | 2.70 / 2.75 / 3.20 s | 3.38 / 3.96 / 4.93 s | 1.44× | 204–220 → 230 MB |
| 800k | 9.26 / 10.11 / 10.20 s | 9.83 / 9.88 / 10.59 s | 1.04× | 1560–1754 → 1832 MB |

Allocated bytes per run rise 278 → 289 MB (100k) and 2071 → 2154 MB
(800k): two 56-byte states per group in place of two float64s. The
engine's per-row cost (record decode, map-keyed field lookups, the
group map) dominates; under the owner's 2× stop rule the switch
proceeds. Command:
`PULSE_BENCH_DIR=<dir> go test -run='^$' -bench=GroupedSumScale -benchmem -count=3 -benchtime=3x -timeout=0 ./internal/service/`.

### Grouped engine path, fanned out (U51 E3-S3)

Since E3-S3 `GROUP_ROUNDED` merges, so the same request (4M rows,
`AGG_SUM` + `AGG_AVERAGE`, binned sums) runs on both parallel reducers;
the bench asserts each sub-run's arm before timing it. `decode=N` is
the single file under `DecodeWorkers` N (parallel decode for N ≥ 2),
`shards=N` an 8-shard archive of the same rows under `ShardWorkers` N
(`shards=1` the serial archive scan). Median of three runs,
`-benchtime=3x`, Apple M1 Max (10 cores):

| Groups | Arm | Wall (median) | vs serial | Alloc / run | Allocs / run | Peak heap |
|---|---|---|---|---|---|---|
| 100k | decode=1 (serial) | 3.44 s | 1.00× | 271 MB | 6.70M | 214–222 MB |
| 100k | decode=2 | 2.37 s | 1.45× faster | 933 MB | 19.3M | 210–222 MB |
| 100k | decode=8 | 1.64 s | 2.10× faster | 1257 MB | 22.9M | 566–613 MB |
| 100k | shards=1 (serial) | 3.51 s | 1.00× | 273 MB | 6.70M | 205–219 MB |
| 100k | shards=8 | 1.63 s | 2.15× faster | 1259 MB | 22.9M | 526–586 MB |
| 800k | decode=1 (serial) | 10.28 s | 1.00× | 2008 MB | 25.5M | 1763 MB |
| 800k | decode=2 | 9.93 s | 1.04× faster | 3009 MB | 41.6M | 1763 MB |
| 800k | decode=8 | 10.87 s | 0.95× (slower) | 3942 MB | 51.0M | 1763–1973 MB |
| 800k | shards=1 (serial) | 10.51 s | 1.00× | 2010 MB | 25.5M | 1616–1753 MB |
| 800k | shards=8 | 10.82 s | 0.97× (slower) | 3944 MB | 51.0M | 1648–2218 MB |

The end-to-end grouped binned-merge cost: at 100k groups (40 rows per
group) fan-out pays, 2.1× at 8 workers. At 800k groups (5 rows per
group) every partition mints most of the keys, so the partitions hold
~3–4× the merged bucket count and the per-key binned merge
(`MergeOnline`, 56-byte states) plus that minting cancel the parallel
scan: 8 workers land within ±5% of serial and allocate ~2× the bytes.
Peak heap stays bounded by the incremental merge window (E2-S4).
Command:
`PULSE_BENCH_DIR=<dir> go test -run='^$' -bench=GroupedSumScale -benchmem -count=3 -benchtime=3x -timeout=0 ./internal/service/`.

### Grouped fan-out by cardinality (U51 E3-S4)

Where does the grouped fan-out stop paying? `BenchmarkGroupedSumScale`
sweeps the key count over the same 4M rows. The x-axis is the **share**:
the keys a worker partition is expected to create divided by its rows
(uniform keys: `G·(1 − (1 − 1/G)^n) / n`, n = rows per partition). Cells
are the fan-out's speedup over the serial run (`DecodeWorkers` 8 vs 1,
mean of two runs of `-benchtime=3x`), Apple M1 Max (10 cores). Mixes:
SUM + AVERAGE / `AGG_COUNT` alone / five slots (SUM, AVERAGE, MIN, MAX,
VARIANCE).

Value-keyed (`GROUP_ROUNDED`, interval 1, u32 key):

| Keys | Share | SUM+AVG | COUNT | 5 slots | 8-shard archive, `ShardWorkers` 8 |
|---|---|---|---|---|---|
| 100k | 0.20 | 1.97× | 1.88× | 2.03× | 1.82× |
| 200k | 0.37 | 1.42× | 1.47× | 1.55× | 1.46× |
| 300k | 0.49 | 1.21× | 1.31× | 1.26× | 1.23× |
| 400k | 0.57 | 1.10× | 1.21× | 1.17× | 1.12× |
| 600k | 0.68 | 1.07× | 1.07× | 1.10× | 0.99× |
| 800k | 0.74 | 1.01× | 1.05× | 0.96× | 1.01× |

Fewer workers gain less at a given cardinality: SUM + AVERAGE on
`DecodeWorkers` 2 runs 1.38× (100k) down to 1.07× (800k), on 4 workers
1.88× down to 1.06×.

Dictionary-keyed (`GROUP_CATEGORY` over a `categorical_u32` holding
every key):

| Keys | Share | SUM+AVG | COUNT | 5 slots |
|---|---|---|---|---|
| 100k | 0.20 | 1.86× | 1.99× | 2.17× |
| 200k | 0.37 | 1.56× | 1.75× | 1.75× |
| 300k | 0.49 | 1.40× | 1.63× | 1.48× |
| 400k | 0.57 | 1.29× | 1.52× | 1.38× |
| 600k | 0.68 | 1.20× | 1.42× | 1.22× |
| 800k | 0.74 | 1.15× | 1.34× | 1.18× |
| 1.6M | 0.86 | 1.06× | 1.18× | 1.11× |
| 3.2M | 0.92 | 1.05× | 1.04× | 1.04× |

Allocation: past share 0.57 the fanned-out run allocates 2–2.8× the
serial run's bytes (value-keyed SUM + AVERAGE at 800k: 3.94 GB vs
2.01 GB), falling to 1.35× at share 0.92 (categorical 3.2M: 8.88 vs
6.59 GB); peak heap is within ~20%.

So the fan-out stays on whatever the cardinality. The value-keyed
groupers stop gaining at a share of about 0.7, but predict cannot see
their key count up front (it depends on the values). The groupers whose
count the schema states (dictionaries) still gain at a share of 0.92. A
cardinality rule that kept such requests serial was measured and
dropped. The categorical rows were measured without the dispatch's
second schema read (the fix below; re-checked at 400k and 800k keys
against the final code, within 3%); before it, each fanned-out
categorical run paid ~0.45 s more at 600k entries.

**Record count on the parallel-decode dispatch.** The dispatch used to
count a single file's records with `CountRecords`, which decodes the
schema again — two unbuffered reads per inline dictionary entry. It now
takes the count from the file length less the header and schema bytes
`Open` read (`openedRecordCount`, same floor division). On a
600k-entry categorical file that took ~0.45 s off every
`DecodeWorkers` ≠ 1 Process (`TestOpenedRecordCount_NoSecondSchemaRead`).

## Retention-shaped chain (U51 E5-S4, E5-S5)

`BenchmarkRetentionShape` (`internal/service/retention_shape_bench_test.go`)
runs the retail showcase's two `ProcessChain`s at 1/20 scale: a
12-shard archive of 2.52M order lines (one shard a month, 33-byte rows)
over 1.2M customer ids (1,053,033 present), `ShardWorkers` 4,
components off. `retention` stage 0 carries 13 row-local attributes
(`ATTR_DATE_PART` + 12 `ATTR_FORMULA` month flags) and 15 aggregations
per customer; `spend` carries 3 aggregations and no attribute.
`retention-budget` is `retention` under `MaxEstimatedMemory` 35 GB,
which shrinks the merge window from 8 to 2. One shape per process
(generate the cached archive with a first run, then measure):
`/usr/bin/time -l ./service.test -test.run '^$' -test.bench 'BenchmarkRetentionShape/retention$' -test.benchtime 1x`
with `PULSE_BENCH_DIR` set. Each process runs the chain twice (the
timed loop and the peak-heap sample), so wall and CPU are per process,
ns/op per chain. Apple M1 Max (10 cores), go1.27.2, one run each:

| Commit | Shape | ns/op | peak heap | alloc / op | allocs / op | wall | CPU | max RSS |
|---|---|---|---|---|---|---|---|---|
| `632292f6` (before) | retention | 9.03 s | 3,534 MB | 9.38 GB | 151.0 M | 18.6 s | 332% | 4.87 GB |
| `632292f6` (before) | spend | 3.99 s | 1,668 MB | 3.39 GB | 44.0 M | 8.6 s | 219% | 1.89 GB |
| + (a) attribute slots | retention | 9.29 s | 3,538 MB | 7.67 GB | 135.9 M | 20.6 s | 287% | 5.03 GB |
| + (b) pooled expr VM | retention | 8.78 s | 2,998 MB | 6.70 GB | 105.7 M | 19.4 s | 266% | 4.97 GB |
| + (c) merge window | retention | 7.84 s | 2,799 MB | 6.70 GB | 105.7 M | 16.2 s | 303% | 4.66 GB |
| + (c) merge window | retention-budget | 11.67 s | 3,589 MB | 6.70 GB | 105.7 M | 24.4 s | 193% | 4.72 GB |
| + (c) merge window | spend | 3.80 s | 1,627 MB | 3.35 GB | 42.9 M | 7.9 s | 231% | 1.99 GB |
| `c89ecc9d`, same session (before E5-S5) | retention | 8.67 s | 2,743 MB | 6.70 GB | 105.7 M | 18.2 s | 275% | 4.56 GB |
| `c89ecc9d`, same session (before E5-S5) | spend | 3.91 s | 1,694 MB | 3.35 GB | 42.9 M | 8.2 s | 221% | 1.94 GB |
| + E5-S5 allocation-free limit check | retention | 8.17 s | 3,048 MB | 6.45 GB | 102.5 M | 17.0 s | 289% | 4.13 GB |
| + E5-S5 allocation-free limit check | spend | 4.30 s | 1,591 MB | 3.10 GB | 39.7 M | 8.8 s | 210% | 1.97 GB |

(a) and (b) cut retention's garbage by 29% (allocations a chain 151 M →
106 M); (c) changes nothing without a budget (the default). Wall time
and peak heap move within run-to-run noise on this host. Max RSS stays
~2.3× spend's (4.66 vs 1.99 GB), against a target of 1.5×: the peak is
LIVE state, not garbage. Stage 0 alone at 4 workers (live heap after a
GC, components off): at the last shard merge the merged buckets hold
1,171 MB for retention vs 602 MB for spend (1.9×), and the finalized
response holds 1,327 MB vs 395 MB (3.4×; ~1.26 KB a customer row,
almost all of it `FinalizeGroupedStream`'s per-row `Data` maps of 18
columns). Both are alive together while the response is built, and
the chain's next stage reads that response. Shrinking the merge
window does not reach either: at window 2 max RSS is unchanged and the
run is slower (193% CPU). The predict estimate is far above the
measured peak for this shape (33.4 GB at one live partial, 39.6 GB at
the full window): `GROUP_CATEGORY` over a `u32` has no schema-known
cardinality, so every bucket term is bounded by the record count.

E5-S5: `limits.Value` read a field through a func-table accessor taking
`*Limits`, so the `Limits` value every limit check is handed escaped to
the heap, one ~100 B allocation per `MaxGroups` check at each bucket
mint. With a direct switch the check allocates nothing
(`TestGroupMemory_MaxGroupsCheckAllocationFree` and the grouped-mint
siblings). Per chain, retention drops 3.17 M allocations and 254 MB
(6.70 → 6.45 GB), spend 3.17 M and 254 MB (3.35 → 3.10 GB): the same
count on both, one check per bucket mint across the shard partials and
the merge. The "before" rows are a same-session build with only the
escaping accessor restored, since run-to-run noise on this host is
large: a repeat retention run measured 4.79 GB (before) and 4.84 GB
(after) max RSS. Wall time, peak heap and max RSS move within that
noise; the live-state peak above (#499, `docs/roadmap/TODO.md`) is
untouched.

E5-S6: the estimate now bounds a data-dependent grouper by the width of
the integer or set word it keys on (16 keys for `u4`, 256 for `u8`,
65,536 for `u16`), not the record count. That bound is attained
(`TestLimits_ValueWidthBoundCoversEveryKey`), so it can never
under-estimate. It does not reach this shape: `customer_id` is a `u32`,
whose 2^32 values exceed the 2.52M records. Header, schema and sidecars
hold no provable distinct-customer count, so the retention estimate
stays 33.4 GB at one live partial (2.52M buckets charged against
1,053,033 present, at 12,800 bytes a bucket against ~2,400 measured with
components off). Estimate / peak on the memory fixtures after the change
(Apple M1 Max, go1.27.2):

| Fixture | Estimate | Peak heap | Estimate / peak |
|---|---|---|---|
| `TestGroupMemory_EstimateBoundsPeak` unweighted (50,000 `u32` keys) | 189.8 MB | 124.0 MB | 1.53 |
| `TestGroupMemory_EstimateBoundsPeak` weighted | 226.4 MB | 185.3 MB | 1.22 |
| calibration `streaming grouped` (16-entry dictionary) | 153.6 MB | 80.9 MB | 1.90 |
| calibration `streaming value-width grouped` (`u16`, 65,536 charged, 4,096 present; 563 MB before) | 287.8 MB | 88.4 MB | 3.26 |
| retention stage 0 (`u32` customer id) | 33.4 GB | ~3.0 GB | ~11 |

E5-S7: two changes to the per-bucket charge, each a measured ceiling
pinned by a test that measures it. (C) A built-in aggregator's state is
its own entry in a per-operator table (`AGG_MIN` / `AGG_MAX` 96 bytes,
`AGG_SUM` 128, `AGG_DISTINCT_COUNT` 320, …), checked for every
registered built-in by `TestGroupMemory_AggregatorStateTable`; an
extension keeps 256 / 512. (B) With components off, a merged bucket's
output is the row alone, 512 + ~96 bytes a column, in place of
1,024 + 576 a column (`TestGroupMemory_FinalizedRowBytes`). Measuring
every built-in also raised three charges the flat 256 bytes had hidden:
the `AGG_VEC_*` fragments, 128 bytes per extra grouper, and the
components-on column (512 → 576). Retention stage 0 is now charged
3,904 bytes a bucket (state 1,952 + output 1,952) against 12,800.

The retention rows below are this build's `BenchmarkRetentionShape`
runs, components off, `ShardWorkers` 4 (peak heap is the sampled
`HeapAlloc` high-water mark of the whole chain, max RSS from
`/usr/bin/time -l`); `retention-budget` now sets 11.2 GB, which still
shrinks the merge window from 8 to 2. Estimate / peak, Apple M1 Max,
go1.27.2:

| Fixture | Estimate | Peak heap | Estimate / peak |
|---|---|---|---|
| `TestGroupMemory_EstimateBoundsPeak` unweighted | 179.8 MB | 124.1 MB | 1.45 |
| `TestGroupMemory_EstimateBoundsPeak` weighted | 244.7 MB | 185.3 MB | 1.32 |
| `TestGroupMemory_EstimateBoundsPeak` unweighted, components off | 86.8 MB | 35.5 MB | 2.45 |
| `TestGroupMemory_EstimateBoundsPeak` weighted, components off | 151.6 MB | 73.9 MB | 2.05 |
| calibration `streaming grouped` (16-entry dictionary) | 153.6 MB | 80.8 MB | 1.90 |
| calibration `streaming value-width grouped` | 283.6 MB | 89.1 MB | 3.18 |
| calibration `matrix` (grouped) | 153.7 MB | 81.2 MB | 1.89 |
| calibration `fan-out value state` | 268.8 MB | 87.9 MB | 3.06 |
| retention stage 0, one live partial | 10.55 GB | 3,431 MB | 3.07 |
| retention stage 0, full window (8) | 13.56 GB | 3,431 MB | 3.95 |
| retention-budget stage 0, window 2 | 10.98 GB | 3,594 MB | 3.06 |
| spend stage 0, one live partial | 4.50 GB | 1,447 MB | 3.11 |

Every other `TestMemoryEstimate_Calibration` case moved by less than
1% (none of them is high-cardinality) and stays 1.20–4.85. Retention max
RSS was 4.51–5.04 GB over two runs (`retention-budget` 4.37 GB), spend
1.69 GB; wall time and allocations are unchanged. Most of what is left
of the retention gap is the bucket bound: 2.52M buckets charged against
1,053,033 present (2.4×), which no header-only figure can tighten. A
recorded distinct-count statistic would (TODO #500).
