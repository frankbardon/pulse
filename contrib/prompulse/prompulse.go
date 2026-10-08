// Package prompulse adapts Pulse's metric instruments to the Prometheus
// Go client (github.com/prometheus/client_golang).
//
// A host that already runs a Prometheus registry hands it to Metrics
// and sets the result as pulse.Options.Metrics:
//
//	reg := prometheus.NewRegistry()
//	p, err := pulse.New(pulse.Options{Metrics: prompulse.Metrics(reg)})
//	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
//
// Pulse's documented metric set (pulse_operations_total,
// pulse_operation_duration_seconds, pulse_phase_duration_seconds,
// pulse_rows_scanned_total, pulse_bytes_read_total,
// pulse_limit_trips_total, pulse_hook_panics_total,
// pulse_operations_in_flight) is registered as one collector vector per
// metric, created on the first instrument Pulse resolves for it. Every
// label value Pulse uses is drawn from a closed enum, so cardinality is
// bounded.
//
// A labelled series is created on its first write, not when Pulse
// resolves the instrument: Pulse resolves several hundred instruments
// while building an instance (every operation kind × scope × phase),
// and exporting them all at zero would bloat every scrape with series
// that never move.
//
// This package lives in its own Go module so the Pulse core module never
// depends on a Prometheus client. Pulse also ships a dependency-free
// exporter behind `pulse mcp --metrics-addr`; this adapter is for hosts
// that want Pulse's metrics in their own registry.
package prompulse

import (
	stderrors "errors"
	"math"
	"slices"
	"sync"

	"github.com/frankbardon/pulse/observe"
	"github.com/prometheus/client_golang/prometheus"
)

// DefaultBuckets are the histogram upper bounds, in seconds, used unless
// WithBuckets overrides them. Pulse operations span sub-millisecond
// header reads to minute-long scans, so the bounds run from 0.5ms to
// 60s. They match the bounds of Pulse's built-in exporter.
var DefaultBuckets = []float64{
	0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1,
	0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// help is the HELP text of Pulse's documented metric set; a name not
// listed gets a generic line. It mirrors the text Pulse's built-in
// exporter prints (a test pins the two together).
var help = map[string]string{
	"pulse_operations_total":           "Pulse operations finished, by operation kind, result code (ok or an error code) and scope (top or child).",
	"pulse_operation_duration_seconds": "Pulse operation wall time in seconds, by operation kind and scope.",
	"pulse_phase_duration_seconds":     "Time spent in each execution phase of a top-level Pulse operation, in seconds.",
	"pulse_rows_scanned_total":         "Records scanned by top-level Pulse operations.",
	"pulse_bytes_read_total":           "Cohort bytes read by top-level Pulse operations.",
	"pulse_limit_trips_total":          "Operations refused by a Pulse resource limit, by limit name.",
	"pulse_hook_panics_total":          "Observability hook panics Pulse recovered, by hook.",
	"pulse_operations_in_flight":       "Top-level Pulse operations currently running, by operation kind.",
}

func helpFor(name string) string {
	if h, ok := help[name]; ok {
		return h
	}
	return "Pulse metric " + name + "."
}

// Option configures Metrics.
type Option func(*config)

type config struct {
	buckets []float64
}

// WithBuckets sets the histogram upper bounds, in seconds. The slice is
// copied and sorted; NaN and +Inf bounds are dropped (+Inf is implied).
func WithBuckets(buckets []float64) Option {
	return func(c *config) {
		b := make([]float64, 0, len(buckets))
		for _, v := range buckets {
			if !math.IsNaN(v) && !math.IsInf(v, 1) {
				b = append(b, v)
			}
		}
		slices.Sort(b)
		c.buckets = slices.Compact(b)
	}
}

// Metrics returns an observe.Metrics that registers Pulse's instruments
// with reg (prometheus.DefaultRegisterer when reg is nil).
//
// Several Pulse instances may share one registry: a vector another
// adapter already registered under the same name is reused. An
// instrument whose name is already registered with a different type,
// help text or label set cannot be created; Metrics then returns a
// no-op instrument for it rather than failing the Pulse instance.
//
// The returned value is safe for concurrent use: Pulse may resolve an
// instrument after New, from any goroutine.
func Metrics(reg prometheus.Registerer, opts ...Option) observe.Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	c := config{buckets: DefaultBuckets}
	for _, o := range opts {
		o(&c)
	}
	return &adapter{reg: reg, buckets: c.buckets, fams: map[string]*family{}}
}

