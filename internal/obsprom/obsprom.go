// Package obsprom is Pulse's stdlib-only Prometheus exporter: an
// observe.Metrics implementation that keeps its instruments in memory
// and renders them in the Prometheus text exposition format (version
// 0.0.4) over net/http. It backs `pulse mcp --metrics-addr`, so the core
// module never depends on a Prometheus client library.
//
// Exposition rules:
//
//   - Every registered metric family prints its HELP and TYPE lines,
//     even before it has a sample, so a scrape always shows the whole
//     documented set.
//   - A series prints once its instrument has been written at least
//     once (Add or Observe). Pulse resolves many instruments up front
//     (every operation kind × scope × phase, and more) and printing them
//     all at zero would bloat every scrape with series that never move.
//   - Resolving an instrument is cheap: a histogram's bucket storage is
//     allocated on its first Observe, not when it is created.
//   - Families sort by name and series by their rendered label set, so
//     output is deterministic.
//
// Instruments are safe for concurrent use; writes are lock-free atomics.
package obsprom

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/frankbardon/pulse/observe"
)

// DefaultBuckets are the histogram upper bounds, in seconds, used by New.
// Pulse operations span sub-millisecond header reads (predict, inspect,
// count) to minute-long scans, so the bounds run from 0.5ms to 60s,
// roughly ×2–2.5 apart.
var DefaultBuckets = []float64{
	0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1,
	0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// ContentType is the media type of the text exposition format.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Instrument kinds as the TYPE line spells them.
const (
	kindCounter   = "counter"
	kindGauge     = "gauge"
	kindHistogram = "histogram"
)

// help is the HELP text of Pulse's documented metric set. A name not
// listed gets a generic line.
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

// Help returns the HELP text the exporter prints for name and whether
// name is one of Pulse's documented metrics.
func Help(name string) (string, bool) {
	h, ok := help[name]
	if !ok {
		return "Pulse metric " + name + ".", false
	}
	return h, true
}

// Registry is an in-memory observe.Metrics. The zero value is not
// usable; build one with New or NewWithBuckets.
type Registry struct {
	buckets []float64

	mu   sync.Mutex
	fams map[string]*family
}

var _ observe.Metrics = (*Registry)(nil)

// New returns an empty Registry whose histograms use DefaultBuckets.
func New() *Registry { return NewWithBuckets(DefaultBuckets) }

// NewWithBuckets returns an empty Registry whose histograms use the
// given upper bounds. They are copied and sorted; NaN and +Inf bounds
// are dropped (the +Inf bucket is always implied).
func NewWithBuckets(buckets []float64) *Registry {
	b := make([]float64, 0, len(buckets))
	for _, v := range buckets {
		if !math.IsNaN(v) && !math.IsInf(v, 1) {
			b = append(b, v)
		}
	}
	sort.Float64s(b)
	return &Registry{buckets: b, fams: map[string]*family{}}
}

type family struct {
	name, kind string
	series     map[string]*series
}

// series is one label combination of a family.
type series struct {
	labels  string // rendered, without braces; "" when unlabeled
	touched atomic.Bool
	val     atomicFloat // counter / gauge value
	// upper is the histogram bucket bounds (nil for other kinds); hist is
	// allocated on the first Observe so an unwritten series costs only
	// this struct.
	upper []float64
	hist  atomic.Pointer[histData]
}

type histData struct {
	counts []atomic.Uint64 // per bucket, non-cumulative; last is +Inf
	sum    atomicFloat
}

// histData returns the series' histogram storage, allocating it on first
// use. Concurrent first Observes race on a CompareAndSwap; the loser's
// allocation is dropped and every writer lands in the winner's storage.
func (s *series) histData() *histData {
	if d := s.hist.Load(); d != nil {
		return d
	}
	d := &histData{counts: make([]atomic.Uint64, len(s.upper)+1)}
	if s.hist.CompareAndSwap(nil, d) {
		return d
	}
	return s.hist.Load()
}

// atomicFloat is a float64 updated with compare-and-swap.
type atomicFloat struct{ bits atomic.Uint64 }

func (f *atomicFloat) add(d float64) {
	for {
		old := f.bits.Load()
		if f.bits.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+d)) {
			return
		}
	}
}

func (f *atomicFloat) load() float64 { return math.Float64frombits(f.bits.Load()) }

// Counter implements observe.Metrics. Asking twice for the same name and
// labels returns the same instrument. An invalid metric or label name,
// or a name already registered as another kind, yields a no-op
// instrument (the interface has no error return).
func (r *Registry) Counter(name string, labels ...observe.Label) observe.Counter {
	s := r.series(name, kindCounter, labels)
	if s == nil {
		return discard{}
	}
	return counter{s}
}

// UpDownCounter implements observe.Metrics; it is exposed as a gauge.
func (r *Registry) UpDownCounter(name string, labels ...observe.Label) observe.UpDownCounter {
	s := r.series(name, kindGauge, labels)
	if s == nil {
		return discard{}
	}
	return gauge{s}
}

// Histogram implements observe.Metrics with the Registry's buckets.
func (r *Registry) Histogram(name string, labels ...observe.Label) observe.Histogram {
	s := r.series(name, kindHistogram, labels)
	if s == nil {
		return discard{}
	}
	return histogram{s}
}

