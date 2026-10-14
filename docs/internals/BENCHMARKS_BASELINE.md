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
| Commit | `2554d810` + the matrix-extensions-hardening E8-S1 change; the `linalg` section re-taken at `aa2a1146` + E8-S5 |
| Date | 2026-10-14 |

The FMA-free kernels (`linalg.CoMoment`, the SPD reference kernels) give
the same bits on amd64; their timings do not carry across architectures.

## Files

| File | Benchmarks | Command |
|---|---|---|
| `matrix-bench-baseline.txt` | `BenchmarkMatrixScale*` (`internal/service/matrix_scale_bench_test.go`), default tier 1M rows × p = 50, one run each; `BenchmarkCoMomentAdd` / `BenchmarkCoMomentMerge` (`linalg/comoment_bench_test.go`), p = 8 / 50 / 256, `-count=6` | `go test -run='^$' -bench=MatrixScale -benchmem -count=1 -timeout=0 ./internal/service/` and `go test -run='^$' -bench=CoMoment -benchmem -count=6 ./linalg/` |
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
- **`DecodeWorkers` sweep** (single file): 6.0 s at 1 worker, 0.96 s at
  8, with the peak heap flat at 35–49 MB.
- **`ShardWorkers` sweep** (8-shard archive of the same rows): 6.2 s at 1
  worker, 1.9 s at 8, but the peak heap is 463 MB at 1 worker and
  811–820 MB at 2 or more, against the single file's 35–49 MB (about 58M
  allocations at 2+ shard workers, against 3M for parallel decode). A
  shard archive is read into memory whole (`Service.Open`, by design
  today), and the parallel shard arm allocates per row on top of that.
  Predict's `estimated_bytes` and `MaxEstimatedMemory` count neither.
  This is a known gap that has been handed back to the owner. The ratio
  test's band does not cover it, because that test reads a single file.

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
