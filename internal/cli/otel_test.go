package cli

import (
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/frankbardon/pulse/observe"
	"github.com/spf13/afero"
	colmetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// mapEnv is an injected environment: unset keys are absent.
func mapEnv(kv map[string]string) envLookup {
	return func(k string) (string, bool) {
		v, ok := kv[k]
		return v, ok
	}
}

// otlpReceiver is an in-process OTLP/HTTP collector: it decodes every
// /v1/traces and /v1/metrics protobuf POST and records span names,
// metric names and service.name values.
type otlpReceiver struct {
	srv *httptest.Server

	mu       sync.Mutex
	spans    []string
	metrics  []string
	services []string
}

func newOTLPReceiver(t *testing.T) *otlpReceiver {
	t.Helper()
	r := &otlpReceiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if ct := req.Header.Get("Content-Type"); ct != "application/x-protobuf" {
			http.Error(w, "content type "+ct, http.StatusUnsupportedMediaType)
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		switch req.URL.Path {
		case "/v1/traces":
			var m coltrace.ExportTraceServiceRequest
			if err := proto.Unmarshal(body, &m); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, rs := range m.ResourceSpans {
				r.services = append(r.services, serviceName(rs.GetResource().GetAttributes()))
				for _, ss := range rs.ScopeSpans {
					for _, s := range ss.Spans {
						r.spans = append(r.spans, s.Name)
					}
				}
			}
			out, _ := proto.Marshal(&coltrace.ExportTraceServiceResponse{})
			_, _ = w.Write(out)
		case "/v1/metrics":
			var m colmetric.ExportMetricsServiceRequest
			if err := proto.Unmarshal(body, &m); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, rm := range m.ResourceMetrics {
				r.services = append(r.services, serviceName(rm.GetResource().GetAttributes()))
				for _, sm := range rm.ScopeMetrics {
					for _, mm := range sm.Metrics {
						r.metrics = append(r.metrics, mm.Name)
					}
				}
			}
			out, _ := proto.Marshal(&colmetric.ExportMetricsServiceResponse{})
			_, _ = w.Write(out)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *otlpReceiver) snapshot() (spans, metrics, services []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.spans), slices.Clone(r.metrics), slices.Clone(r.services)
}

func serviceName(attrs []*commonpb.KeyValue) string {
	for _, kv := range attrs {
		if kv.GetKey() == "service.name" {
			return kv.GetValue().GetStringValue()
		}
	}
	return ""
}

func codedOf(t *testing.T, err error) *perrors.CodedError {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want a *CodedError", err, err)
	}
	return ce
}

// TestStartOTLP_UnsetBuildsNothing: with no endpoint in the environment
// startOTLP builds no provider — even when a protocol variable is set
// (a shell-wide grpc setting for other tools must not stop pulse mcp).
func TestStartOTLP_UnsetBuildsNothing(t *testing.T) {
	t.Parallel()

	for _, env := range []map[string]string{
		{},
		{envOTLPEndpoint: "", envOTLPTracesEndpoint: "  "},
		{envOTLPProtocol: "grpc"},
	} {
		x, err := startOTLP(context.Background(), mapEnv(env))
		if err != nil || x != nil {
			t.Errorf("env %v: startOTLP = %v, %v; want nil, nil", env, x, err)
		}
	}
	// A nil export shuts down as a no-op.
	var x *otlpExport
	if err := x.Shutdown(context.Background()); err != nil {
		t.Errorf("nil Shutdown = %v", err)
	}
}

// TestStartOTLP_RefusesNonHTTPProtocol: an effective protocol other
// than http/protobuf for an exporting signal is CLI_INPUT naming the
// variable that supplied it; a per-signal value overrides the shared one.
func TestStartOTLP_RefusesNonHTTPProtocol(t *testing.T) {
	t.Parallel()

	const ep = "http://127.0.0.1:1"
	cases := []struct {
		name    string
		env     map[string]string
		wantVar string
		wantVal string
	}{
		{"shared grpc", map[string]string{envOTLPEndpoint: ep, envOTLPProtocol: "grpc"}, envOTLPProtocol, "grpc"},
		{"traces grpc", map[string]string{envOTLPEndpoint: ep, envOTLPTracesProtocol: "grpc"}, envOTLPTracesProtocol, "grpc"},
		{"metrics http/json", map[string]string{envOTLPMetricsEndpoint: ep, envOTLPMetricsProtocol: "http/json"}, envOTLPMetricsProtocol, "http/json"},
		{"shared grpc, one override", map[string]string{envOTLPEndpoint: ep, envOTLPProtocol: "grpc", envOTLPTracesProtocol: otlpProtocolHTTP}, envOTLPProtocol, "grpc"},
		{"shared grpc, both overridden", map[string]string{envOTLPEndpoint: ep, envOTLPProtocol: "grpc", envOTLPTracesProtocol: otlpProtocolHTTP, envOTLPMetricsProtocol: otlpProtocolHTTP}, "", ""},
		{"http/protobuf", map[string]string{envOTLPEndpoint: ep, envOTLPProtocol: otlpProtocolHTTP}, "", ""},
		// grpc on a signal that does not export is not consulted.
		{"grpc on the idle signal", map[string]string{envOTLPTracesEndpoint: ep, envOTLPMetricsProtocol: "grpc"}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x, err := startOTLP(context.Background(), mapEnv(tc.env))
			t.Cleanup(func() { _ = x.Shutdown(context.Background()) })
			if tc.wantVar == "" {
				if err != nil || x == nil {
					t.Fatalf("startOTLP = %v, %v; want an export", x, err)
				}
				return
			}
			if x != nil {
				t.Errorf("refused config built an export")
			}
			ce := codedOf(t, err)
			if ce.Code != perrors.CLI_INPUT {
				t.Errorf("code = %s, want CLI_INPUT", ce.Code)
			}
			if ce.Details["variable"] != tc.wantVar || ce.Details["value"] != tc.wantVal {
				t.Errorf("details = %v, want variable %s value %s", ce.Details, tc.wantVar, tc.wantVal)
			}
			if !strings.Contains(ce.Message, "HTTP only") {
				t.Errorf("message = %q, want it to say only HTTP is supported", ce.Message)
			}
		})
	}
}

