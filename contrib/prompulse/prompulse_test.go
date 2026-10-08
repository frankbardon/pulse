package prompulse_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/contrib/prompulse"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/spf13/afero"
)

const (
	cohort   = "sales.pulse"
	salesCSV = "region,amount\nnorth,10\nsouth,20\nnorth,5\n"
)

// documented is Pulse's documented metric set and its Prometheus type.
var documented = map[string]dto.MetricType{
	"pulse_operations_total":           dto.MetricType_COUNTER,
	"pulse_operation_duration_seconds": dto.MetricType_HISTOGRAM,
	"pulse_phase_duration_seconds":     dto.MetricType_HISTOGRAM,
	"pulse_rows_scanned_total":         dto.MetricType_COUNTER,
	"pulse_bytes_read_total":           dto.MetricType_COUNTER,
	"pulse_limit_trips_total":          dto.MetricType_COUNTER,
	"pulse_hook_panics_total":          dto.MetricType_COUNTER,
	"pulse_operations_in_flight":       dto.MetricType_GAUGE,
}

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

func gather(t *testing.T, reg *prometheus.Registry) map[string]*dto.MetricFamily {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, mf := range mfs {
		out[mf.GetName()] = mf
	}
	return out
}

// series finds the series of mf whose labels include every want pair.
func series(mf *dto.MetricFamily, want ...string) *dto.Metric {
	for _, m := range mf.GetMetric() {
		got := map[string]string{}
		for _, lp := range m.GetLabel() {
			got[lp.GetName()] = lp.GetValue()
		}
		ok := true
		for i := 0; i+1 < len(want); i += 2 {
			if got[want[i]] != want[i+1] {
				ok = false
			}
		}
		if ok {
			return m
		}
	}
	return nil
}

