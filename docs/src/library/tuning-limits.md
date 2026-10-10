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
| `MaxEstimatedMemory` | `max_estimated_memory` | none (`Unlimited`) | bytes | a run whose buffered state cannot fit | predict (`certain`) and the same pre-flight before any record decodes |
| `MaxMatrixDim` | `max_matrix_dim` | `2,048` | columns `p` | a quadratic matrix output | predict (`certain`) + pre-flight |
| `MaxComposeSlots` | `max_compose_slots` | `1,000` | requests | fan-out through Compose; a `sweep` counts its EXPANDED slots plus the explicit ones | up front, serial and parallel, and in `PredictCompose` |
| `MaxChainStages` | `max_chain_stages` | `1,000` | stages | fan-out through ProcessChain | up front |
| `MaxJoinBuildRows` | `max_join_build_rows` | `100,000,000` | records | a join's in-memory build side | predict (`certain`, from the right side's header) + the build loop |

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
(`{limit, configured, estimated, grade}`) for every limit a figure
computed from the request and schema exceeds.

- `certain`: the exact figure the runtime would see. The finding also adds
  the `PULSE_LIMIT_EXCEEDED` error, so `valid` is false, and `Process`
  refuses with the identical code, message and details before any record
  decodes.
- `possible`: an upper bound (a group count estimated from dictionary
  sizes, a crosstab grid). Reported only; the request stays `valid` and
  the runtime decides. `MaxGroups` and `MaxCrosstabCells` are `possible`.

`Predict` reports a Request's findings (a Compose slot's, a chain's first
stage). Slot and stage counts, `RequestTimeout` and a rich facet's group
count are knowable only at runtime.

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
roughly 1.3x to 4.9x the peak depending on the arm. Not modelled: a 1:N
fan-out join (it can exceed the estimate), a `GOGC` above 100, and
extension aggregators. There is no `GOMEMLIMIT` coupling: the limit is
opt-in and independent of the Go runtime's own limit. Set it
a little under the memory you can spare.

An unknown record count produces no estimate and no finding. With the
default (`Unlimited`) no count is read at all.

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
