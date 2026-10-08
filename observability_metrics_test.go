package pulse

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/obsprom"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// fakeMetrics is a recording observe.Metrics. Factory calls after
// freeze are recorded as late — only lazy operations_total{code≠ok}
// resolutions may make one, once per tuple.
type fakeMetrics struct {
	mu     sync.Mutex
	frozen bool
	calls  int
	late   []string
	inst   map[string]*fakeInst
}

type fakeInst struct {
	kind   string
	name   string
	labels []observe.Label
	mu     sync.Mutex
	writes int
	value  float64 // counter / gauge running value; histogram sum
	peak   float64 // gauge high-water mark
}

func (f *fakeInst) Add(d float64) {
	f.mu.Lock()
	f.writes++
	f.value += d
	if f.value > f.peak {
		f.peak = f.value
	}
	f.mu.Unlock()
}

func (f *fakeInst) Observe(v float64) {
	f.mu.Lock()
	f.writes++
	f.value += v
	f.mu.Unlock()
}

func newFakeMetrics() *fakeMetrics { return &fakeMetrics{inst: map[string]*fakeInst{}} }

func metricKey(name string, kv ...string) string {
	return name + "{" + strings.Join(kv, ",") + "}"
}

func (m *fakeMetrics) resolve(kind, name string, labels []observe.Label) *fakeInst {
	m.mu.Lock()
	defer m.mu.Unlock()
	kv := make([]string, 0, 2*len(labels))
	for _, l := range labels {
		kv = append(kv, l.Key+"="+l.Value)
	}
	key := metricKey(name, kv...)
	if m.frozen {
		m.late = append(m.late, key)
	}
	m.calls++
	in := m.inst[key]
	if in == nil {
		in = &fakeInst{kind: kind, name: name, labels: append([]observe.Label(nil), labels...)}
		m.inst[key] = in
	}
	return in
}

func (m *fakeMetrics) Counter(name string, labels ...observe.Label) observe.Counter {
	return m.resolve("counter", name, labels)
}

func (m *fakeMetrics) Histogram(name string, labels ...observe.Label) observe.Histogram {
	return m.resolve("histogram", name, labels)
}

func (m *fakeMetrics) UpDownCounter(name string, labels ...observe.Label) observe.UpDownCounter {
	return m.resolve("updown", name, labels)
}

func (m *fakeMetrics) freeze() {
	m.mu.Lock()
	m.frozen = true
	m.mu.Unlock()
}

// get returns the instrument for name and "k=v" label pairs (in
// registration order); a missing one fails the test.
func (m *fakeMetrics) get(t *testing.T, name string, kv ...string) *fakeInst {
	t.Helper()
	m.mu.Lock()
	in := m.inst[metricKey(name, kv...)]
	m.mu.Unlock()
	if in == nil {
		t.Fatalf("instrument %s never resolved", metricKey(name, kv...))
	}
	return in
}

// snapshot returns writes per instrument key.
func (m *fakeMetrics) snapshot() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.inst))
	for k, in := range m.inst {
		in.mu.Lock()
		out[k] = in.writes
		in.mu.Unlock()
	}
	return out
}

