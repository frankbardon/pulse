package sweep_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/internal/sweep/sweeptest"
	"github.com/frankbardon/pulse/types"
)

// adaptive_test.go pins the adaptive executor (adaptive.go) with the
// deterministic test strategy (sweeptest) and two test-only modes
// registered here: test_record (proposes one point a round and records
// every history it is handed) and test_bad (proposes an out-of-range
// list index in round 1).

const (
	recordMode types.SweepMode = "test_record"
	badMode    types.SweepMode = "test_bad"
)

// recorded maps a run's seed to the histories its recording strategy
// was handed, round by round.
var recorded sync.Map

type recordStrategy struct {
	seed int64
	dims []sweep.Dim
}

func (r *recordStrategy) Propose(round int, history []sweep.Observation, limit int) ([]sweep.Point, error) {
	v, _ := recorded.LoadOrStore(r.seed, &[][]sweep.Observation{})
	h := v.(*[][]sweep.Observation)
	*h = append(*h, history)
	p := sweep.Point{U: make([]float64, len(r.dims)), Index: make([]int, len(r.dims))}
	for i, d := range r.dims {
		if d.Range != nil {
			p.U[i] = float64(round%4) / 4
		} else {
			p.Index[i] = round % d.Levels
		}
	}
	return []sweep.Point{p}, nil
}

type badStrategy struct{ dims []sweep.Dim }

func (b *badStrategy) Propose(round int, _ []sweep.Observation, _ int) ([]sweep.Point, error) {
	p := sweep.Point{U: make([]float64, len(b.dims)), Index: make([]int, len(b.dims))}
	if round > 0 {
		p.Index[0] = 99
	}
	return []sweep.Point{p}, nil
}

func init() {
	sweep.RegisterAdaptive(sweep.AdaptiveMode{Mode: recordMode, New: func(cfg sweep.StrategyConfig) (sweep.Strategy, error) {
		return &recordStrategy{seed: cfg.Seed, dims: cfg.Dims}, nil
	}})
	sweep.RegisterAdaptive(sweep.AdaptiveMode{Mode: badMode, New: func(cfg sweep.StrategyConfig) (sweep.Strategy, error) {
		return &badStrategy{dims: cfg.Dims}, nil
	}})
}

func fptr(v float64) *float64 { return &v }

// adaptiveSpec is an adaptive sweep over a list axis `kind` (three
// values) and a range axis `decay` on [0.1, 0.9], ranked by
// aggregations.m.value.
func adaptiveSpec(mode types.SweepMode, budget int) *types.SweepSpec {
	return &types.SweepSpec{
		Mode:   mode,
		Budget: budget,
		Axes: []types.SweepAxis{
			{Name: "kind", Values: []any{"a", "b", "c"}},
			{Name: "decay", Range: &types.SweepRange{Min: fptr(0.1), Max: fptr(0.9)}},
		},
		Request: json.RawMessage(`{"cohort":{"filename":"c.pulse"},"params":{"k":{"$var":"kind"},"d":{"$var":"decay"}},
			"aggregations":[{"type":"AGG_COUNT","field":"x","label":"m"}]}`),
		Rank: &types.SweepRank{By: "aggregations.m.value"},
	}
}

