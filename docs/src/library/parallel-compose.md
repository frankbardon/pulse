# Parallel Compose

**Audience:** Go embedders running multiple requests concurrently
against the same cohort or set of cohorts.

`pulse.ComposeParallel` fans a `ComposedRequest` across a bounded
worker pool. Workers share the engine's read-only registries; each
`Process` call constructs fresh stateful operators per request, so
concurrent execution is safe.

> **LLM agents using MCP:** the MCP server today exposes
> `pulse_compose` as a sequential operation. Parallelism is a
> library-side capability.

## When to use

| Goal | Reach for |
|---|---|
| Single request, single result | `Process` |
| Single request, pulled as rows | `ProcessStream` |
| Batch of independent requests, in order, sequential | `Compose` |
| Batch of independent requests, **in parallel**, with bounded workers | `ComposeParallel` |

Order of results is preserved regardless of completion order — a
worker that finishes early is held until its slot's index is the
next to emit. So callers can index `responses[i]` against
`req.Requests[i]` directly.

## ComposeOptions

From [`internal/service/compose_parallel.go`](https://github.com/frankbardon/pulse/blob/main/internal/service/compose_parallel.go),
re-exported as `pulse.ComposeOptions`:

```go
type ComposeOptions struct {
    // MaxWorkers caps concurrent in-flight Process calls. Zero means
    // runtime.GOMAXPROCS; negatives clamp to 1.
    MaxWorkers int

    // PerRequestTimeout, if positive, derives a context.WithTimeout for
    // each request.
    PerRequestTimeout time.Duration

    // FailFast cancels in-flight siblings on the first request error.
    // Defaults to true. Set false to aggregate all errors instead.
    FailFast bool
}
```

| Field | Default | Notes |
|---|---|---|
| `MaxWorkers` | `runtime.GOMAXPROCS(0)` | `0` resolves to GOMAXPROCS; `<1` clamps to 1 |
| `PerRequestTimeout` | unlimited | When positive, each worker derives `context.WithTimeout` |
| `FailFast` | `true` | First error cancels siblings and returns immediately |

## Example

```go
ctx := context.Background()

composed := &pulse.ComposedRequest{
    Requests: []*pulse.Request{req1, req2, req3, req4},
}

resps, err := p.ComposeParallel(ctx, composed, pulse.ComposeOptions{
    MaxWorkers:        4,
    PerRequestTimeout: 30 * time.Second,
    FailFast:          true,
})
if err != nil {
    return err
}

for i, resp := range resps {
    fmt.Printf("request %d: %d rows\n", i, len(resp.Data))
}
```

## FailFast semantics

With `FailFast = true` (the default):

- The first request to return an error cancels the shared context.
- In-flight siblings observe cancellation via `ctx.Err()` (every
  serial per-record loop polls it every 4,096 rows) and return early
  with `context.Canceled`.
- `ComposeParallel` returns `(nil, err)` where `err` is the
  lowest-index failure that is not such a sibling cancellation, so the
  error that tripped FailFast is the one reported. When the caller's
  own ctx is done, the lowest-index failure is reported as is.

With `FailFast = false`:

- Every request runs to completion (or its own per-request timeout).
- Errors are aggregated into a single `SERVICE_INTERNAL` error whose
  `details` map carries `failed_indices` (a list of slot indices
  that errored).
- Successful slots populate the returned response array; failed
  slots are `nil` at their index.

A ctx that is already done before every slot launched returns the
ctx error (with no slot error to report), never a response with
unrun `nil` slots.

## Timeouts

`PerRequestTimeout` bounds each slot; a slot that runs out returns
`context.DeadlineExceeded`, unchanged. The instance-wide
`Options.Limits.RequestTimeout`, when set, bounds the whole
`ComposeParallel` (or `Compose`) call instead: when it fires, the call
returns `PULSE_LIMIT_EXCEEDED` with
`{limit: "request_timeout", configured, observed}` (nanoseconds) and no
partial result. A deadline or cancel on the caller's own ctx always
passes through as `context.DeadlineExceeded` / `context.Canceled`.

## CLI parity

```
pulse api compose --request batch.json --parallel 4
pulse api compose --request batch.json --parallel 4 --no-fail-fast
```

`--parallel N`:

- `1` (default) → sequential `Compose`.
- `0` → `runtime.GOMAXPROCS`.
- `> 1` → exactly that many workers.

`--no-fail-fast` mirrors `FailFast = false`.

## Performance considerations

- Each worker performs its own filesystem reads. If your cohort lives
  on slow remote storage, parallelism amortises latency well; on
  local SSD the gain is smaller and CPU-bound.
- Streaming aggregations are CPU-friendly — `ComposeParallel` over a
  pool of streaming requests scales near-linearly to the worker count.
- Buffered request shapes (window operators, median, ...) hold
  memory per request. Watch `MaxWorkers × per_request_peak_memory`.
- The internal registries are read-only and shared across workers
  with no locking; only the per-request operator instances are
  fresh allocations.

## Safety

- `Pulse` is safe for concurrent use after `New`.
- Per-request operator state (running sums, dictionaries, sorted
  buffers) is allocated fresh inside each `Process` call.
- The `afero.Fs` you supply must itself be safe for concurrent reads
  — every shipped backend (`OsFs`, `MemMapFs`) is.
