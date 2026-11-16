package sweep_test

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/internal/sweep/sweeptest"
	"github.com/frankbardon/pulse/types"
)

// bo_test.go pins mode bo (bo.go): the knob rules and defaults, a known
// optimum over every axis form inside the budget, the round schedule
// (initial design, then batch points a round), determinism per seed
// with batch > 1, the constant-liar fill, rank order desc, the random
// initial design, a penalised region and the EI formula. The SciPy-free
// oracle comparison and the beats-random check are heavy
// (bo_oracle_heavy_test.go).

// mixedBO is mixedDE's space (linear, log and integer ranges and a list
// axis) under mode bo.
func mixedBO(budget int, seed int64) *types.SweepSpec {
	s := mixedDE(budget, seed)
	s.Mode = types.SweepModeBO
	return s
}

// quadBO is a one-axis bo sweep over x in [0, 1].
func quadBO(budget int, seed int64) *types.SweepSpec {
	return &types.SweepSpec{
		Mode: types.SweepModeBO, Budget: budget, RNGSeed: seed,
		Axes:    []types.SweepAxis{{Name: "x", Range: &types.SweepRange{Min: fptr(0), Max: fptr(1)}}},
		Request: json.RawMessage(`{"params":{"x":{"$var":"x"}}}`),
		Rank:    &types.SweepRank{By: "v"},
	}
}

// quadObjective has its minimum 0 at x = 0.3.
func quadObjective(t *testing.T) func(map[string]any) (float64, bool) {
	return func(a map[string]any) (float64, bool) {
		d := axisNumber(t, a["x"]) - 0.3
		return float64(d * d), true
	}
}

