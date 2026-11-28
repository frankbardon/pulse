# Memory Gates

A memory gate is a test that reads the Go heap (`runtime.ReadMemStats`,
`runtime/metrics` or `testing.AllocsPerRun`) and fails when a bound
regresses: bytes a group, allocations a row, a peak against the memory
estimate. Most live in `internal/service`. This page explains how they
measure, what makes one trustworthy, and records the last audit: the
regression injected into each gate and whether the gate caught it.

## How the gates measure

| Measure | Helper | What it sees | GC timing |
|---|---|---|---|
| Live heap after two collections | `liveHeap`, `measureAggMemory`, `retainedPerRecord` | what is still reachable | deterministic: two `runtime.GC()` calls clear a `sync.Pool` victim cache too |
| Bytes or objects allocated | `totalAlloc`, `allocDuring`, `allocatedBytes`, `Mallocs`, `AllocsPerRun` | everything allocated, garbage included | deterministic per build; process-wide, so the test runs serially |
| Live peak, probed | `liveHeapPeakDuring` (at each I/O call), `peakLive` (every 1% of heap growth, under `GODEBUG=gcstoptheworld=1`) | the most fn held at once | exact under a stop-the-world collector |
| Sampled `HeapAlloc` peak | `measurePeakHeap`, `minPeakHeap` | live data plus garbage not yet collected | depends on the GC goal, so on whatever else is live in the process |

The sampled peak is the weakest of the four. It counts garbage up to the
GC goal, and the goal grows with the live heap. A fixture kept alive
next to the measured run therefore inflates the peak. During the audit,
a 48 MB in-memory archive kept alive beside a matrix run raised that
run's peak from 18 MB to 47 MB. Gates that use the sampled peak compare
it against loose bands. Keep each fixture alive only for the cases that
read it.

## Writing a memory gate

- Run the gate serially, with a `// serial:` reason. The figures are
  process-wide.
- Measure the state you claim to bound and nothing else. Create the
  fixture before the `before` reading, and keep the measured state
  reachable (`runtime.KeepAlive`) until after the `after` reading.
- Set the bound from a measured figure with stated headroom. Record the
  measurement (value, architecture and Go version) in a comment beside
  the bound.
- Falsify the gate: inject the regression it guards against, confirm
  that it fails, then restore the code. Add a row to the audit table
  below.
- Count partials, buckets or buffers by reachability (weak pointers),
  not by bookkeeping. Register each object when it is created, not when
  it reaches the code you expect to release it. A pile-up happens
  before that point.
- If a gate takes 1 s or more, put it in a `*_heavy_test.go` file.

## Audit (E7-S3)

Each row names the gate, what it measures, the one-line regression
injected into production code, and the result. Every injection was
reverted after its run. Measured on darwin/arm64, Go 1.26.

