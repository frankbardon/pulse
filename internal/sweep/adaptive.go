package sweep

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"sync"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// adaptive.go is the executor every adaptive sweep mode shares: a mode
// whose points are chosen round by round from the ranked values of the
// rounds before (differential evolution, Bayesian optimisation). A mode
// supplies only a Strategy — what to propose next — registered with
// RegisterAdaptive; everything else is this file's, so every adaptive
// mode keeps the same contract:
//
//   - the sweep carries `budget` (the slot count, MaxComposeSlots-bound
//     exactly as `samples` is) and a `rank` (the objective) — refused
//     without either (adaptive_requires_budget, adaptive_requires_rank);
//   - an axis is a list axis (`values`, searched by value index) or a
//     range axis (`range`, searched on the unit interval and mapped by
//     RangeNumber), in any mix;
//   - Expand sizes the slot list to budget up front: round 0 — proposed
//     from no history, so a function of the spec and its seed alone —
//     renders at expansion with its real labels; every later slot is a
//     placeholder rendered at the representative point (range midpoints,
//     each list axis's first value) and labelled `sample=<slot>`, so
//     predict and the runtime judge one slot list;
//   - Run then evaluates round after round, each a barrier: a round's
//     slots, ordered by slot index, all finish before the strategy sees
//     their values and proposes the next; slot indices are round-major,
//     so `failed_indices`, run masks and RankEntry.Slot stay stable;
//   - every adaptive slot is labelled `sample=<slot>_<pattern>` (the
//     authored label pattern, or the default `<axis>=<value>` pairs):
//     a strategy may propose one point twice, and the ordinal keeps the
//     labels unique.

// Dim is one axis of an adaptive sweep's search space, in declared
// order.
type Dim struct {
	// Name is the axis name.
	Name string
	// Range is a range axis's interval: the axis is searched through
	// Point.U, mapped by RangeValue. Nil on a list axis.
	Range *types.SweepRange
	// Levels is a list axis's value count: the axis is searched through
	// Point.Index in [0, Levels). Zero on a range axis.
	Levels int
}

// Point is one candidate an adaptive strategy proposes, axis by axis in
// declared order (both slices len(Dims)).
type Point struct {
	// U is each range axis's unit-interval coordinate in [0, 1],
	// mapped onto the axis by RangeValue (a value outside [0, 1] lands
	// on a bound); ignored, and 0 in an Observation, on a list axis.
	U []float64
	// Index is each list axis's 0-based value index, in [0, Levels);
	// ignored, and 0 in an Observation, on a range axis.
	Index []int
}

// Observation is one evaluated slot as a strategy sees it.
type Observation struct {
	// Slot is the 0-based sweep slot index (round-major: every slot of
	// round r precedes every slot of round r+1).
	Slot int
	// Round is the 0-based round that proposed the point.
	Round int
	// Point is the point the slot ran with.
	Point Point
	// Loss is the slot's ranked value as a quantity to MINIMISE: the
	// value under rank order asc, its negation under desc. A penalised
	// slot carries the worst (largest) Loss among the unpenalised
	// observations of the history it is handed — 0 when there is none —
	// recomputed every round.
	Loss float64
	// Penalised reports a slot that failed or that the rank leaves out
	// (its value at rank.by is null or missing).
	Penalised bool
}

// Strategy proposes an adaptive sweep's points, round by round.
//
// Propose returns round's batch: at least 1 point and at most limit (the
// budget left). history is every slot evaluated so far, ordered by
// slot index (a fresh copy per call). Round 0 is proposed from an empty
// history and must be a function of the StrategyConfig alone — predict
// lists its labels without running anything. The executor calls rounds
// 0, 1, 2… in order on one Strategy, so a strategy may keep state
// between calls, but every proposal must be a pure function of the
// config and the histories handed so far: bit-identical on every
// architecture (explicit float64(x*y) barriers, linalg/detmath for
// exp / log / erf, seeded draws from the config's Seed, deterministic
// tie-breaks). An error stops the batch.
type Strategy interface {
	Propose(round int, history []Observation, limit int) ([]Point, error)
}

// StrategyConfig is what a strategy is built from.
type StrategyConfig struct {
	// Dims is the search space, axis by axis in declared order.
	Dims []Dim
	// Budget is the total slot count (SweepSpec.Budget).
	Budget int
	// Seed is the sweep's effective seed (EffectiveSeed): rng_seed, or
	// one derived from the spec.
	Seed int64
	// Spec is the validated sweep, for the mode's own knobs.
	Spec *types.SweepSpec
}

