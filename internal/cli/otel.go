package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/observe"
	otelpulse "github.com/frankbardon/pulse/observe/otel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The standard OpenTelemetry environment `pulse mcp` reads to decide
// whether, and where, to export over OTLP/HTTP. Endpoint resolution
// follows the OTLP exporter spec: a per-signal endpoint is used as-is,
// the shared one gets the signal path (/v1/traces, /v1/metrics)
// appended. Everything else (headers, timeouts, compression, the batch
// and periodic-reader knobs, OTEL_SERVICE_NAME,
// OTEL_RESOURCE_ATTRIBUTES) is read by the OTel SDK itself.
const (
	envOTLPEndpoint        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPTracesEndpoint  = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envOTLPMetricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	envOTLPProtocol        = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTLPTracesProtocol  = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	envOTLPMetricsProtocol = "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"
	envOTLPResourceAttrs   = "OTEL_RESOURCE_ATTRIBUTES"

	// otlpProtocolHTTP is the only OTLP protocol pulse exports with.
	otlpProtocolHTTP = "http/protobuf"

	// otlpDefaultServiceName is service.name when OTEL_SERVICE_NAME and
	// OTEL_RESOURCE_ATTRIBUTES leave it unset.
	otlpDefaultServiceName = "pulse"

	// otlpShutdownTimeout bounds the exit flush + shutdown of both
	// providers, like the --metrics-addr server's.
	otlpShutdownTimeout = 5 * time.Second
)

// envLookup reads one environment variable (os.LookupEnv in
// production); injected so tests stay parallel.
type envLookup func(string) (string, bool)

// otlpSignal is one OTLP signal's environment spelling.
type otlpSignal struct {
	name, path, endpointVar, protocolVar string
}

var (
	otlpTraces  = otlpSignal{"traces", "/v1/traces", envOTLPTracesEndpoint, envOTLPTracesProtocol}
	otlpMetrics = otlpSignal{"metrics", "/v1/metrics", envOTLPMetricsEndpoint, envOTLPMetricsProtocol}
)

// otlpExport is the running OTLP/HTTP export of `pulse mcp`: the Pulse
// hooks / metrics to wire into pulse.Options (nil for a signal with no
// endpoint) and the providers behind them.
type otlpExport struct {
	Hooks   *observe.Hooks
	Metrics observe.Metrics
	// ResourceWarning is a partly-invalid OTEL_RESOURCE_ATTRIBUTES: the
	// valid rest is used, the caller warns.
	ResourceWarning error
	// Signals names the exporting signals for the startup notice
	// ("traces+metrics", "traces", "metrics").
	Signals string

	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider
}

// lookupNonEmpty is env(name) with an empty value read as unset.
func lookupNonEmpty(env envLookup, name string) string {
	v, _ := env(name)
	return strings.TrimSpace(v)
}

// endpoint resolves s's OTLP/HTTP URL from env: empty when neither the
// per-signal nor the shared endpoint is set. A value that is not an
// http(s) URL with a host is CLI_INPUT naming the variable.
func (s otlpSignal) endpoint(env envLookup) (string, error) {
	name, raw := s.endpointVar, lookupNonEmpty(env, s.endpointVar)
	full := raw
	if raw == "" {
		name, raw = envOTLPEndpoint, lookupNonEmpty(env, envOTLPEndpoint)
		if raw == "" {
			return "", nil
		}
		full = strings.TrimRight(raw, "/") + s.path
	}
	u, err := url.Parse(full)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", otlpEnvError(name, raw, name+"="+raw+": want an http:// or https:// URL")
	}
	return full, nil
}

// checkProtocol refuses an effective OTLP protocol for s other than
// http/protobuf: the per-signal variable wins over the shared one.
func (s otlpSignal) checkProtocol(env envLookup) error {
	name, v := s.protocolVar, lookupNonEmpty(env, s.protocolVar)
	if v == "" {
		name, v = envOTLPProtocol, lookupNonEmpty(env, envOTLPProtocol)
	}
	if v == "" || v == otlpProtocolHTTP {
		return nil
	}
	return otlpEnvError(name, v, name+"="+v+": pulse mcp exports OTLP over HTTP only; set it to "+
		otlpProtocolHTTP+" or unset it")
}

func otlpEnvError(name, value, msg string) error {
	return perrors.NewCodedErrorWithDetails(perrors.CLI_INPUT, msg,
		map[string]any{"variable": name, "value": value})
}