// written returns the instruments whose writes changed since before.
func (m *fakeMetrics) written(before map[string]int) map[string]int {
	out := map[string]int{}
	for k, n := range m.snapshot() {
		if d := n - before[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

func metricsFixture(t *testing.T, opts Options) (*Pulse, *fakeMetrics) {
	t.Helper()
	fm := newFakeMetrics()
	opts.Metrics = fm
	p, _ := obsFixture(t, opts)
	fm.freeze()
	return p, fm
}

// TestObservabilityMetricsResolvedAtNew (FR-19): New resolves every
// instrument of the documented set whose label space is small — all of
// them except operations_total for an error code, which is keyed by the
// ~280-entry code list and resolved lazily. Operations then call the
// factory only for operations_total{code≠ok}, at most once per (op,
// scope, code) tuple; re-running every operation calls it never again.
//
// Falsified by resolving an instrument inside opMetrics.end, or by
// dropping the cell Store in resolveOpsCounter (every failure re-calls
// the factory).
func TestObservabilityMetricsResolvedAtNew(t *testing.T) {
	p, fm := metricsFixture(t, Options{})
	e := metricEnumSet()
	nOps := len(observe.AllOperationKinds())
	want := nOps*2 + // operations_total{code="ok"}
		nOps*2 + // operation_duration_seconds
		nOps*len(observe.AllPhases()) + // phase_duration_seconds
		3*nOps + // rows, bytes, in-flight
		len(limits.Names()) + len(e.hooks)
	if fm.calls != want || len(fm.inst) != want {
		t.Errorf("New made %d factory calls for %d instruments, want %d", fm.calls, len(fm.inst), want)
	}
	names := map[string]bool{}
	for _, in := range fm.inst {
		names[in.name] = true
	}
	for _, n := range metricNames {
		if !names[n] {
			t.Errorf("documented metric %s never resolved", n)
		}
		if _, ok := obsprom.Help(n); !ok {
			t.Errorf("obsprom has no HELP text for %s", n)
		}
	}
	if len(names) != len(metricNames) {
		t.Errorf("resolved %d metric names, documented set has %d", len(names), len(metricNames))
	}

	ctx := context.Background()
	runAll := func() {
		for _, c := range obsCalls() {
			_ = c.call(ctx, p)
		}
		for _, c := range sentinelCalls() {
			_ = c.call(ctx, p)
		}
	}
	runAll()
	fm.mu.Lock()
	late := append([]string(nil), fm.late...)
	fm.mu.Unlock()
	if len(late) == 0 {
		t.Fatal("no operation failed with an error code; the lazy path went unexercised")
	}
	seen := map[string]bool{}
	for _, k := range late {
		if !strings.HasPrefix(k, metricOperations+"{") || strings.Contains(k, "code="+observe.CodeOK+",") {
			t.Errorf("the hot path resolved %s; only operations_total for an error code is lazy", k)
		}
		if seen[k] {
			t.Errorf("the factory was called twice for %s", k)
		}
		seen[k] = true
	}
	runAll()
	fm.mu.Lock()
	again := len(fm.late) - len(late)
	fm.mu.Unlock()
	if again != 0 {
		t.Errorf("re-running every operation called the factory %d more times, want 0", again)
	}
}

// TestObservabilityMetricsLazyConcurrent: concurrent first uses of the
// same lazy tuple call the factory exactly once and every writer lands
// in that one instrument.
//
// Falsified by calling the factory before taking lazyMu (or dropping the
// re-check under it) in resolveOpsCounter.
func TestObservabilityMetricsLazyConcurrent(t *testing.T) {
	fm := newFakeMetrics()
	om := newOpMetrics(fm)
	fm.freeze()
	e := metricEnumSet()
	c := e.codeIdx[string(errors.PULSE_LIMIT_EXCEEDED)]
	const workers = 32
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() { om.opsCounter(1, scopeSlotChild, c).Add(1) })
	}
	wg.Wait()
	if len(fm.late) != 1 {
		t.Fatalf("factory called %d times for one tuple, want 1: %v", len(fm.late), fm.late)
	}
	in := fm.get(t, metricOperations, "op="+string(e.ops[1]), "code=PULSE_LIMIT_EXCEEDED", "scope=child")
	if in.value != workers {
		t.Errorf("counter %v, want %d", in.value, workers)
	}
}

// newMetricsAllocCeiling bounds the allocations metrics add to one New
// beyond a plain New. Eager resolution of operations_total over every
// error code made ~22.4k factory calls and ~620k allocations per New; the
// split resolution makes ~600 calls (≈ 6 fake-backend allocations each)
// plus the lazy table. The ceiling leaves headroom for growth in the op,
// phase and limit enums but is an order of magnitude under eager.
const newMetricsAllocCeiling = 10_000

// TestObservabilityMetricsNewCost: building an instance with Metrics set
// costs a bounded number of allocations over a plain New — a host may
// call New per tenant or per request.
//
// Falsified by pre-resolving operations_total over every code in
// newOpMetrics (the pre-fix eager design).
func TestObservabilityMetricsNewCost(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are a plain-build figure")
	}
	dir := t.TempDir()
	plain := testing.AllocsPerRun(5, func() {
		if _, err := New(Options{DataDir: dir}); err != nil {
			t.Fatal(err)
		}
	})
	withMetrics := testing.AllocsPerRun(5, func() {
		if _, err := New(Options{DataDir: dir, Metrics: newFakeMetrics()}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("New allocations: %v plain, %v with Metrics", plain, withMetrics)
	if extra := withMetrics - plain; extra > newMetricsAllocCeiling {
		t.Fatalf(`New with Metrics allocated %v per run vs %v plain (+%v, ceiling +%d).
Resolve only small-label-space instruments at New; operations_total for an
error code must stay lazy (opMetrics.opsCounter), or every New pays ~22k
factory calls.`, withMetrics, plain, extra, newMetricsAllocCeiling)
	}
}

// TestObservabilityMetricsSet (FR-20): each scenario writes exactly the
// documented instruments with the expected labels — a top op, child ops,
// a failed op and a limit trip.
//
// Falsified by dropping the scope split (children counted as top), or the
// limit-trip Add in opMetrics.end.
func TestObservabilityMetricsSet(t *testing.T) {
	ctx := context.Background()
	p, fm := metricsFixture(t, Options{Limits: Limits{MaxGroups: 1}})

	t.Run("top op", func(t *testing.T) {
		before := fm.snapshot()
		if _, err := p.Process(ctx, obsRequest()); err != nil {
			t.Fatal(err)
		}
		got := fm.written(before)
		for _, k := range []string{
			metricKey(metricOperations, "op=process", "code=ok", "scope=top"),
			metricKey(metricOpDuration, "op=process", "scope=top"),
			metricKey(metricRowsScanned, "op=process"),
			metricKey(metricBytesRead, "op=process"),
			metricKey(metricPhaseDuration, "op=process", "phase=plan"),
			metricKey(metricPhaseDuration, "op=process", "phase=scan"),
		} {
			if got[k] != 1 {
				t.Errorf("%s written %d times, want 1 (all writes: %v)", k, got[k], got)
			}
		}
		if got[metricKey(metricInFlight, "op=process")] != 2 {
			t.Errorf("in-flight written %d times, want +1 and -1", got[metricKey(metricInFlight, "op=process")])
		}
		if v := fm.get(t, metricRowsScanned, "op=process").value; v < 3 {
			t.Errorf("rows scanned %v, want ≥ 3", v)
		}
		for k := range got {
			if !strings.Contains(k, "op=process") {
				t.Errorf("unexpected write to %s", k)
			}
		}
	})

	t.Run("child ops", func(t *testing.T) {
		before := fm.snapshot()
		if _, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{obsRequest(), obsRequest()}}); err != nil {
			t.Fatal(err)
		}
		got := fm.written(before)
		want := map[string]int{
			metricKey(metricOperations, "op=compose", "code=ok", "scope=top"):   1,
			metricKey(metricOperations, "op=compose", "code=ok", "scope=child"): 2,
			metricKey(metricOpDuration, "op=compose", "scope=top"):              1,
			metricKey(metricOpDuration, "op=compose", "scope=child"):            2,
			metricKey(metricRowsScanned, "op=compose"):                          1, // parent only: it aggregates the children
			metricKey(metricInFlight, "op=compose"):                             2,
		}
		for k, n := range want {
			if got[k] != n {
				t.Errorf("%s written %d times, want %d", k, got[k], n)
			}
		}
		if v := fm.get(t, metricRowsScanned, "op=compose").value; v != 6 {
			t.Errorf("compose rows scanned %v, want 6 (2 slots × 3, counted once)", v)
		}
	})

	t.Run("failed op", func(t *testing.T) {
		bad := obsRequest()
		bad.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "no_such_field"}}
		before := fm.snapshot()
		_, err := p.Process(ctx, bad)
		code := operationCode(err)
		if code == observe.CodeOK || code == observe.CodeUncoded {
			t.Fatalf("want a coded failure, got %q (%v)", code, err)
		}
		got := fm.written(before)
		if k := metricKey(metricOperations, "op=process", "code="+code, "scope=top"); got[k] != 1 {
			t.Errorf("%s written %d times, want 1 (all: %v)", k, got[k], got)
		}
		if k := metricKey(metricOperations, "op=process", "code=ok", "scope=top"); got[k] != 0 {
			t.Errorf("failure counted as ok")
		}
	})

	t.Run("limit trip", func(t *testing.T) {
		r := obsRequest()
		r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
		before := fm.snapshot()
		_, err := p.Process(ctx, r)
		if operationCode(err) != string(errors.PULSE_LIMIT_EXCEEDED) {
			t.Fatalf("want PULSE_LIMIT_EXCEEDED, got %v", err)
		}
		got := fm.written(before)
		for _, k := range []string{
			metricKey(metricLimitTrips, "limit="+string(limits.MaxGroups)),
			metricKey(metricOperations, "op=process", "code=PULSE_LIMIT_EXCEEDED", "scope=top"),
		} {
			if got[k] != 1 {
				t.Errorf("%s written %d times, want 1 (all: %v)", k, got[k], got)
			}
		}
	})
}

