# Tuning Limits

An instance carries a closed set of resource limits. They exist so a
pathological request (a group-by on a unique ID, a thousand-slot Compose,
a join whose right side will not fit) fails fast with a message that
names the knob to turn, instead of exhausting the host. The defaults are
high enough that no legitimate request trips them, so most embedders set
nothing. Tighten them on shared hosts (an MCP server, a multi-tenant
service).

A request can never override an instance limit. A breach is a single
coded error, `PULSE_LIMIT_EXCEEDED`, with no partial result.

## The limits

`pulse.Limits` (the `Options.Limits` field):

| Field | JSON / `--limit` key | Default | Unit | Guards against | Checked |
|---|---|---|---|---|---|
| `RequestTimeout` | `request_timeout` | none (`Unlimited`) | duration | a call that runs far longer than intended | runtime, an inner deadline |
| `MaxGroups` | `max_groups` | `10,000,000` | distinct groups | grouping on a high-cardinality field | predict (`possible`) + every bucket mint at runtime |
| `MaxCrosstabCells` | `max_crosstab_cells` | `10,000,000` | rows x columns | a huge crosstab grid | predict (`possible`) + both crosstab arms |
| `MaxEstimatedMemory` | `max_estimated_memory` | none (`Unlimited`) | bytes | a run whose buffered state cannot fit | predict (`certain`) and the same pre-flight before any record decodes; a 1:N join also predict (`possible`) + a check after the join build |
| `MaxMatrixDim` | `max_matrix_dim` | `2,048` | columns `p` | a quadratic matrix output | predict (`certain`) + pre-flight |
| `MaxComposeSlots` | `max_compose_slots` | `1,000` | requests | fan-out through Compose; a `sweep` or `banner` counts its EXPANDED slots plus the explicit ones | up front, serial and parallel, and in `PredictCompose` |
| `MaxChainStages` | `max_chain_stages` | `1,000` | stages | fan-out through ProcessChain | up front, and in `PredictChain` |
| `MaxJoinBuildRows` | `max_join_build_rows` | `100,000,000` | records | a join's in-memory build side | predict (`certain`, from the right side's header) + the build loop |
| `MaxDerivedColumns` | `max_derived_columns` | `1,000` | window + feature + attribute items | fan-out through an item `for_each`; counts every plain item plus every EXPANDED one (a multi-output feature counts once) | predict (`certain`) + before any `for_each` item renders, on every request host (Process, each Compose slot, each chain stage, Materialize) |

`RequestTimeout` and `MaxEstimatedMemory` are opt-in: their default is
`Unlimited`. There is no output row cap; output volume is the embedder's
to manage (see [Response Shaping](response-shaping.md) for what is
returned).

Read the effective values with `p.Limits()`, or `pulse manifest --json`
(`limits` array `{name, value, default, unit}`). The Go constants
`pulse.DefaultMaxGroups`, `pulse.DefaultMaxCrosstabCells`, … are the
defaults.

## The encoding: `0` and `-1`

The same on every input layer (Go, feature profile, `--limit`):

- `0`, or absent: use the default.
- `-1` (`pulse.Unlimited`; `unlimited` as text): no limit.
- any other negative value: refused with `PULSE_LIMIT_INVALID`
  (`PULSE_FEATURE_PROFILE_INVALID`, reason `invalid_limits`, in a profile).

The effective limits `Pulse.Limits()` returns never hold `0`.

## Precedence

Per field: a non-zero `Options.Limits` value, else the feature profile's
`limits` value, else the built-in default.

```go
p, err := pulse.New(pulse.Options{
    DataDir: "/var/data/pulse",
    Limits: pulse.Limits{
        MaxGroups:      1_000_000,
        RequestTimeout: 30 * time.Second,
        MaxMatrixDim:   pulse.Unlimited,
    },
})
```

A feature-profile file carries the same keys in snake_case;
`request_timeout` is a Go duration string (`"30s"`, `"-1"`, `"unlimited"`):

```json
{
  "profile": "shared-host",
  "written_with": "1.0.0",
  "features": [],
  "limits": { "max_groups": 1000000, "request_timeout": "30s", "max_matrix_dim": -1 }
}
```

Limits are behaviour, not features: they are not in `feature_set_digest`
and do not hide anything. See [Feature Profiles](feature-profiles.md).

