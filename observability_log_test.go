package pulse

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// logRecord is one captured slog record, attrs flattened by key.
type logRecord struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

// captureHandler records every slog record it is handed. It also
// renders each one through a TextHandler into text, so a privacy check
// can scan the full output.
type captureHandler struct {
	mu   sync.Mutex
	recs []logRecord
	text bytes.Buffer
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *captureHandler) WithGroup(string) slog.Handler            { return h }
func (h *captureHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec := logRecord{level: r.Level, msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.Any()
		return true
	})
	h.recs = append(h.recs, rec)
	return slog.NewTextHandler(&h.text, &slog.HandlerOptions{Level: slog.LevelDebug}).Handle(ctx, r)
}

func (h *captureHandler) take() []logRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.recs
	h.recs = nil
	return out
}

func (h *captureHandler) output() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text.String()
}

func (h *captureHandler) logger() *slog.Logger { return slog.New(h) }

// only returns the records with msg.
func only(recs []logRecord, msg string) []logRecord {
	var out []logRecord
	for _, r := range recs {
		if r.msg == msg {
			out = append(out, r)
		}
	}
	return out
}

// TestLogNewEmitsOnceAtInfo: pulse.New logs one Info lifecycle record.
func TestLogNewEmitsOnceAtInfo(t *testing.T) {
	h := &captureHandler{}
	_, _ = obsFixture(t, Options{Logger: h.logger()})
	recs := only(h.take(), logMsgNew)
	if len(recs) != 1 {
		t.Fatalf("want 1 %q record, got %d", logMsgNew, len(recs))
	}
	r := recs[0]
	if r.level != slog.LevelInfo || r.attrs[logKeyOp] != "new" {
		t.Errorf("record = %+v, want Info op=new", r)
	}
	if _, ok := r.attrs[logKeyDurationMS]; !ok {
		t.Errorf("record lacks %s: %+v", logKeyDurationMS, r.attrs)
	}
}

// TestLogFailedOperationCodeOnly: a failed operation logs one Error
// record carrying the code and the stable attrs — never the error
// message, which here echoes a request value.
func TestLogFailedOperationCodeOnly(t *testing.T) {
	h := &captureHandler{}
	p, _ := obsFixture(t, Options{Logger: h.logger()})
	h.take()

	const sentinel = "zz_sentinel_type_value"
	_, err := p.WidenSetField(context.Background(), obsCohort, "region", sentinel)
	if err == nil || !strings.Contains(err.Error(), sentinel) {
		t.Fatalf("precondition: want an error whose message echoes the sentinel, got %v", err)
	}
	recs := only(h.take(), logMsgFailed)
	if len(recs) != 1 {
		t.Fatalf("want 1 %q record, got %d", logMsgFailed, len(recs))
	}
	r := recs[0]
	if r.level != slog.LevelError || r.attrs[logKeyCode] != string(errors.ENCODING_TYPE_MISMATCH) ||
		r.attrs[logKeyOp] != string(observe.OpWiden) || r.attrs[logKeyCohort] != obsCohort {
		t.Errorf("record = %+v, want Error code=%s op=widen cohort=%s", r, errors.ENCODING_TYPE_MISMATCH, obsCohort)
	}
	out := h.output()
	if strings.Contains(out, sentinel) || strings.Contains(out, err.Error()) {
		t.Errorf("error message leaked into the log: %q", out)
	}

	// A successful operation logs no failure.
	if _, err := p.Process(context.Background(), obsRequest()); err != nil {
		t.Fatal(err)
	}
	if recs := only(h.take(), logMsgFailed); len(recs) != 0 {
		t.Errorf("successful Process logged %d failures", len(recs))
	}
}

// TestLogLimitTripWarn: a PULSE_LIMIT_EXCEEDED failure logs a Warn with
// the limit / configured / observed triple (and nothing else from the
// details), then the Error by code.
func TestLogLimitTripWarn(t *testing.T) {
	h := &captureHandler{}
	p, _ := obsFixture(t, Options{Logger: h.logger(), Limits: Limits{MaxGroups: 1}})
	h.take()
	req := obsRequest()
	req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	if _, err := p.Process(context.Background(), req); !errors.HasCode(err, errors.PULSE_LIMIT_EXCEEDED) {
		t.Fatalf("want PULSE_LIMIT_EXCEEDED, got %v", err)
	}
	recs := h.take()
	limit := only(recs, logMsgLimit)
	if len(limit) != 1 {
		t.Fatalf("want 1 %q record, got %d", logMsgLimit, len(limit))
	}
	r := limit[0]
	if r.level != slog.LevelWarn || r.attrs["limit"] != "max_groups" ||
		r.attrs["configured"] != int64(1) || r.attrs["observed"] != int64(2) {
		t.Errorf("limit record = %+v, want Warn max_groups configured=1 observed=2", r)
	}
	for k := range r.attrs {
		switch k {
		case logKeyOp, logKeyCohort, logKeyRequestHash, logKeyDurationMS, "limit", "configured", "observed":
		default:
			t.Errorf("limit record carries unexpected attr %q", k)
		}
	}
	if failed := only(recs, logMsgFailed); len(failed) != 1 || failed[0].attrs[logKeyCode] != string(errors.PULSE_LIMIT_EXCEEDED) {
		t.Errorf("want one Error with code PULSE_LIMIT_EXCEEDED, got %+v", failed)
	}
}

