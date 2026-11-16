package sweep

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// de.go is mode de: differential evolution, strategy rand/1/bin
// (Storn & Price 1997; R DEoptim's strategy 1, scipy's "rand1bin") on
// the adaptive executor (adaptive.go).
//
// Every axis is searched on the unit interval: a range axis's
// coordinate is mapped by RangeValue (its integer / log form
// honoured); a list axis's coordinate u picks the value at the
// rounded index ⌊u·L⌋ (the nearest integer to u·L − ½), clipped into
// [0, L). The population keeps the continuous coordinates, so a list
// axis mutates like any other.
//
//   - Round 0 is the initial population: Population points of a Latin
//     hypercube (the lhs draw, LatinHypercube's order).
//   - Each later round is one generation: for target i = 0, 1, … the
//     trial draws r1, r2, r3 uniformly from the population, distinct
//     from i and each other, and a crossover axis jrand; the mutant is
//     x[r1] + F·(x[r2] − x[r3]) (the product rounded before its add, so
//     no architecture fuses it), each axis j taking the mutant's
//     coordinate when its crossover draw is below CR or j = jrand and
//     the target's otherwise, then clipped into [0, 1].
//   - Selection (synchronous, after the generation ran): a trial
//     replaces its target when the trial was not penalised and either
//     the target was or the trial's loss is no larger (<=, DEoptim's
//     rule, so the search may drift across a plateau).
//   - budget ÷ Population rounds run in all; a remainder r is a final
//     partial generation: trials for targets 0 … r−1 only.
//
// Draws come from one PCG stream over the effective seed (seed,
// seed ^ PCGStream), raw words only: the initial hypercube, then per
// trial r1, r2, r3, jrand and one crossover draw per axis (every axis,
// jrand included) — so a seed replays the search bit-identically on
// every architecture.

// The defaults of mode de's knobs (types.SweepDE).
const (
	// DEPopulationPerAxis is the default population per axis: 10 × the
	// axis count (DEoptim's NP), capped at the budget.
	DEPopulationPerAxis = 10
	// DEMinPopulation is the smallest population rand/1 can mutate:
	// the target plus three distinct others.
	DEMinPopulation = 4
	// DEDefaultF is the default differential weight.
	DEDefaultF = 0.8
	// DEDefaultCR is the default crossover probability.
	DEDefaultCR = 0.9
)

// The reasons mode de's knob rules give. PULSE_SWEEP_INVALID:
const (
	// ReasonDEPopulationInvalid: `de.population` is below 4.
	ReasonDEPopulationInvalid = "de_population_invalid"
	// ReasonDEPopulationOverBudget: `de.population` exceeds `budget`.
	ReasonDEPopulationOverBudget = "de_population_over_budget"
	// ReasonDEFInvalid: `de.f` is not in (0, 2].
	ReasonDEFInvalid = "de_f_invalid"
	// ReasonDECRInvalid: `de.cr` is not in [0, 1].
	ReasonDECRInvalid = "de_cr_invalid"
)

// ReasonDEWithoutDE (PULSE_SWEEP_MODE_MISMATCH): `de` is set under a
// mode other than de.
const ReasonDEWithoutDE = "de_without_de_mode"

func init() {
	RegisterAdaptive(AdaptiveMode{Mode: types.SweepModeDE, Validate: validateDE, New: newDE})
}

// DESettings are a validated de sweep's resolved knobs.
type DESettings struct {
	Population int
	F          float64
	CR         float64
}

// ResolveDE resolves spec's de knobs against their defaults (spec
// validated): the population 10 × axes capped at the budget unless set.
func ResolveDE(spec *types.SweepSpec) DESettings {
	s := DESettings{Population: min(DEPopulationPerAxis*len(spec.Axes), spec.Budget), F: DEDefaultF, CR: DEDefaultCR}
	if k := spec.DE; k != nil {
		if k.Population != nil {
			s.Population = *k.Population
		}
		if k.F != nil {
			s.F = *k.F
		}
		if k.CR != nil {
			s.CR = *k.CR
		}
	}
	return s
}

// validateDE is mode de's knob rule set (AdaptiveMode.Validate).
func validateDE(spec *types.SweepSpec) *errors.CodedError {
	k := spec.DE
	if k == nil {
		return nil
	}
	if p := k.Population; p != nil {
		if *p < DEMinPopulation {
			return invalid("sweep.de.population", "", ReasonDEPopulationInvalid,
				"sweep de.population is "+strconv.Itoa(*p)+"; rand/1 mutates each member with three distinct others, so set it to at least 4",
				map[string]any{"value": *p, "min": DEMinPopulation})
		}
		if *p > spec.Budget {
			return invalid("sweep.de.population", "", ReasonDEPopulationOverBudget,
				"sweep de.population is "+strconv.Itoa(*p)+" but budget is "+strconv.Itoa(spec.Budget)+
					"; round 0 evaluates the whole population, so set population to at most the budget (or raise budget)",
				map[string]any{"value": *p, "budget": spec.Budget})
		}
	}
	if f := k.F; f != nil && !(*f > 0 && *f <= 2) {
		return invalid("sweep.de.f", "", ReasonDEFInvalid,
			"sweep de.f is "+formatKnob(*f)+"; the differential weight must be in (0, 2] (default 0.8)",
			map[string]any{"value": knobDetail(*f)})
	}
	if cr := k.CR; cr != nil && !(*cr >= 0 && *cr <= 1) {
		return invalid("sweep.de.cr", "", ReasonDECRInvalid,
			"sweep de.cr is "+formatKnob(*cr)+"; the crossover probability must be in [0, 1] (default 0.9)",
			map[string]any{"value": knobDetail(*cr)})
	}
	return nil
}