For an MCP server, pass `--limit` once per limit. A bad key or value is a
`CLI_INPUT` startup error:

```sh
pulse mcp --limit max_groups=1000000 --limit request_timeout=30s --limit max_estimated_memory=4_000_000_000
```

## Manifest and the cache key

`manifest.limits` reports the effective values (`-1` = none) and
`manifest.limits_digest` (`lim1:` + SHA-256 over the `name=value` lines)
changes exactly when an effective limit changes. The manifest cache key
stays `(pulse_version, feature_set_digest)`; a client that plans around
limits re-fetches when `limits_digest` moves.

## Predict findings: `certain` and `possible`

`Predict` reports `PredictResult.LimitFindings`
(`{limit, configured, estimated, grade, details}`) for every limit a figure
computed from the request and schema exceeds.

- `certain`: the exact figure the runtime would see. The finding also adds
  the `PULSE_LIMIT_EXCEEDED` error, so `valid` is false, and `Process`
  refuses with the identical code, message and details before any record
  decodes.
- `possible`: an upper bound (a group count estimated from dictionary
  sizes, a crosstab grid). Reported only; the request stays `valid` and
  the runtime decides. `MaxGroups` and `MaxCrosstabCells` are `possible`.

The other predict roots report the same `limit_findings`, graded the
same way:

- `PredictCompose`: the slot count (`MaxComposeSlots`, explicit plus
  sweep- and banner-expanded, `certain`), then every slot's Request findings on its
  own cohort's header counts, each located `details.request` (a fit
  request's `details.fit`).
- `PredictChain`: the stage count (`MaxChainStages`, `certain`, checked
  right after the empty check, as `ProcessChain` does), then every
  stage's findings located `details.stage`. Stage 0 is judged on the
  cohort's header count, exactly as its run. A later stage's input rows
  exist only at runtime, so it never carries a `certain` memory finding
  and its group bounds are not clamped by a row count.
- `PredictFacet`: a `possible` `MaxGroups` bound (the largest faceted
  field's dictionary size, 2 for `packed_bool`; a numeric field mints no
  group) and the `certain` memory estimate the facet pre-flight refuses
  with.

Only `RequestTimeout` is knowable at runtime alone. The simple
`Facet(path, field)` has no predict root: its streamed (numeric) path
runs the facet memory pre-flight before any record is read, and its
categorical path returns the schema's dictionary, reads no record and
is never refused by a limit.

## Reading `PULSE_LIMIT_EXCEEDED`

`details` is `{limit, configured, observed, option}`; the message names the
three places to raise it (`Options.Limits.<Field>`, the profile key,
`--limit`). Look the code up with `pulse errors lookup PULSE_LIMIT_EXCEEDED`.

- A parallel run (shard or decode workers, `ComposeParallel`) stops at the
  first worker to breach, so `observed` is NONDETERMINISTIC there: compare
  `{code, limit, configured}`, never `observed`. A merge-only breach
  (every shard under the limit, the union over it) reports the merged
  count.
- Fused crosstab `observed` is the first grid product past the limit; a
  buffered crosstab reports the final grid. The fused count is the axis
  grid (rows x columns of interned keys), not populated cells.
- For `request_timeout`, `configured` and `observed` are nanoseconds
  (`observed` = elapsed time from the outermost call).
- A trip is a bare coded error, never wrapped in a slot or stage locator.

## The memory estimate

`MaxEstimatedMemory` compares against an UPPER bound, never a measurement:
no filter selectivity is assumed, and the model carries a x3 collector
headroom. Calibration against measured peak heap lands the estimate at
roughly 1.3x to 4.9x the peak depending on the arm. Not modelled: a
`GOGC` above 100. There is no `GOMEMLIMIT` coupling: the limit is
opt-in and independent of the Go runtime's own limit. Set it
a little under the memory you can spare.

**Grouped runs.** Each group bucket is charged in two parts. Its
STATE is what it holds while records fold: about 256 bytes for the key,
its map slot and the grouper's tally (128 more for each grouper past
the first), plus each aggregation's own measured state. A built-in
aggregator is charged from a per-operator table: 96 bytes for
`AGG_MIN` or `AGG_MAX`, 128 for `AGG_SUM` (576 when weighted, since it
keeps three reproducible sums and a weighted floor), 320 for
`AGG_DISTINCT_COUNT`, and so on. Each figure is a measured ceiling,
pinned by a test that measures every built-in. An extension aggregator,
and the order statistics (`AGG_MEDIAN`, `AGG_PERCENTILE`, `AGG_ZSCORE`,
`AGG_AUC`), are charged 256 bytes (512 weighted). The vector
aggregators (`AGG_VEC_*`) also pay for their member count and for one
block partial per 4,096-record merge block a bucket holds.

