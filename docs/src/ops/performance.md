# Performance Notes

**Audience:** operators sizing a Pulse deployment, and library users
debugging memory or latency surprises.

Most requests stream: Pulse reads each record once and keeps only
operator state. A request that needs every row at once buffers instead,
and [`pulse api predict`](../cli/api-predict.md) says so before it runs.

> **LLM agents using MCP:** `tool-predict` covers driving predict, and
> `streaming-and-watching` lists what forces a buffered run.

## Streaming or buffered

Streaming covers ungrouped and grouped requests over online aggregators
(count, sum, mean, variance, frequency and the like), row-local and
two-pass attributes (`ATTR_ZSCORE` and kin take a second scan), features,
and requests that carry only a matrix, a tier-1 test or a regression.
Grouped streaming holds one aggregator state per distinct group.

A request buffers when an operator needs a sorted or complete view:
medians and percentiles, `ATTR_PERCENTILE`, `ATTR_PREVIOUS_STATE`,
`GROUP_QUANTILE` / `GROUP_DATE` / `GROUP_KMEANS`, every window, built-in
`decimal128` aggregation, tier-2 tests, buffered matrix specs, and some
combinations (a tier-1 test beside a grouper, for example). The
authoritative list is the `streaming-and-watching` skill
([guide copy](../guide/skills/streaming-and-watching.md)); predict and
the engine read the same rule.

```bash
pulse api predict --request request.json --json | jq '.data | {streamable, streamable_reasons}'
```

```json
{
  "streamable": false,
  "streamable_reasons": [
    "aggregation AGG_MEDIAN is not streamable"
  ]
}
```

Each reason names one operator or combination; drop or substitute it
(`AGG_AVERAGE` for `AGG_MEDIAN`) and predict again. Predict also flags a
request whose estimated memory or size would pass a configured limit
([Tuning Limits → Predict findings](../library/tuning-limits.md#predict-findings-certain-and-possible)).

| Path | Memory |
|---|---|
| Ungrouped streaming | Constant: aggregator state |
| Grouped streaming | Distinct groups × aggregator state |
| Two-pass attributes | Constant; one extra scan per attribute layer |
| Buffered | Filtered rows × fields read, plus operator state |

## Store cohorts for fast scans

- **Sort a denormalised join by its parent key.** The decoder skips
  fields whose bytes did not change from the previous row, so a parent
  block repeated over its child rows decodes about 1.5x faster sorted
  (synthetic 95-field, 12.5x-fanout join). On unsorted data it stops
  comparing and costs within a few percent. Results are identical.
- **Group the parent block.** A [parent group](../format/parent-groups.md)
  stores each parent tuple once. A row whose parent did not change costs
  nothing for the parent block, so a projection costs the same at any
  parent width, and a filter on parent fields is evaluated once per
  distinct parent rather than once per row.
- **Shard large cohorts** into a [shard archive](../internals/managing-shard-archives.md):
  shards scan in parallel.

## Parallelism

- **Decode and shard workers.** `--decode-workers` (`Options.DecodeWorkers`)
  splits a large single file into segments; `--shard-workers`
  (`Options.ShardWorkers`) scans shards in parallel. `0` is one worker
  per CPU, `1` serial. Results are bit-identical at every worker count.
  Detail: [Tuning Limits → Worker counts](../library/tuning-limits.md#worker-counts).
- **Parallel compose.** `(*Pulse).ComposeParallel` (CLI:
  `pulse api compose --parallel N`) runs compose slots on a bounded
  worker pool; defaults `MaxWorkers = GOMAXPROCS` and fail-fast
  (`CollectErrors = false`; the CLI's `--no-fail-fast` sets it)
  ([Parallel Compose](../library/parallel-compose.md)).

## Embed or shell out

For high-throughput pipelines, embed the library: no process boundary
per request, and `ProcessStream` hands rows to your own writer
([Streaming](../library/streaming.md)). For ad-hoc analysis,
`pulse api process --json` is quicker to write and debug.

## Benchmarks

Reproducible benches back the figures above and catch regressions
(`BenchmarkExprCompileOnce`, `BenchmarkTransfer` and the
`Response.Components` pair below). Components baselines (Apple M1 Max,
in-memory cohorts; the process pair is a 100K-record `f64` cohort under
five aggregators, the crosstab pair a 200-field × 10K-row cohort with
full margins; median of three runs):

| Bench | ms/op | MB/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkProcess_BufferedComponents` (`/`) | 30.16 | 49.25 | 600,122 |
| `BenchmarkProcessStream_WithComponents` (`/`) | 30.44 | 49.38 | 600,487 |
| `BenchmarkCrosstabWideCohort_Fused` (`/service/`) | 8.69 | 21.07 | 143,903 |
| `BenchmarkCrosstabWideCohort_Buffered` (`/service/`) | 9.08 | 22.03 | 83,978 |

A sustained regression above 5% on any line warrants investigation.

```sh
go test -bench='BenchmarkProcessStream_WithComponents|BenchmarkProcess_BufferedComponents' -run='^$' -count=3 -benchmem ./
go test -bench='BenchmarkCrosstabWideCohort_(Fused|Buffered)' -run='^$' -count=3 -benchmem ./internal/service/
```