// TestStartOTLP_BadEndpointIsCLIInput: an endpoint that is not an
// http(s) URL is refused, naming the variable — never silently swapped
// for the exporter's localhost default.
func TestStartOTLP_BadEndpointIsCLIInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, value string }{
		{envOTLPEndpoint, "localhost:4318"},
		{envOTLPTracesEndpoint, "grpc://collector:4317"},
		{envOTLPMetricsEndpoint, "http://"},
	} {
		_, err := startOTLP(context.Background(), mapEnv(map[string]string{tc.name: tc.value}))
		ce := codedOf(t, err)
		if ce.Code != perrors.CLI_INPUT || ce.Details["variable"] != tc.name || ce.Details["value"] != tc.value {
			t.Errorf("%s=%s: %v %v", tc.name, tc.value, ce.Code, ce.Details)
		}
	}
}

// TestStartOTLP_SignalsFollowEndpoints: the shared endpoint exports
// both signals; a per-signal endpoint exports only its signal.
func TestStartOTLP_SignalsFollowEndpoints(t *testing.T) {
	t.Parallel()

	const ep = "http://127.0.0.1:1"
	for _, tc := range []struct {
		env              map[string]string
		signals          string
		hooks, metricsOn bool
	}{
		{map[string]string{envOTLPEndpoint: ep}, "traces+metrics", true, true},
		{map[string]string{envOTLPTracesEndpoint: ep + "/t"}, "traces", true, false},
		{map[string]string{envOTLPMetricsEndpoint: ep + "/m"}, "metrics", false, true},
	} {
		x, err := startOTLP(context.Background(), mapEnv(tc.env))
		if err != nil {
			t.Fatalf("%v: %v", tc.env, err)
		}
		if x.Signals != tc.signals || (x.Hooks != nil) != tc.hooks || (x.Metrics != nil) != tc.metricsOn {
			t.Errorf("%v: signals %q hooks %v metrics %v", tc.env, x.Signals, x.Hooks != nil, x.Metrics != nil)
		}
		_ = x.Shutdown(context.Background())
	}
}

