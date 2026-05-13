# Performance Notes

**Audience:** operators sizing a Pulse deployment, and library users
debugging memory or latency surprises.

Pulse is built to keep "the streaming path" the default for most
analytical requests. When the engine has to leave that path it says so
— via the `Streamable` flag in
[`pulse api predict`](../cli/api-predict.md) — and falls back to a
buffered execution. This page tells you what stays streaming, what
buffers, and how to read predict's diagnostics.

> **LLM agents using MCP:** there is no direct skill counterpart for
> this page — `debugging-with-predict` covers how to drive predict;
> this page tells operators what predict's answers imply.

## Streaming path: what stays out of memory

The streaming `Process` path covers four orchestrator modes (from
[CLAUDE.md → What streams today](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md#what-streams-today)):

- **Single-pass streaming.** No-group requests with online aggregators
  (`COUNT`, `SUM`, `AVG`, `STDDEV`, `VARIANCE`, `RANGE`, `FREQUENCY`,
  `MODE`, `SKEWNESS`, `KURTOSIS`, `DISTINCT_COUNT`) on numeric
  (non-decimal) fields. Row-local attributes (`FORMULA`, `DATE_PART`)
  apply inline.
- **Grouped streaming.** Groupers implementing the streaming key path
  (`GROUP_CATEGORY`, `GROUP_RANGE`, `GROUP_ROUNDED`, `GROUP_H3_CELL`)
  drive per-key online aggregator buckets. Memory is
  `O(distinct_groups × per-aggregator-state)`.
- **Two-pass streaming.** Two-pass attributes (`ATTR_ZSCORE`,
  `ATTR_TSCORE`, `ATTR_NORMALIZED`) compute population stats via
  Welford-Pébaÿ pass 1, then emit per-row values in pass 2.
- **Streaming features.** Every registered `FEAT_*` operator
  implements the streaming computer interface and composes with the
  three modes above.

These paths benefit from three optimisations landed during the streaming
refactor (commit `cdd72d5`): record reuse (the same record buffer flows
through the pipeline), zero-allocation decoding into reused buffers,
and an mmap reader for `.pulse` files large enough to benefit from
demand paging.

## Buffered path: when Pulse has to materialise

`pulse api predict` reports `Streamable=false` and lists every
buffering reason. The current set, from CLAUDE.md:

- `AGG_MEDIAN`, `AGG_PERCENTILE`, and `AGG_ZSCORE` — require sorts or
  summed deviations.
- `ATTR_PERCENTILE` — sorted view of every value; no streaming
  algorithm preserves exact rank.
- `GROUP_QUANTILE`, `GROUP_DATE` — finalize-time work over the full
  set.
- Window operators (`WIN_*`) — operate on a sorted post-aggregate row
  set.
- Decimal-typed field aggregations — precision-preserving path.
- Geo aggregations (`AGG_GEO_BBOX`, `AGG_GEO_CENTROID`) — typed
  buffered path.
- Two-pass attributes combined with features or groups — orchestration
  matrix not yet extended.
- Tier-1 statistical tests combined with groupers, features, or
  two-pass attributes — same orchestration limit.
- Tier-2 post-tests (`req.PostTests`) — always run after the result
  set is materialised, regardless of `TestType`.

## Reading predict output

```bash
pulse api predict --request request.json --json | jq '.data | {streamable, streamable_reasons}'
```

```json
{
  "streamable": false,
  "streamable_reasons": [
    "AGG_MEDIAN on field price"
  ]
}
```

If `streamable_reasons` is empty and `streamable=true`, the request
executes without buffering. Each reason is a one-line gate that pushed
the request to the buffered path; you can drop or substitute the
offending operator (e.g., `AGG_AVG` instead of `AGG_MEDIAN`) and
re-run predict.

## Memory rules of thumb

| Path | Memory profile |
|---|---|
| Single-pass streaming | Constant — `O(aggregator state)` |
| Grouped streaming | `O(distinct_groups × per-aggregator state)` |
| Two-pass streaming | Constant; cost is 2× iter scan (typically OS-page-cached) |
| Buffered | `O(filtered_rows × output_width)` for the working set, plus per-operator state |

## Concurrency

`pulse.ComposeParallel` (CLI: `pulse api compose --parallel N`)
fans `ComposedRequest` slots over a bounded worker pool. Workers share
the engine's read-only registries; each `Process` call constructs
fresh stateful operators per request, so concurrent execution is
safe. Defaults: `MaxWorkers = GOMAXPROCS`, `FailFast = true`. See
[Parallel Compose](../library/parallel-compose.md).

## When to embed vs shell out

For high-throughput pipelines, embed Pulse directly via the Go library
— you avoid one process boundary per request and can stream rows
through your own writer with `ProcessStream`. For ad-hoc analysis,
JSON-in/JSON-out via `pulse api process --json` is faster to write
and easier to debug.
