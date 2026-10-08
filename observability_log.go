package pulse

import (
	"context"
	stderrors "errors"
	"log/slog"
	"time"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// Log messages. The text is not covered by the stability promise; the
// attribute keys below are.
const (
	logMsgFailed    = "pulse: operation failed"
	logMsgLimit     = "pulse: limit exceeded"
	logMsgWarning   = "pulse: operation warning"
	logMsgLifecycle = "pulse: operation completed"
	logMsgNew       = "pulse: instance ready"
)

// Stable log attribute keys (FR-15).
const (
	logKeyOp          = "op"
	logKeyCohort      = "cohort"
	logKeyRequestHash = "request_hash"
	logKeyDurationMS  = "duration_ms"
	logKeyRowsScanned = "rows_scanned"
	logKeyCode        = "code"
)

// lifecycleKinds are the operations whose successful completion is an
// Info lifecycle record: index build/drop and every shard-archive
// rewrite. (The imports TTL sweep and template reloads log from their
// own packages, which also see the sweeps and rescans no facade call
// triggers.)
var lifecycleKinds = map[observe.OperationKind]bool{
	observe.OpIndexBuild:   true,
	observe.OpIndexDrop:    true,
	observe.OpShardCreate:  true,
	observe.OpShardAdd:     true,
	observe.OpShardRemove:  true,
	observe.OpShardCompact: true,
}

// durationMS renders a duration as fractional milliseconds.
func durationMS(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// opAttrs builds the stable attribute set every per-operation record
// carries. Identifiers, counts and timings only.
func opAttrs(info observe.OperationInfo, res observe.OperationResult) []slog.Attr {
	attrs := make([]slog.Attr, 0, 8)
	attrs = append(attrs, slog.String(logKeyOp, string(info.Kind)))
	if info.Cohort != "" {
		attrs = append(attrs, slog.String(logKeyCohort, info.Cohort))
	}
	if info.RequestHash != "" {
		attrs = append(attrs, slog.String(logKeyRequestHash, info.RequestHash))
	}
	attrs = append(attrs, slog.Float64(logKeyDurationMS, durationMS(res.Duration)))
	if res.RowsScanned > 0 {
		attrs = append(attrs, slog.Int64(logKeyRowsScanned, res.RowsScanned))
	}
	return attrs
}

// logOperation writes the per-operation records for one finished
// operation: Warn for a limit trip, Error for a failure (by code only),
// Warn per result warning (by code only) and Info for a lifecycle
// operation. Called only when a Logger is set. Never logs an error
// message, an error's details beyond the limit triple, or a warning's
// message or details — those can echo request values or row data.
func (p *Pulse) logOperation(ctx context.Context, info observe.OperationInfo, res observe.OperationResult, err error, out any) {
	lg := p.logger
	base := opAttrs(info, res)
	if err != nil {
		var coded *errors.CodedError
		if stderrors.As(err, &coded) && coded != nil && coded.Code == errors.PULSE_LIMIT_EXCEEDED {
			attrs := append(append([]slog.Attr(nil), base...), limitAttrs(coded.Details)...)
			lg.LogAttrs(ctx, slog.LevelWarn, logMsgLimit, attrs...)
		}
		lg.LogAttrs(ctx, slog.LevelError, logMsgFailed, append(base, slog.String(logKeyCode, res.Code))...)
		return
	}
	if out != nil && lg.Enabled(ctx, slog.LevelWarn) {
		for _, code := range resultWarningCodes(out) {
			attrs := append(append([]slog.Attr(nil), base...), slog.String(logKeyCode, code))
			lg.LogAttrs(ctx, slog.LevelWarn, logMsgWarning, attrs...)
		}
	}
	if lifecycleKinds[info.Kind] {
		lg.LogAttrs(ctx, slog.LevelInfo, logMsgLifecycle, base...)
	}
}

// limitAttrs projects the PULSE_LIMIT_EXCEEDED details triple — the
// limit's name and its configured and observed magnitudes. Every other
// detail key is dropped.
func limitAttrs(details map[string]any) []slog.Attr {
	var attrs []slog.Attr
	for _, key := range [...]string{"limit", "configured", "observed"} {
		if v, ok := details[key]; ok {
			attrs = append(attrs, slog.Any(key, v))
		}
	}
	return attrs
}

// resultWarningCodes collects the code of every warning an operation's result
// carries, in result order. Uncoded (string) warnings contribute
// nothing: their text is a message and may echo values.
func resultWarningCodes(out any) []string {
	var codes []string
	addCoded := func(ws []*errors.CodedError) {
		for _, w := range ws {
			if w != nil && w.Code != "" {
				codes = append(codes, string(w.Code))
			}
		}
	}
	addLabel := func(ws []pio.LabelWarning) {
		for _, w := range ws {
			if w.Code != "" {
				codes = append(codes, w.Code)
			}
		}
	}
	addResponse := func(r *types.Response) {
		if r == nil {
			return
		}
		for _, w := range r.Warnings {
			if w != nil && w.Code != "" {
				codes = append(codes, w.Code)
			}
		}
		for _, l := range r.Overlays {
			for _, w := range l.Warnings {
				if w.Code != "" {
					codes = append(codes, w.Code)
				}
			}
		}
	}
	addCohesion := func(ws []encx.CohesionWarning) {
		for _, w := range ws {
			if w.Code != "" {
				codes = append(codes, w.Code)
			}
		}
	}
	switch v := out.(type) {
	case *types.Response:
		addResponse(v)
	case *types.ComposedResponse:
		if v == nil {
			break
		}
		for _, r := range v.Responses {
			addResponse(r)
		}
		for _, l := range v.Overlays {
			for _, w := range l.Warnings {
				if w.Code != "" {
					codes = append(codes, w.Code)
				}
			}
		}
	case *types.ChainResponse:
		if v == nil {
			break
		}
		for _, r := range v.Stages {
			addResponse(r)
		}
	case *SampleResult:
		if v == nil {
			break
		}
		for _, w := range v.Warnings {
			if w.Code != "" {
				codes = append(codes, w.Code)
			}
		}
	case *descriptor.Envelope:
		if v == nil {
			break
		}
		for _, w := range v.Warnings {
			if w != nil && w.Code != "" {
				codes = append(codes, w.Code)
			}
		}
	case *AddShardResult:
		if v != nil {
			addCohesion(v.Warnings)
		}
	case *CreateShardArchiveResult:
		if v != nil {
			addCohesion(v.Warnings)
		}
	case *VerifyResult:
		if v != nil {
			addCohesion(v.Warnings)
		}
	case *ImportResult:
		if v != nil {
			addCoded(v.WidthWarnings)
			addCoded(v.SourceWarnings)
			addCoded(v.GroupWarnings)
		}
	case *pio.ImportReport:
		if v != nil {
			addCoded(v.WidthWarnings)
			addCoded(v.ZoneWarnings)
			addCoded(v.SourceWarnings)
			addCoded(v.GroupWarnings)
		}
	case *pio.ExportReport:
		if v != nil {
			addLabel(v.LabelWarnings)
			addCoded(v.OverlayWarnings)
			addCoded(v.TargetWarnings)
		}
	case *pio.ConvertReport:
		if v != nil {
			addLabel(v.LabelWarnings)
			addCoded(v.OverlayWarnings)
			addCoded(v.SourceWarnings)
			addCoded(v.TargetWarnings)
			addCoded(v.WidthWarnings)
			addCoded(v.ZoneWarnings)
		}
	case *DedupResult:
		if v != nil {
			addCoded(v.SidecarWarnings)
		}
	}
	return codes
}

// logNew writes the Info lifecycle record for a constructed instance.
func logNew(lg *slog.Logger, start time.Time, templateDirs int) {
	if lg == nil {
		return
	}
	lg.LogAttrs(context.Background(), slog.LevelInfo, logMsgNew,
		slog.String(logKeyOp, "new"),
		slog.Float64(logKeyDurationMS, durationMS(time.Since(start))),
		slog.Int("template_dirs", templateDirs),
	)
}