// AdaptiveMode is one adaptive sweep mode's registration.
type AdaptiveMode struct {
	// Mode is the SweepMode name.
	Mode types.SweepMode
	// Validate checks the mode's own knobs on a sweep that passed the
	// shared rules (nil: the mode has none), returning a
	// PULSE_SWEEP_* refusal.
	Validate func(spec *types.SweepSpec) *errors.CodedError
	// New builds the strategy for one expansion.
	New func(cfg StrategyConfig) (Strategy, error)
}

var (
	adaptiveMu    sync.RWMutex
	adaptiveModes = map[types.SweepMode]AdaptiveMode{}
)

// RegisterAdaptive registers an adaptive sweep mode. It panics on an
// empty mode, a nil New, a built-in non-adaptive mode name or a second
// registration of one name: registration is wiring, done once from an
// init.
func RegisterAdaptive(m AdaptiveMode) {
	if m.Mode == "" || m.New == nil {
		panic("sweep: RegisterAdaptive needs a mode name and a New")
	}
	switch m.Mode {
	case types.SweepModeGrid, types.SweepModeZip, types.SweepModeRandom, types.SweepModeLHS:
		panic("sweep: RegisterAdaptive: " + string(m.Mode) + " is not an adaptive mode")
	}
	adaptiveMu.Lock()
	defer adaptiveMu.Unlock()
	if _, dup := adaptiveModes[m.Mode]; dup {
		panic("sweep: adaptive mode " + string(m.Mode) + " registered twice")
	}
	adaptiveModes[m.Mode] = m
}

// IsAdaptive reports whether mode is a registered adaptive mode.
func IsAdaptive(mode types.SweepMode) bool {
	_, ok := adaptiveFor(mode)
	return ok
}

func adaptiveFor(mode types.SweepMode) (AdaptiveMode, bool) {
	adaptiveMu.RLock()
	defer adaptiveMu.RUnlock()
	m, ok := adaptiveModes[mode]
	return m, ok
}