Its OUTPUT is what the run writes for it at the end. With components
on, that is the `data` row and the per-group `components` entries,
charged 1,024 bytes plus 576 per aggregation. With components off
(`Options.DisableComponents`, or `disable_components: true` on the
request; the request's setting wins), it is the `data` row alone,
charged 512 bytes plus about 96 per aggregation. An `AGG_SET_FREQUENCY`
slot adds a counter and an output label per member of its set field
(192 bytes a member) to every bucket. Measured at 50,000 one-row
groups of `AGG_COUNT` + `AGG_SUM` + `AGG_AVERAGE`, the estimate is
1.3 to 1.5 times the whole run's peak with components on and 2.1 to
2.5 times with them off, where the fixed 16 MiB run base weighs more.
A slot counts as weighted when a weight is in force on it (its own, the
request's, or `Options.DefaultWeight`), whether or not the operator
reads weights.

**Crosstabs.** A crosstab is charged per measure (`cell`, or each
`cells` entry), because each measure keeps its own grid. Every cell, and
every row, column and grand margin slot, is charged the measure's
per-operator state plus a measured per-cell output: the matrix value,
the cell's `components` entry and the scratch the finalize lays out per
cell. For `AGG_COUNT` that is about 1,100 bytes a cell with components
on and about 600 off; for an `AGG_VEC_*` element cell it is about 2,450
on (3,150 weighted), its 4,096-record merge-block partials included. Each row and column key adds
784 bytes (544 off). Each `margin_aggregations` entry costs about a cell
in every margin slot, once per measure, and nothing with components off.
`normalize_within` adds a denominator slot per cell, `normalize_level`
one per axis key. The buffered arm also pays for its partitions: 128
bytes a row, and per measure 448 a cell and 416 an axis key. An axis
whose cardinality the schema does not fix is bounded like a grouper
(below), and the grid by the record count. These figures replaced a flat
512 bytes a cell, which was below the measured cost of every built-in
cell with components on, so crosstab estimates rise.

**How many buckets.** The bucket count is an upper bound read from the
schema alone. A dictionary-backed grouper (`GROUP_CATEGORY` over a
categorical field), a `packed_bool`, an `include` list, a quantile or a
k-means grouper is charged its key count. A grouper whose keys depend on
the data is charged by the width of the field it keys on, when that
field is an integer or a set word. `GROUP_CATEGORY`, `GROUP_ROUNDED` and
`GROUP_RANGE` over a `u4`, `u8`, `u16` or `u32` field, and
`GROUP_SET_VALUE` over a `set_u8`, `set_u16` or `set_u32` field, mint at
most one key per stored value: 16 for `u4`, 256 for `u8`, 65,536 for
`u16`. Two or more groupers multiply their bounds. Anything else (a
float, a date or a 64-bit field, `GROUP_DATE`, an extension grouper) is
bounded by the record count, because a run cannot mint more keys than
it reads rows. When a `GROUP_SET_PER_ELEMENT` grouper fans a record out,
the record bound is multiplied by the set's size. A shard's or a
worker's partial uses the same rule over its own records. So grouping
50 million rows by a `u32` customer id is charged 50 million buckets
however few customers there are: predict reads no records, so it cannot
count them, and no sidecar proves a smaller figure. A point-lookup
index records a distinct-key count, but nothing proves the index
matches the cohort without reading it, so the estimate never uses it.

**Extension aggregators.** An extension aggregator is priced as a
fixed-size accumulator of 256 bytes (512 weighted, and a 576-byte
output column even with components off) unless its registration declares
`ValueState: true`. Declared, it is charged the per-distinct-value term
a built-in value-state aggregator (`AGG_MODE`, `AGG_DISTINCT_COUNT`,
`AGG_FREQUENCY`, …) pays — the same estimate on predict and the run.
Declare it for any operator whose state keeps a map entry per value it
sees; an undeclared one can exceed the estimate.

**Fan-out groupers and tests.** A `GROUP_SET_PER_ELEMENT` grouper puts a
record in one bucket per selected set member. The estimate assumes every
member is selected: it multiplies the per-value state, a rank-method
matrix's kept rows and a buffered test's kept values by the set
dictionary's size (or the grouper's `include` count). Rows that select
only a few members therefore estimate well above their peak. Tier-1
tests are charged one state per test per group bucket, plus the split
levels each one keys. A test that cannot stream (a rank-based test such
as `TEST_MANN_WHITNEY_U` or `TEST_KRUSKAL_WALLIS`) is also charged every
value it keeps, which grows with the record count. A rank-method matrix
(`spearman`, `kendall`) is charged its kept rows plus the working copies
it ranks at the end of the run.

**Joins whose right key repeats.** The pre-flight estimate assumes one
right match per left row. A 1:N join emits one joined row per matching
right row, which a buffered arm keeps and per-value state counts. The
header and schema cannot prove a key unique (only a one-row right side
is), so the extra rows are judged twice:

- Predict re-runs the estimate on a joined-row bound and reports a
  `possible` `max_estimated_memory` finding when it exceeds the limit.
  The bound is left x (right − dictionary size + 1) for a categorical
  right key, which holds only when every dictionary entry occurs in the
  right cohort. A key with no dictionary uses left x right, the worst
  case. `details` carries `join`, `join_rows_bound` and `join_bound`
  (`right_key_dictionary` or `left_x_right`). The finding never makes
  predict invalid.
- The runtime knows the most rows one right key holds once the build
  side is hashed. When the estimate on left x that multiplicity exceeds
  the limit, the run is refused before any left row is probed. The
  error carries the memory refusal's `details` plus `join`,
  `join_rows_bound` and `right_key_multiplicity`.

With the limit Unlimited (the default) nothing is counted and the join
runs unchanged.

**Shard archives.** An archive is read by random access: opening it
reads only the zip central directory and the headers, and the run reads
each shard through its own buffered window, so the archive is never
resident. The estimate charges what the run holds instead of the
archive's bytes. Where a single file charges 3 x records x stride (its
payload plus the collector's headroom), an archive charges 3 x the
LARGEST shard's records x stride, since one shard streams at a time.
It adds 8 x the central directory's bytes (the directory a scan holds
parsed: a few hundred bytes per shard). It also adds one decode of the
canonical schema with its dictionaries, whatever the shard count.
Neither the open nor the run decodes a shard's own dictionaries: the
open counts each shard's records and the scan skips each shard's schema
block, both reading the schema without its dictionaries, because the
canonical schema decodes every row. So an archive whose shards each
carry a large dictionary is charged that dictionary once, not once per
shard. A shard opened through the anchor form
(`archive.pulse#shard.pulse`) is charged as the single file it is: the
run reads only that shard. Predict charges the same. Per-row state
(buffered records, per-value state, buckets) still counts every record
of the archive. On the per-shard
parallel arm (`ShardWorkers` other than 1, two or more shards, a
mergeable request) each shard's partial keeps its own group buckets
until it is merged. The merge is incremental: shard i is merged as
soon as shards 0 to i are done, in shard order, and is then released.
No shard is handed to a worker until it is within twice the worker
count of the first unmerged shard. So at most 2 x `ShardWorkers`
partials are alive beside the merged result, however many shards the
archive has. The estimate charges that window: the bucket state of the
2 x workers shards with the largest bucket bounds, and the per-value
state of the 2 x workers largest shards. Predict reads the instance's
`ShardWorkers` to pick the arm, as the run does.