// TestStartOTLP_ExportsToReceiver: an operation run through an instance
// wired with the export reaches an OTLP/HTTP receiver as a pulse.<op>
// span plus Pulse's metrics once the providers shut down (flush).
func TestStartOTLP_ExportsToReceiver(t *testing.T) {
	t.Parallel()

	rcv := newOTLPReceiver(t)
	x, err := startOTLP(context.Background(), mapEnv(map[string]string{envOTLPEndpoint: rcv.srv.URL}))
	if err != nil || x == nil {
		t.Fatalf("startOTLP = %v, %v", x, err)
	}

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs, Hooks: x.Hooks, Metrics: x.Metrics, DisablePanicRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte("region,amount\nnorth,10\n"), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewImportJob(r, "s.pulse")
	job.FS = fs
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Inspect(context.Background(), "s.pulse"); err != nil {
		t.Fatal(err)
	}
	if err := x.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	spans, metrics, services := rcv.snapshot()
	if !slices.Contains(spans, "pulse.inspect") || !slices.Contains(spans, "pulse.import") {
		t.Errorf("receiver spans = %v, want pulse.inspect and pulse.import", spans)
	}
	if !slices.Contains(metrics, "pulse_operations_total") || !slices.Contains(metrics, "pulse_operation_duration_seconds") {
		t.Errorf("receiver metrics = %v, want Pulse's metric set", metrics)
	}
	if len(services) == 0 || slices.ContainsFunc(services, func(s string) bool { return s == "" }) {
		t.Errorf("receiver resources' service.name = %v, want every batch named", services)
	}
}

// recSink is an observe.Metrics recording every Add / Observe by name.
type recSink struct {
	mu  sync.Mutex
	got map[string]float64
}

type recInst struct {
	s    *recSink
	name string
}

func (r recInst) Add(d float64)     { r.s.mu.Lock(); r.s.got[r.name] += d; r.s.mu.Unlock() }
func (r recInst) Observe(v float64) { r.Add(v) }

func (s *recSink) Counter(n string, _ ...observe.Label) observe.Counter     { return recInst{s, n} }
func (s *recSink) Histogram(n string, _ ...observe.Label) observe.Histogram { return recInst{s, n} }
func (s *recSink) UpDownCounter(n string, _ ...observe.Label) observe.UpDownCounter {
	return recInst{s, n}
}

// TestFanMetrics_RecordsToEverySink: the --metrics-addr + OTLP fan-out
// forwards each counter, histogram and up-down counter to every sink;
// nil sinks drop out and a single sink is returned unwrapped.
func TestFanMetrics_RecordsToEverySink(t *testing.T) {
	t.Parallel()

	if fanMetrics() != nil || fanMetrics(nil, nil) != nil {
		t.Error("no sinks: want nil (pulse.Options.Metrics stays unset)")
	}
	only := &recSink{got: map[string]float64{}}
	if fanMetrics(nil, only) != observe.Metrics(only) {
		t.Error("one sink: want it returned as-is")
	}

	a, b := &recSink{got: map[string]float64{}}, &recSink{got: map[string]float64{}}
	f := fanMetrics(a, nil, b)
	f.Counter("c").Add(2)
	f.Histogram("h").Observe(3)
	f.UpDownCounter("u").Add(-1)
	for _, s := range []*recSink{a, b} {
		if s.got["c"] != 2 || s.got["h"] != 3 || s.got["u"] != -1 {
			t.Errorf("sink got %v", s.got)
		}
	}
}

// TestOTLPErrorHandler_WarnsNeverFatal: export errors become warn
// records on the root logger, or on stderr at warn when logging is off.
func TestOTLPErrorHandler_WarnsNeverFatal(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var stderr bytes.Buffer
	otlpErrorHandler(lg, &stderr).Handle(stderrors.New("connection refused"))
	if !strings.Contains(logged.String(), `"level":"WARN"`) || !strings.Contains(logged.String(), "connection refused") {
		t.Errorf("logger got %q", logged.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("with a logger, stderr got %q", stderr.String())
	}

	otlpErrorHandler(nil, &stderr).Handle(stderrors.New("connection refused"))
	if !strings.Contains(stderr.String(), "level=WARN") || !strings.Contains(stderr.String(), "connection refused") {
		t.Errorf("logging off: stderr got %q", stderr.String())
	}
}

// TestMCPStartupLine_NamesOTLPSignals: the startup notice names the
// exporting OTLP signals, never an endpoint.
func TestMCPStartupLine_NamesOTLPSignals(t *testing.T) {
	t.Parallel()

	info := mcpserve.ServeInfo{CohortScan: true}
	line := mcpStartupLine("/d", true, info, mcpObsSettings{OTLP: "traces+metrics"})
	if !strings.HasSuffix(line, ", otlp: traces+metrics)") {
		t.Errorf("startup line = %q", line)
	}
	if strings.Contains(mcpStartupLine("/d", true, info, mcpObsSettings{}), "otlp") {
		t.Error("no OTLP: the line names it anyway")
	}
}
