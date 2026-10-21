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