// TestBO_Validate: each knob's rule, the bo block under another mode,
// and the defaults (init lhs, n_init 2 × axes capped at the budget,
// batch 1).
func TestBO_Validate(t *testing.T) {
	t.Parallel()
	ok := mixedBO(20, 1)
	ok.BO = &types.SweepBO{Init: types.SweepBOInitRandom, NInit: iptr(20), Batch: iptr(20)}
	if ce := sweep.Validate(ok); ce != nil {
		t.Fatalf("boundary knobs refused: %v", ce)
	}
	ok.BO = &types.SweepBO{NInit: iptr(1), Batch: iptr(1)}
	if ce := sweep.Validate(ok); ce != nil {
		t.Fatalf("lower boundary knobs refused: %v", ce)
	}
	cases := []struct {
		name   string
		edit   func(*types.SweepSpec)
		code   perr.Code
		reason string
	}{
		{"init grid", func(s *types.SweepSpec) { s.BO = &types.SweepBO{Init: "grid"} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonBOInitInvalid},
		{"n_init 0", func(s *types.SweepSpec) { s.BO = &types.SweepBO{NInit: iptr(0)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonBONInitInvalid},
		{"n_init over budget", func(s *types.SweepSpec) { s.BO = &types.SweepBO{NInit: iptr(21)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonBONInitOverBudget},
		{"batch negative", func(s *types.SweepSpec) { s.BO = &types.SweepBO{Batch: iptr(-1)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonBOBatchInvalid},
		{"batch over budget", func(s *types.SweepSpec) { s.BO = &types.SweepBO{Batch: iptr(21)} }, perr.PULSE_SWEEP_INVALID, sweep.ReasonBOBatchOverBudget},
		{"bo under de", func(s *types.SweepSpec) { s.Mode, s.BO = types.SweepModeDE, &types.SweepBO{} }, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonBOWithoutBO},
		{"bo under lhs", func(s *types.SweepSpec) {
			s.Mode, s.Budget, s.Samples, s.Axes, s.BO = types.SweepModeLHS, 0, 4, s.Axes[:1], &types.SweepBO{}
		}, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonBOWithoutBO},
		{"de under bo", func(s *types.SweepSpec) { s.DE = &types.SweepDE{} }, perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonDEWithoutDE},
	}
	for _, c := range cases {
		spec := mixedBO(20, 1)
		c.edit(spec)
		ce := sweep.Validate(spec)
		if ce == nil || ce.Code != c.code || ce.Details["reason"] != c.reason {
			t.Errorf("%s: %v, want %s %s", c.name, ce, c.code, c.reason)
		}
	}
	ce := sweep.Validate(func() *types.SweepSpec { s := mixedBO(20, 1); s.BO = &types.SweepBO{Init: "grid"}; return s }())
	if !reflect.DeepEqual(ce.Details["valid"], []string{"lhs", "random"}) || ce.Details["field"] != "sweep.bo.init" {
		t.Errorf("init refusal details %v", ce.Details)
	}

	if got := sweep.ResolveBO(mixedBO(100, 1)); got != (sweep.BOSettings{Init: types.SweepBOInitLHS, NInit: 8, Batch: 1}) {
		t.Errorf("defaults %+v", got)
	}
	if got := sweep.ResolveBO(mixedBO(5, 1)); got.NInit != 5 {
		t.Errorf("default n_init over a budget of 5: %d", got.NInit)
	}
	set := mixedBO(100, 1)
	set.BO = &types.SweepBO{Init: types.SweepBOInitRandom, NInit: iptr(3), Batch: iptr(4)}
	if got := sweep.ResolveBO(set); got != (sweep.BOSettings{Init: types.SweepBOInitRandom, NInit: 3, Batch: 4}) {
		t.Errorf("set knobs %+v", got)
	}
}

// TestBO_FindsKnownOptimum: within a budget of 30, a one-axis bo
// search lands within 0.005 of the optimum for every seed, and over the
// mixed space (linear, log, integer and list axes) within 40 slots it
// reaches the best list value, the integer optimum and the linear and
// log axes' basins.
func TestBO_FindsKnownOptimum(t *testing.T) {
	t.Parallel()
	for seed := int64(1); seed <= 3; seed++ {
		ev, err := sweeptest.Search(quadBO(12, seed), quadObjective(t))
		if err != nil {
			t.Fatal(err)
		}
		if b := best(ev); len(ev) != 12 || math.Abs(axisNumber(t, b.Axes["x"])-0.3) > 0.005 {
			t.Errorf("seed %d: %d slots, best %v at %v", seed, len(ev), b.Value, b.Axes)
		}
	}
	ev, err := sweeptest.Search(mixedBO(40, 2), mixedObjective(t))
	if err != nil {
		t.Fatal(err)
	}
	b := best(ev)
	if len(ev) != 40 || b.Axes["kind"] != "c" || axisNumber(t, b.Axes["n"]) != 12 ||
		math.Abs(axisNumber(t, b.Axes["x"])-3.7) > 0.25 || math.Abs(math.Log10(axisNumber(t, b.Axes["lambda"]))+1) > 0.25 {
		t.Errorf("mixed: %d slots, best %v at %v", len(ev), b.Value, b.Axes)
	}
}

// TestBO_RoundsAndBatch: round 0 is the initial design, each later
// round batch points, the last round what budget is left — budget 10,
// n_init 4, batch 4 runs 4, 4 and 2 slots; the defaults (n_init 2 ×
// axes, batch 1) run 2 then one slot a round.
func TestBO_RoundsAndBatch(t *testing.T) {
	t.Parallel()
	sizes := func(spec *types.SweepSpec) map[int]int {
		ev, err := sweeptest.Search(spec, quadObjective(t))
		if err != nil {
			t.Fatal(err)
		}
		out := map[int]int{}
		for i, e := range ev {
			if e.Slot != i {
				t.Fatalf("slot %d at position %d", e.Slot, i)
			}
			out[e.Round]++
		}
		return out
	}
	spec := quadBO(10, 4)
	spec.BO = &types.SweepBO{NInit: iptr(4), Batch: iptr(4)}
	if got := sizes(spec); !reflect.DeepEqual(got, map[int]int{0: 4, 1: 4, 2: 2}) {
		t.Errorf("batch 4: round sizes %v", got)
	}
	if got := sizes(quadBO(5, 4)); !reflect.DeepEqual(got, map[int]int{0: 2, 1: 1, 2: 1, 3: 1}) {
		t.Errorf("defaults: round sizes %v", got)
	}
}

// TestBO_DeterministicPerSeed: a seed replays the search bit for bit,
// with batch > 1 too; another seed searches elsewhere.
func TestBO_DeterministicPerSeed(t *testing.T) {
	t.Parallel()
	run := func(seed int64, batch int) []sweeptest.Evaluated {
		spec := mixedBO(16, seed)
		spec.BO = &types.SweepBO{Batch: iptr(batch)}
		ev, err := sweeptest.Search(spec, mixedObjective(t))
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	for _, batch := range []int{1, 3} {
		a, b := run(11, batch), run(11, batch)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("batch %d: one seed searched two ways", batch)
		}
		c := run(12, batch)
		if reflect.DeepEqual(a[0].Axes, c[0].Axes) && reflect.DeepEqual(a[15].Axes, c[15].Axes) {
			t.Fatalf("batch %d: seeds 11 and 12 searched alike", batch)
		}
	}
}

// TestBO_ConstantLiarSpreadsBatch: each later round's batch points are
// distinct — the constant liar conditions the surrogate on each pick
// before the next, so EI there drops to zero and the next pick moves
// (without it one round's surrogate and candidates give one pick, batch
// times over).
func TestBO_ConstantLiarSpreadsBatch(t *testing.T) {
	t.Parallel()
	spec := quadBO(12, 3)
	spec.BO = &types.SweepBO{NInit: iptr(4), Batch: iptr(4)}
	ev, err := sweeptest.Search(spec, quadObjective(t))
	if err != nil {
		t.Fatal(err)
	}
	for i := 4; i < len(ev); i++ {
		for j := 4; j < i; j++ {
			if ev[i].Round == ev[j].Round && axisNumber(t, ev[i].Axes["x"]) == axisNumber(t, ev[j].Axes["x"]) {
				t.Errorf("round %d proposed x = %v twice (slots %d and %d)", ev[i].Round, ev[i].Axes["x"], j, i)
			}
		}
	}
}

// TestBO_DescMatchesAsc: rank order desc on −f searches exactly as asc
// on f (the strategy sees the same losses).
func TestBO_DescMatchesAsc(t *testing.T) {
	t.Parallel()
	obj := quadObjective(t)
	asc, err := sweeptest.Search(quadBO(8, 5), obj)
	if err != nil {
		t.Fatal(err)
	}
	spec := quadBO(8, 5)
	spec.RNGSeed, spec.Rank = 5, &types.SweepRank{By: "v", Order: types.SweepRankDesc}
	desc, err := sweeptest.Search(spec, func(a map[string]any) (float64, bool) { v, ok := obj(a); return -v, ok })
	if err != nil {
		t.Fatal(err)
	}
	for i := range asc {
		if !reflect.DeepEqual(asc[i].Axes, desc[i].Axes) {
			t.Fatalf("slot %d: asc %v, desc %v", i, asc[i].Axes, desc[i].Axes)
		}
	}
}

// TestBO_InitialDesign: init lhs draws round 0 as the lhs draw (one
// point per stratum); init random draws it uniformly — another design
// from the same seed.
func TestBO_InitialDesign(t *testing.T) {
	t.Parallel()
	design := func(init types.SweepBOInit) []float64 {
		spec := quadBO(6, 9)
		spec.BO = &types.SweepBO{Init: init, NInit: iptr(6)}
		ev, err := sweeptest.Search(spec, quadObjective(t))
		if err != nil {
			t.Fatal(err)
		}
		out := make([]float64, len(ev))
		for i, e := range ev {
			out[i] = axisNumber(t, e.Axes["x"])
		}
		return out
	}
	lhs := design(types.SweepBOInitLHS)
	if want := sweep.LatinHypercube(9, 6, 1)[0]; !reflect.DeepEqual(lhs, want) {
		t.Errorf("init lhs %v, want the lhs draw %v", lhs, want)
	}
	if rnd := design(types.SweepBOInitRandom); reflect.DeepEqual(rnd, lhs) {
		t.Error("init random drew the lhs design")
	}
	if !reflect.DeepEqual(design(""), lhs) {
		t.Error("the default init is not lhs")
	}
}

// TestBO_PenalisedRegion: a failing region (x > 0.6 penalised) never
// holds the best point, and the search still reaches the optimum.
func TestBO_PenalisedRegion(t *testing.T) {
	t.Parallel()
	obj := quadObjective(t)
	ev, err := sweeptest.Search(quadBO(16, 6), func(a map[string]any) (float64, bool) {
		if axisNumber(t, a["x"]) > 0.6 {
			return -1, false // a value of -1 would win if it were not penalised
		}
		return obj(a)
	})
	if err != nil {
		t.Fatal(err)
	}
	var b *sweeptest.Evaluated
	failed := 0
	for i := range ev {
		if axisNumber(t, ev[i].Axes["x"]) > 0.6 {
			failed++
			continue
		}
		if b == nil || ev[i].Value < b.Value {
			b = &ev[i]
		}
	}
	if failed == 0 || b == nil || math.Abs(axisNumber(t, b.Axes["x"])-0.3) > 0.01 {
		t.Fatalf("%d penalised slots, best %+v", failed, b)
	}
}

// TestExpectedImprovement: EI's closed form at known points.
func TestExpectedImprovement(t *testing.T) {
	t.Parallel()
	cases := []struct{ mu, v, best, want float64 }{
		{0, 1, 0, 0.3989422804014327},           // σ·φ(0)
		{0, 4, 0, 0.7978845608028654},           // scales with σ
		{-1, 1, 0, 1.0833154705876864},          // 1·Φ(1) + φ(1)
		{1, 1, 0, 0.08331547058768644},          // −1·Φ(−1) + φ(−1)
		{0, 0, 1, 0},                            // no variance: no improvement expected
		{0, -1e-18, 1, 0},                       // a rounded-negative variance likewise
		{50, 1, 0, 0},                           // far above the incumbent
		{0.25, 0.0625, 0.5, 0.2708288676469216}, // imp = σ = ¼, z = 1: ¼·(Φ(1) + φ(1))
	}
	for _, c := range cases {
		if got := sweep.ExpectedImprovement(c.mu, c.v, c.best); math.Abs(got-c.want) > 1e-14*math.Max(1, c.want) {
			t.Errorf("EI(mu %v, v %v, best %v) = %.17g, want %.17g", c.mu, c.v, c.best, got, c.want)
		}
	}
}