When `MaxEstimatedMemory` is set, the window shrinks to fit it. If the
whole-run estimate at 2 x workers live partials exceeds the limit, the
run takes the largest window, down to one partial, whose estimate fits,
and the limit refuses the request only when even one live partial does
not fit (the refusal reports that one-partial estimate). A smaller
window holds fewer shards' group buckets at once but keeps fewer
workers busy, so it trades speed for memory. It never changes an
answer: shards still merge in shard order. Predict applies the same
rule, so a request it reports as fitting runs at the window predict
assumed. With the default (`Unlimited`) the window is always
2 x workers. Measured on archives,
the estimate lands at roughly 1.2x to 2.4x the peak heap.

On a single file the parallel-decode arm (`DecodeWorkers` other than 1,
at least 100,000 records, a file the engine can memory-map, a mergeable
request) holds one partial per worker until the merge, and the
estimate counts those partials' bucket state too. Predict reads the instance's
`DecodeWorkers` and asks the engine whether it could map the cohort, so
it charges that arm exactly when the run takes it.

An unknown record count produces no estimate and no finding. With the
default (`Unlimited`) no count is read at all.

## Worker counts

`Options.ShardWorkers` (per-shard parallel arm over a shard archive) and
`Options.DecodeWorkers` (segment-parallel decode of one large `.pulse`
file) cap the worker pools: `0` means one worker per CPU, `1` forces the
serial arm, a negative value is refused at `pulse.New`. The CLI exposes
both as global flags on every leaf, `pulse mcp` included:
`--shard-workers N` and `--decode-workers N` (a negative value is a usage
error, exit 2; there is no environment-variable form). The two knobs are
independent: an archive never decodes in segments and a single file
never fans out by shard. Both parallel arms decode only the fields the
request reads, as the serial arm does. A grouped request fans out
whatever its key count. With a value-keyed grouper (`GROUP_ROUNDED`,
`GROUP_RANGE`) whose keys approach the rows each worker reads, the
fan-out gains nothing and allocates about twice the memory; set the
worker count to `1` for such a run. Predict cannot see that key count.