| Gate | Measures | Injected regression | Result |
|---|---|---|---|
| `TestGroupMemory_PartitionBytesPerGroup` | live B/group of a grouped partition (≤ 512) | `GroupBucket` +96 B | caught: 518 |
| `TestGroupMemory_ExtremumBytesPerGroup` | live B/group, 13 extremum slots (≤ 480) | `GroupBucket` +96 B | caught: 534 |
| `TestGroupMemory_DistinctCountBytesPerGroup` | an `AGG_DISTINCT_COUNT` slot's B/key (≤ 160) | inline set off (`distinctInlineMax` 0); threshold 2 | caught: 253; caught at 4 values: 257 |
| `TestGroupMemory_MergedBytesPerGroup` (heavy) | merged state B/group (≤ 512) | the merger keeps each absorbed partition | **missed** (474); **fixed**: also bound by one partition's figure + 16 B, now caught |
| `TestGroupMemory_LivePartialsBoundedByWindow` | unmerged partials reachable (≤ window) | dispatch window off; every partial kept to the end | **missed** both (it registered a partial only at its merge); **fixed**: registered at scan completion (`shardScannedHook`); now caught: 39 alive |
| `TestGroupMemory_FinalizeReleasesBuckets` | bucket state the partial keeps after finalize (≤ 25%) | grouped tail never deletes a bucket | caught: 1074 of 1137 |
| `TestGroupMemory_VectorBufferedRows` | B per buffered vector row ≤ its charge | row member charge 24 → 8 | caught at p = 16, 64 |
| `TestGroupMemory_AggregatorStateTable` (heavy) | every built-in's slot bytes ≤ its table entry | `distinctCountAggregator` +64 B | caught: 193–225 over 176 |
| `TestGroupMemory_FinalizedRowBytes` (heavy) | state + output ≤ `BucketBytes` | `aggOutputBytes` 576 → 400; `bucketOutputOffBytes` 512 → 128 | caught (7 cases; 455 over 384) |
| `TestGroupMemory_EstimateBoundsPeak` (heavy) | estimate ≥ sampled peak, ≤ factor | per-aggregation output term dropped | caught: estimate 0.90 × peak |
| `TestArchiveMemory_DirectoryTermBoundsOpen` | resident / directory charge in [0.45, 1.0] | `directoryCopyFactor` 8 → 16; 8 → 4 | caught: 0.40; 1.69 |
| `TestArchiveMemory_EstimatedBytesRatio` (heavy) | sampled peak / estimate in [0.3, 1.2] | schema-decode term dropped | **missed** (the 11 MB dictionary sat inside the run base); **fixed**: 250,000-entry dictionaries, now caught: 2.13; dictionary archive dropped before the matrix cases |
| `TestShardScan_SkipsShardDictionaries` | bytes a run allocates over a dictionary archive | every shard schema decoded with its dictionary | **missed** (61 MB under an 80 MB bound); **fixed**: bound 1.5 decodes (17 MB), now caught |
| `TestShardParallel_DecodeAllocsPerRow` | allocations a row, per-shard arm | one more allocation a decoded row | **missed** (4.4 under 10); **fixed**: bound 4, now caught |
| `TestShardParallel_ProjectedDecodeAllocatesLess` | projected bytes < half the full decode | `shardDecodeFor` drops plan and keep | caught: 616 vs 616 B/row |
| `TestShardStream_AnchoredShardStreamsSection` | bytes < half the anchored shard | `anchorSectionSize` branch off | caught: 2.2 MB over 0.8 MB |
| `TestArchiveOpen_AllocsIndependentOfBytes` | open-path bytes independent of payload | `archiveFromFile` reads the archive whole | caught: all 11 surfaces |
| `TestOpenRecordLocator_SkipsDictionaries`, `TestRecordCountFromBytes_SkipsDictionaries`, `TestSkipShardPreamble_SkipsDictionaries` | bytes ≤ 64 KiB | `ReadSchemaLayout` → `ReadSchema` | caught: ~12 MB each |
| `TestShardStream_CreateMemoryBoundedBySource` | create bytes / live peak vs one source | every source read and held | caught: both arms |
| `TestShardStream_MutationAllocationBoundedByShard` | add / remove / compact bytes vs the archive | `archiveFromFile` reads the archive whole | caught: all three verbs |
| `TestShardStream_FlattenMemoryBoundedByShard` (heavy) | flatten bytes / live peak vs the archive | staged stream and spool buffer the whole output | caught: all three arms |
| `TestShardStream_FilterSingleFileMemoryBounded` (heavy) | copy bytes / live peak vs the input | staged stream buffers the whole output | caught: all four arms |
| `TestAttributeSlots_AllocsFlatInAttributeCount`, `TestAttributeSlots_ProcessAllocsFlat` | allocations a row flat in attribute count | `attributeLabels` reserves nothing | caught: every arm, +3 a row |
| `TestCtxPoll_JoinBuildCancelled` | objects a cancelled join allocates | the build loop's ctx poll off | caught: 900,564 over 25,000 |
| `TestJoinShapeFixture_BufferedRetainedRatio`, `_DecodeAllocsRatio` | positional / map record bytes and allocations | one more retained 64-byte allocation a record | caught by projected4 (0.675, 0.445); the full case alone admits it |
| `TestChainTotals_PeakMemoryNotWorseThanSerial` (heavy) | parallel chain sampled peak ≤ 1.1 × serial | the per-shard arm keeps every decoded record | caught: 195 over 129 MB |
| `TestChainStageTotals_PeakMemoryNotWorseThanSerial` (heavy) | two-phase stage peak ≤ 1.1 × serial | the two-phase scan keeps every row's record | caught: 333 over 225 MB |
| `TestCrosstabMemory_CellChargeTable` (heavy) | stop-the-world live peak ≤ the cell charge | `AGG_COUNT` cell 976 → 700 | caught: 876 over 822 B a cell |
| `TestMatrixScale_EstimatedBytesRatio` (heavy) | sampled peak / matrix state in band | bucket factor dropped; block count halved | bucket: caught by pairwise (4.92 > 3), listwise admits it (5.98 < 8); halved blocks: admitted (a factor of 2 is inside the band) |
| `TestLimitsReleaseMemory` (heavy) | B/record a tripped run retains (≤ 2) | materialized records kept past the trip | caught: 162 B/record |
| `TestMemoryEstimate_Calibration` (heavy) | estimate ≥ sampled peak, ≤ factor | `slotBytes` 10 → 4 | caught by the join cases |

Benchmarks that call `measurePeakHeap` (`BenchmarkBufferedRecordFootprint`,
`BenchmarkCrosstabWideCohort_*`, `BenchmarkCrosstabSetFanoutOverlay`)
only report a metric and fail nothing. `BenchmarkJoinShape_BufferedPeakHeap`
fails on its ratio, but only under `make bench`.