// TestMetricsDocumentedSet: a Pulse instance wired to a client_golang
// registry through Metrics registers and observes every metric of the
// documented set, with its documented type and help text.
func TestMetricsDocumentedSet(t *testing.T) {
	ctx := context.Background()
	reg := prometheus.NewRegistry()
	p := engine(t, pulse.Options{
		Metrics: prompulse.Metrics(reg),
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
	_ = p.Manifest(ctx) // its start hook panics; Pulse recovers and counts it

	fams := gather(t, reg)
	for name, typ := range documented {
		mf, ok := fams[name]
		if !ok {
			t.Errorf("%s not exported", name)
			continue
		}
		if mf.GetType() != typ {
			t.Errorf("%s type %v, want %v", name, mf.GetType(), typ)
		}
		if strings.HasPrefix(mf.GetHelp(), "Pulse metric ") {
			t.Errorf("%s has the generic help line %q", name, mf.GetHelp())
		}
	}
	for name := range fams {
		if _, ok := documented[name]; !ok {
			t.Errorf("unexpected family %s", name)
		}
	}

	ops := fams["pulse_operations_total"]
	for _, c := range []struct {
		labels []string
		want   float64
	}{
		{[]string{"op", "process", "code", "ok", "scope", "top"}, 1},
		{[]string{"op", "compose", "code", "ok", "scope", "top"}, 1},
		{[]string{"op", "compose", "code", "ok", "scope", "child"}, 2},
		{[]string{"op", "process", "code", string(perrors.PULSE_LIMIT_EXCEEDED), "scope", "top"}, 1},
	} {
		m := series(ops, c.labels...)
		if m == nil || m.GetCounter().GetValue() != c.want {
			t.Errorf("pulse_operations_total%v = %v, want %v", c.labels, m.GetCounter().GetValue(), c.want)
		}
	}
	if m := series(fams["pulse_rows_scanned_total"], "op", "process"); m == nil || m.GetCounter().GetValue() < 3 {
		t.Errorf("rows scanned for process = %v, want ≥ 3", m.GetCounter().GetValue())
	}
	if m := series(fams["pulse_phase_duration_seconds"], "op", "process", "phase", "scan"); m == nil || m.GetHistogram().GetSampleCount() == 0 {
		t.Error("no scan phase observation for process")
	}
	if m := series(fams["pulse_operation_duration_seconds"], "op", "compose", "scope", "child"); m == nil || m.GetHistogram().GetSampleCount() != 2 {
		t.Error("want 2 compose child duration observations")
	}
	if m := series(fams["pulse_limit_trips_total"], "limit", "max_groups"); m == nil || m.GetCounter().GetValue() != 1 {
		t.Errorf("limit trips series: %v", fams["pulse_limit_trips_total"])
	}
	if m := series(fams["pulse_hook_panics_total"], "hook", "start"); m == nil || m.GetCounter().GetValue() != 1 {
		t.Errorf("hook panics series: %v", fams["pulse_hook_panics_total"])
	}
	if m := series(fams["pulse_operations_in_flight"], "op", "process"); m == nil || m.GetGauge().GetValue() != 0 {
		t.Errorf("in-flight gauge for process should return to 0: %v", m)
	}
}

// TestMetricsSeriesOnFirstWrite: resolving an instrument registers its
// family but exports no series until the instrument is written, so a
// fresh instance does not export hundreds of zero series.
func TestMetricsSeriesOnFirstWrite(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := prompulse.Metrics(reg)
	c := m.Counter("pulse_rows_scanned_total", observe.Label{Key: "op", Value: "process"})
	if n := testutil.CollectAndCount(reg); n != 0 {
		t.Fatalf("%d series before any write, want 0", n)
	}
	c.Add(2)
	c.Add(-1) // ignored: a counter never decreases
	if n := testutil.CollectAndCount(reg); n != 1 {
		t.Fatalf("%d series after a write, want 1", n)
	}
	if got := gather(t, reg)["pulse_rows_scanned_total"].GetMetric()[0].GetCounter().GetValue(); got != 2 {
		t.Fatalf("counter = %v, want 2", got)
	}
}

// TestMetricsSharedRegistry: two adapters (two Pulse instances) on one
// registry share the vectors instead of failing registration.
func TestMetricsSharedRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	l := observe.Label{Key: "op", Value: "process"}
	prompulse.Metrics(reg).Counter("pulse_rows_scanned_total", l).Add(1)
	prompulse.Metrics(reg).Counter("pulse_rows_scanned_total", l).Add(2)
	mf := gather(t, reg)["pulse_rows_scanned_total"]
	if mf == nil || len(mf.GetMetric()) != 1 || mf.GetMetric()[0].GetCounter().GetValue() != 3 {
		t.Fatalf("shared counter: %v", mf)
	}
}

// TestMetricsClashIsNoop: an instrument that cannot be created (name
// taken by another type, or a label-key set that disagrees with the
// family) is a no-op, never a panic.
func TestMetricsClashIsNoop(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := prompulse.Metrics(reg)
	m.Counter("pulse_x", observe.Label{Key: "a", Value: "1"}).Add(1)
	m.Histogram("pulse_x", observe.Label{Key: "a", Value: "1"}).Observe(1)
	m.Counter("pulse_x", observe.Label{Key: "b", Value: "1"}).Add(1)
	other := prompulse.Metrics(reg)
	other.UpDownCounter("pulse_x", observe.Label{Key: "a", Value: "1"}).Add(1)
	mf := gather(t, reg)
	if len(mf) != 1 || len(mf["pulse_x"].GetMetric()) != 1 {
		t.Fatalf("want only pulse_x{a=1}: %v", mf)
	}
}

// TestMetricsConcurrent: Pulse may resolve instruments lazily after New
// from any goroutine, so the factory and its instruments must be safe
// for concurrent use (run with -race).
func TestMetricsConcurrent(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := prompulse.Metrics(reg)
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				code := fmt.Sprintf("CODE_%d", (g+i)%5)
				m.Counter("pulse_operations_total",
					observe.Label{Key: "op", Value: "process"},
					observe.Label{Key: "code", Value: code},
					observe.Label{Key: "scope", Value: "top"}).Add(1)
				m.Histogram("pulse_operation_duration_seconds",
					observe.Label{Key: "op", Value: "process"},
					observe.Label{Key: "scope", Value: "top"}).Observe(0.01)
				m.UpDownCounter("pulse_operations_in_flight", observe.Label{Key: "op", Value: "process"}).Add(1)
			}
		}()
	}
	wg.Wait()
	mf := gather(t, reg)
	var total float64
	for _, s := range mf["pulse_operations_total"].GetMetric() {
		total += s.GetCounter().GetValue()
	}
	if total != 16*50 {
		t.Fatalf("operations_total sum %v, want %d", total, 16*50)
	}
	if v := mf["pulse_operations_in_flight"].GetMetric()[0].GetGauge().GetValue(); v != 16*50 {
		t.Fatalf("in-flight %v, want %d", v, 16*50)
	}
}
