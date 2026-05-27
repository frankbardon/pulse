package pulse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
)

// StreamStatus reports the terminal state of a StreamResult.
type StreamStatus int

const (
	// StreamCompleted means the operation produced all chunks and
	// finished without error.
	StreamCompleted StreamStatus = iota
	// StreamCancelled means the caller's context was cancelled or
	// timed out before the operation finished.
	StreamCancelled
	// StreamErrored means the operation aborted mid-stream. The
	// terminator's Error field carries the underlying cause.
	StreamErrored
)

// String returns a stable lowercase identifier for the status, suitable
// for logging and JSON marshalling.
func (s StreamStatus) String() string {
	switch s {
	case StreamCompleted:
		return "completed"
	case StreamCancelled:
		return "cancelled"
	case StreamErrored:
		return "errored"
	}
	return "unknown"
}

// StreamHeader is the single-shot prelude describing what the stream
// is about to produce. Sent before any chunks.
type StreamHeader struct {
	// RequestHash is the canonical hash of the originating request,
	// as returned by Request.Hash / SynthSpec.Hash. Identifies the
	// stream's input for caching and deduplication.
	RequestHash string

	// EstimatedTotal is the best-effort row-count estimate for the
	// stream. -1 when unknown (predict not available, source is a
	// live cohort whose size is dynamic, etc.).
	EstimatedTotal int64

	// StartedAt is the wall-clock time the stream began producing.
	StartedAt time.Time
}

// StreamChunk wraps a single incremental payload. Sequence is a
// monotonic 0-based counter so consumers can detect gaps or reorder
// out-of-band telemetry; Progress reports a best-effort fraction in
// [0.0, 1.0] (or -1.0 when unknown).
type StreamChunk[T any] struct {
	Sequence int
	Data     T
	Progress float64
}

// StreamTerminator is the single-shot epilogue describing how the
// stream ended. Sent after the chunks channel closes; the Done channel
// itself is closed immediately after delivering this value so a
// receiver that selects on Done sees one value and then channel-closed.
type StreamTerminator struct {
	CompletedAt time.Time
	TotalRows   int64
	Status      StreamStatus
	Error       error
}

// StreamResult is the canonical streaming-output shape. Every operation
// that produces incremental data exposes a *Stream variant returning
// StreamResult[T]. The receiver pattern:
//
//	res, err := p.ProcessStreamResult(ctx, req)
//	if err != nil { ... }
//	for chunk := range res.Chunks {
//	    ... handle chunk.Data
//	}
//	term := <-res.Done
//	if term.Status != pulse.StreamCompleted { ... }
//
// Backpressure: the Chunks channel carries a small buffer (4). Slow
// consumers slow the producer; the producer never drops chunks.
type StreamResult[T any] struct {
	Header StreamHeader
	Chunks <-chan StreamChunk[T]
	Done   <-chan StreamTerminator
}

// streamBuffer is the channel buffer depth for StreamResult.Chunks.
// Small enough that slow consumers slow the producer (backpressure),
// large enough to absorb minor scheduling jitter.
const streamBuffer = 4

// ProcessStreamResult executes req and returns a StreamResult that
// yields each result row as a StreamChunk[Row]. The header is built
// before any rows are read; the chunks channel closes when the
// operation completes, and the Done channel delivers exactly one
// StreamTerminator describing the terminal state.
//
// Cancellation: closing ctx delivers a StreamTerminator with
// Status: StreamCancelled and a non-nil Error matching ctx.Err().
func (p *Pulse) ProcessStreamResult(ctx context.Context, req *Request) (StreamResult[Row], error) {
	if req == nil {
		return StreamResult[Row]{}, errors.New("pulse: nil request")
	}
	estimated := int64(-1)
	if req.Cohort != nil && req.Cohort.Filename != "" {
		if n, err := p.CountRecords(ctx, req.Cohort.Filename); err == nil {
			if n <= uint64(1<<62) {
				estimated = int64(n)
			}
		}
	}
	iter, err := p.svc.ProcessStream(ctx, req)
	if err != nil {
		return StreamResult[Row]{}, err
	}

	chunks := make(chan StreamChunk[Row], streamBuffer)
	done := make(chan StreamTerminator, 1)
	started := time.Now()

	go func() {
		defer close(chunks)
		defer iter.Close()
		var (
			seq  int
			rows int64
		)
		emitTerminator := func(status StreamStatus, err error) {
			done <- StreamTerminator{
				CompletedAt: time.Now(),
				TotalRows:   rows,
				Status:      status,
				Error:       err,
			}
			close(done)
		}
		for {
			row, ok, err := iter.Next(ctx)
			if err != nil {
				if ctx.Err() != nil {
					emitTerminator(StreamCancelled, ctx.Err())
					return
				}
				emitTerminator(StreamErrored, err)
				return
			}
			if !ok {
				emitTerminator(StreamCompleted, nil)
				return
			}
			chunk := StreamChunk[Row]{
				Sequence: seq,
				Data:     row,
				Progress: streamProgress(rows+1, estimated),
			}
			select {
			case <-ctx.Done():
				emitTerminator(StreamCancelled, ctx.Err())
				return
			case chunks <- chunk:
				seq++
				rows++
			}
		}
	}()

	return StreamResult[Row]{
		Header: StreamHeader{
			RequestHash:    req.Hash(),
			EstimatedTotal: estimated,
			StartedAt:      started,
		},
		Chunks: chunks,
		Done:   done,
	}, nil
}