func expandAdaptive(t *testing.T, spec *types.SweepSpec, explicit int) *sweep.Expansion {
	t.Helper()
	c := &types.ComposedRequest{Sweep: spec}
	for i := range explicit {
		c.Requests = append(c.Requests, &types.Request{Label: fmt.Sprintf("explicit_%d", i)})
	}
	exp, err := sweep.Expand(c, sweep.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return exp
}

// TestAdaptive_Validate: an adaptive mode needs budget and rank, refuses
// samples and per-slot overlays, takes list and range axes in any mix
// (never both on one axis); budget under a non-adaptive mode is a mode
// mismatch.
func TestAdaptive_Validate(t *testing.T) {
	t.Parallel()
	ok := adaptiveSpec(sweeptest.Mode, 5)
	if ce := sweep.Validate(ok); ce != nil {
		t.Fatalf("valid adaptive spec refused: %v", ce)
	}
	cases := []struct {
		name   string
		edit   func(*types.SweepSpec)
		code   perr.Code
		reason string
	}{
		{"no budget", func(s *types.SweepSpec) { s.Budget = 0 }, perr.PULSE_SWEEP_INVALID, sweep.ReasonAdaptiveRequiresBudget},
		{"negative budget", func(s *types.SweepSpec) { s.Budget = -2 }, perr.PULSE_SWEEP_INVALID, sweep.ReasonBudgetInvalid},
		{"no rank", func(s *types.SweepSpec) { s.Rank = nil }, perr.PULSE_SWEEP_INVALID, sweep.ReasonAdaptiveRequiresRank},
		{"samples", func(s *types.SweepSpec) { s.Samples = 3 }, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonSamplesUnderAdaptive},
		{"overlays", func(s *types.SweepSpec) { s.Overlays = json.RawMessage(`[]`) }, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonOverlaysUnderAdaptive},
		{"values and range", func(s *types.SweepSpec) { s.Axes[1].Values = []any{1} }, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonValuesAndRange},
		{"neither", func(s *types.SweepSpec) { s.Axes[1].Range = nil }, perr.PULSE_SWEEP_INVALID, sweep.ReasonValuesEmpty},
		{"bad range", func(s *types.SweepSpec) { s.Axes[1].Range.Max = fptr(0) }, perr.PULSE_SWEEP_RANGE_INVALID, sweep.ReasonRangeEmpty},
		{"budget under grid", func(s *types.SweepSpec) { s.Mode = types.SweepModeGrid; s.Axes = s.Axes[:1] }, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonBudgetWithoutAdaptive},
		{"budget under random", func(s *types.SweepSpec) { s.Mode = types.SweepModeRandom; s.Axes = s.Axes[1:]; s.Samples = 2 }, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonBudgetWithoutAdaptive},
	}
	for _, c := range cases {
		spec := adaptiveSpec(sweeptest.Mode, 5)
		c.edit(spec)
		ce := sweep.Validate(spec)
		if ce == nil || ce.Code != c.code || ce.Details["reason"] != c.reason {
			t.Errorf("%s: %v, want %s %s", c.name, ce, c.code, c.reason)
		}
	}
	// An unknown mode's refusal names the registered adaptive modes.
	spec := adaptiveSpec("nope", 5)
	ce := sweep.Validate(spec)
	if ce == nil || ce.Details["reason"] != sweep.ReasonModeUnknown || !slices.Contains(ce.Details["valid"].([]string), string(sweeptest.Mode)) {
		t.Errorf("unknown mode: %v", ce)
	}
	// The hidden-instance refusals: an adaptive mode is unknown there,
	// its valid list naming the other modes only.
	for _, refuse := range []func(types.SweepMode) *perr.CodedError{sweep.NonAdaptiveModeRefusal, sweep.EnumerationModeRefusal} {
		ce := refuse(sweeptest.Mode)
		if ce == nil || ce.Details["reason"] != sweep.ReasonModeUnknown || slices.Contains(ce.Details["valid"].([]string), string(sweeptest.Mode)) {
			t.Errorf("hidden adaptive: %v", ce)
		}
	}
	if ce := sweep.NonAdaptiveModeRefusal(types.SweepModeLHS); ce != nil {
		t.Errorf("lhs refused as adaptive: %v", ce)
	}
}

// TestAdaptive_ExpandSizesBudget: the slot list is budget long after the
// explicit slots; round 0 (sweeptest.Batch slots) renders with its real
// `sample=<i>_` labels and axes, every later slot is a placeholder
// labelled `sample=<i>` at the representative point (range midpoint,
// first list value), its label "" and axes nil in the expansion. Count
// is the budget and the seed is set.
func TestAdaptive_ExpandSizesBudget(t *testing.T) {
	t.Parallel()
	spec := adaptiveSpec(sweeptest.Mode, 7)
	exp := expandAdaptive(t, spec, 2)
	a := exp.Adaptive
	if a == nil || a.Budget != 7 || a.First != sweeptest.Batch || sweep.Count(spec) != 7 || exp.Seed == nil {
		t.Fatalf("adaptive %+v count %d seed %v", a, sweep.Count(spec), exp.Seed)
	}
	if len(exp.Composed.Requests) != 9 || len(exp.Labels) != 7 || len(exp.Axes) != 7 || exp.Composed.Sweep != nil {
		t.Fatalf("requests %d labels %d axes %d", len(exp.Composed.Requests), len(exp.Labels), len(exp.Axes))
	}
	for i := range 7 {
		req := exp.Composed.Requests[2+i]
		if i < sweeptest.Batch {
			if !strings.HasPrefix(exp.Labels[i], fmt.Sprintf("sample=%d_kind=", i)) || req.Label != exp.Labels[i] || exp.Axes[i] == nil {
				t.Errorf("round-0 slot %d: label %q / %q axes %v", i, exp.Labels[i], req.Label, exp.Axes[i])
			}
			if !a.Pending(i) == false {
				t.Errorf("slot %d pending", i)
			}
			continue
		}
		if exp.Labels[i] != "" || exp.Axes[i] != nil || req.Label != sweep.PendingLabel(i) || !a.Pending(i) {
			t.Errorf("placeholder %d: label %q / %q axes %v", i, exp.Labels[i], req.Label, exp.Axes[i])
		}
		if req.Params["k"] != "a" || fmt.Sprint(req.Params["d"]) != "0.5" {
			t.Errorf("placeholder %d params %v, want the representative point", i, req.Params)
		}
	}
}

// runAll runs exp's adaptive sweep with eval valuing each slot by f and
// returns the rounds' slot lists and the rendered slots.
func runAll(t *testing.T, exp *sweep.Expansion, f func(sweep.AdaptiveSlot) sweep.Outcome) ([][]int, []sweep.AdaptiveSlot) {
	t.Helper()
	var rounds [][]int
	var all []sweep.AdaptiveSlot
	err := exp.Adaptive.Run(func(round int, slots []sweep.AdaptiveSlot) ([]sweep.Outcome, error) {
		if round != len(rounds) {
			t.Fatalf("round %d after %d rounds", round, len(rounds))
		}
		var idx []int
		outs := make([]sweep.Outcome, len(slots))
		for k, s := range slots {
			idx = append(idx, s.Slot)
			all = append(all, s)
			outs[k] = f(s)
		}
		rounds = append(rounds, idx)
		return outs, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return rounds, all
}

func decayOf(s sweep.AdaptiveSlot) float64 {
	v, _ := s.Axes["decay"].(json.Number).Float64()
	return v
}

// TestAdaptive_RunRoundsAndBudget: rounds run in order, each a
// contiguous block of slots (round-major), the last one cut to what the
// budget leaves, the total exactly the budget (the test strategy itself
// refuses a history that is not every earlier round, in slot order);
// round 0 hands back the slots Expand rendered; every slot's label is
// `sample=<slot>_…`; and the same spec replays the same slots.
func TestAdaptive_RunRoundsAndBudget(t *testing.T) {
	t.Parallel()
	spec := adaptiveSpec(sweeptest.Mode, 7)
	exp := expandAdaptive(t, spec, 0)
	rounds, all := runAll(t, exp, func(s sweep.AdaptiveSlot) sweep.Outcome { return sweep.Outcome{Value: decayOf(s)} })
	if want := [][]int{{0, 1, 2}, {3, 4, 5}, {6}}; !reflect.DeepEqual(rounds, want) {
		t.Fatalf("rounds %v, want %v", rounds, want)
	}
	for k, s := range all {
		if !strings.HasPrefix(s.Request.Label, sweep.AdaptiveLabelPrefix(k)) {
			t.Errorf("slot %d label %q", k, s.Request.Label)
		}
		if k < sweeptest.Batch && s.Request != exp.Composed.Requests[k] {
			t.Errorf("round-0 slot %d is not the expanded one", k)
		}
	}
	// Run is one-shot.
	if err := exp.Adaptive.Run(nil); err == nil {
		t.Error("a second Run succeeded")
	}
	// Replay: the same spec runs the same slots.
	_, again := runAll(t, expandAdaptive(t, adaptiveSpec(sweeptest.Mode, 7), 0), func(s sweep.AdaptiveSlot) sweep.Outcome { return sweep.Outcome{Value: decayOf(s)} })
	for k := range all {
		if all[k].Request.Label != again[k].Request.Label {
			t.Errorf("slot %d: %q then %q", k, all[k].Request.Label, again[k].Request.Label)
		}
	}
	// Later rounds read the history: a different objective moves them.
	_, flipped := runAll(t, expandAdaptive(t, adaptiveSpec(sweeptest.Mode, 7), 0), func(s sweep.AdaptiveSlot) sweep.Outcome { return sweep.Outcome{Value: -decayOf(s)} })
	same := true
	for k := sweeptest.Batch; k < len(all); k++ {
		same = same && all[k].Request.Label == flipped[k].Request.Label
	}
	if same {
		t.Error("later rounds ignore the history: a reversed objective proposed the same points")
	}
}

// TestAdaptive_PenaltyAndOrder: the history a strategy sees carries
// each value as a loss to minimise (negated under desc), every point in
// unit coordinates; a penalised slot carries the worst unpenalised loss
// of that history, re-evaluated every round (0 while none is
// unpenalised); every history is a fresh copy.
func TestAdaptive_PenaltyAndOrder(t *testing.T) {
	t.Parallel()
	spec := adaptiveSpec(recordMode, 5)
	spec.RNGSeed = 424242
	spec.Rank.Order = types.SweepRankDesc
	exp := expandAdaptive(t, spec, 0)
	values := []sweep.Outcome{{Penalised: true}, {Value: 2}, {Penalised: true}, {Value: 7}, {Value: 1}}
	runAll(t, exp, func(s sweep.AdaptiveSlot) sweep.Outcome { return values[s.Slot] })
	v, _ := recorded.Load(int64(424242))
	hist := *v.(*[][]sweep.Observation)
	if len(hist) != 5 {
		t.Fatalf("%d proposals, want 5", len(hist))
	}
	loss := func(h []sweep.Observation) []float64 {
		var out []float64
		for _, o := range h {
			out = append(out, o.Loss)
		}
		return out
	}
	want := [][]float64{
		{},
		{0},              // penalised, nothing unpenalised yet
		{-2, -2},         // desc: value 2 is loss -2, the penalty the worst
		{-2, -2, -2},     // still the only unpenalised loss
		{-2, -2, -2, -7}, // a better point does not move the worst
	}
	for r, h := range hist {
		if got := loss(h); !(len(got) == 0 && len(want[r]) == 0) && !reflect.DeepEqual(got, want[r]) {
			t.Errorf("round %d losses %v, want %v", r, got, want[r])
		}
		for k, o := range h {
			if o.Slot != k || o.Round != k || o.Penalised != values[k].Penalised {
				t.Errorf("round %d observation %d: %+v", r, k, o)
			}
			if o.Point.Index[1] != 0 || o.Point.U[0] != 0 || o.Point.U[1] < 0 || o.Point.U[1] > 1 {
				t.Errorf("round %d observation %d point %+v", r, k, o.Point)
			}
		}
	}
	if &hist[3][0] == &hist[4][0] {
		t.Error("histories share storage")
	}
}

// TestAdaptive_StrategyGuard: a strategy proposing an out-of-range list
// index is refused as PROCESSING_INTERNAL naming the mode and round,
// before any slot of that round renders.
func TestAdaptive_StrategyGuard(t *testing.T) {
	t.Parallel()
	exp := expandAdaptive(t, adaptiveSpec(badMode, 3), 0)
	calls := 0
	err := exp.Adaptive.Run(func(int, []sweep.AdaptiveSlot) ([]sweep.Outcome, error) {
		calls++
		return []sweep.Outcome{{Value: 1}}, nil
	})
	ce, ok := err.(*perr.CodedError)
	if !ok || ce.Code != perr.PROCESSING_INTERNAL || ce.Details["round"] != 1 || calls != 1 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

// TestObjective: a slot's ranked value by Rank's own rule — a number,
// a computed exact 0 omitempty hid, ok false for null or missing, a
// refusal for a non-number.
func TestObjective(t *testing.T) {
	t.Parallel()
	rk := &types.SweepRank{By: "data.north.v"}
	resp := func(v any) *types.Response {
		return &types.Response{Data: []map[string]any{{"name": "north", "v": v}}}
	}
	if v, ok, err := sweep.Objective(rk, "s", resp(3.5)); err != nil || !ok || v != 3.5 {
		t.Errorf("number: %v %v %v", v, ok, err)
	}
	if _, ok, err := sweep.Objective(rk, "s", resp(nil)); err != nil || ok {
		t.Errorf("null: %v %v", ok, err)
	}
	if _, ok, err := sweep.Objective(&types.SweepRank{By: "data.south.v"}, "s", resp(1.0)); err != nil || ok {
		t.Errorf("missing: %v %v", ok, err)
	}
	if _, _, err := sweep.Objective(rk, "s", resp("text")); err == nil {
		t.Error("a string ranked")
	}
	// A computed exact 0 the wire's omitempty hid ranks as 0.
	zero := &types.Response{Regressions: []*types.RegressionResult{{Name: "fit", Type: types.REG_OLS, ResidualStdErr: 0}}}
	if v, ok, err := sweep.Objective(&types.SweepRank{By: "regressions.fit.residual_std_err"}, "s", zero); err != nil || !ok || v != 0 {
		t.Errorf("typed zero: %v %v %v", v, ok, err)
	}
}