func formatKnob(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// knobDetail is a knob value as a details entry: the number, or its
// text when it is not finite (a details map is marshalled).
func knobDetail(v float64) any {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return formatKnob(v)
	}
	return v
}

// deStrategy is mode de's Strategy.
type deStrategy struct {
	dims    []Dim
	set     DESettings
	draw    *sampler
	pop     [][]float64 // member i's unit coordinates, axis by axis
	slot    []int       // the sweep slot that evaluated member i
	trials  [][]float64 // the last generation's trials (trial k for target k)
	tSlot   int         // the sweep slot of trials[0]
	started bool
}

// newDE builds mode de's strategy (AdaptiveMode.New).
func newDE(cfg StrategyConfig) (Strategy, error) {
	return &deStrategy{dims: cfg.Dims, set: ResolveDE(cfg.Spec), draw: newSampler(cfg.Seed)}, nil
}

// Propose implements Strategy.
func (s *deStrategy) Propose(round int, history []Observation, limit int) ([]Point, error) {
	if !s.started {
		s.started = true
		n := min(s.set.Population, limit)
		h := s.draw.latinHypercube(n, len(s.dims))
		s.pop = make([][]float64, n)
		s.slot = make([]int, n)
		for i := range s.pop {
			x := make([]float64, len(s.dims))
			for j := range x {
				x[j] = h[j][i]
			}
			s.pop[i], s.slot[i] = x, i
		}
		return s.points(s.pop), nil
	}
	s.selectTrials(history)
	n := min(len(s.pop), limit)
	s.tSlot = len(history)
	s.trials = make([][]float64, n)
	for i := range n {
		s.trials[i] = s.trial(i)
	}
	return s.points(s.trials), nil
}

// selectTrials replaces each target its last-generation trial beat.
func (s *deStrategy) selectTrials(history []Observation) {
	for k, x := range s.trials {
		t, o := history[s.tSlot+k], history[s.slot[k]]
		if !t.Penalised && (o.Penalised || t.Loss <= o.Loss) {
			s.pop[k], s.slot[k] = x, s.tSlot+k
		}
	}
	s.trials = nil
}

// trial is target i's rand/1/bin trial vector.
func (s *deStrategy) trial(i int) []float64 {
	n := uint64(len(s.pop))
	r1 := s.distinct(n, i)
	r2 := s.distinct(n, i, r1)
	r3 := s.distinct(n, i, r1, r2)
	d := len(s.dims)
	jrand := int(s.draw.below(uint64(d)))
	x1, x2, x3, tgt := s.pop[r1], s.pop[r2], s.pop[r3], s.pop[i]
	out := make([]float64, d)
	for j := range out {
		u := s.draw.unit()
		v := tgt[j]
		if u < s.set.CR || j == jrand {
			v = x1[j] + float64(s.set.F*(x2[j]-x3[j]))
		}
		out[j] = min(max(v, 0), 1)
	}
	return out
}

// distinct draws an index uniformly from [0, n) minus the excluded
// ones: a draw k over the n − len(excluded) allowed slots, stepped past
// each excluded index at or below it in ascending order.
func (s *deStrategy) distinct(n uint64, excluded ...int) int {
	k := int(s.draw.below(n - uint64(len(excluded))))
	sorted := append([]int(nil), excluded...)
	for a := 1; a < len(sorted); a++ {
		for b := a; b > 0 && sorted[b] < sorted[b-1]; b-- {
			sorted[b], sorted[b-1] = sorted[b-1], sorted[b]
		}
	}
	for _, e := range sorted {
		if k >= e {
			k++
		}
	}
	return k
}

// points maps unit coordinates onto executor Points: a range axis
// keeps its coordinate, a list axis takes the rounded index.
func (s *deStrategy) points(xs [][]float64) []Point {
	out := make([]Point, len(xs))
	for k, x := range xs {
		p := Point{U: make([]float64, len(s.dims)), Index: make([]int, len(s.dims))}
		for j, d := range s.dims {
			if d.Range != nil {
				p.U[j] = x[j]
				continue
			}
			p.Index[j] = min(int(math.Floor(float64(x[j]*float64(d.Levels)))), d.Levels-1)
		}
		out[k] = p
	}
	return out
}
