package pulse

import (
	stderrors "errors"
	"sync"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/observe"
)

// The documented metric set (FR-20). Every label value is drawn from a
// closed enum — operation kinds, scopes, phases, limit names, hook names
// and the error-code list — so cardinality is bounded and no label can
// carry a cohort name, request value or row data.
const (
	metricOperations    = "pulse_operations_total"           // {op,code,scope}
	metricOpDuration    = "pulse_operation_duration_seconds" // {op,scope}
	metricPhaseDuration = "pulse_phase_duration_seconds"     // {op,phase}
	metricRowsScanned   = "pulse_rows_scanned_total"         // {op}
	metricBytesRead     = "pulse_bytes_read_total"           // {op}
	metricLimitTrips    = "pulse_limit_trips_total"          // {limit}
	metricHookPanics    = "pulse_hook_panics_total"          // {hook}
	metricInFlight      = "pulse_operations_in_flight"       // {op}
)

// metricNames lists the documented metric set, in documentation order.
var metricNames = [...]string{
	metricOperations, metricOpDuration, metricPhaseDuration, metricRowsScanned,
	metricBytesRead, metricLimitTrips, metricHookPanics, metricInFlight,
}

// Metric label keys.
const (
	labelOp    = "op"
	labelCode  = "code"
	labelScope = "scope"
	labelPhase = "phase"
	labelLimit = "limit"
	labelHook  = "hook"
)

// metricEnums are the closed label-value sets the instruments are
// pre-resolved over, plus index maps so the hot path turns a value into
// a slot with a map lookup (no allocation). Built once per process.
type metricEnums struct {
	ops    []observe.OperationKind
	opIdx  map[observe.OperationKind]int
	phases []observe.Phase
	phIdx  map[observe.Phase]int
	// codes is ok, uncoded, then every registered error code. A code
	// outside the list (none today) is counted as uncoded.
	codes   []string
	codeIdx map[string]int
	limits  []string
	hooks   []string
}

var metricEnumSet = sync.OnceValue(func() *metricEnums {
	e := &metricEnums{
		ops:     observe.AllOperationKinds(),
		phases:  observe.AllPhases(),
		codes:   []string{observe.CodeOK, observe.CodeUncoded},
		hooks:   []string{hookStart, hookEnd, hookPh},
		opIdx:   map[observe.OperationKind]int{},
		phIdx:   map[observe.Phase]int{},
		codeIdx: map[string]int{},
	}
	for _, c := range errors.AllCodes() {
		e.codes = append(e.codes, string(c))
	}
	for _, n := range limits.Names() {
		e.limits = append(e.limits, string(n))
	}
	for i, k := range e.ops {
		e.opIdx[k] = i
	}
	for i, ph := range e.phases {
		e.phIdx[ph] = i
	}
	for i, c := range e.codes {
		e.codeIdx[c] = i
	}
	return e
})

// scope slots in the per-op tables.
const (
	scopeSlotTop = iota
	scopeSlotChild
	scopeSlots
)

var scopeBySlot = [scopeSlots]observe.Scope{observe.ScopeTop, observe.ScopeChild}

// opMetrics holds every instrument of the documented set, resolved from
// Options.Metrics once at New over the enumerated label combinations.
// The hot path indexes these tables and never calls the factory.
//
// Attribution: operations_total and operation_duration_seconds count
// every operation, top-level and child, split by scope. The remaining
// per-op metrics (phases, rows, bytes, in-flight, limit trips) count
// top-level operations only: a parent's counters already aggregate its
// children, so counting both would double-count.
type opMetrics struct {
	enums *metricEnums
	// ops[op][scope][code]
	ops      [][scopeSlots][]observe.Counter
	duration [][scopeSlots]observe.Histogram
	// phases[op][phase]
	phases   [][]observe.Histogram
	rows     []observe.Counter
	bytes    []observe.Counter
	inFlight []observe.UpDownCounter
	limits   map[string]observe.Counter
	panics   map[string]observe.Counter
}

