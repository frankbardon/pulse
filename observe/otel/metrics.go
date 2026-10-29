package otelpulse

import (
	"context"
	"strings"
	"sync"

	"github.com/frankbardon/pulse/observe"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// DefaultBuckets are the explicit bucket boundaries, in seconds, advised
// for Pulse's duration histograms. They match the bounds of Pulse's
// built-in exporter (0.5ms to 60s); a MeterProvider view may override
// them.
var DefaultBuckets = []float64{
	0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1,
	0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// descriptions of Pulse's documented metric set.
var descriptions = map[string]string{
	"pulse_operations_total":           "Pulse operations finished, by operation kind, result code (ok or an error code) and scope (top or child).",
	"pulse_operation_duration_seconds": "Pulse operation wall time in seconds, by operation kind and scope.",
	"pulse_phase_duration_seconds":     "Time spent in each execution phase of a top-level Pulse operation, in seconds.",
	"pulse_rows_scanned_total":         "Records scanned by top-level Pulse operations.",
	"pulse_bytes_read_total":           "Cohort bytes read by top-level Pulse operations.",
	"pulse_limit_trips_total":          "Operations refused by a Pulse resource limit, by limit name.",
	"pulse_hook_panics_total":          "Observability hook panics Pulse recovered, by hook.",
	"pulse_operations_in_flight":       "Top-level Pulse operations currently running, by operation kind.",
}

// Metrics returns an observe.Metrics that records Pulse's instruments
// through mp (the global MeterProvider when mp is nil). Instrument names
// are Pulse's metric names unchanged (pulse_operations_total, …);
// labels become attributes. Counters map to Float64Counter, histograms
// to Float64Histogram (unit "s" for a *_seconds name, with
// DefaultBuckets advised) and up-down counters to Float64UpDownCounter.
//
// The returned value is safe for concurrent use: Pulse may resolve an
// instrument after New, from any goroutine. An instrument the meter
// refuses (or a name reused with a different instrument kind) is a
// no-op.
func Metrics(mp metric.MeterProvider) observe.Metrics {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	return &meter{m: mp.Meter(ScopeName), inst: map[string]cached{}}
}

type meter struct {
	m metric.Meter

	mu   sync.Mutex
	inst map[string]cached
}

// Instrument kinds. The kind is tracked explicitly: an SDK instrument
// value may satisfy several of OTel's instrument interfaces at once, so
// a type assertion cannot tell a counter from a histogram.
const (
	kindCounter = iota + 1
	kindHistogram
	kindUpDown
)

// cached is one created instrument; inst is nil when creation failed.
type cached struct {
	kind int
	inst any
}

var _ observe.Metrics = (*meter)(nil)

func unit(name string) string {
	switch {
	case strings.HasSuffix(name, "_seconds"):
		return "s"
	case strings.HasPrefix(name, "pulse_bytes_"):
		return "By"
	}
	return ""
}

func attrSet(labels []observe.Label) attribute.Set {
	kv := make([]attribute.KeyValue, len(labels))
	for i, l := range labels {
		kv[i] = attribute.String(l.Key, l.Value)
	}
	return attribute.NewSet(kv...)
}

// instrument returns the cached instrument for name, creating it with
// create on first use. It returns nil (a no-op) when creation failed or
// name was first created as another kind.
func (m *meter) instrument(kind int, name string, create func() (any, error)) any {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.inst[name]
	if !ok {
		c.kind = kind
		if in, err := create(); err == nil {
			c.inst = in
		}
		m.inst[name] = c
	}
	if c.kind != kind {
		return nil
	}
	return c.inst
}

// Counter implements observe.Metrics.
func (m *meter) Counter(name string, labels ...observe.Label) observe.Counter {
	c, ok := m.instrument(kindCounter, name, func() (any, error) {
		return m.m.Float64Counter(name, metric.WithDescription(descriptions[name]), metric.WithUnit(unit(name)))
	}).(metric.Float64Counter)
	if !ok {
		return noop{}
	}
	return &counter{c: c, opt: metric.WithAttributeSet(attrSet(labels))}
}

// Histogram implements observe.Metrics.
func (m *meter) Histogram(name string, labels ...observe.Label) observe.Histogram {
	h, ok := m.instrument(kindHistogram, name, func() (any, error) {
		opts := []metric.Float64HistogramOption{metric.WithDescription(descriptions[name]), metric.WithUnit(unit(name))}
		if strings.HasSuffix(name, "_seconds") {
			opts = append(opts, metric.WithExplicitBucketBoundaries(DefaultBuckets...))
		}
		return m.m.Float64Histogram(name, opts...)
	}).(metric.Float64Histogram)
	if !ok {
		return noop{}
	}
	return &histogram{h: h, opt: metric.WithAttributeSet(attrSet(labels))}
}

// UpDownCounter implements observe.Metrics.
func (m *meter) UpDownCounter(name string, labels ...observe.Label) observe.UpDownCounter {
	u, ok := m.instrument(kindUpDown, name, func() (any, error) {
		return m.m.Float64UpDownCounter(name, metric.WithDescription(descriptions[name]), metric.WithUnit(unit(name)))
	}).(metric.Float64UpDownCounter)
	if !ok {
		return noop{}
	}
	return &upDown{u: u, opt: metric.WithAttributeSet(attrSet(labels))}
}

// The instruments carry their attribute set as a prebuilt option, so a
// write does not rebuild it. Pulse's instrument interfaces take no
// context; writes use context.Background.

type counter struct {
	c   metric.Float64Counter
	opt metric.MeasurementOption
}

func (c *counter) Add(delta float64) {
	if !(delta >= 0) {
		return // a counter never decreases
	}
	c.c.Add(context.Background(), delta, c.opt)
}

type histogram struct {
	h   metric.Float64Histogram
	opt metric.MeasurementOption
}

func (h *histogram) Observe(v float64) { h.h.Record(context.Background(), v, h.opt) }

type upDown struct {
	u   metric.Float64UpDownCounter
	opt metric.MeasurementOption
}

func (u *upDown) Add(delta float64) { u.u.Add(context.Background(), delta, u.opt) }

type noop struct{}

func (noop) Add(float64)     {}
func (noop) Observe(float64) {}
