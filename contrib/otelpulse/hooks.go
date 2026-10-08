// Package otelpulse adapts Pulse's observability surface to
// OpenTelemetry: Hooks turns every Pulse operation into a span, and
// Metrics records Pulse's documented metric set through an OTel
// MeterProvider.
//
//	p, err := pulse.New(pulse.Options{
//		Hooks:   otelpulse.Hooks(tracerProvider),
//		Metrics: otelpulse.Metrics(meterProvider),
//	})
//
// Spans. Each operation is one span named "pulse.<op>" (pulse.process,
// pulse.compose, …), started from the operation's context, so a call
// made under a host request span nests beneath it. A Compose,
// ComposeParallel or ProcessChain slot or stage runs as a child
// operation; its span, named "pulse.process" because each one runs a
// single process request, nests under the parent operation's span and
// carries the parent's kind in pulse.op, pulse.scope=child and its
// slot or stage number in pulse.index.
//
// Attributes: pulse.op, pulse.scope, pulse.cohort and pulse.request_hash
// at start; pulse.code, pulse.arm, pulse.rows_scanned,
// pulse.rows_matched, pulse.rows_out, pulse.bytes_read, pulse.shards,
// pulse.workers and pulse.projected at end (a counter that stayed zero
// is omitted). A failed operation also carries pulse.error_code and an
// Error status whose description is the code — never the error message,
// which may echo request values.
//
// Phases are span events named after the phase (plan, open, decode,
// scan, reduce, post, overlay, shape) with pulse.phase and
// pulse.phase.duration_ms attributes. Pulse reports phases when the
// operation ends, not as they happen; each event is timestamped at the
// phase's start, reconstructed from the operation's start plus the
// durations of the phases before it (a phase that ran in several pieces
// is reported once, with their sum).
//
// This package lives in its own Go module so the Pulse core module never
// depends on OpenTelemetry.
package otelpulse

import (
	"context"
	"time"

	"github.com/frankbardon/pulse/observe"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ScopeName is the instrumentation scope of the tracer and meter this
// package creates.
const ScopeName = "github.com/frankbardon/pulse/contrib/otelpulse"

// Attribute keys.
const (
	AttrOp          = attribute.Key("pulse.op")
	AttrScope       = attribute.Key("pulse.scope")
	AttrIndex       = attribute.Key("pulse.index")
	AttrCohort      = attribute.Key("pulse.cohort")
	AttrRequestHash = attribute.Key("pulse.request_hash")
	AttrCode        = attribute.Key("pulse.code")
	AttrErrorCode   = attribute.Key("pulse.error_code")
	AttrArm         = attribute.Key("pulse.arm")
	AttrRowsScanned = attribute.Key("pulse.rows_scanned")
	AttrRowsMatched = attribute.Key("pulse.rows_matched")
	AttrRowsOut     = attribute.Key("pulse.rows_out")
	AttrBytesRead   = attribute.Key("pulse.bytes_read")
	AttrShards      = attribute.Key("pulse.shards")
	AttrWorkers     = attribute.Key("pulse.workers")
	AttrProjected   = attribute.Key("pulse.projected")
	AttrPhase       = attribute.Key("pulse.phase")
	AttrPhaseMillis = attribute.Key("pulse.phase.duration_ms")
)

// Hooks returns observe.Hooks that trace every Pulse operation with tp
// (the global TracerProvider when tp is nil).
func Hooks(tp trace.TracerProvider) *observe.Hooks {
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	t := &tracer{tr: tp.Tracer(ScopeName)}
	return &observe.Hooks{
		OnOperationStart: t.start,
		OnPhase:          t.phase,
		OnOperationEnd:   t.end,
	}
}

// SpanName is the span name Hooks gives an operation: "pulse.<kind>" for
// a top-level operation, "pulse.process" for a child (a Compose slot or
// ProcessChain stage, each a single process request).
func SpanName(info observe.OperationInfo) string {
	if info.Scope == observe.ScopeChild {
		return "pulse." + string(observe.OpProcess)
	}
	return "pulse." + string(info.Kind)
}

type tracer struct {
	tr trace.Tracer
}

// opSpan is the per-operation state carried in the context
// OnOperationStart returns: the span, its start and the running phase
// offset used to timestamp phase events.
type opSpan struct {
	span   trace.Span
	start  time.Time
	offset time.Duration
}

type spanKey struct{}

func (t *tracer) start(ctx context.Context, info observe.OperationInfo) context.Context {
	attrs := make([]attribute.KeyValue, 0, 5)
	attrs = append(attrs, AttrOp.String(string(info.Kind)), AttrScope.String(string(info.Scope)))
	if info.Scope == observe.ScopeChild {
		attrs = append(attrs, AttrIndex.Int(info.Index))
	}
	if info.Cohort != "" {
		attrs = append(attrs, AttrCohort.String(info.Cohort))
	}
	if info.RequestHash != "" {
		attrs = append(attrs, AttrRequestHash.String(info.RequestHash))
	}
	now := time.Now()
	ctx, span := t.tr.Start(ctx, SpanName(info),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithTimestamp(now),
		trace.WithAttributes(attrs...))
	return context.WithValue(ctx, spanKey{}, &opSpan{span: span, start: now})
}

func (t *tracer) phase(ctx context.Context, _ observe.OperationInfo, ph observe.PhaseTiming) {
	st, _ := ctx.Value(spanKey{}).(*opSpan)
	if st == nil {
		return
	}
	at := st.start.Add(st.offset)
	st.offset += ph.Duration
	st.span.AddEvent(string(ph.Phase),
		trace.WithTimestamp(at),
		trace.WithAttributes(
			AttrPhase.String(string(ph.Phase)),
			AttrPhaseMillis.Float64(float64(ph.Duration)/float64(time.Millisecond)),
		))
}

func (t *tracer) end(ctx context.Context, _ observe.OperationInfo, res observe.OperationResult) {
	st, _ := ctx.Value(spanKey{}).(*opSpan)
	if st == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 10)
	attrs = append(attrs, AttrCode.String(res.Code))
	if res.Arm != "" {
		attrs = append(attrs, AttrArm.String(string(res.Arm)))
	}
	for _, c := range []struct {
		k attribute.Key
		v int64
	}{
		{AttrRowsScanned, res.RowsScanned},
		{AttrRowsMatched, res.RowsMatched},
		{AttrRowsOut, res.RowsOut},
		{AttrBytesRead, res.BytesRead},
		{AttrShards, int64(res.Shards)},
		{AttrWorkers, int64(res.Workers)},
	} {
		if c.v != 0 {
			attrs = append(attrs, c.k.Int64(c.v))
		}
	}
	if res.Projected {
		attrs = append(attrs, AttrProjected.Bool(true))
	}
	if res.Code != observe.CodeOK {
		attrs = append(attrs, AttrErrorCode.String(res.Code))
		st.span.SetStatus(codes.Error, res.Code)
	}
	st.span.SetAttributes(attrs...)
	st.span.End(trace.WithTimestamp(st.start.Add(res.Duration)))
}