// TestObservabilityMetricsInFlight: the in-flight gauge rises to 1 while
// an operation runs and returns to 0 after success, failure and every
// streaming end (drain, early close, the channel form).
//
// Falsified by dropping the in-flight Add(-1) in opMetrics.end.
func TestObservabilityMetricsInFlight(t *testing.T) {
	ctx := context.Background()
	p, fm := metricsFixture(t, Options{})
	cases := []struct {
		name string
		kind observe.OperationKind
		run  func()
	}{
		{"success", observe.OpProcess, func() { _, _ = p.Process(ctx, obsRequest()) }},
		{"error", observe.OpProcess, func() {
			_, _ = p.Process(ctx, &Request{Cohort: &types.Cohort{Filename: "missing.pulse"}})
		}},
		{"stream drained", observe.OpProcessStream, func() {
			it, err := p.ProcessStream(ctx, obsRequest())
			if err != nil {
				t.Fatal(err)
			}
			for {
				_, ok, err := it.Next(ctx)
				if !ok || err != nil {
					break
				}
			}
		}},
		{"stream closed early", observe.OpProcessStream, func() {
			it, err := p.ProcessStream(ctx, obsRequest())
			if err != nil {
				t.Fatal(err)
			}
			_ = it.Close()
		}},
		{"stream result", observe.OpProcessStream, func() {
			sr, _ := p.ProcessStreamResult(ctx, obsRequest())
			drainStream(sr)
		}},
		{"compose", observe.OpCompose, func() {
			_, _ = p.Compose(ctx, &ComposedRequest{Requests: []*Request{obsRequest(), obsRequest()}})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := fm.get(t, metricInFlight, "op="+string(c.kind))
			g.mu.Lock()
			g.peak = 0
			g.mu.Unlock()
			c.run()
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.value != 0 || g.peak != 1 {
				t.Errorf("in-flight %s: value %v peak %v, want back to 0 after peaking at 1", c.kind, g.value, g.peak)
			}
		})
	}
}

