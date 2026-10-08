package otelpulse_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/contrib/otelpulse"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	cohort   = "sales.pulse"
	salesCSV = "region,amount\nnorth,10\nsouth,20\nnorth,5\n"
)

func engine(t *testing.T, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	fs := afero.NewMemMapFs()
	opts.FS = fs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	r, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(salesCSV), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewImportJob(r, cohort)
	job.FS = fs
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("Import: %v", err)
	}
	return p
}

func request() *pulse.Request {
	return &pulse.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}},
	}
}

func attr(s tracetest.SpanStub, k attribute.Key) (attribute.Value, bool) {
	for _, kv := range s.Attributes {
		if kv.Key == k {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func eventNames(s tracetest.SpanStub) []string {
	var out []string
	for _, e := range s.Events {
		out = append(out, e.Name)
	}
	return out
}

// TestHooksSpanNesting: under a host request span, Compose is one
// pulse.compose span child of the host span, and each slot is a
// pulse.process span child of the compose span. Phases are span events
// with durations, and the run facts ride as attributes.
func TestHooksSpanNesting(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	p := engine(t, pulse.Options{Hooks: otelpulse.Hooks(tp)})
	exp.Reset() // drop the fixture's import span

	ctx, host := tp.Tracer("host").Start(context.Background(), "host.request")
	if _, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*pulse.Request{request(), request()}}); err != nil {
		t.Fatal(err)
	}
	host.End()

	spans := exp.GetSpans()
	byName := map[string][]tracetest.SpanStub{}
	for _, s := range spans {
		byName[s.Name] = append(byName[s.Name], s)
	}
	if len(byName["host.request"]) != 1 || len(byName["pulse.compose"]) != 1 || len(byName["pulse.process"]) != 2 || len(spans) != 4 {
		t.Fatalf("spans %v, want host.request, pulse.compose, 2×pulse.process", eventNamesOf(spans))
	}
	hostSpan, compose := byName["host.request"][0], byName["pulse.compose"][0]
	if compose.Parent.SpanID() != hostSpan.SpanContext.SpanID() || compose.SpanContext.TraceID() != hostSpan.SpanContext.TraceID() {
		t.Fatalf("pulse.compose not a child of the host span")
	}
	var indexes []int64
	for _, c := range byName["pulse.process"] {
		if c.Parent.SpanID() != compose.SpanContext.SpanID() {
			t.Errorf("slot span not a child of pulse.compose")
		}
		if v, _ := attr(c, otelpulse.AttrScope); v.AsString() != "child" {
			t.Errorf("slot scope %q, want child", v.AsString())
		}
		if v, _ := attr(c, otelpulse.AttrOp); v.AsString() != "compose" {
			t.Errorf("slot pulse.op %q, want compose", v.AsString())
		}
		if v, _ := attr(c, otelpulse.AttrCohort); v.AsString() == "" {
			t.Error("slot has no pulse.cohort")
		}
		if v, _ := attr(c, otelpulse.AttrRequestHash); v.AsString() == "" {
			t.Error("slot has no pulse.request_hash")
		}
		if v, _ := attr(c, otelpulse.AttrRowsScanned); v.AsInt64() != 3 {
			t.Errorf("slot rows_scanned %d, want 3", v.AsInt64())
		}
		if _, ok := attr(c, otelpulse.AttrArm); !ok {
			t.Error("slot has no pulse.arm")
		}
		if !slices.Contains(eventNames(c), "scan") {
			t.Errorf("slot phase events %v lack scan", eventNames(c))
		}
		v, _ := attr(c, otelpulse.AttrIndex)
		indexes = append(indexes, v.AsInt64())
	}
	slices.Sort(indexes)
	if !slices.Equal(indexes, []int64{0, 1}) {
		t.Errorf("slot indexes %v, want [0 1]", indexes)
	}

	if v, _ := attr(compose, otelpulse.AttrCode); v.AsString() != observe.CodeOK {
		t.Errorf("compose pulse.code %q, want ok", v.AsString())
	}
	if _, ok := attr(compose, otelpulse.AttrErrorCode); ok {
		t.Error("successful compose carries pulse.error_code")
	}
	if v, _ := attr(compose, otelpulse.AttrRowsScanned); v.AsInt64() != 6 {
		t.Errorf("compose rows_scanned %d, want 6 (sum of slots)", v.AsInt64())
	}
	if len(compose.Events) == 0 || compose.Events[0].Name != "plan" {
		t.Fatalf("compose phase events %v, want plan first", eventNames(compose))
	}
	prev := compose.StartTime
	for _, e := range compose.Events {
		ph := attribute.NewSet(e.Attributes...)
		if v, ok := ph.Value(otelpulse.AttrPhase); !ok || v.AsString() != e.Name {
			t.Errorf("event %s: pulse.phase %v", e.Name, v)
		}
		if v, ok := ph.Value(otelpulse.AttrPhaseMillis); !ok || v.AsFloat64() < 0 {
			t.Errorf("event %s: no duration", e.Name)
		}
		if e.Time.Before(prev) || e.Time.After(compose.EndTime) {
			t.Errorf("event %s at %v outside [%v, %v]", e.Name, e.Time, prev, compose.EndTime)
		}
		prev = e.Time
	}
}

func eventNamesOf(spans tracetest.SpanStubs) []string {
	var out []string
	for _, s := range spans {
		out = append(out, s.Name)
	}
	return out
}

// TestHooksErrorCode: a failed operation's span carries the Pulse error
// code as pulse.error_code and an Error status whose description is the
// code alone.
func TestHooksErrorCode(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	p := engine(t, pulse.Options{Hooks: otelpulse.Hooks(tp)})
	exp.Reset()

	bad := request()
	bad.Aggregations[0].Field = "no_such_field"
	if _, err := p.Process(context.Background(), bad); err == nil {
		t.Fatal("want a failure")
	}
	spans := exp.GetSpans()
	if len(spans) != 1 || spans[0].Name != "pulse.process" {
		t.Fatalf("spans %v, want one pulse.process", eventNamesOf(spans))
	}
	s := spans[0]
	code, ok := attr(s, otelpulse.AttrErrorCode)
	if !ok || code.AsString() == "" || code.AsString() == observe.CodeOK {
		t.Fatalf("pulse.error_code = %q", code.AsString())
	}
	if c, _ := attr(s, otelpulse.AttrCode); c.AsString() != code.AsString() {
		t.Errorf("pulse.code %q != pulse.error_code %q", c.AsString(), code.AsString())
	}
	if s.Status.Code != codes.Error || s.Status.Description != code.AsString() {
		t.Errorf("status %+v, want Error with the code as description", s.Status)
	}
}

// documented is Pulse's documented metric set.
var documented = []string{
	"pulse_operations_total", "pulse_operation_duration_seconds", "pulse_phase_duration_seconds",
	"pulse_rows_scanned_total", "pulse_bytes_read_total", "pulse_limit_trips_total",
	"pulse_hook_panics_total", "pulse_operations_in_flight",
}

// TestMetricsDocumentedSet: a Pulse instance wired to an OTel
// MeterProvider through Metrics records every metric of the documented
// set, with the documented instrument kind and labels as attributes.
func TestMetricsDocumentedSet(t *testing.T) {
	ctx := context.Background()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	p := engine(t, pulse.Options{
		Metrics: otelpulse.Metrics(mp),
		Limits:  pulse.Limits{MaxGroups: 1},
		Hooks: &observe.Hooks{
			OnOperationStart: func(ctx context.Context, info observe.OperationInfo) context.Context {
				if info.Kind == observe.OpManifest {
					panic("host hook bug")
				}
				return ctx
			},
		},
	})
	if _, err := p.Process(ctx, request()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*pulse.Request{request(), request()}}); err != nil {
		t.Fatal(err)
	}
	grouped := request()
	grouped.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	if _, err := p.Process(ctx, grouped); err == nil {
		t.Fatal("want a MaxGroups limit trip")
	}
	_ = p.Manifest(ctx)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != otelpulse.ScopeName {
			continue
		}
		for _, m := range sm.Metrics {
			got[m.Name] = m
		}
	}
	for _, name := range documented {
		if _, ok := got[name]; !ok {
			t.Errorf("%s not recorded", name)
		}
	}

	sumOf := func(name string, want ...attribute.KeyValue) (float64, bool) {
		s, ok := got[name].Data.(metricdata.Sum[float64])
		if !ok {
			t.Fatalf("%s is %T, want a float64 sum", name, got[name].Data)
		}
		for _, dp := range s.DataPoints {
			match := true
			for _, kv := range want {
				if v, ok := dp.Attributes.Value(kv.Key); !ok || v != kv.Value {
					match = false
				}
			}
			if match {
				return dp.Value, s.IsMonotonic
			}
		}
		return -1, s.IsMonotonic
	}
	if v, mono := sumOf("pulse_operations_total", attribute.String("op", "compose"), attribute.String("code", "ok"), attribute.String("scope", "child")); v != 2 || !mono {
		t.Errorf("compose child ops = %v (monotonic %v), want 2", v, mono)
	}
	if v, _ := sumOf("pulse_operations_total", attribute.String("op", "process"), attribute.String("code", "PULSE_LIMIT_EXCEEDED")); v != 1 {
		t.Errorf("limit-exceeded process ops = %v, want 1", v)
	}
	if v, _ := sumOf("pulse_limit_trips_total", attribute.String("limit", "max_groups")); v != 1 {
		t.Errorf("max_groups trips = %v, want 1", v)
	}
	if v, _ := sumOf("pulse_hook_panics_total", attribute.String("hook", "start")); v != 1 {
		t.Errorf("start hook panics = %v, want 1", v)
	}
	if v, mono := sumOf("pulse_operations_in_flight", attribute.String("op", "process")); v != 0 || mono {
		t.Errorf("process in-flight = %v (monotonic %v), want 0 non-monotonic", v, mono)
	}
	h, ok := got["pulse_phase_duration_seconds"].Data.(metricdata.Histogram[float64])
	if !ok || got["pulse_phase_duration_seconds"].Unit != "s" {
		t.Fatalf("phase duration: %T unit %q", got["pulse_phase_duration_seconds"].Data, got["pulse_phase_duration_seconds"].Unit)
	}
	var scans uint64
	for _, dp := range h.DataPoints {
		if v, _ := dp.Attributes.Value("phase"); v.AsString() == "scan" {
			scans += dp.Count
		}
		if !slices.Equal(dp.Bounds, otelpulse.DefaultBuckets) {
			t.Errorf("bounds %v, want DefaultBuckets", dp.Bounds)
		}
	}
	if scans == 0 {
		t.Error("no scan phase observation")
	}
}

// TestMetricsConcurrentAndClash: the factory is safe for concurrent use
// (run with -race), and a name reused with another kind is a no-op.
func TestMetricsConcurrentAndClash(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	m := otelpulse.Metrics(mp)
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				m.Counter("pulse_operations_total",
					observe.Label{Key: "op", Value: "process"},
					observe.Label{Key: "code", Value: fmt.Sprintf("CODE_%d", (g+i)%5)},
					observe.Label{Key: "scope", Value: "top"}).Add(1)
			}
		}()
	}
	wg.Wait()
	m.Histogram("pulse_operations_total").Observe(1) // kind clash: no-op
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, sm := range rm.ScopeMetrics {
		for _, mt := range sm.Metrics {
			s, ok := mt.Data.(metricdata.Sum[float64])
			if !ok {
				t.Fatalf("%s is %T", mt.Name, mt.Data)
			}
			for _, dp := range s.DataPoints {
				total += dp.Value
			}
		}
	}
	if total != 16*50 {
		t.Fatalf("total %v, want %d", total, 16*50)
	}
}