type adapter struct {
	reg     prometheus.Registerer
	buckets []float64

	mu   sync.Mutex
	fams map[string]*family
}

// Instrument kinds.
const (
	kindCounter = iota
	kindHistogram
	kindGauge
)

// family is one registered vector. vec is nil when registration failed;
// every instrument of the family is then a no-op.
type family struct {
	kind int
	keys []string
	vec  any // *prometheus.CounterVec | *prometheus.HistogramVec | *prometheus.GaugeVec
}

var _ observe.Metrics = (*adapter)(nil)

// Counter implements observe.Metrics.
func (a *adapter) Counter(name string, labels ...observe.Label) observe.Counter {
	vec, values := a.resolve(kindCounter, name, labels)
	if vec == nil {
		return noop{}
	}
	return &counter{vec: vec.(*prometheus.CounterVec), values: values}
}

// Histogram implements observe.Metrics.
func (a *adapter) Histogram(name string, labels ...observe.Label) observe.Histogram {
	vec, values := a.resolve(kindHistogram, name, labels)
	if vec == nil {
		return noop{}
	}
	return &histogram{vec: vec.(*prometheus.HistogramVec), values: values}
}

// UpDownCounter implements observe.Metrics. It maps to a Prometheus
// gauge.
func (a *adapter) UpDownCounter(name string, labels ...observe.Label) observe.UpDownCounter {
	vec, values := a.resolve(kindGauge, name, labels)
	if vec == nil {
		return noop{}
	}
	return &gauge{vec: vec.(*prometheus.GaugeVec), values: values}
}

// resolve returns the family vector for name (registering it on first
// use) and the label values in key order. A nil vector means the
// instrument cannot be created: a failed registration, or a kind or
// label-key set that disagrees with the family's first instrument.
func (a *adapter) resolve(kind int, name string, labels []observe.Label) (any, []string) {
	keys := make([]string, len(labels))
	values := make([]string, len(labels))
	for i, l := range labels {
		keys[i], values[i] = l.Key, l.Value
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f, ok := a.fams[name]
	if !ok {
		f = &family{kind: kind, keys: keys, vec: a.register(kind, name, keys)}
		a.fams[name] = f
	}
	if f.vec == nil || f.kind != kind || !slices.Equal(f.keys, keys) {
		return nil, nil
	}
	return f.vec, values
}

// register creates and registers the vector for one family, reusing an
// identical collector already in the registry. It returns nil when the
// name is taken by an incompatible collector or is invalid.
func (a *adapter) register(kind int, name string, keys []string) (vec any) {
	defer func() {
		// Defensive: the client panics on a name its validation scheme rejects.
		if recover() != nil {
			vec = nil
		}
	}()
	var c prometheus.Collector
	switch kind {
	case kindCounter:
		c = prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: helpFor(name)}, keys)
	case kindHistogram:
		c = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: helpFor(name), Buckets: a.buckets}, keys)
	default:
		c = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: helpFor(name)}, keys)
	}
	if err := a.reg.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if !stderrors.As(err, &are) {
			return nil
		}
		c = are.ExistingCollector
	}
	switch v := c.(type) {
	case *prometheus.CounterVec:
		if kind == kindCounter {
			return v
		}
	case *prometheus.HistogramVec:
		if kind == kindHistogram {
			return v
		}
	case *prometheus.GaugeVec:
		if kind == kindGauge {
			return v
		}
	}
	return nil
}

// counter creates its series on the first Add.
type counter struct {
	vec    *prometheus.CounterVec
	values []string
	once   sync.Once
	c      prometheus.Counter
}

func (c *counter) Add(delta float64) {
	// A counter never decreases; the client panics on a negative delta.
	if !(delta >= 0) {
		return
	}
	c.once.Do(func() { c.c = c.vec.WithLabelValues(c.values...) })
	c.c.Add(delta)
}

type histogram struct {
	vec    *prometheus.HistogramVec
	values []string
	once   sync.Once
	h      prometheus.Observer
}

func (h *histogram) Observe(v float64) {
	h.once.Do(func() { h.h = h.vec.WithLabelValues(h.values...) })
	h.h.Observe(v)
}

type gauge struct {
	vec    *prometheus.GaugeVec
	values []string
	once   sync.Once
	g      prometheus.Gauge
}

func (g *gauge) Add(delta float64) {
	g.once.Do(func() { g.g = g.vec.WithLabelValues(g.values...) })
	g.g.Add(delta)
}

// noop is the instrument returned when one cannot be created.
type noop struct{}

func (noop) Add(float64)     {}
func (noop) Observe(float64) {}
