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

From [`service/compose_parallel.go`](https://github.com/frankbardon/pulse/blob/main/service/compose_parallel.go),
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
- In-flight siblings observe cancellation via `ctx.Err()` and return
  early.
- `ComposeParallel` returns `(nil, theFirstError)`.

With `FailFast = false`:

- Every request runs to completion (or its own per-request timeout).
- Errors are aggregated into a single `SERVICE_INTERNAL` error whose
  `details` map carries `failed_indices` (a list of slot indices
  that errored).
- Successful slots populate the returned response array; failed
  slots are `nil` at their index.

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