Measured on an Apple M1 Max (10 cores), 1M rows × 50 `f64` members,
grouped `AGG_SUM` + `AGG_AVERAGE` over two members
(`BenchmarkArchiveWorkerScaling`):

| Workers | 8-shard archive: wall | peak heap | single file: wall | peak heap |
|---|---|---|---|---|
| 1 | 423 ms | 1.9 MB | 411 ms | 0.04 MB |
| 2 | 268 ms | 3.2 MB | 267 ms | 2.3 MB |
| 4 | 157 ms | 4.5 MB | 177 ms | 2.5 MB |
| 8 | 107 ms | 15.3 MB | 125 ms | 12.3 MB |

A request that reads every member (a 50 × 50 pairwise covariance) scales
almost linearly: 6.1 s at 1 worker, 3.3 s at 2, 1.7 s at 4, 1.1 s at 8 on
the archive, with the peak heap 48–69 MB. Each extra worker adds one
read window and up to two unmerged partials (their group buckets) to
the peak, so a high-cardinality grouping costs more memory per worker
than these four-bucket runs. The shard count does not: merged partials
are released. Beyond the number of shards, extra shard workers sit
idle. Detail: `docs/internals/BENCHMARKS_BASELINE.md`.

## `RequestTimeout` and the caller's context

`RequestTimeout` is an inner deadline in addition to the caller's ctx. It
covers `Process`, `ProcessStream` / `ProcessStreamResult` (the run and the
drain, so a stalled consumer trips it), `Facet` / `FacetSchema`, and the
WHOLE `Compose` / `ComposeParallel` / `ProcessChain` call (nested entries
share one budget). Only the inner deadline becomes
`PULSE_LIMIT_EXCEEDED`:

| Who ended the call | Error |
|---|---|
| `RequestTimeout` | `PULSE_LIMIT_EXCEEDED` `{limit: "request_timeout"}` |
| the caller's deadline | `context.DeadlineExceeded`, unchanged |
| the caller cancelled | `context.Canceled`, unchanged |

Serial per-record loops (streaming, buffered materialize, fused and
buffered crosstab, the join build, facet) poll the ctx on the first row and
then every 4,096 rows, so a cancel or timeout lands within 4,096 rows.
`Pulse.ProcessStream` (a `RowIter`) bounds only the run; draining with
`Next(ctx)` is governed by the caller's ctx. `ComposeOptions.PerRequestTimeout`
still bounds each slot and still surfaces `context.DeadlineExceeded`. See
[Parallel Compose](parallel-compose.md).

## Choosing values

- Shared MCP host: set `request_timeout` (30s to a few minutes) and
  `max_estimated_memory` first; the count limits are rarely the binding one.
- Tighten `max_groups` when users group freely and a unique-ID group-by
  would be the common mistake.
- Never lower a limit below what existing saved requests need. Run
  `Predict` over them and look at `limit_findings`.
