# StreamResult[T]

**Audience:** Go embedders that want a structured streaming shape with
header, chunks, terminator, and built-in cancellation semantics.

`StreamResult[T]` is Pulse's canonical streaming shape. Every operation
that produces incremental data exposes a `*Stream` variant that returns
`StreamResult[T]`. The shape's contract — request hash in the header,
sequence-numbered chunks, terminator on a separate channel — is
generic across operators, so a consumer can write one row-handling loop
that drives any streamable Pulse call.

## Shape

```go
type StreamResult[T any] struct {
    Header StreamHeader
    Chunks <-chan StreamChunk[T]
    Done   <-chan StreamTerminator
}

type StreamHeader struct {
    RequestHash    string    // From req.Hash()
    EstimatedTotal int64     // Best-effort; -1 if unknown
    StartedAt      time.Time
}

type StreamChunk[T any] struct {
    Sequence int          // Monotonic 0-based
    Data     T
    Progress float64      // 0.0–1.0, or -1 if unknown
}

type StreamTerminator struct {
    CompletedAt time.Time
    TotalRows   int64
    Status      StreamStatus  // Completed | Cancelled | Errored
    Error       error
}
```

## Available variants

- `Pulse.ProcessStreamResult(ctx, req) (StreamResult[Row], error)` —
  wraps the existing `ProcessStream` engine.
- `Pulse.SynthStream(ctx, spec, opts) (StreamResult[Row], error)` —
  generates synth respondents and yields one row per chunk.

## Receiver pattern

```go
res, err := p.ProcessStreamResult(ctx, req)
if err != nil {
    return err
}

for chunk := range res.Chunks {
    handle(chunk.Sequence, chunk.Data, chunk.Progress)
}

term := <-res.Done
if term.Status != pulse.StreamCompleted {
    return fmt.Errorf("stream %s: %w", term.Status, term.Error)
}
fmt.Printf("delivered %d rows in %s\n",
    term.TotalRows, term.CompletedAt.Sub(res.Header.StartedAt))
```

The `Chunks` channel closes when the operation finishes. The `Done`
channel delivers exactly one `StreamTerminator` and then closes — a
receiver that selects on `Done` sees one value followed by
channel-closed.

## Backpressure

`Chunks` carries a 4-deep buffer. Slow consumers slow the producer;
the producer never drops chunks. This makes `StreamResult` safe to
hand to a downstream HTTP response writer or SSE relay without
worrying about chunk loss under pressure.

## Cancellation

Cancelling the context cancels the operation:

```go
ctx, cancel := context.WithCancel(parent)
res, _ := p.ProcessStreamResult(ctx, req)

go func() {
    time.Sleep(5 * time.Second)
    cancel()
}()

for chunk := range res.Chunks { ... }
term := <-res.Done
// term.Status == pulse.StreamCancelled, term.Error == context.Canceled
```

## Errors

A mid-stream error closes `Chunks` early and delivers a
`StreamTerminator{Status: StreamErrored, Error: err}` on `Done`.
Consumers should always check the terminator status — a closed
`Chunks` channel alone does not distinguish success from error.

## Non-streaming variants

The non-streaming entry points (`Process`, `Synth`) remain — they are
convenience wrappers that drain the stream and return the full
result. Use the streaming variants when you need incremental output,
progress reporting, or early cancellation; use the buffered variants
when you want one materialised result and don't care about the
intermediate chunks.

## Relationship to `ProcessStream`

`Pulse.ProcessStream` (returning `RowIter`) remains the lower-level
pull-iterator API for callers that want the simpler shape. See
[Streaming & ProcessStream](./streaming.md). `ProcessStreamResult`
adds the structured header, terminator, and cancellation semantics
on top of the same engine.