// TestObservabilityMetricLabelsBounded: every label value of every
// instrument comes from a closed enum — operation kinds, scopes,
// phases, result codes, limit names, hook names — so no label can carry
// a cohort name, request hash or row data, however the operations ran.
//
// Falsified by adding a cohort label to any instrument.
func TestObservabilityMetricLabelsBounded(t *testing.T) {
	p, fm := metricsFixture(t, Options{})
	ctx := context.Background()
	for _, c := range obsCalls() {
		_ = c.call(ctx, p)
	}
	for _, c := range sentinelCalls() {
		_ = c.call(ctx, p)
	}
	e := metricEnumSet()
	allowed := map[string]map[string]bool{
		labelOp: {}, labelScope: {}, labelPhase: {}, labelCode: {}, labelLimit: {}, labelHook: {},
	}
	for _, v := range e.ops {
		allowed[labelOp][string(v)] = true
	}
	for _, v := range observe.AllScopes() {
		allowed[labelScope][string(v)] = true
	}
	for _, v := range e.phases {
		allowed[labelPhase][string(v)] = true
	}
	for _, v := range e.codes {
		allowed[labelCode][v] = true
	}
	for _, v := range e.limits {
		allowed[labelLimit][v] = true
	}
	for _, v := range e.hooks {
		allowed[labelHook][v] = true
	}
	fm.mu.Lock()
	defer fm.mu.Unlock()
	keys := make([]string, 0, len(fm.inst))
	for k := range fm.inst {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, l := range fm.inst[k].labels {
			set, ok := allowed[l.Key]
			if !ok || !set[l.Value] {
				t.Errorf("%s: label %s=%q is not from a closed enum", k, l.Key, l.Value)
			}
			if strings.Contains(l.Value, obsCohort) || strings.Contains(l.Value, ".pulse") {
				t.Errorf("%s: label %s=%q names a cohort", k, l.Key, l.Value)
			}
			for _, s := range allSentinels() {
				if strings.Contains(l.Value, s) {
					t.Errorf("%s: label %s=%q carries input data", k, l.Key, l.Value)
				}
			}
		}
	}
}

// TestObservabilityMetricsObsprom: the facade's metric set renders
// through the stdlib exporter as valid exposition carrying all 8
// documented families.
func TestObservabilityMetricsObsprom(t *testing.T) {
	reg := obsprom.New()
	p, _ := obsFixture(t, Options{Metrics: reg})
	if _, err := p.Process(context.Background(), obsRequest()); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := reg.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, n := range metricNames {
		if !strings.Contains(out, "# TYPE "+n+" ") {
			t.Errorf("exposition lacks %s", n)
		}
	}
	for _, want := range []string{
		`pulse_operations_total{op="process",code="ok",scope="top"} 1`,
		`pulse_operation_duration_seconds_count{op="process",scope="top"} 1`,
		`pulse_operations_in_flight{op="process"} 0`,
		"# TYPE pulse_operations_in_flight gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition lacks %q", want)
		}
	}
	if strings.Contains(out, obsCohort) {
		t.Errorf("exposition names the cohort")
	}
}