// newOpMetrics resolves every instrument from m. Called only by New.
func newOpMetrics(m observe.Metrics) *opMetrics {
	e := metricEnumSet()
	om := &opMetrics{
		enums:    e,
		ops:      make([][scopeSlots][]observe.Counter, len(e.ops)),
		duration: make([][scopeSlots]observe.Histogram, len(e.ops)),
		phases:   make([][]observe.Histogram, len(e.ops)),
		rows:     make([]observe.Counter, len(e.ops)),
		bytes:    make([]observe.Counter, len(e.ops)),
		inFlight: make([]observe.UpDownCounter, len(e.ops)),
		limits:   make(map[string]observe.Counter, len(e.limits)),
		panics:   make(map[string]observe.Counter, len(e.hooks)),
	}
	for i, k := range e.ops {
		op := observe.Label{Key: labelOp, Value: string(k)}
		for s, scope := range scopeBySlot {
			sc := observe.Label{Key: labelScope, Value: string(scope)}
			om.ops[i][s] = make([]observe.Counter, len(e.codes))
			for c, code := range e.codes {
				om.ops[i][s][c] = m.Counter(metricOperations, op, observe.Label{Key: labelCode, Value: code}, sc)
			}
			om.duration[i][s] = m.Histogram(metricOpDuration, op, sc)
		}
		om.phases[i] = make([]observe.Histogram, len(e.phases))
		for j, ph := range e.phases {
			om.phases[i][j] = m.Histogram(metricPhaseDuration, op, observe.Label{Key: labelPhase, Value: string(ph)})
		}
		om.rows[i] = m.Counter(metricRowsScanned, op)
		om.bytes[i] = m.Counter(metricBytesRead, op)
		om.inFlight[i] = m.UpDownCounter(metricInFlight, op)
	}
	for _, n := range e.limits {
		om.limits[n] = m.Counter(metricLimitTrips, observe.Label{Key: labelLimit, Value: n})
	}
	for _, h := range e.hooks {
		om.panics[h] = m.Counter(metricHookPanics, observe.Label{Key: labelHook, Value: h})
	}
	return om
}

// begin marks a top-level operation in flight.
func (om *opMetrics) begin(info observe.OperationInfo) {
	if info.Scope != observe.ScopeTop {
		return
	}
	if i, ok := om.enums.opIdx[info.Kind]; ok {
		om.inFlight[i].Add(1)
	}
}

// end records a finished operation. err is the operation's error, read
// only for a PULSE_LIMIT_EXCEEDED limit name (an enum value).
func (om *opMetrics) end(info observe.OperationInfo, res observe.OperationResult, phases []observe.PhaseTiming, err error) {
	i, ok := om.enums.opIdx[info.Kind]
	if !ok {
		return
	}
	s := scopeSlotTop
	if info.Scope == observe.ScopeChild {
		s = scopeSlotChild
	}
	c, ok := om.enums.codeIdx[res.Code]
	if !ok {
		c = om.enums.codeIdx[observe.CodeUncoded]
	}
	om.ops[i][s][c].Add(1)
	om.duration[i][s].Observe(res.Duration.Seconds())
	if s != scopeSlotTop {
		return
	}
	for _, ph := range phases {
		if j, ok := om.enums.phIdx[ph.Phase]; ok {
			om.phases[i][j].Observe(ph.Duration.Seconds())
		}
	}
	if res.RowsScanned > 0 {
		om.rows[i].Add(float64(res.RowsScanned))
	}
	if res.BytesRead > 0 {
		om.bytes[i].Add(float64(res.BytesRead))
	}
	if err != nil {
		var coded *errors.CodedError
		if stderrors.As(err, &coded) && coded != nil && coded.Code == errors.PULSE_LIMIT_EXCEEDED {
			if name, _ := coded.Details["limit"].(string); name != "" {
				if ctr, ok := om.limits[name]; ok {
					ctr.Add(1)
				}
			}
		}
	}
	om.inFlight[i].Add(-1)
}

// hookPanic counts one recovered hook panic.
func (om *opMetrics) hookPanic(hook string) {
	if ctr, ok := om.panics[hook]; ok {
		ctr.Add(1)
	}
}