// AdaptiveModes are the registered adaptive modes, sorted by name.
func AdaptiveModes() []types.SweepMode {
	adaptiveMu.RLock()
	defer adaptiveMu.RUnlock()
	out := make([]types.SweepMode, 0, len(adaptiveModes))
	for m := range adaptiveModes {
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}

// The reasons the adaptive rules add. PULSE_SWEEP_INVALID:
const (
	// ReasonAdaptiveRequiresBudget: an adaptive mode sets no `budget`.
	ReasonAdaptiveRequiresBudget = "adaptive_requires_budget"
	// ReasonBudgetInvalid: `budget` is below 1.
	ReasonBudgetInvalid = "budget_invalid"
	// ReasonAdaptiveRequiresRank: an adaptive mode sets no `rank` — the
	// objective its rounds read.
	ReasonAdaptiveRequiresRank = "adaptive_requires_rank"
)

// PULSE_SWEEP_MODE_MISMATCH:
const (
	// ReasonBudgetWithoutAdaptive: `budget` is set under a mode that is
	// not adaptive.
	ReasonBudgetWithoutAdaptive = "budget_without_adaptive"
	// ReasonSamplesUnderAdaptive: `samples` is set under an adaptive
	// mode, whose slot count is `budget`.
	ReasonSamplesUnderAdaptive = "samples_under_adaptive"
	// ReasonOverlaysUnderAdaptive: `sweep.overlays` is set under an
	// adaptive mode — a per-slot overlay names slots whose labels are
	// known only once their round is proposed.
	ReasonOverlaysUnderAdaptive = "overlays_under_adaptive"
)

// AdaptiveLabelPrefix is the prefix every adaptive slot's label takes:
// `sample=<slot>_`.
func AdaptiveLabelPrefix(slot int) string { return "sample=" + strconv.Itoa(slot) + "_" }

// PendingLabel is the label a placeholder slot (one a later round
// fills) carries until its round renders it: `sample=<slot>`.
func PendingLabel(slot int) string { return "sample=" + strconv.Itoa(slot) }

// validateAdaptive is the adaptive modes' sweep-level rule set, run
// after the axes passed (validateAxes): budget and rank required,
// samples and overlays refused, then the mode's own knobs.
func validateAdaptive(spec *types.SweepSpec, m AdaptiveMode) *errors.CodedError {
	if spec.Samples != 0 {
		return mismatch("sweep.samples", "", spec.Mode, ReasonSamplesUnderAdaptive,
			"sweep sets samples under adaptive mode "+string(spec.Mode)+", whose slot count is `budget`; drop samples")
	}
	if spec.Budget < 1 {
		reason, msg := ReasonAdaptiveRequiresBudget, "adaptive sweep mode "+string(spec.Mode)+" evaluates `budget` slots in rounds but sets none; set budget to at least 1"
		if spec.Budget < 0 {
			reason, msg = ReasonBudgetInvalid, "sweep budget is "+strconv.Itoa(spec.Budget)+"; set it to at least 1"
		}
		return invalid("sweep.budget", "", reason, msg, map[string]any{"mode": string(spec.Mode), "value": spec.Budget})
	}
	if spec.Rank == nil {
		return invalid("sweep.rank", "", ReasonAdaptiveRequiresRank,
			"adaptive sweep mode "+string(spec.Mode)+" chooses each round's points from the ranked values of the rounds before, "+
				"but sets no rank; set rank.by (and order) to the objective",
			map[string]any{"mode": string(spec.Mode)})
	}
	if ov := spec.Overlays; len(ov) > 0 && string(ov) != "null" {
		return mismatch("sweep.overlays", "", spec.Mode, ReasonOverlaysUnderAdaptive,
			"sweep sets overlays under adaptive mode "+string(spec.Mode)+": a per-slot overlay names its slot by label, "+
				"and an adaptive slot's label is known only once its round runs; declare the overlay in the top-level overlays instead")
	}
	if m.Validate != nil {
		return m.Validate(spec)
	}
	return nil
}

// validateAdaptiveAxis is one adaptive axis's rule: a list axis
// (values, every one a scalar) or a range axis (the sampling modes'
// range rules), never both.
func validateAdaptiveAxis(at string, ax types.SweepAxis, mode types.SweepMode) *errors.CodedError {
	if ax.Range == nil && len(ax.Values) == 0 {
		return invalid(at+".values", ax.Name, ReasonValuesEmpty,
			"sweep axis "+describe(at, ax.Name)+" has neither values nor a range; under adaptive mode "+string(mode)+
				" an axis lists values or carries a range {min, max}", nil)
	}
	if ax.Range != nil {
		if len(ax.Values) > 0 {
			return mismatch(at, ax.Name, mode, ReasonValuesAndRange,
				"sweep axis "+describe(at, ax.Name)+" sets both values and range; an adaptive axis lists values or carries a range — drop one")
		}
		return validateRangeAxis(at, ax, mode)
	}
	for j, v := range ax.Values {
		if !isScalar(v) {
			vp := at + ".values[" + strconv.Itoa(j) + "]"
			return invalid(vp, ax.Name, ReasonValueNotScalar,
				"sweep axis "+describe(at, ax.Name)+" value "+vp+" is not a scalar; values are numbers, strings or booleans", nil)
		}
	}
	return nil
}

// dimsOf is spec's search space.
func dimsOf(spec *types.SweepSpec) []Dim {
	dims := make([]Dim, len(spec.Axes))
	for i, ax := range spec.Axes {
		dims[i] = Dim{Name: ax.Name, Range: ax.Range}
		if ax.Range == nil {
			dims[i].Levels = len(ax.Values)
		}
	}
	return dims
}

// Representative is the point predict and the placeholder slots render
// at: every range axis at its midpoint (u = ½: the log midpoint under
// scale log), every list axis at its first value.
func Representative(dims []Dim) Point {
	p := Point{U: make([]float64, len(dims)), Index: make([]int, len(dims))}
	for i, d := range dims {
		if d.Range != nil {
			p.U[i] = 0.5
		}
	}
	return p
}

// pointValues maps p onto the axes: a range axis's RangeNumber of its
// coordinate, a list axis's value at its index.
func pointValues(spec *types.SweepSpec, p Point) map[string]any {
	values := make(map[string]any, len(spec.Axes))
	for i, ax := range spec.Axes {
		if ax.Range != nil {
			values[ax.Name] = RangeNumber(ax.Range, p.U[i])
			continue
		}
		values[ax.Name] = ax.Values[p.Index[i]]
	}
	return values
}

// observed is p as an Observation carries it: list coordinates in U and
// range coordinates in Index zeroed, a range coordinate clamped into
// [0, 1] (where RangeValue maps it).
func observed(dims []Dim, p Point) Point {
	out := Point{U: make([]float64, len(dims)), Index: make([]int, len(dims))}
	for i, d := range dims {
		if d.Range != nil {
			out.U[i] = min(max(p.U[i], 0), 1)
			continue
		}
		out.Index[i] = p.Index[i]
	}
	return out
}

// checkPoints is the executor's guard on a strategy's batch: 1..max
// points, each sized to the space, every range coordinate finite and
// every list index in range. A breach is the strategy's bug:
// PROCESSING_INTERNAL.
func checkPoints(mode types.SweepMode, round int, pts []Point, dims []Dim, maxN int) *errors.CodedError {
	fault := func(msg string) *errors.CodedError {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"adaptive sweep mode "+string(mode)+" round "+strconv.Itoa(round)+": "+msg,
			map[string]any{"mode": string(mode), "round": round})
	}
	if len(pts) < 1 || len(pts) > maxN {
		return fault("the strategy proposed " + strconv.Itoa(len(pts)) + " points; a round takes 1 to " + strconv.Itoa(maxN))
	}
	for k, p := range pts {
		if len(p.U) != len(dims) || len(p.Index) != len(dims) {
			return fault("point " + strconv.Itoa(k) + " is not sized to the " + strconv.Itoa(len(dims)) + " axes")
		}
		for i, d := range dims {
			if d.Range != nil && (math.IsNaN(p.U[i]) || math.IsInf(p.U[i], 0)) {
				return fault("point " + strconv.Itoa(k) + " axis " + strconv.Quote(d.Name) + " has a non-finite coordinate")
			}
			if d.Range == nil && (p.Index[i] < 0 || p.Index[i] >= d.Levels) {
				return fault("point " + strconv.Itoa(k) + " axis " + strconv.Quote(d.Name) + " index " + strconv.Itoa(p.Index[i]) +
					" is outside [0, " + strconv.Itoa(d.Levels) + ")")
			}
		}
	}
	return nil
}