// SynthStream generates synth respondents as a StreamResult. Chunks
// carry one map[string]any per generated row, the same shape returned
// by the buffered Synth path. EstimatedTotal mirrors spec.RowCount.
//
// The current implementation drives the buffered synth pipeline behind
// the scenes and yields rows from the materialized result; it does not
// expose true row-at-a-time generation yet but is API-stable so callers
// can adopt the streaming shape today.
func (p *Pulse) SynthStream(ctx context.Context, spec *SynthSpec, opts SynthOptions) (StreamResult[Row], error) {
	if spec == nil {
		return StreamResult[Row]{}, errors.New("pulse: nil synth spec")
	}

	chunks := make(chan StreamChunk[Row], streamBuffer)
	done := make(chan StreamTerminator, 1)
	started := time.Now()
	estimated := int64(spec.RowCount)

	go func() {
		defer close(chunks)
		emit := func(status StreamStatus, err error, rows int64) {
			done <- StreamTerminator{
				CompletedAt: time.Now(),
				TotalRows:   rows,
				Status:      status,
				Error:       err,
			}
			close(done)
		}

		buf, _, err := synth.SynthBytes(spec, opts)
		if err != nil {
			if ctx.Err() != nil {
				emit(StreamCancelled, ctx.Err(), 0)
				return
			}
			emit(StreamErrored, err, 0)
			return
		}
		rows, err := decodeSynthRows(buf)
		if err != nil {
			emit(StreamErrored, err, 0)
			return
		}
		var emitted int64
		for i, row := range rows {
			select {
			case <-ctx.Done():
				emit(StreamCancelled, ctx.Err(), emitted)
				return
			case chunks <- StreamChunk[Row]{
				Sequence: i,
				Data:     row,
				Progress: streamProgress(int64(i+1), estimated),
			}:
				emitted++
			}
		}
		emit(StreamCompleted, nil, emitted)
	}()

	return StreamResult[Row]{
		Header: StreamHeader{
			RequestHash:    spec.Hash(),
			EstimatedTotal: estimated,
			StartedAt:      started,
		},
		Chunks: chunks,
		Done:   done,
	}, nil
}

// decodeSynthRows reads a complete synth .pulse byte buffer into a
// row slice keyed by field name. Decimal128 wide values are surfaced
// as the typed encoding.Decimal128 alongside their float64 view.
func decodeSynthRows(raw []byte) ([]Row, error) {
	r := bytes.NewReader(raw)
	if err := encoding.ReadHeader(r); err != nil {
		return nil, err
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, err
	}
	rr := encoding.NewRecordReader(r, schema)
	var out []Row
	for {
		values := make(map[string]float64, len(schema.Fields))
		nulls := make(map[string]bool, len(schema.Fields))
		wide := make(map[string]any, 0)
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
		row := make(Row, len(schema.Fields))
		for k, v := range values {
			row[k] = v
		}
		for k, v := range wide {
			row[k] = v
		}
		for k := range nulls {
			row[k] = nil
		}
		out = append(out, row)
	}
}

// streamProgress returns a [0.0, 1.0] fraction or -1 when total is
// unknown / non-positive. Clamps to 1.0 if seen exceeds total (e.g.
// when an estimate undershoots reality).
func streamProgress(seen, total int64) float64 {
	if total <= 0 {
		return -1
	}
	if seen >= total {
		return 1
	}
	return float64(seen) / float64(total)
}