// startOTLP builds the OTLP/HTTP export the standard OTel environment
// asks for: a batch-processed TracerProvider when a traces endpoint
// resolves, a periodic-reader MeterProvider when a metrics endpoint
// does. Neither resolving returns nil, nil — no provider, no goroutine.
// A non-HTTP protocol or a malformed endpoint for an exporting signal is
// CLI_INPUT, refused before anything is built. Providers are passed
// explicitly, never installed as OTel globals.
func startOTLP(ctx context.Context, env envLookup) (*otlpExport, error) {
	tracesURL, err := otlpTraces.endpoint(env)
	if err != nil {
		return nil, err
	}
	metricsURL, err := otlpMetrics.endpoint(env)
	if err != nil {
		return nil, err
	}
	if tracesURL == "" && metricsURL == "" {
		return nil, nil
	}
	var signals []string
	if tracesURL != "" {
		if err := otlpTraces.checkProtocol(env); err != nil {
			return nil, err
		}
		signals = append(signals, otlpTraces.name)
	}
	if metricsURL != "" {
		if err := otlpMetrics.checkProtocol(env); err != nil {
			return nil, err
		}
		signals = append(signals, otlpMetrics.name)
	}

	// service.name defaults to pulse; OTEL_SERVICE_NAME and
	// OTEL_RESOURCE_ATTRIBUTES (WithFromEnv, merged later) override it.
	// A partly-invalid attribute list still yields the valid rest.
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", otlpDefaultServiceName)),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
	)
	x := &otlpExport{Signals: strings.Join(signals, "+")}
	if err != nil {
		if !errors.Is(err, resource.ErrPartialResource) {
			return nil, otlpEnvError(envOTLPResourceAttrs, lookupNonEmpty(env, envOTLPResourceAttrs),
				"building the OTel resource: "+err.Error())
		}
		x.ResourceWarning = err
	}
	if tracesURL != "" {
		exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(tracesURL))
		if err != nil {
			return nil, otlpEnvError(otlpTraces.endpointVar, tracesURL, "OTLP traces exporter: "+err.Error())
		}
		x.tp = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
		x.Hooks = otelpulse.Hooks(x.tp)
	}
	if metricsURL != "" {
		exp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(metricsURL))
		if err != nil {
			_ = x.Shutdown(ctx)
			return nil, otlpEnvError(otlpMetrics.endpointVar, metricsURL, "OTLP metrics exporter: "+err.Error())
		}
		x.mp = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)),
			sdkmetric.WithResource(res))
		x.Metrics = otelpulse.Metrics(x.mp)
	}
	return x, nil
}

// Shutdown flushes and shuts down both providers within
// otlpShutdownTimeout. A nil export is a no-op.
func (x *otlpExport) Shutdown(ctx context.Context) error {
	if x == nil {
		return nil
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), otlpShutdownTimeout)
	defer cancel()
	var errs []error
	if x.tp != nil {
		errs = append(errs, x.tp.Shutdown(sctx))
	}
	if x.mp != nil {
		errs = append(errs, x.mp.Shutdown(sctx))
	}
	return errors.Join(errs...)
}

// otlpErrorHandler routes OTel's asynchronous export errors to lg at
// warn — never fatal. With logging off (lg nil) they still reach stderr
// (w) at warn, so an unreachable collector is never silent; stdout, the
// JSON-RPC transport, is never written.
func otlpErrorHandler(lg *slog.Logger, w io.Writer) otel.ErrorHandler {
	if lg == nil {
		lg = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	return otel.ErrorHandlerFunc(func(err error) {
		lg.Warn("otlp export failed", "err", err)
	})
}

// fanMetrics combines metric sinks into one observe.Metrics recording
// to every sink: `pulse mcp --metrics-addr` with OTLP export feeds both.
// No sink is nil; one sink is returned as-is.
func fanMetrics(sinks ...observe.Metrics) observe.Metrics {
	var live []observe.Metrics
	for _, s := range sinks {
		if s != nil {
			live = append(live, s)
		}
	}
	switch len(live) {
	case 0:
		return nil
	case 1:
		return live[0]
	}
	return metricsFan(live)
}

// metricsFan is concurrency-safe because every sink is: instruments
// resolve per call, with no state of its own.
type metricsFan []observe.Metrics

func (f metricsFan) Counter(name string, labels ...observe.Label) observe.Counter {
	out := make(addFan, len(f))
	for i, m := range f {
		out[i] = m.Counter(name, labels...)
	}
	return out
}

func (f metricsFan) Histogram(name string, labels ...observe.Label) observe.Histogram {
	out := make(histFan, len(f))
	for i, m := range f {
		out[i] = m.Histogram(name, labels...)
	}
	return out
}

func (f metricsFan) UpDownCounter(name string, labels ...observe.Label) observe.UpDownCounter {
	out := make(addFan, len(f))
	for i, m := range f {
		out[i] = m.UpDownCounter(name, labels...)
	}
	return out
}

// addFan is a counter or up-down counter fanned out to every sink.
type addFan []interface{ Add(float64) }

func (a addFan) Add(delta float64) {
	for _, c := range a {
		c.Add(delta)
	}
}

type histFan []observe.Histogram

func (h histFan) Observe(v float64) {
	for _, x := range h {
		x.Observe(v)
	}
}
