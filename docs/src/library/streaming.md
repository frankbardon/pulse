# Streaming & ProcessStream

**Audience:** Go embedders feeding rows into an HTTP response, an
NDJSON pipeline, or any consumer that wants result rows one at a time
instead of buffering the full set.

`pulse.ProcessStream` returns a pull-based iterator. The API is
stable regardless of whether the underlying request shape streams
inside the engine — non-streamable requests return the same iterator,
they just buffer once internally before yielding.

> **LLM agents using MCP:** see `skills/request-recipes.md` for the
> MCP-side streaming surface (`pulse_process` with the streaming
> option). The Streamable predicate is the same on both surfaces.

## The iterator API

```go
type RowIter = service.RowIter

// In service:
type RowIter interface {
    Next(ctx context.Context) (Row, bool, error)
    Close() error
    Metadata() *ResponseMetadata
}

type Row = service.Row // map[string]any
```

Usage:

```go
iter, err := p.ProcessStream(ctx, req)
if err != nil {
    return err
}
defer iter.Close()

for {
    row, ok, err := iter.Next(ctx)
    if err != nil {
        return err
    }
    if !ok {
        break
    }
    // … emit row …
}

meta := iter.Metadata() // available after drain
```

`Metadata()` returns the full `ResponseMetadata` (total rows,
filtered rows, cohort file) once the iterator has been drained.

## What actually streams

`ProcessStream` always returns an iterator, but the engine only
avoids the buffered intermediate row set for a subset of request
shapes. Run `pulse api predict` (or `Predict` from the library) and
check the `Streamable` flag in the result:

```go
pred, err := p.Predict(ctx, req)
if !pred.Streamable {
    for _, reason := range pred.StreamableReasons {
        log.Printf("buffered because: %s", reason)
    }
}
```

The streaming-eligible request shapes are listed in
[Performance Notes → Streaming path](../ops/performance.md#streaming-path-what-stays-out-of-memory).

The complement — the request shapes that force the buffered path —
is at [Performance Notes → Buffered
path](../ops/performance.md#buffered-path-when-pulse-has-to-materialise).

`Streamable=false` doesn't mean the iterator is broken; it just
means rows materialise inside the engine before `Next` yields them.
The output API is identical either way.

## CLI parity

`pulse api process --stream` writes NDJSON to stdout, one row per
line. `pulse api compose --stream` does the same with an `index`
field per row identifying which sub-request produced it.

## Cancellation

Every `Next` call accepts a context. Cancellation propagates to the
underlying reader; rows that are already in flight may still be
returned before `Next` returns `(_, false, ctx.Err())`. `Close()`
releases any reader resources and is safe to call multiple times.

## Backpressure

The iterator is pull-based: the engine produces rows only as fast as
the consumer calls `Next`. For HTTP responders that flush periodically,
this means you can stream a multi-GB result set through a
constant-memory buffer.

For pipelines that want to fan rows out across goroutines, copy each
row into your own struct before processing — `Row` is
`map[string]any` and the engine may re-use the backing data after
`Next` returns. Treat it as borrowed.

## Inside the engine

Under the hood, `ProcessStream` calls one of four orchestrator modes
depending on the request shape: single-pass streaming, grouped
streaming, two-pass streaming, or the buffered fallback. The choice
is made via `processing.CanStreamRequest(req, schema)`, which is the
same predicate `Predict.Streamable` reports — this parity is
enforced by `TestPredict_Streamable_MatchesRuntime`.

If you find a request that predict says is streamable but `Next`
materialises something large, that's a parity drift and a bug —
please report it with the request JSON.
