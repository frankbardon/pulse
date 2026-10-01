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
  (`GROUP_CATEGORY`, `GROUP_RANGE`, `GROUP_ROUNDED`) drive per-key
  online aggregator buckets. Memory is
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

**Sorted cohorts decode faster, automatically.** On the record-reuse
paths the decoder compares each field's bytes against the previous
row's and skips rewriting the ones that did not change. A cohort that
is a denormalised join — a parent block repeated across each parent's
child rows — pays off when it is stored SORTED by the parent key: on a
synthetic 95-field, 12.5x-fanout join the full-row reuse decode runs
about 1.5x faster sorted. There is nothing to declare and no option;
on unsorted data the decoder notices the low repeat rate and stops
comparing, so the cost there stays within a few percent. Results are
identical either way. If you control the import, `ORDER BY` the parent
key.

**Parent-grouped cohorts decode in time independent of the parent
block's width.** A cohort whose parent block is stored once per parent
(a `0x02` parent group) is scanned without rebuilding each row: a row
whose parent did not change costs nothing for the parent block, and a
changed parent is copied in from decoded values cached per parent
(bounded to 8 MiB per reader). On a synthetic 12.5x-fanout join with a
29-field child block, the full-row streaming scan runs 1.2x (10 parent
fields) to 1.4x (80) faster than the ungrouped file when sorted, and up
to 2.2x faster unsorted; a four-field projection costs the same at every
parent width, 2.5–3.3x under the ungrouped file. Results are identical.

### Filters on grouped cohorts

A filter whose fields all belong to ONE parent group is evaluated once
per distinct parent tuple, not once per row: its verdict per dictionary
entry is computed the first time a row carrying that entry reaches it,
then every later row is a bit test on its entry index. Every built-in
filterer qualifies, and so does `FILTER_EXPRESSION` when it calls only
pure functions; a filter mixing a parent field with a child field (or
two groups) is evaluated per row as before. Results and
`Response.Components` filterer counts are identical either way.

On the synthetic 12.5x-fanout join (66 parent / 29 child fields,
100,000 rows, 8,000 parents) the filter does 8,000 evaluations instead
of 100,000. A cheap `FILTER_INCLUDE` on a parent categorical goes from
~35 to ~22 ns/row for the filter pass (sorted; 43 → 30 scattered) —
invisible end to end, where decode dominates. A `FILTER_EXPRESSION` on a
parent field goes from ~95 to ~19 ns/row for the filter pass (sorted;
~110 → ~24 scattered) and ~640 → ~580 ns/row end to end. (Before
expressions compiled once — below — the same filter went from ~7 µs to
~0.6 µs/row.) The verdict table costs two bits per dictionary entry per filter (27 KB
for a 109,000-entry group). The ungrouped (`0x01`) cohort and child-field
filters are unchanged.

### Expressions compile once

`FILTER_EXPRESSION` and `ATTR_FORMULA` compile their expr-lang program
once per request build — against a prototype of the schema, typed as the
row values are — and run the cached program per row, with an env holding
only the fields the expression names. They used to compile per row
against the whole row, which cost more the wider the schema. On the
synthetic 12.5x-fanout join (95 fields, 100,000 rows, filter precompute
off), `p_10 > 1.5 && c_u64_00 < 549755813888` went from ~8.7 µs to ~0.14
µs/row for the filter pass, and ~24 µs to ~2.1 µs/row end to end (0x01;
~24 → ~1.1 on 0x02), against ~1.9 / ~1.0 µs/row for the same request
with a `FILTER_RANGE` instead. `ATTR_FORMULA` `p_10 * 2 + c_u64_00 /
1024`: ~9.1 µs → ~0.3 µs/row through the Processor, ~24 → ~2.2 µs/row end
to end. Results are identical. Bench: `BenchmarkExprCompileOnce`.

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

## Components emission baselines

Always-on `Response.Components` emission baselines (Apple M1 Max, `go1.x`,
hermetic `afero.NewMemMapFs()` cohorts). The buffered/streaming pair drives a
100K-record single-`f64` cohort through a five-aggregator mix covering both
mergeable (`AGG_SUM` / `AGG_COUNT` / `AGG_AVERAGE` / `AGG_VARIANCE`) and
non-mergeable (`AGG_MEDIAN`) paths. The crosstab fused/buffered pair drives a
200-field × 10K-row wide cohort with a `region × segment` crosstab over
`AGG_COUNT(value)` and full margins. Each cell is the median of three
`b.Loop()` runs.

| Bench | ms/op | MB/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkProcess_BufferedComponents` (`/`) | 30.16 | 49.25 | 600,122 |
| `BenchmarkProcessStream_WithComponents` (`/`) | 30.44 | 49.38 | 600,487 |
| `BenchmarkCrosstabWideCohort_Fused` (`/service/`) | 8.69 | 21.07 | 143,903 |
| `BenchmarkCrosstabWideCohort_Buffered` (`/service/`) | 9.08 | 22.03 | 83,978 |

These are the canonical post-Components-always-on baselines and the regression
frontier: a sustained `> +5%` regression on any line warrants investigation.

A relative "+5% vs no-Components" gate (`BenchmarkProcess_NoComponents` against
`_WithComponents`, plus the matching crosstab pair) would need paired
`_NoComponents` sub-cases that do not exist today; add them and flip the gate
from absolute to relative if the pairing lands.

Reproduce with:

```sh
go test -bench=BenchmarkProcessStream_WithComponents -run=^$ -count=3 -benchmem ./
go test -bench=BenchmarkProcess_BufferedComponents  -run=^$ -count=3 -benchmem ./
go test -bench=BenchmarkCrosstabWideCohort_Fused    -run=^$ -count=3 -benchmem ./internal/service/
go test -bench=BenchmarkCrosstabWideCohort_Buffered -run=^$ -count=3 -benchmem ./internal/service/
```