func (r *Registry) series(name, kind string, labels []observe.Label) *series {
	if !validMetricName(name) {
		return nil
	}
	for _, l := range labels {
		if !validLabelName(l.Key) {
			return nil
		}
	}
	key := renderLabels(labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	fam := r.fams[name]
	if fam == nil {
		fam = &family{name: name, kind: kind, series: map[string]*series{}}
		r.fams[name] = fam
	} else if fam.kind != kind {
		return nil
	}
	if s := fam.series[key]; s != nil {
		return s
	}
	s := &series{labels: key}
	if kind == kindHistogram {
		s.upper = r.buckets
	}
	fam.series[key] = s
	return s
}

type counter struct{ s *series }

// Add increases the counter; a negative or NaN delta is ignored (a
// Prometheus counter never decreases).
func (c counter) Add(delta float64) {
	if !(delta >= 0) {
		return
	}
	c.s.val.add(delta)
	c.s.touched.Store(true)
}

type gauge struct{ s *series }

func (g gauge) Add(delta float64) {
	g.s.val.add(delta)
	g.s.touched.Store(true)
}

type histogram struct{ s *series }

func (h histogram) Observe(v float64) {
	d := h.s.histData()
	// First bucket whose upper bound is >= v (le semantics); NaN lands
	// in +Inf only.
	i := len(h.s.upper)
	if !math.IsNaN(v) {
		i = sort.SearchFloat64s(h.s.upper, v)
	}
	d.counts[i].Add(1)
	d.sum.add(v)
	h.s.touched.Store(true)
}

type discard struct{}

func (discard) Add(float64)     {}
func (discard) Observe(float64) {}

// WriteText renders every family in the text exposition format.
func (r *Registry) WriteText(w io.Writer) error {
	r.mu.Lock()
	fams := make([]*family, 0, len(r.fams))
	for _, f := range r.fams {
		fams = append(fams, f)
	}
	type famSeries struct {
		f  *family
		ss []*series
	}
	snap := make([]famSeries, 0, len(fams))
	sort.Slice(fams, func(i, j int) bool { return fams[i].name < fams[j].name })
	for _, f := range fams {
		ss := make([]*series, 0, len(f.series))
		for _, s := range f.series {
			ss = append(ss, s)
		}
		sort.Slice(ss, func(i, j int) bool { return ss[i].labels < ss[j].labels })
		snap = append(snap, famSeries{f, ss})
	}
	r.mu.Unlock()

	var b bytes.Buffer
	for _, fs := range snap {
		f := fs.f
		h, _ := Help(f.name)
		b.WriteString("# HELP " + f.name + " " + escapeHelp(h) + "\n")
		b.WriteString("# TYPE " + f.name + " " + f.kind + "\n")
		for _, s := range fs.ss {
			if !s.touched.Load() {
				continue
			}
			if f.kind != kindHistogram {
				writeSample(&b, f.name, s.labels, "", s.val.load())
				continue
			}
			d := s.hist.Load()
			if d == nil {
				continue // touched is set only after the first Observe stored d
			}
			var cum uint64
			for i, ub := range s.upper {
				cum += d.counts[i].Load()
				writeSample(&b, f.name+"_bucket", s.labels, `le="`+formatFloat(ub)+`"`, float64(cum))
			}
			cum += d.counts[len(s.upper)].Load()
			writeSample(&b, f.name+"_bucket", s.labels, `le="+Inf"`, float64(cum))
			writeSample(&b, f.name+"_sum", s.labels, "", d.sum.load())
			writeSample(&b, f.name+"_count", s.labels, "", float64(cum))
		}
	}
	_, err := w.Write(b.Bytes())
	return err
}

func writeSample(b *bytes.Buffer, name, labels, extra string, v float64) {
	b.WriteString(name)
	if labels != "" || extra != "" {
		b.WriteByte('{')
		b.WriteString(labels)
		if labels != "" && extra != "" {
			b.WriteByte(',')
		}
		b.WriteString(extra)
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(formatFloat(v))
	b.WriteByte('\n')
}

// Handler serves the exposition for GET and HEAD.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var b bytes.Buffer
		if err := r.WriteText(&b); err != nil {
			http.Error(w, "exposition failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", ContentType)
		w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
		if req.Method == http.MethodGet {
			_, _ = w.Write(b.Bytes())
		}
	})
}

func renderLabels(labels []observe.Label) string {
	var b strings.Builder
	for i, l := range labels {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l.Key)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(l.Value))
		b.WriteByte('"')
	}
	return b.String()
}

// Escapers are built once: strings.NewReplacer is costly, and label
// rendering runs on every instrument resolution.
var (
	labelValueEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	helpEscaper       = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

// escapeLabelValue escapes backslash, double quote and line feed, as the
// text format requires inside a label value.
func escapeLabelValue(s string) string {
	if !strings.ContainsAny(s, "\\\"\n") {
		return s
	}
	return labelValueEscaper.Replace(s)
}

// escapeHelp escapes backslash and line feed, as the text format
// requires in a HELP docstring.
func escapeHelp(s string) string {
	return helpEscaper.Replace(s)
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// validMetricName reports whether s matches [a-zA-Z_:][a-zA-Z0-9_:]*.
func validMetricName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		ok := c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// validLabelName reports whether s matches [a-zA-Z_][a-zA-Z0-9_]* and
// is not reserved (leading "__", or "le", which histograms own).
func validLabelName(s string) bool {
	if s == "" || strings.HasPrefix(s, "__") || s == "le" {
		return false
	}
	for i, c := range s {
		ok := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}