// Adaptive is an expanded adaptive sweep (Expansion.Adaptive): its
// strategy, the round-0 slots Expand rendered and what Run needs to
// render the rest.
type Adaptive struct {
	spec       *types.SweepSpec
	dims       []Dim
	strategy   Strategy
	labelBody  json.RawMessage
	firstSlots []AdaptiveSlot
	firstPts   []Point
	ran        bool

	// Budget is the slot count.
	Budget int
	// First is the number of round-0 slots — the sweep slots [0, First)
	// carry their real bodies and labels from expansion on; slots
	// [First, Budget) are placeholders until their round renders them.
	First int
}

// Pending reports whether sweep slot i (0-based in the sweep) is a
// placeholder a later round fills.
func (a *Adaptive) Pending(i int) bool { return a != nil && i >= a.First && i < a.Budget }

// AdaptiveSlot is one rendered slot of a round.
type AdaptiveSlot struct {
	// Slot is the 0-based sweep slot index.
	Slot int
	// Request is the rendered, strictly decoded slot request, labelled.
	Request *types.Request
	// Axes is the axis → value map the slot ran with.
	Axes map[string]any
}

// Outcome is one evaluated slot's answer for the strategy.
type Outcome struct {
	// Value is the slot's ranked value (unused when Penalised).
	Value float64
	// Penalised reports a slot that failed or that the rank leaves out.
	Penalised bool
}

// newAdaptive builds the expansion's strategy and proposes round 0.
func newAdaptive(spec *types.SweepSpec, m AdaptiveMode, seed int64, labelBody json.RawMessage) (*Adaptive, *errors.CodedError) {
	dims := dimsOf(spec)
	st, err := m.New(StrategyConfig{Dims: dims, Budget: spec.Budget, Seed: seed, Spec: spec})
	if err != nil {
		return nil, strategyFault(spec.Mode, 0, err)
	}
	pts, err := st.Propose(0, []Observation{}, spec.Budget)
	if err != nil {
		return nil, strategyFault(spec.Mode, 0, err)
	}
	if ce := checkPoints(spec.Mode, 0, pts, dims, spec.Budget); ce != nil {
		return nil, ce
	}
	return &Adaptive{spec: spec, dims: dims, strategy: st, labelBody: labelBody, firstPts: pts, Budget: spec.Budget, First: len(pts)}, nil
}