// TestLogResponseWarningsByCode: each warning on an operation's result
// logs one Warn with its code — never its message or details.
func TestLogResponseWarningsByCode(t *testing.T) {
	h := &captureHandler{}
	p := &Pulse{logger: h.logger()}
	const sentinel = "zz_warning_value_sentinel"
	resp := &types.Response{
		Warnings: []*types.ResponseWarning{
			{Code: string(errors.PULSE_LABEL_LOOKUP_MISS), Message: sentinel, Details: map[string]any{"value": sentinel}},
			{Code: string(errors.PULSE_LABEL_COLLISION), Message: sentinel},
		},
	}
	info := observe.OperationInfo{Kind: observe.OpProcess, Cohort: obsCohort, RequestHash: "h1"}
	p.logOperation(context.Background(), info, observe.OperationResult{Duration: time.Millisecond, Code: observe.CodeOK}, nil, resp)
	recs := only(h.take(), logMsgWarning)
	if len(recs) != 2 {
		t.Fatalf("want 2 warning records, got %d", len(recs))
	}
	for i, want := range []errors.Code{errors.PULSE_LABEL_LOOKUP_MISS, errors.PULSE_LABEL_COLLISION} {
		r := recs[i]
		if r.level != slog.LevelWarn || r.attrs[logKeyCode] != string(want) ||
			r.attrs[logKeyOp] != "process" || r.attrs[logKeyCohort] != obsCohort || r.attrs[logKeyRequestHash] != "h1" {
			t.Errorf("warning %d = %+v, want Warn code=%s with stable attrs", i, r, want)
		}
	}
	if strings.Contains(h.output(), sentinel) {
		t.Errorf("warning message/details leaked: %q", h.output())
	}
}

// TestResultWarningCodes covers every warning carrier the logger reads.
func TestResultWarningCodes(t *testing.T) {
	coded := func(c errors.Code) *errors.CodedError { return errors.NewCodedError(c, "msg") }
	resp := &types.Response{
		Warnings: []*types.ResponseWarning{{Code: "A"}, nil, {Code: ""}},
		Overlays: []types.OverlayLayer{{Warnings: []types.OverlayWarning{{Code: "B"}}}},
	}
	cases := []struct {
		name string
		out  any
		want []string
	}{
		{"nil", nil, nil},
		{"response", resp, []string{"A", "B"}},
		{"nil response", (*types.Response)(nil), nil},
		{"composed", &types.ComposedResponse{Responses: []*types.Response{resp, nil}, Overlays: []types.OverlayLayer{{Warnings: []types.OverlayWarning{{Code: "C"}}}}}, []string{"A", "B", "C"}},
		{"chain", &types.ChainResponse{Stages: []*types.Response{resp}}, []string{"A", "B"}},
		{"sample", &SampleResult{Warnings: []SampleWarning{{Code: "S"}}}, []string{"S"}},
		{"envelope", &descriptor.Envelope{Warnings: []*descriptor.EnvelopeEntry{{Code: "E"}}}, []string{"E"}},
		{"add shard", &AddShardResult{Warnings: []encx.CohesionWarning{{Code: "W"}}}, []string{"W"}},
		{"dedup", &DedupResult{SidecarWarnings: []*errors.CodedError{coded(errors.SERVICE_RESOURCE)}}, []string{string(errors.SERVICE_RESOURCE)}},
		{"string warnings ignored", &FacetResult{Warnings: []string{"text"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resultWarningCodes(tc.out)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("codes = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLogLifecycleOperations: index build/drop and shard-archive
// rewrites each log one Info lifecycle record; read operations do not.
func TestLogLifecycleOperations(t *testing.T) {
	h := &captureHandler{}
	p, _ := obsFixture(t, Options{Logger: h.logger()})
	h.take()
	ctx := context.Background()

	steps := []struct {
		kind observe.OperationKind
		run  func() error
	}{
		{observe.OpIndexBuild, func() error { _, err := p.BuildIndex(ctx, obsCohort, []string{"id"}); return err }},
		{observe.OpIndexDrop, func() error { return p.DropIndex(ctx, obsCohort, []string{"id"}) }},
		{observe.OpShardCreate, func() error { _, err := p.CreateShardArchive(ctx, "arch.pulse", []string{obsCohort}); return err }},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			t.Fatalf("%s: %v", s.kind, err)
		}
		recs := only(h.take(), logMsgLifecycle)
		if len(recs) != 1 || recs[0].level != slog.LevelInfo || recs[0].attrs[logKeyOp] != string(s.kind) {
			t.Errorf("%s: want one Info lifecycle record, got %+v", s.kind, recs)
		}
	}

	if _, err := p.Process(ctx, obsRequest()); err != nil {
		t.Fatal(err)
	}
	if recs := only(h.take(), logMsgLifecycle); len(recs) != 0 {
		t.Errorf("Process logged lifecycle records: %+v", recs)
	}
}

// TestLogImportsSweep: an explicit TTL sweep logs one Info record with
// the removed count, through the imports manager the Logger reaches.
func TestLogImportsSweep(t *testing.T) {
	h := &captureHandler{}
	p, _ := obsFixture(t, Options{Logger: h.logger()})
	h.take()
	if _, err := p.SweepImports(context.Background()); err != nil {
		t.Fatal(err)
	}
	recs := only(h.take(), "pulse: imports swept")
	if len(recs) != 1 || recs[0].level != slog.LevelInfo || recs[0].attrs["op"] != "imports_sweep" ||
		recs[0].attrs["removed"] != int64(0) || recs[0].attrs["explicit"] != true {
		t.Fatalf("want one Info imports_sweep removed=0 explicit=true, got %+v", recs)
	}
}
