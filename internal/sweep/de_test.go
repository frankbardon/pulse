package sweep_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/internal/sweep/sweeptest"
	"github.com/frankbardon/pulse/types"
)

// de_test.go pins mode de (de.go): the knob rules and defaults, a known
// optimum over every axis form (linear, log and integer ranges and a
// list axis) inside the budget, the generation schedule with its
// remainder, determinism per seed, a penalised region and the
// list-value probe at expansion.

func iptr(v int) *int { return &v }

// axisNumber reads a rendered numeric axis value.
func axisNumber(t *testing.T, v any) float64 {
	t.Helper()
	n, ok := v.(json.Number)
	if !ok {
		t.Fatalf("axis value %v (%T) is not a json.Number", v, v)
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// mixedDE is a de sweep over x (linear [0, 10]), lambda (log
// [1e-3, 1e2]), n (integer [1, 20]) and a list axis kind (a..d).
func mixedDE(budget int, seed int64) *types.SweepSpec {
	return &types.SweepSpec{
		Mode: types.SweepModeDE, Budget: budget, RNGSeed: seed,
		Axes: []types.SweepAxis{
			{Name: "x", Range: &types.SweepRange{Min: fptr(0), Max: fptr(10)}},
			{Name: "lambda", Range: &types.SweepRange{Min: fptr(1e-3), Max: fptr(1e2), Scale: types.SweepScaleLog}},
			{Name: "n", Range: &types.SweepRange{Min: fptr(1), Max: fptr(20), Integer: true}},
			{Name: "kind", Values: []any{"a", "b", "c", "d"}},
		},
		Request: json.RawMessage(`{"params":{"x":{"$var":"x"},"l":{"$var":"lambda"},"n":{"$var":"n"},"k":{"$var":"kind"}}}`),
		Rank:    &types.SweepRank{By: "v"},
	}
}

// mixedObjective has its minimum 0 at x = 3.7, lambda = 0.1, n = 12,
// kind = c. Every product is rounded before its add.
func mixedObjective(t *testing.T) func(map[string]any) (float64, bool) {
	return func(a map[string]any) (float64, bool) {
		dx := axisNumber(t, a["x"]) - 3.7
		dl := math.Log10(axisNumber(t, a["lambda"])) + 1
		dn := axisNumber(t, a["n"]) - 12
		v := float64(dx*dx) + float64(dl*dl) + float64(dn*dn)
		if a["kind"] != "c" {
			v += 5
		}
		return v, true
	}
}

func best(ev []sweeptest.Evaluated) sweeptest.Evaluated {
	b := ev[0]
	for _, e := range ev[1:] {
		if e.Value < b.Value {
			b = e
		}
	}
	return b
}

// TestDE_Validate: each knob's rule, the de block under another mode,
// and the defaults (population 10 × axes capped at the budget, F 0.8,
// CR 0.9).
func TestDE_Validate(t *testing.T) {
	t.Parallel()
	ok := mixedDE(60, 1)
	ok.DE = &types.SweepDE{Population: iptr(4), F: fptr(2), CR: fptr(0)}
	if ce := sweep.Validate(ok); ce != nil {
		t.Fatalf("boundary knobs refused: %v", ce)
	}
	cases := []struct {
		name   string
		edit   func(*types.SweepSpec)
		code   perr.Code
		reason string
	}{
		{"population 3", func(s *types.SweepSpec) { s.DE = &types.SweepDE{Population: iptr(3)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonDEPopulationInvalid},
		{"population 0", func(s *types.SweepSpec) { s.DE = &types.SweepDE{Population: iptr(0)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonDEPopulationInvalid},
		{"population over budget", func(s *types.SweepSpec) { s.DE = &types.SweepDE{Population: iptr(61)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonDEPopulationOverBudget},
		{"f 0", func(s *types.SweepSpec) { s.DE = &types.SweepDE{F: fptr(0)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonDEFInvalid},
		{"f over 2", func(s *types.SweepSpec) { s.DE = &types.SweepDE{F: fptr(2.5)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonDEFInvalid},
		{"f NaN", func(s *types.SweepSpec) { s.DE = &types.SweepDE{F: fptr(math.NaN())} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonDEFInvalid},
		{"cr negative", func(s *types.SweepSpec) { s.DE = &types.SweepDE{CR: fptr(-0.1)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonDECRInvalid},
		{"cr over 1", func(s *types.SweepSpec) { s.DE = &types.SweepDE{CR: fptr(1.5)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonDECRInvalid},
		{"de under lhs", func(s *types.SweepSpec) {
			s.Mode, s.Budget, s.Samples, s.Axes, s.DE = types.SweepModeLHS, 0, 4, s.Axes[:1], &types.SweepDE{}
		}, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonDEWithoutDE},
		{"de under test_fixed", func(s *types.SweepSpec) { s.Mode, s.DE = sweeptest.Mode, &types.SweepDE{} }, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonDEWithoutDE},
	}
	for _, c := range cases {
		spec := mixedDE(60, 1)
		c.edit(spec)
		ce := sweep.Validate(spec)
		if ce == nil || ce.Code != c.code || ce.Details["reason"] != c.reason {
			t.Errorf("%s: %v, want %s %s", c.name, ce, c.code, c.reason)
		}
	}

	if got := sweep.ResolveDE(mixedDE(1000, 1)); got != (sweep.DESettings{Population: 40, F: 0.8, CR: 0.9}) {
		t.Errorf("defaults %+v", got)
	}
	if got := sweep.ResolveDE(mixedDE(25, 1)); got.Population != 25 {
		t.Errorf("default population over a budget of 25: %d", got.Population)
	}
	set := mixedDE(100, 1)
	set.DE = &types.SweepDE{Population: iptr(8), F: fptr(0.5), CR: fptr(0.3)}
	if got := sweep.ResolveDE(set); got != (sweep.DESettings{Population: 8, F: 0.5, CR: 0.3}) {
		t.Errorf("set knobs %+v", got)
	}
}

// TestDE_FindsKnownOptimum: within a budget of 1200 (population 40, 30
// generations) every seed reaches the optimum's basin on every axis
// form — the list axis at its best value, the integer axis exactly at
// its optimum, the linear and log axes within 0.1 (x, log10 lambda).
func TestDE_FindsKnownOptimum(t *testing.T) {
	t.Parallel()
	for seed := int64(1); seed <= 3; seed++ {
		ev, err := sweeptest.Search(mixedDE(1200, seed), mixedObjective(t))
		if err != nil {
			t.Fatal(err)
		}
		b := best(ev)
		if len(ev) != 1200 || b.Axes["kind"] != "c" || axisNumber(t, b.Axes["n"]) != 12 ||
			math.Abs(axisNumber(t, b.Axes["x"])-3.7) > 0.1 || math.Abs(math.Log10(axisNumber(t, b.Axes["lambda"]))+1) > 0.1 {
			t.Errorf("seed %d: %d slots, best %v at %v", seed, len(ev), b.Value, b.Axes)
		}
		// The integer axis renders in integer form.
		if s := string(b.Axes["n"].(json.Number)); s != "12" {
			t.Errorf("seed %d: integer axis renders %q", seed, s)
		}
	}
}

// TestDE_GenerationsAndRemainder: round 0 is the population, each later
// round one generation of population trials, and a budget remainder a
// final partial generation — budget 45 at population 20 runs rounds of
// 20, 20 and 5 slots.
func TestDE_GenerationsAndRemainder(t *testing.T) {
	t.Parallel()
	spec := mixedDE(45, 3)
	spec.DE = &types.SweepDE{Population: iptr(20)}
	ev, err := sweeptest.Search(spec, mixedObjective(t))
	if err != nil {
		t.Fatal(err)
	}
	sizes := map[int]int{}
	for i, e := range ev {
		if e.Slot != i {
			t.Fatalf("slot %d at position %d", e.Slot, i)
		}
		sizes[e.Round]++
	}
	if !reflect.DeepEqual(sizes, map[int]int{0: 20, 1: 20, 2: 5}) {
		t.Fatalf("round sizes %v", sizes)
	}
	// Default population over a budget below it: one round, no generation.
	ev, err = sweeptest.Search(mixedDE(12, 3), mixedObjective(t))
	if err != nil || len(ev) != 12 || ev[11].Round != 0 {
		t.Fatalf("budget 12: %d slots, err %v", len(ev), err)
	}
}

// TestDE_DeterministicPerSeed: a seed replays the search bit for bit
// (every slot's axis values and value); another seed searches
// elsewhere.
func TestDE_DeterministicPerSeed(t *testing.T) {
	t.Parallel()
	a, err := sweeptest.Search(mixedDE(120, 11), mixedObjective(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := sweeptest.Search(mixedDE(120, 11), mixedObjective(t))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("one seed searched two ways")
	}
	c, err := sweeptest.Search(mixedDE(120, 12), mixedObjective(t))
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(a[0].Axes, c[0].Axes) && reflect.DeepEqual(a[119].Axes, c[119].Axes) {
		t.Fatal("seeds 11 and 12 searched alike")
	}
}

// TestDE_PenalisedRegion: a failing region (x > 5 penalised) never
// holds the best point, and the search still reaches the optimum.
func TestDE_PenalisedRegion(t *testing.T) {
	t.Parallel()
	obj := mixedObjective(t)
	ev, err := sweeptest.Search(mixedDE(1200, 5), func(a map[string]any) (float64, bool) {
		if axisNumber(t, a["x"]) > 5 {
			return 0, false // a value of 0 would win if it were not penalised
		}
		return obj(a)
	})
	if err != nil {
		t.Fatal(err)
	}
	var b *sweeptest.Evaluated
	failed := 0
	for i := range ev {
		if axisNumber(t, ev[i].Axes["x"]) > 5 {
			failed++
			continue
		}
		if b == nil || ev[i].Value < b.Value {
			b = &ev[i]
		}
	}
	if failed == 0 || b == nil || b.Axes["kind"] != "c" || math.Abs(axisNumber(t, b.Axes["x"])-3.7) > 0.05 {
		t.Fatalf("%d penalised slots, best %+v", failed, b)
	}
}

// TestAdaptive_ProbesListValues: a list value that does not decode is
// refused at expansion — whatever round 0 drew — as the
// PULSE_SWEEP_INVALID a later round would raise, located at the first
// placeholder slot; without a later round there is nothing to probe.
func TestAdaptive_ProbesListValues(t *testing.T) {
	t.Parallel()
	spec := func(budget int) *types.SweepSpec {
		return &types.SweepSpec{
			Mode: sweeptest.Mode, Budget: budget, RNGSeed: 3,
			Axes: []types.SweepAxis{
				{Name: "x", Range: &types.SweepRange{Min: fptr(0), Max: fptr(1)}},
				{Name: "flag", Values: []any{true, false, true, false, true, false, true, false, true, "nope"}},
			},
			Request: json.RawMessage(`{"params":{"x":{"$var":"x"}},"disable_components":{"$var":"flag"}}`),
			Rank:    &types.SweepRank{By: "v"},
		}
	}
	// Round 0 (three test_fixed points) never draws index 9 at seed 3.
	exp, err := sweep.Expand(&types.ComposedRequest{Sweep: spec(sweeptest.Batch)}, sweep.Options{}, nil)
	if err != nil {
		t.Fatalf("budget = round 0: %v", err)
	}
	for _, a := range exp.Axes {
		if a["flag"] == "nope" {
			t.Fatal("round 0 drew the bad value; pick another seed")
		}
	}
	_, err = sweep.Expand(&types.ComposedRequest{Sweep: spec(7)}, sweep.Options{}, nil)
	ce, ok := err.(*perr.CodedError)
	if !ok || ce.Code != perr.PULSE_SWEEP_INVALID || ce.Details["reason"] != sweep.ReasonRequestDecode ||
		ce.Details["slot"] != sweeptest.Batch {
		t.Fatalf("got %v (%+v), want PULSE_SWEEP_INVALID request_decode at slot %d", err, ce, sweeptest.Batch)
	}
}