// strategyFault passes a coded strategy error through and wraps any
// other as PROCESSING_INTERNAL.
func strategyFault(mode types.SweepMode, round int, err error) *errors.CodedError {
	if ce, ok := err.(*errors.CodedError); ok {
		return ce
	}
	ce := errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		"adaptive sweep mode "+string(mode)+" round "+strconv.Itoa(round)+": "+err.Error(),
		map[string]any{"mode": string(mode), "round": round})
	ce.Cause = err
	return ce
}

// render renders sweep slot i at p: the body (strictly decoded, targets
// applied) and the label `sample=<i>_<pattern>`.
func (a *Adaptive) render(i int, p Point) (AdaptiveSlot, *errors.CodedError) {
	values := pointValues(a.spec, p)
	vars := variablesFor(a.spec.Axes, values)
	req, ce := renderRequest(a.spec, vars, values, i)
	if ce != nil {
		return AdaptiveSlot{}, ce
	}
	applyTargets(req, a.spec.Axes, values)
	label, ce := renderLabel(a.labelBody, a.spec, vars, values, i)
	if ce != nil {
		return AdaptiveSlot{}, ce
	}
	req.Label = AdaptiveLabelPrefix(i) + label
	return AdaptiveSlot{Slot: i, Request: req, Axes: values}, nil
}

// placeholder renders sweep slot i at the representative point,
// labelled PendingLabel(i): the body predict and the runtime judge
// statically until the slot's round renders its real one.
func (a *Adaptive) placeholder(i int) (*types.Request, *errors.CodedError) {
	s, ce := a.render(i, Representative(a.dims))
	if ce != nil {
		return nil, ce
	}
	s.Request.Label = PendingLabel(i)
	return s.Request, nil
}

// Run evaluates the sweep: round 0 (the slots Expand rendered), then
// round after round until Budget slots ran. Each round eval is handed
// the round's rendered slots in slot-index order and returns one
// Outcome per slot, index-aligned; its error stops the run at once and
// is returned unchanged. Between rounds the strategy sees every outcome
// so far as an Observation history ordered by slot index (penalised
// slots at the worst observed loss), so the next round is a function of
// the finished rounds alone — never of completion order. A render
// fault of a later-round slot (its body does not decode at the point
// proposed) is returned as the PULSE_SWEEP_INVALID refusal Expand would
// give it. Run may be called once.
func (a *Adaptive) Run(eval func(round int, slots []AdaptiveSlot) ([]Outcome, error)) error {
	if a.ran {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL, "adaptive sweep run twice")
	}
	a.ran = true
	order := a.spec.Rank.Order
	var history []Observation
	slots := a.firstSlots
	pts := a.firstPts
	next := 0
	for round := 0; ; round++ {
		if round > 0 {
			rendered := make([]AdaptiveSlot, len(pts))
			for k, p := range pts {
				s, ce := a.render(next+k, p)
				if ce != nil {
					return ce
				}
				rendered[k] = s
			}
			slots = rendered
		}
		outs, err := eval(round, slots)
		if err != nil {
			return err
		}
		if len(outs) != len(slots) {
			return errors.NewCodedError(errors.PROCESSING_INTERNAL,
				"adaptive sweep round "+strconv.Itoa(round)+": "+strconv.Itoa(len(outs))+" outcomes for "+strconv.Itoa(len(slots))+" slots")
		}
		for k, o := range outs {
			loss := o.Value
			if order == types.SweepRankDesc {
				loss = -loss
			}
			history = append(history, Observation{Slot: next + k, Round: round, Point: observed(a.dims, pts[k]), Loss: loss, Penalised: o.Penalised})
		}
		next += len(slots)
		left := a.Budget - next
		if left <= 0 {
			return nil
		}
		var perr error
		pts, perr = a.strategy.Propose(round+1, penalised(history), left)
		if perr != nil {
			return strategyFault(a.spec.Mode, round+1, perr)
		}
		if ce := checkPoints(a.spec.Mode, round+1, pts, a.dims, left); ce != nil {
			return ce
		}
	}
}

// penalised is a fresh copy of history with every penalised entry's
// Loss set to the worst (largest) Loss among the unpenalised ones, 0
// when none is.
func penalised(history []Observation) []Observation {
	worst, seen := 0.0, false
	for _, o := range history {
		if !o.Penalised && (!seen || o.Loss > worst) {
			worst, seen = o.Loss, true
		}
	}
	out := make([]Observation, len(history))
	for i, o := range history {
		o.Point = Point{U: slices.Clone(o.Point.U), Index: slices.Clone(o.Point.Index)}
		if o.Penalised {
			o.Loss = worst
		}
		out[i] = o
	}
	return out
}
